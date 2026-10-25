#!/usr/bin/env python3
"""Deterministic renderer for screencast lesson edits.

edit.json is the single source of truth (source path, chunks, cuts, speed-ups).
encode.json holds the source stream parameters captured once by `init`.
Every render is rebuilt from these two files; nothing is reconstructed by hand.

Commands:
  init <source> [<source2> ...] [--joined PATH] [--workdir DIR] [--chunk-minutes 10]
  suggest-chunks <edit.json> [--every 600] [--window 120]
  joins <edit.json> [--chunk N | --all]
  map <edit.json> (--chunk N | --all) <output_time>
  frames <edit.json> (--join K | --range FROM TO)
  preview <edit.json> (--chunk N | --all)
  final <edit.json> [--force]
  verify <edit.json> <rendered.mp4> [--chunk N | --all | --final]
  proxy <edit.json> FROM TO
  silences <edit.json> FROM TO [--noise -32] [--min 0.08]
  listen <edit.json> --join K [--span 3]
"""

import argparse
import json
import math
import os
import subprocess
import sys
import tempfile
from fractions import Fraction

PREVIEW_VIDEO = ["-c:v", "libx264", "-preset", "veryfast", "-crf", "24"]
PREVIEW_AUDIO = ["-c:a", "aac", "-b:a", "128k"]
FINAL_CRF = 15
FINAL_PRESET = "slow"
MIN_PSNR = 45
EDGE_FADE_S = 0.005  # 5 ms fade at each splice, no overlap, duration unchanged
CHUNK_STATUSES = ("draft", "in_review", "approved")
MIN_KEEP_S = 0.3  # shorter kept fragments flash on screen
PROXY_ARGS = ["-vf", "scale=-2:540", "-c:v", "libx264", "-preset", "veryfast", "-crf", "28",
              "-c:a", "aac", "-b:a", "96k"]
PROXY_MAX_BYTES = 90 * 1024 * 1024


def die(msg):
    print("ERROR: " + msg, file=sys.stderr)
    sys.exit(1)


def run(cmd, capture=False):
    if capture:
        return subprocess.run(cmd, check=True, capture_output=True, text=True).stdout
    subprocess.run(cmd, check=True)


def ffprobe_json(path):
    out = run(["ffprobe", "-v", "error", "-show_streams", "-show_format", "-of", "json", path], capture=True)
    return json.loads(out)


def fmt_tc(seconds):
    seconds = round(max(0.0, seconds), 3)  # avoid 59.9996 printing as "60.000"
    m, s = divmod(seconds, 60)
    h, m = divmod(int(m), 60)
    if h:
        return "%d:%02d:%06.3f" % (h, m, s)
    return "%d:%06.3f" % (m, s)


def parse_tc(text):
    parts = [float(p) for p in str(text).split(":")]
    total = 0.0
    for p in parts:
        total = total * 60 + p
    return total


def load_json(path):
    with open(path) as f:
        return json.load(f)


def save_json(path, data):
    with open(path, "w") as f:
        json.dump(data, f, ensure_ascii=False, indent=2)
        f.write("\n")


# ---------------------------------------------------------------- probe/init

def probe_encode(source):
    info = ffprobe_json(source)
    v = next((s for s in info["streams"] if s["codec_type"] == "video"), None)
    a = next((s for s in info["streams"] if s["codec_type"] == "audio"), None)
    if not v or not a:
        die("source must have one video and one audio stream")
    if v.get("r_frame_rate") != v.get("avg_frame_rate"):
        die("source looks like variable frame rate (r_frame_rate=%s avg_frame_rate=%s). "
            "Frame-accurate cutting is not supported for VFR; stop and ask the user."
            % (v.get("r_frame_rate"), v.get("avg_frame_rate")))
    for s in (v, a):
        if abs(float(s.get("start_time", 0) or 0)) > 1e-3:
            die("stream %s starts at %s, expected 0" % (s["index"], s.get("start_time")))
    a_bitrate = int(a.get("bit_rate") or 192000)
    return {
        "video": {
            "codec": v["codec_name"],
            "profile": v.get("profile"),
            "level": v.get("level"),
            "pix_fmt": v["pix_fmt"],
            "width": v["width"],
            "height": v["height"],
            "fps": v["r_frame_rate"],
            "time_base": v["time_base"],
            "color_range": v.get("color_range"),
            "color_space": v.get("color_space"),
            "color_primaries": v.get("color_primaries"),
            "color_transfer": v.get("color_transfer"),
            "source_bit_rate": int(v.get("bit_rate") or 0),
        },
        "audio": {
            "codec": a["codec_name"],
            "profile": a.get("profile"),
            "sample_rate": int(a["sample_rate"]),
            "channels": a["channels"],
            "channel_layout": a.get("channel_layout"),
            "source_bit_rate": a_bitrate,
            # never below source, rounded up to 32k, at least 192k
            "bit_rate": max(192000, int(math.ceil(a_bitrate / 32000.0)) * 32000),
        },
        "duration": float(info["format"]["duration"]),
    }


# stream parameters that must match for a lossless join
JOIN_KEYS = {
    "video": ("codec", "profile", "level", "pix_fmt", "width", "height", "fps", "time_base",
              "color_range", "color_space", "color_primaries", "color_transfer"),
    "audio": ("codec", "profile", "sample_rate", "channels", "channel_layout"),
}


