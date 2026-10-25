#!/usr/bin/env python3
"""Generate golden outputs from the reference render.py (copy pinned next to this file).

For every fixture in testdata/fixtures it runs a list of commands through render.py with
ffmpeg/ffprobe faked (no media needed) and records stdout, stderr, exit code, every
external command line and every file written into the work folder. The Go tests run the
same cases through video-production-cli with the same fakes and compare byte for byte.

    python3 testdata/golden/gen.py
"""

import contextlib
import io
import json
import os
import shutil
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))
FIXTURES = os.path.join(ROOT, "testdata", "fixtures")
OUT = os.path.join(HERE, "out")
sys.path.insert(0, HERE)
import render  # noqa: E402

# ---------------------------------------------------------------- fakes (mirrored in Go: internal/golden_test)

FAKES = json.load(open(os.path.join(HERE, "fakes.json")))


class FakeProc(object):
    def __init__(self, stderr="", stdout=""):
        self.stderr, self.stdout, self.returncode = stderr, stdout, 0


def fake_stderr(cmd):
    line = " ".join(cmd)
    if "silencedetect" in line:
        return FAKES["silencedetect_stderr"]
    if "psnr" in line:
        return FAKES["psnr_stderr"]
    return ""


class Recorder(object):
    def __init__(self):
        self.cmds = []

    def run(self, cmd, capture=False):
        self.cmds.append(list(cmd))
        if capture:
            if cmd[0] == "ffprobe":
                return json.dumps(FAKES["ffprobe_render"])
            return ""
        out = cmd[-1]
        if cmd[0] == "ffmpeg" and os.path.isabs(out) and not os.path.exists(out):
            with open(out, "wb") as f:
                f.write(b"\0" * FAKES["output_size"])

    def subprocess_run(self, cmd, **kw):
        self.cmds.append(list(cmd))
        return FakeProc(stderr=fake_stderr(cmd))


# ---------------------------------------------------------------- cases

def map_times(edit):
    """Output times to probe in every scope: spread over the length, plus edges and beyond."""
    out = []
    scopes = [(None, None, ["--all"])] + [(c["start"], c["end"], ["--chunk", str(c["id"])]) for c in edit.data["chunks"]]
    for start, end, flag in scopes:
        keeps = edit.keeps(start, end)
        total = sum(edit.out_len(a, b) for a, b, _ in keeps)
        times = ["0", "%.3f" % total, "%.3f" % (total + 1.0)]
        for k in range(1, 12):
            times.append("%.3f" % (total * k / 12.0))
        times.append(render.fmt_tc(total / 3.0)[:-2])  # m:ss.s form
        for t in times:
            out.append(["map"] + flag + [t])
    return out


def cases_for(name):
    work = os.path.join(FIXTURES, name)
    try:
        edit = render.Edit(os.path.join(work, "edit.json"))
    except SystemExit:
        return [["joins", "--all"], ["preview", "--all"]]
    cs = [["joins", "--all"]]
    cs += [["joins", "--chunk", str(c["id"])] for c in edit.data["chunks"]]
    cs += [["preview", "--chunk", str(c["id"])] for c in edit.data["chunks"]]
    cs += [["preview", "--all"], ["final"], ["final", "--force"], ["verify", "FILE", "--chunk", "1"],
           ["verify", "FILE", "--final"]]
    cs += map_times(edit)
    _, rows = render.join_table(edit, None, None)
    for k in sorted(set([1, (len(rows) + 1) // 2, len(rows)])):
        if k >= 1:
            cs.append(["frames", "--join", str(k)])
            cs.append(["listen", "--join", str(k)])
    cs.append(["frames", "--join", str(len(rows) + 1)])
    cs.append(["listen", "--join", str(len(rows) + 1)])
    cs.append(["frames", "--join", "2", "--span", "0.25"])
    cs.append(["listen", "--join", "1", "--span", "1.5"])
    d = edit.duration
    cs.append(["frames", "--range", "%.3f" % (d / 2), "%.3f" % (d / 2 + 2.0)])
    cs.append(["frames", "--range", "10", "20"])  # too many frames
    cs.append(["proxy", "0", "%.3f" % min(d, 300)])
    cs.append(["proxy", "%.2f" % (d - 30), "%.2f" % (d + 30)])
    cs.append(["silences", "%.3f" % (d / 3), "%.3f" % (d / 3 + 20)])
    cs.append(["silences", "1", "30", "--noise", "-40", "--min", "0.25"])
    cs.append(["suggest-chunks"])
    cs.append(["suggest-chunks", "--every", "300", "--window", "60"])
    return cs


def py_argv(case, edit_path, rendered):
    case = [rendered if a == "FILE" else a for a in case]
    return [case[0], edit_path] + case[1:]


def run_case(name, case):
    tmp = tempfile.mkdtemp(prefix="golden-")
    try:
        work = os.path.join(tmp, "work")
        shutil.copytree(os.path.join(FIXTURES, name), work)
        edit_path = os.path.join(work, "edit.json")
        rendered = os.path.join(work, "rendered.mp4")
        before = set(os.listdir(work))
        rec = Recorder()
        render.run = rec.run
        render.ffprobe_json = lambda path: (rec.cmds.append(["ffprobe", "-v", "error", "-show_streams",
                                                             "-show_format", "-of", "json", path])
                                            or json.loads(json.dumps(FAKES["ffprobe_render"])))
        render.subprocess.run = rec.subprocess_run
        render.os.path.getsize = lambda p: FAKES["output_size"]
        stdout, stderr = io.StringIO(), io.StringIO()
        code = 0
        sys.argv = ["render.py"] + py_argv(case, edit_path, rendered)
        with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
            try:
                render.main()
            except SystemExit as e:
                code = e.code if isinstance(e.code, int) else 1
        files = {}
        for root, _, fnames in os.walk(work):
            for f in fnames:
                p = os.path.join(root, f)
                rel = os.path.relpath(p, work)
                if rel in before or not (f.endswith(".txt") or f.endswith(".json")):
                    continue
                files[rel] = open(p).read()
        norm = lambda s: s.replace(work, "$WORK")
        return {
            "args": case,
            "exit": code,
            "stdout": norm(stdout.getvalue()),
            "stderr": norm(stderr.getvalue()),
            "commands": [[norm(a) for a in c] for c in rec.cmds],
            "files": {k: norm(v) for k, v in sorted(files.items())},
        }
    finally:
        shutil.rmtree(tmp)


def main():
    real_run = subprocess.run
    if os.path.isdir(OUT):
        shutil.rmtree(OUT)
    os.makedirs(OUT)
    total = 0
    for name in sorted(os.listdir(FIXTURES)):
        results = [run_case(name, c) for c in cases_for(name)]
        subprocess.run = real_run
        with open(os.path.join(OUT, name + ".json"), "w") as f:
            json.dump(results, f, ensure_ascii=False, indent=1)
            f.write("\n")
        total += len(results)
        print("%-20s %d cases" % (name, len(results)))
    print("total %d cases" % total)


if __name__ == "__main__":
    main()
