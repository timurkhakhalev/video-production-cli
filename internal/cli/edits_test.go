package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/ffmpeg"
	"video-production-cli/internal/jsonx"
)

func TestEditCommands(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	copyDir(t, "../../testdata/fixtures/syn-ntsc", work)
	epath := filepath.Join(work, "edit.json")
	saved := ffmpeg.R
	ffmpeg.R = &fakeRunner{f: readFakes(t)}
	defer func() { ffmpeg.R = saved }()
	run := func(args ...string) (string, string, int) {
		return runTestCLI(t, append(args, "--edit", epath)...)
	}

	out, errs, code := run("cuts", "add", "10", "12.5", "--reason", "пауза")
	if code != 0 || !strings.Contains(out, "cut #2 added") || !strings.Contains(out, "frames --join") {
		t.Fatalf("cuts add: %d %q %q", code, out, errs)
	}
	e, err := edit.Load(epath)
	if err != nil {
		t.Fatal(err)
	}
	cuts, _ := e.Doc.Get("cuts")
	second := cuts.([]jsonx.Value)[1].(*jsonx.Object)
	if from, _ := second.Get("from"); from != jsonx.Number("10.0") {
		t.Fatalf("cut not inserted in source order with a float literal: %v", from)
	}
	if v, _ := e.Doc.Get("version"); v == nil {
		t.Fatal("version not added on save")
	}

	if _, errs, code = run("cuts", "add", "11", "13", "--reason", "x"); code == 0 || !strings.Contains(errs, "overlaps the existing cut") {
		t.Fatalf("overlap accepted: %q", errs)
	}
	if _, errs, code = run("cuts", "add", "20", "21"); code == 0 || !strings.Contains(errs, "needs a reason") {
		t.Fatalf("cut without reason accepted: %q", errs)
	}
	if _, errs, code = run("listen", "--apply", "--reason", "x"); code == 0 || !strings.Contains(errs, "--try") {
		t.Fatalf("--apply without --try: %q", errs)
	}
	out, errs, code = run("listen", "--try", "20", "21", "--apply", "--reason", "кашель")
	if code != 0 || !strings.Contains(out, "cut #3 added") {
		t.Fatalf("listen --try --apply: %d %q %q", code, out, errs)
	}

	out, _, code = run("cuts", "--json")
	var list []EntryResult
	if code != 0 || json.Unmarshal([]byte(out), &list) != nil || len(list) != 8 || list[2].From != 20 || list[2].Join == 0 {
		t.Fatalf("cuts list: %s", out)
	}
	if out, errs, code = run("cuts", "rm", "2"); code != 0 || !strings.Contains(out, "#2 removed: 10.0 - 12.5 (пауза)") {
		t.Fatalf("cuts rm: %d %q %q", code, out, errs)
	}
	if _, errs, code = run("cuts", "rm", "99"); code == 0 || !strings.Contains(errs, "no cuts entry #99") {
		t.Fatalf("rm out of range: %q", errs)
	}

	if out, errs, code = run("speedups", "add", "240", "260", "--speed", "4", "--reason", "сборка"); code != 0 {
		t.Fatalf("speedups add: %q %q", out, errs)
	}
	if _, errs, code = run("speedups", "add", "270", "280", "--speed", "1", "--reason", "x"); code == 0 || !strings.Contains(errs, "whole number >= 2") {
		t.Fatalf("speed 1 accepted: %q", errs)
	}
	if _, errs, code = run("mode", "fast"); code == 0 || !strings.Contains(errs, "step or full") {
		t.Fatalf("bad mode accepted: %q", errs)
	}
	if _, _, code = run("mode", "full"); code != 0 {
		t.Fatal("mode full failed")
	}

	e, err = edit.Load(epath)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := e.Doc.Get("mode"); m != "full" || len(e.Speedups) != 3 || len(e.Cuts) != 7 {
		t.Fatalf("not persisted: mode %v, %d speed-ups, %d cuts", m, len(e.Speedups), len(e.Cuts))
	}
	raw, _ := os.ReadFile(epath)
	if !strings.Contains(string(raw), `"reason": "кашель"`) {
		t.Fatal("non-ASCII reason not written as is")
	}
}