def first_pts(path, kind):
    """Earliest packet pts of the first video ("v") or audio ("a") stream, in seconds."""
    out = run(["ffprobe", "-v", "error", "-select_streams", kind + ":0", "-read_intervals", "%+#8",
               "-show_entries", "packet=pts:stream=time_base", "-of", "json", path], capture=True)
    info = json.loads(out)
    tb = Fraction(info["streams"][0]["time_base"])
    return min(int(pk["pts"]) for pk in info["packets"] if pk.get("pts") is not None) * tb


def video_frames(path):
    v = next(st for st in ffprobe_json(path)["streams"] if st["codec_type"] == "video")
    return int(v.get("nb_frames") or 0)


def joined_path(sources):
    stems = [os.path.splitext(os.path.basename(p))[0] for p in sources]
    stem = os.path.commonprefix(stems).rstrip(" -_.")
    if not stem or stem in stems:
        stem = stems[0] + "-joined"
    return os.path.join(os.path.dirname(sources[0]), stem + os.path.splitext(sources[0])[1])


def join_sources(sources, out):
    """Concatenate recordings of one lesson without re-encoding; returns [{path, start, duration}]."""
    encs = [probe_encode(p) for p in sources]
    for p, e in zip(sources[1:], encs[1:]):
        for kind, keys in JOIN_KEYS.items():
            for k in keys:
                if e[kind].get(k) != encs[0][kind].get(k):
                    die("%s: %s %s is %r, but %r in %s; parts must be recorded with the same settings"
                        % (p, kind, k, e[kind].get(k), encs[0][kind].get(k), sources[0]))
    frames = [video_frames(p) for p in sources]
    with tempfile.TemporaryDirectory(dir=os.path.dirname(out)) as tmp:
        listing = os.path.join(tmp, "parts.txt")
        with open(listing, "w") as f:
            for p in sources:
                f.write("file '%s'\n" % p.replace("'", "'\\''"))
        raw = os.path.join(tmp, "raw" + os.path.splitext(out)[1])
        run(["ffmpeg", "-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", listing,
             "-map", "0:v:0", "-map", "0:a:0", "-c", "copy", raw])
        # AAC priming (first audio packet before 0) makes the muxer shift the streams by ~20 ms,
        # not by the same amount; put every stream's first packet back where it was in part 1
        shifts = {}
        for kind in ("v", "a"):
            tb = Fraction(next(st for st in ffprobe_json(raw)["streams"]
                               if st["codec_type"][0] == kind)["time_base"])
            shifts[kind] = int(round((first_pts(raw, kind) - first_pts(sources[0], kind)) / tb))
        if any(shifts.values()):
            run(["ffmpeg", "-v", "error", "-y", "-i", raw, "-map", "0", "-c", "copy"]
                + sum([["-bsf:" + k, "setts=pts=PTS-%d:dts=DTS-%d" % (n, n)] for k, n in shifts.items()], [])
                + ["-movflags", "+faststart", out])
        else:
            os.replace(raw, out)
    joined = probe_encode(out)
    got = video_frames(out)
    if got != sum(frames):
        die("joined file has %d frames, parts have %d; check %s" % (got, sum(frames), out))
    parts, t = [], 0.0
    for p, e in zip(sources, encs):
        parts.append({"path": p, "start": round(t, 6), "duration": e["duration"]})
        t += e["duration"]
    if abs(joined["duration"] - t) > 2.0 / float(Fraction(encs[0]["video"]["fps"])):
        die("joined duration %.3f differs from the parts' sum %.3f" % (joined["duration"], t))
    return parts


def cmd_init(args):
    sources = [os.path.abspath(p) for p in args.sources]
    for p in sources:
        if not os.path.exists(p):
            die("source not found: " + p)
    parts = None
    if len(sources) > 1:
        source = os.path.abspath(args.joined or joined_path(sources))
        if source in sources:
            die("--joined must differ from the sources")
        if os.path.exists(source):
            die("%s already exists; delete it or pass --joined PATH" % source)
    else:
        source = sources[0]
    workdir = os.path.abspath(args.workdir or os.path.splitext(source)[0] + "-edit")
    os.makedirs(workdir, exist_ok=True)
    edit_path = os.path.join(workdir, "edit.json")
    encode_path = os.path.join(workdir, "encode.json")
    if os.path.exists(edit_path):
        die("edit.json already exists in %s; refusing to overwrite" % workdir)
    if len(sources) > 1:
        print("joining %d parts without re-encoding -> %s" % (len(sources), source))
        parts = join_sources(sources, source)
    enc = probe_encode(source)
    save_json(encode_path, enc)
    duration = enc["duration"]
    step = args.chunk_minutes * 60.0
    chunks = []
    start, n = 0.0, 1
    while start < duration - 1e-6:
        end = min(duration, start + step)
        if duration - end < step * 0.3:  # do not leave a tiny tail chunk
            end = duration
        chunks.append({"id": n, "start": round(start, 3), "end": round(end, 3), "status": "draft"})
        start, n = end, n + 1
    data = {"source": source}
    if parts:
        data["parts"] = parts
    data.update({
        "mode": None,
        "markers": ["Удалить"],
        "chunks": chunks,
        "cuts": [],
        "review_flags": [],
        "lessons": [],
    })
    save_json(edit_path, data)
    print("workdir:     " + workdir)
    print("edit.json:   " + edit_path)
    print("encode.json: " + encode_path)
    print("duration %s, %d chunk(s) at nominal %d min; refine boundaries with suggest-chunks"
          % (fmt_tc(duration), len(chunks), args.chunk_minutes))
    for i, part in enumerate(parts or [], 1):
        print("part %d starts at source %s (%.3f): %s" % (i, fmt_tc(part["start"]), part["start"], part["path"]))


