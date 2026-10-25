package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/ffmpeg"
	"video-production-cli/internal/graph"
	"video-production-cli/internal/jsonx"
)

func runTestCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Run("test", args, &out, &errs)
	return out.String(), errs.String(), code
}

func TestInspectionCommands(t *testing.T) {
	work := filepath.Join(t.TempDir(), "work")
	copyDir(t, "../../testdata/fixtures/syn-ntsc", work)
	epath := filepath.Join(work, "edit.json")
	original, _ := os.ReadFile(epath)
	value, _ := jsonx.Parse(original)
	doc := value.(*jsonx.Object)
	doc.Set("custom_note", jsonx.NewObject().Set("text", "preserve me"))
	if err := os.WriteFile(epath, jsonx.Dump(doc), 0644); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{f: readFakes(t)}
	saved := ffmpeg.R
	ffmpeg.R = r
	defer func() { ffmpeg.R = saved }()
	for _, args := range [][]string{
		{"status"}, {"chunks", "set", "1", "in_review"},
		{"frames", "--at", "2:00", "121"}, {"frames", "--range", "10", "20", "--fps", "2"},
		{"listen", "--range", "10", "12"}, {"listen", "--try", "10", "12"}, {"listen", "--join", "1"},
		{"audio", "--range", "10", "12", "--at", "11"}, {"silences", "--range", "10", "12"},
		{"frames", "--file", "rendered.mp4", "--at", "1"},
	} {
		args = append(args, "--edit", epath, "--json")
		out, errs, code := runTestCLI(t, args...)
		if code != 0 || !json.Valid([]byte(out)) {
			t.Fatalf("%v: %d %s %s", args, code, out, errs)
		}
	}
	e, err := edit.Load(epath)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := e.Doc.Get("custom_note"); !ok || jsonx.Str(ffmpeg.Get(v.(*jsonx.Object), "text")) != "preserve me" {
		t.Fatal("save lost unknown fields")
	}
	if e.Chunks[0].Status != "in_review" {
		t.Fatal("status not persisted")
	}
	if v, ok := e.Doc.Get("version"); !ok || jsonx.Str(v) != "1" {
		t.Fatal("no version on first save")
	}
	for _, cmd := range r.cmds {
		line := strings.Join(cmd, " ")
		if strings.HasSuffix(line, ".mp3") && (strings.Contains(line, "atrim=start_sample") || strings.Contains(line, "-write_xing 0")) {
			t.Fatalf("MP3 drops source samples or timing metadata: %s", line)
		}
	}
	// Errors must arrive before running ffmpeg, including invalid numeric values and conflicting modes.
	before := len(r.cmds)
	for _, args := range [][]string{
		{"audio", "1", "2", "--step", "0"}, {"audio", "1", "2", "--step", "NaN"},
		{"frames", "--at", "NaN"}, {"frames", "--range", "1", "2", "--fps", "-1"},
		{"listen", "--join", "1", "--try", "10", "12"}, {"listen", "--range", "1", "2", "--span", "-1"},
		{"silences", "2", "1"}, {"map", "Inf"},
	} {
		_, errs, code := runTestCLI(t, append(args, "--edit", epath)...)
		if code == 0 || errs == "" {
			t.Fatalf("accepted %v", args)
		}
	}
	if len(r.cmds) != before {
		t.Fatal("invalid arguments ran external commands")
	}
}

func TestKeepSyntheticParity(t *testing.T) {
	orig, err := edit.Load("../../testdata/fixtures/syn-short-multirange/edit.json")
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	copyDir(t, "../../testdata/fixtures/syn-short-multirange", work)
	path := filepath.Join(work, "edit.json")
	b, _ := os.ReadFile(path)
	v, _ := jsonx.Parse(b)
	doc := v.(*jsonx.Object)
	doc.Set("keep", []jsonx.Value{
		jsonx.NewObject().Set("from", 100.0).Set("to", 140.0),
		jsonx.NewObject().Set("from", 650.0).Set("to", 670.0),
	})
	doc.Set("chunks", []jsonx.Value{
		jsonx.NewObject().Set("id", jsonx.Number("1")).Set("start", 100.0).Set("end", 140.0).Set("status", "approved"),
		jsonx.NewObject().Set("id", jsonx.Number("2")).Set("start", 650.0).Set("end", 670.0).Set("status", "approved"),
	})
	doc.Set("cuts", []jsonx.Value{jsonx.NewObject().Set("from", 110.0).Set("to", 112.0).Set("reason", "internal cut")})
	os.WriteFile(path, jsonx.Dump(doc), 0644)
	kept, err := edit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Build(orig, orig.Keeps(nil, nil)) != graph.Build(kept, kept.Keeps(nil, nil)) {
		t.Fatal("keep graph differs from synthetic cut representation")
	}
	if err := kept.Save(); err != nil {
		t.Fatal(err)
	}
	again, err := edit.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Build(again, again.Keeps(nil, nil)) != graph.Build(kept, kept.Keeps(nil, nil)) {
		t.Fatal("save changed graph")
	}
	for _, bad := range []string{
		`[{"from":2115,"to":2181},{"from":2180,"to":2200}]`,
		`[{"from":-1,"to":2180}]`, `[{"from":2115,"to":9999}]`,
		`[{"from":2115,"to":2115}]`, `[]`, `null`, `[1]`, `{}`,
	} {
		v, _ := jsonx.Parse([]byte(bad))
		doc.Set("keep", v)
		os.WriteFile(path, jsonx.Dump(doc), 0644)
		if _, err := edit.Load(path); err == nil {
			t.Fatalf("accepted invalid keep %s", bad)
		}
	}
}

func TestTimecodes(t *testing.T) {
	for s, want := range map[string]float64{"123.4": 123.4, "2:03.4": 123.4, "1:02:03.4": 3723.4} {
		got, err := parseTime(s)
		if err != nil || got != want {
			t.Fatalf("%s: %v %v", s, got, err)
		}
	}
	for _, s := range []string{"NaN", "Inf", "-1", "1:2:3:4", "", "1::2"} {
		if _, err := parseTime(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
}