# ---------------------------------------------------------------- model

class Edit(object):
    def __init__(self, path):
        self.path = os.path.abspath(path)
        self.workdir = os.path.dirname(self.path)
        self.data = load_json(self.path)
        enc_path = os.path.join(self.workdir, "encode.json")
        if not os.path.exists(enc_path):
            die("encode.json missing next to edit.json; run init")
        self.enc = load_json(enc_path)
        self.fps = Fraction(self.enc["video"]["fps"])
        self.duration = self.enc["duration"]
        self.source = self.data["source"]
        self._validate()

    def snap(self, t):
        """Round a time to the nearest frame boundary."""
        frame = int(round(Fraction(t).limit_denominator(100000) * self.fps))
        return float(Fraction(frame) / self.fps)

    def _validate(self):
        chunks = self.data.get("chunks") or []
        if not chunks:
            die("edit.json has no chunks")
        prev_end = 0.0
        for c in chunks:
            if c.get("status") not in CHUNK_STATUSES:
                die("chunk %s: status must be one of %s" % (c.get("id"), CHUNK_STATUSES))
            if abs(c["start"] - prev_end) > 1e-3:
                die("chunk %s must start where previous ends (%.3f)" % (c["id"], prev_end))
            if c["end"] <= c["start"]:
                die("chunk %s has non-positive length" % c["id"])
            prev_end = c["end"]
        if abs(prev_end - self.duration) > 0.05:
            die("last chunk must end at source duration %.3f" % self.duration)
        cuts = sorted(self.data.get("cuts") or [], key=lambda c: c["from"])
        last_to = -1.0
        for c in cuts:
            if c["to"] <= c["from"]:
                die("cut %.3f-%.3f has non-positive length" % (c["from"], c["to"]))
            if c["from"] < -1e-6 or c["to"] > self.duration + 1e-3:
                die("cut %.3f-%.3f outside source" % (c["from"], c["to"]))
            if c["from"] < last_to - 1e-6:
                die("cuts overlap near %.3f" % c["from"])
            if not c.get("reason"):
                die("cut %.3f-%.3f has no reason" % (c["from"], c["to"]))
            for ch in chunks:
                if c["from"] < ch["end"] - 1e-6 < c["to"] - 2e-6:
                    die("cut %.3f-%.3f crosses chunk %s boundary %.3f; move the boundary"
                        % (c["from"], c["to"], ch["id"], ch["end"]))
            last_to = c["to"]
        self.cuts = cuts
        speedups = sorted(self.data.get("speedups") or [], key=lambda s: s["from"])
        last_to = -1.0
        for s in speedups:
            where = "speed-up %.3f-%.3f" % (s["from"], s["to"])
            if s["to"] <= s["from"]:
                die(where + " has non-positive length")
            if s["from"] < -1e-6 or s["to"] > self.duration + 1e-3:
                die(where + " outside source")
            if s["from"] < last_to - 1e-6:
                die("speed-ups overlap near %.3f" % s["from"])
            if not s.get("reason"):
                die(where + " has no reason")
            speed = s.get("speed")
            if isinstance(speed, bool) or not isinstance(speed, (int, float)) \
                    or not float(speed).is_integer() or speed < 2:
                die(where + ": speed must be a whole number >= 2 (got %r)" % (speed,))
            s["speed"] = int(speed)
            for ch in chunks:
                if s["from"] < ch["end"] - 1e-6 < s["to"] - 2e-6:
                    die("%s crosses chunk %s boundary %.3f; move the boundary" % (where, ch["id"], ch["end"]))
            for c in cuts:
                if c["from"] < s["to"] - 1e-6 and s["from"] < c["to"] - 1e-6:
                    die("%s overlaps cut %.3f-%.3f; a range is either cut or sped up"
                        % (where, c["from"], c["to"]))
            last_to = s["to"]
        self.speedups = speedups

    def chunk(self, cid):
        for c in self.data["chunks"]:
            if c["id"] == cid:
                return c
        die("no chunk %s" % cid)

    def keeps(self, start=None, end=None):
        """Kept source ranges (frame-snapped) within [start, end]."""
        start = self.snap(0.0 if start is None else start)
        end = self.snap(self.duration if end is None else end)
        out, cur = [], start
        for c in self.cuts:
            a, b = self.snap(c["from"]), self.snap(c["to"])
            if b <= start or a >= end:
                continue
            a, b = max(a, start), min(b, end)
            if a > cur:
                out.append([cur, a, c])
            cur = max(cur, b)
        if end > cur:
            out.append([cur, end, None])
        # [in, out, cut_that_follows]
        return [k for k in out if k[1] - k[0] >= 0.5 / float(self.fps)]

    def split(self, a, b):
        """Split a kept range at speed-up boundaries: [(in, out, speed)]."""
        out, cur = [], a
        for s in self.speedups:
            sa, sb = self.snap(s["from"]), self.snap(s["to"])
            if sb <= cur or sa >= b:
                continue
            sa, sb = max(sa, cur), min(sb, b)
            if sa > cur:
                out.append((cur, sa, 1))
            out.append((sa, sb, s["speed"]))
            cur = sb
        if b > cur:
            out.append((cur, b, 1))
        return out

    def out_frames(self, a, b, speed):
        """Output frames of a source range: every speed-th frame is kept."""
        n = int(round((b - a) * self.fps))
        return -(-n // speed)

    def out_len(self, a, b):
        return sum(self.out_frames(*p) for p in self.split(a, b)) / float(self.fps)

    def timeline(self, keeps):
        """Output pieces [(src_in, src_out, speed, out_start, out_frames)] of kept ranges."""
        out, n = [], 0
        for a, b, _ in keeps:
            for pa, pb, speed in self.split(a, b):
                f = self.out_frames(pa, pb, speed)
                out.append((pa, pb, speed, n / float(self.fps), f))
                n += f
        return out

    def scope(self, args):
        if getattr(args, "all", False) or getattr(args, "final", False):
            return None, None, "all"
        if getattr(args, "chunk", None) is not None:
            c = self.chunk(args.chunk)
            return c["start"], c["end"], "chunk%02d" % c["id"]
        die("specify --chunk N or --all")


def join_table(edit, start, end):
    keeps = edit.keeps(start, end)
    rows, t = [], 0.0
    for i, (a, b, cut) in enumerate(keeps):
        t += edit.out_len(a, b)
        if i + 1 < len(keeps):
            rows.append({
                "out_time": t,
                "src_out": b,
                "src_in": keeps[i + 1][0],
                "removed": keeps[i + 1][0] - b,
                "reason": cut["reason"] if cut else "",
            })
    return keeps, rows


# ---------------------------------------------------------------- commands

def cmd_suggest_chunks(args):
    edit = Edit(args.edit)
    proc = subprocess.run(["ffmpeg", "-hide_banner", "-nostats", "-i", edit.source, "-map", "0:a:0",
                           "-af", "silencedetect=noise=-40dB:d=0.4", "-f", "null", "-"],
                          capture_output=True, text=True)
    silences, s0 = [], None
    for line in proc.stderr.splitlines():
        if "silence_start:" in line:
            s0 = float(line.split("silence_start:")[1].split()[0])
        elif "silence_end:" in line and s0 is not None:
            s1 = float(line.split("silence_end:")[1].split()[0])
            silences.append((s0, s1))
            s0 = None
    mark = args.every
    while mark < edit.duration - args.window:
        near = [s for s in silences if abs((s[0] + s[1]) / 2 - mark) <= args.window]
        near.sort(key=lambda s: s[1] - s[0], reverse=True)
        print("around %s:" % fmt_tc(mark))
        for s in near[:5]:
            print("  pause %s - %s (%.2fs), boundary candidate %.3f"
                  % (fmt_tc(s[0]), fmt_tc(s[1]), s[1] - s[0], (s[0] + s[1]) / 2))
        if not near:
            print("  no pause >= 0.4s in window")
        mark += args.every


def cmd_joins(args):
    edit = Edit(args.edit)
    start, end, label = edit.scope(args)
    keeps, rows = join_table(edit, start, end)
    # number joins globally so they match `frames --join K`
    _, all_rows = join_table(edit, None, None)
    offset = len([r for r in all_rows if start is not None and r["src_out"] <= start + 1e-6])
    total = sum(edit.out_len(a, b) for a, b, _ in keeps)
    src_len = (end if end is not None else edit.duration) - (start or 0.0)
    print("%s: source %s -> output %s, %d join(s)" % (label, fmt_tc(src_len), fmt_tc(total), len(rows)))
    for k, r in enumerate(rows, offset + 1):
        print("#%-3d out %s | src %s -> %s | -%.2fs | %s"
              % (k, fmt_tc(r["out_time"]), fmt_tc(r["src_out"]), fmt_tc(r["src_in"]), r["removed"], r["reason"]))
    reasons = {(edit.snap(s["from"]), edit.snap(s["to"])): s["reason"] for s in edit.speedups}
    for a, b, speed, t, f in edit.timeline(keeps):
        if speed > 1:
            print("x%-3d out %s - %s | src %s - %s | %.1fs -> %.1fs, muted | %s"
                  % (speed, fmt_tc(t), fmt_tc(t + f / float(edit.fps)), fmt_tc(a), fmt_tc(b),
                     b - a, f / float(edit.fps), reasons.get((a, b), "")))
    for a, b, _ in keeps:
        if b - a < MIN_KEEP_S:
            print("WARNING: kept fragment %s-%s is only %.2fs and will flash on screen; widen a cut to remove it"
                  % (fmt_tc(a), fmt_tc(b), b - a))


def cmd_map(args):
    edit = Edit(args.edit)
    start, end, label = edit.scope(args)
    keeps, _ = join_table(edit, start, end)
    t = parse_tc(args.time)
    acc = 0.0
    for a, b, speed, acc, f in edit.timeline(keeps):
        acc_end = acc + f / float(edit.fps)
        if t <= acc_end + 1e-9:
            src = min(b, a + (t - acc) * speed)
            note = "  (inside x%d speed-up %s - %s)" % (speed, fmt_tc(a), fmt_tc(b)) if speed > 1 else ""
            print("%s %s -> source %s (%.3f)%s" % (label, fmt_tc(t), fmt_tc(src), src, note))
            return
        acc = acc_end
    die("time %s beyond %s output length %s" % (fmt_tc(t), label, fmt_tc(acc)))


def cmd_frames(args):
    edit = Edit(args.edit)
    fdir = os.path.join(edit.workdir, "frames")
    os.makedirs(fdir, exist_ok=True)
    fps = float(edit.fps)
    if args.join is not None:
        _, rows = join_table(edit, None, None)
        if not 1 <= args.join <= len(rows):
            die("join must be 1..%d (see `joins --all`)" % len(rows))
        r = rows[args.join - 1]
        span = args.span
        n = int(round(span * fps))
        a0 = max(0.0, r["src_out"] - span)
        sheet = os.path.join(fdir, "join%03d_sheet.jpg" % args.join)
        pair = os.path.join(fdir, "join%03d_pair.jpg" % args.join)
        run(["ffmpeg", "-v", "error", "-y",
             "-ss", "%.4f" % a0, "-t", "%.4f" % (r["src_out"] - a0), "-i", edit.source,
             "-ss", "%.4f" % r["src_in"], "-t", "%.4f" % span, "-i", edit.source,
             "-filter_complex",
             "[0:v]scale=384:-2,tile=%dx1[a];[1:v]scale=384:-2,tile=%dx1[b];[a][b]vstack[s]" % (n, n),
             "-map", "[s]", "-frames:v", "1", "-q:v", "3", sheet])
        last = edit.snap(r["src_out"]) - 1.0 / fps
        run(["ffmpeg", "-v", "error", "-y",
             "-ss", "%.4f" % last, "-i", edit.source,
             "-ss", "%.4f" % r["src_in"], "-i", edit.source,
             "-filter_complex", "[0:v]trim=end_frame=1[a];[1:v]trim=end_frame=1[b];[a][b]hstack[s]",
             "-map", "[s]", "-frames:v", "1", "-q:v", "2", pair])
        print("join #%d: src %s -> %s (%s)" % (args.join, fmt_tc(r["src_out"]), fmt_tc(r["src_in"]), r["reason"]))
        print("sheet: " + sheet)
        print("  top row: last %d frames before the cut (left->right, rightmost is the last kept frame)" % n)
        print("  bottom row: first %d frames after the cut (leftmost is the first kept frame)" % n)
        print("pair:  " + pair + "  (left: last frame before, right: first frame after; full resolution)")
        return
    if args.range:
        a, b = args.range
        a, b = edit.snap(a), edit.snap(b)
        n = int(round((b - a) * fps))
        if n <= 0 or n > 120:
            die("range must cover 1..120 frames (got %d)" % n)
        cols = min(10, n)
        rows_n = int(math.ceil(n / float(cols)))
        sheet = os.path.join(fdir, "range_%.3f_%.3f.jpg" % (a, b))
        run(["ffmpeg", "-v", "error", "-y", "-ss", "%.4f" % a, "-t", "%.4f" % (b - a), "-i", edit.source,
             "-vf", "scale=384:-2,tile=%dx%d" % (cols, rows_n), "-frames:v", "1", "-q:v", "3", sheet])
        print("sheet: " + sheet)
        print("frames left->right, top->bottom; cell (row r, col c) = source %.3f + ((r-1)*%d + (c-1)) / %g s"
              % (a, cols, fps))
        return
    die("specify --join K or --range FROM TO")


def cmd_proxy(args):
    edit = Edit(args.edit)
    a, b = max(0.0, args.start), min(edit.duration, args.end)
    if b <= a:
        die("empty range")
    pdir = os.path.join(edit.workdir, "proxy")
    os.makedirs(pdir, exist_ok=True)
    out = os.path.join(pdir, "proxy_%.3f_%.3f.mp4" % (a, b))
    run(["ffmpeg", "-v", "error", "-y", "-ss", "%.4f" % a, "-t", "%.4f" % (b - a), "-i", edit.source]
        + PROXY_ARGS + ["-movflags", "+faststart", out])
    size = os.path.getsize(out)
    print("proxy: " + out)
    print("source time = %.3f + time in proxy  (proxy %s .. %s of source)" % (a, fmt_tc(a), fmt_tc(b)))
    print("size: %.1f MB" % (size / 1048576.0))
    if size > PROXY_MAX_BYTES:
        print("WARNING: larger than %d MB; split the range" % (PROXY_MAX_BYTES // 1048576))


def cmd_silences(args):
    edit = Edit(args.edit)
    a, b = max(0.0, args.start), min(edit.duration, args.end)
    proc = subprocess.run(["ffmpeg", "-hide_banner", "-nostats", "-ss", "%.4f" % a, "-t", "%.4f" % (b - a),
                           "-i", edit.source, "-map", "0:a:0",
                           "-af", "silencedetect=noise=%gdB:d=%g" % (args.noise, args.min), "-f", "null", "-"],
                          capture_output=True, text=True)
    s0, n = None, 0
    print("pauses in source %s .. %s (noise %gdB, min %gs), source seconds:" % (fmt_tc(a), fmt_tc(b), args.noise, args.min))
    for line in proc.stderr.splitlines():
        if "silence_start:" in line:
            s0 = a + float(line.split("silence_start:")[1].split()[0])
        elif "silence_end:" in line and s0 is not None:
            s1 = a + float(line.split("silence_end:")[1].split()[0])
            print("  %.3f - %.3f  (%.2fs)  %s" % (s0, s1, s1 - s0, fmt_tc(s0)))
            s0, n = None, n + 1
    if s0 is not None:
        print("  %.3f - %.3f  (open)" % (s0, b))
    if not n and s0 is None:
        print("  none")


def cmd_listen(args):
    edit = Edit(args.edit)
    keeps, rows = join_table(edit, None, None)
    if not 1 <= args.join <= len(rows):
        die("join must be 1..%d (see `joins --all`)" % len(rows))
    i = args.join - 1
    before, after = keeps[i], keeps[i + 1]
    seg = [[max(before[0], before[1] - args.span), before[1], before[2]],
           [after[0], min(after[1], after[0] + args.span), None]]
    fdir = os.path.join(edit.workdir, "frames")
    os.makedirs(fdir, exist_ok=True)
    out = os.path.join(fdir, "join%03d_listen.mp4" % args.join)
    graph = build_graph(edit, seg).replace("\n", "") + ";[v]scale=-2:540[vs]"
    run(["ffmpeg", "-v", "error", "-y", "-i", edit.source, "-filter_complex", graph,
         "-map", "[vs]", "-map", "[a]"] + PROXY_ARGS[2:] + ["-movflags", "+faststart", out])
    print("join #%d: src %s -> %s (%s)" % (args.join, fmt_tc(rows[i]["src_out"]), fmt_tc(rows[i]["src_in"]),
                                          rows[i]["reason"]))
    print("listen: " + out)
    print("  the join is at %.2fs into the clip; check that no word or breath is clipped on either side"
          % edit.out_len(seg[0][0], seg[0][1]))


def build_graph(edit, keeps):
    fade = EDGE_FADE_S
    fps, audio = edit.fps, edit.enc["audio"]
    pieces = edit.timeline(keeps)
    lines, pairs = [], []
    for i, (a, b, speed, _, frames) in enumerate(pieces):
        dur = b - a
        vfx = "trim=start=%.6f:end=%.6f,setpts=PTS-STARTPTS" % (a, b)
        if speed > 1:
            # keep every speed-th frame and retime it onto the source frame grid; voice is replaced by silence
            vfx += ",select=not(mod(n\\,%d)),setpts=N*%d/%d/TB" % (speed, fps.denominator, fps.numerator)
            samples = int(round(frames * audio["sample_rate"] / fps))
            afx = "anullsrc=r=%d:cl=%s,atrim=end_sample=%d" % (
                audio["sample_rate"], audio.get("channel_layout") or "stereo", samples)
        else:
            afx = "[0:a:0]atrim=start=%.6f:end=%.6f,asetpts=PTS-STARTPTS" % (a, b)
            if fade > 0 and dur > 4 * fade:
                if i > 0:
                    afx += ",afade=t=in:d=%.4f" % fade
                if i < len(pieces) - 1:
                    afx += ",afade=t=out:st=%.6f:d=%.4f" % (dur - fade, fade)
        lines.append("[0:v:0]%s[v%d];" % (vfx, i))
        lines.append("%s[a%d];" % (afx, i))
        pairs.append("[v%d][a%d]" % (i, i))
    lines.append("%sconcat=n=%d:v=1:a=1[v][a]" % ("".join(pairs), len(pieces)))
    return "\n".join(lines) + "\n"


def final_codec_args(enc):
    v, a = enc["video"], enc["audio"]
    if v["codec"] != "h264":
        die("source video codec %s is not h264; extend render.py deliberately, do not improvise" % v["codec"])
    if a["codec"] != "aac":
        die("source audio codec %s is not aac; extend render.py deliberately, do not improvise" % a["codec"])
    out = ["-c:v", "libx264", "-preset", FINAL_PRESET, "-crf", str(FINAL_CRF), "-pix_fmt", v["pix_fmt"]]
    profile = (v.get("profile") or "").lower().replace("constrained ", "")
    if profile in ("baseline", "main", "high"):
        out += ["-profile:v", profile]
    if v.get("level"):
        out += ["-level:v", "%.1f" % (v["level"] / 10.0)]
    for opt, key in (("-color_range", "color_range"), ("-colorspace", "color_space"),
                     ("-color_primaries", "color_primaries"), ("-color_trc", "color_transfer")):
        if v.get(key) and v[key] != "unknown":
            out += [opt, v[key]]
    tb = Fraction(v["time_base"])
    if tb.numerator == 1:
        out += ["-video_track_timescale", str(tb.denominator)]
    out += ["-c:a", "aac", "-b:a", str(a["bit_rate"]), "-ar", str(a["sample_rate"]), "-ac", str(a["channels"])]
    return out


def render(edit, start, end, out_path, final):
    keeps = edit.keeps(start, end)
    if not keeps:
        die("nothing left to render in this range")
    graph_path = os.path.splitext(out_path)[0] + ".graph.txt"
    with open(graph_path, "w") as f:
        f.write(build_graph(edit, keeps))
    codec = final_codec_args(edit.enc) if final else PREVIEW_VIDEO + PREVIEW_AUDIO
    cmd = ["ffmpeg", "-hide_banner", "-v", "error", "-stats", "-y", "-i", edit.source,
           "-/filter_complex", graph_path, "-map", "[v]", "-map", "[a]",
           "-fps_mode", "passthrough"] + codec + ["-movflags", "+faststart", out_path]
    print("graph: " + graph_path)
    print("render: " + " ".join(cmd))
    run(cmd)
    return keeps


def cmd_preview(args):
    edit = Edit(args.edit)
    start, end, label = edit.scope(args)
    pdir = os.path.join(edit.workdir, "previews")
    os.makedirs(pdir, exist_ok=True)
    out = os.path.join(pdir, "preview_%s.mp4" % label)
    keeps = render(edit, start, end, out, final=False)
    report = verify(edit, out, keeps, final=False)
    print("preview: " + out)
    print_report(report)
    if not report["ok"]:
        sys.exit(1)


def cmd_final(args):
    edit = Edit(args.edit)
    pending = [c["id"] for c in edit.data["chunks"] if c["status"] != "approved"]
    if pending and not args.force:
        die("chunks not approved: %s (use --force only if the user asked to render anyway)" % pending)
    stem = os.path.splitext(os.path.basename(edit.source))[0]
    out = os.path.join(edit.workdir, "%s_edit.mp4" % stem)
    keeps = render(edit, None, None, out, final=True)
    report = verify(edit, out, keeps, final=True)
    save_json(os.path.splitext(out)[0] + ".verify.json", report)
    print("final: " + out)
    print_report(report)
    if not report["ok"]:
        sys.exit(1)


def verify(edit, path, keeps, final):
    info = ffprobe_json(path)
    v = next(s for s in info["streams"] if s["codec_type"] == "video")
    a = next(s for s in info["streams"] if s["codec_type"] == "audio")
    fps = float(edit.fps)
    frames = sum(p[4] for p in edit.timeline(keeps))
    expected = frames / fps
    vdur, adur = float(v.get("duration") or 0), float(a.get("duration") or 0)
    checks = [
        ("video duration ~= expected", abs(vdur - expected) <= 1.5 / fps, "%.3f vs %.3f" % (vdur, expected)),
        ("audio ~= video duration", abs(adur - vdur) <= 0.05, "a=%.3f v=%.3f" % (adur, vdur)),
        ("frame count", abs(int(v.get("nb_frames") or 0) - frames) <= 1,
         "%s vs %d" % (v.get("nb_frames"), frames)),
    ]
    if final:
        ev, ea = edit.enc["video"], edit.enc["audio"]
        want = [
            ("video codec", v["codec_name"], ev["codec"]),
            ("profile", v.get("profile"), ev.get("profile")),
            ("level", v.get("level"), ev.get("level")),
            ("pix_fmt", v.get("pix_fmt"), ev["pix_fmt"]),
            ("size", "%sx%s" % (v["width"], v["height"]), "%sx%s" % (ev["width"], ev["height"])),
            ("fps", v.get("r_frame_rate"), ev["fps"]),
            ("time_base", v.get("time_base"), ev["time_base"]),
            ("color_range", v.get("color_range"), ev.get("color_range")),
            ("color_space", v.get("color_space"), ev.get("color_space")),
            ("color_primaries", v.get("color_primaries"), ev.get("color_primaries")),
            ("color_transfer", v.get("color_transfer"), ev.get("color_transfer")),
            ("audio codec", a["codec_name"], ea["codec"]),
            ("sample_rate", int(a["sample_rate"]), ea["sample_rate"]),
            ("channels", a["channels"], ea["channels"]),
        ]
        for name, got, exp in want:
            checks.append((name, got == exp, "%s vs source %s" % (got, exp)))
        # ffmpeg aac spends fewer bits on silence than requested, so allow some slack
        ab = int(a.get("bit_rate") or 0)
        checks.append(("audio bitrate not far below source", ab >= ea["source_bit_rate"] * 0.8,
                       "%d vs source %d (requested %d)" % (ab, ea["source_bit_rate"], ea["bit_rate"])))
        # bitrate says little (screen recorders often encode in hardware, x264 is far more efficient); compare pixels instead
        psnr = sample_psnr(edit, path, keeps)
        checks.append(("picture matches source (PSNR >= %d dB)" % MIN_PSNR, psnr >= MIN_PSNR,
                       "%.1f dB, worst of first/middle/last kept 1x segments" % psnr))
    return {
        "file": path,
        "ok": all(ok for _, ok, _ in checks),
        "checks": [{"check": n, "ok": ok, "detail": d} for n, ok, d in checks],
    }


def _psnr(path, out_t, source, src_t, dur):
    proc = subprocess.run(
        ["ffmpeg", "-hide_banner", "-nostats", "-ss", "%.6f" % out_t, "-i", path, "-ss", "%.6f" % src_t, "-i", source,
         "-lavfi", "[0:v]trim=0:%.6f,setpts=PTS-STARTPTS[x];[1:v]trim=0:%.6f,setpts=PTS-STARTPTS[y];[x][y]psnr"
         % (dur, dur), "-f", "null", "-"], capture_output=True, text=True)
    for line in proc.stderr.splitlines():
        if "PSNR" in line and "average:" in line:
            val = line.split("average:")[1].split()[0]
            return float("inf") if val == "inf" else float(val)
    return 0.0


def sample_psnr(edit, path, keeps, seconds=10.0):
    """Lowest PSNR over the first, middle and last kept 1x segments.

    Float seeks can land one frame apart on the two files, so each sample tries
    offsets of -1/0/+1 frame and keeps the best alignment.
    """
    pieces = [p for p in edit.timeline(keeps) if p[2] == 1]
    if not pieces:
        return 0.0
    picks = sorted(set([0, len(pieces) // 2, len(pieces) - 1]))
    frame = 1.0 / float(edit.fps)
    worst = float("inf")
    for i in picks:
        a, b, _, start, _ = pieces[i]
        dur = min(seconds, b - a - 2 * frame)
        if dur <= frame:
            continue
        best = max(_psnr(path, max(0.0, start + frame + d * frame), edit.source, a + frame, dur)
                   for d in (-1, 0, 1))
        worst = min(worst, best)
    return 0.0 if worst == float("inf") else worst


def print_report(report):
    print("verify: " + ("OK" if report["ok"] else "FAILED"))
    for c in report["checks"]:
        print("  [%s] %s: %s" % ("ok" if c["ok"] else "FAIL", c["check"], c["detail"]))


def cmd_verify(args):
    edit = Edit(args.edit)
    final = bool(args.final)
    if final:
        start = end = None
    else:
        start, end, _ = edit.scope(args)
    report = verify(edit, os.path.abspath(args.rendered), edit.keeps(start, end), final=final)
    print_report(report)
    if not report["ok"]:
        sys.exit(1)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", required=True)

    s = sub.add_parser("init")
    s.add_argument("sources", nargs="+", metavar="source", help="one file, or several parts of one lesson in order")
    s.add_argument("--joined", help="path for the joined source (default: next to the first part)")
    s.add_argument("--workdir")
    s.add_argument("--chunk-minutes", type=float, default=10)
    s.set_defaults(fn=cmd_init)

    s = sub.add_parser("suggest-chunks")
    s.add_argument("edit")
    s.add_argument("--every", type=float, default=600)
    s.add_argument("--window", type=float, default=120)
    s.set_defaults(fn=cmd_suggest_chunks)

    def scoped(name, fn, extra=None):
        s = sub.add_parser(name)
        s.add_argument("edit")
        g = s.add_mutually_exclusive_group(required=name not in ("verify",))
        g.add_argument("--chunk", type=int)
        g.add_argument("--all", action="store_true")
        if extra:
            extra(s, g)
        s.set_defaults(fn=fn)
        return s

    scoped("joins", cmd_joins)
    scoped("map", cmd_map, lambda s, g: s.add_argument("time", help="output time, seconds or m:ss.sss"))
    scoped("preview", cmd_preview)
    scoped("verify", cmd_verify, lambda s, g: (s.add_argument("rendered"), g.add_argument("--final", action="store_true")))

    s = sub.add_parser("frames")
    s.add_argument("edit")
    g = s.add_mutually_exclusive_group(required=True)
    g.add_argument("--join", type=int, help="global join number from `joins --all`")
    g.add_argument("--range", type=float, nargs=2, metavar=("FROM", "TO"), help="source seconds")
    s.add_argument("--span", type=float, default=0.5, help="seconds on each side of a join")
    s.set_defaults(fn=cmd_frames)

    s = sub.add_parser("proxy")
    s.add_argument("edit")
    s.add_argument("start", type=float, metavar="FROM")
    s.add_argument("end", type=float, metavar="TO")
    s.set_defaults(fn=cmd_proxy)

    s = sub.add_parser("silences")
    s.add_argument("edit")
    s.add_argument("start", type=float, metavar="FROM")
    s.add_argument("end", type=float, metavar="TO")
    s.add_argument("--noise", type=float, default=-32)
    s.add_argument("--min", type=float, default=0.08)
    s.set_defaults(fn=cmd_silences)

    s = sub.add_parser("listen")
    s.add_argument("edit")
    s.add_argument("--join", type=int, required=True)
    s.add_argument("--span", type=float, default=3.0)
    s.set_defaults(fn=cmd_listen)

    s = sub.add_parser("final")
    s.add_argument("edit")
    s.add_argument("--force", action="store_true")
    s.set_defaults(fn=cmd_final)

    args = p.parse_args()
    args.fn(args)


if __name__ == "__main__":
    main()
