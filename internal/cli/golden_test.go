package cli

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"video-production-cli/internal/ffmpeg"
)

// Golden cases come from testdata/golden/gen.py, which runs the reference render.py on
// every fixture with ffmpeg/ffprobe faked. Here the same command lines run through the Go
// CLI with the same fakes; stdout, stderr, exit code, every external command and every file
// written must match byte for byte.

type goldenCase struct {
	Args     []string          `json:"args"`
	Exit     int               `json:"exit"`
	Stdout   string            `json:"stdout"`
	Stderr   string            `json:"stderr"`
	Commands [][]string        `json:"commands"`
	Files    map[string]string `json:"files"`
}

type fakes struct {
	OutputSize          int             `json:"output_size"`
	SilencedetectStderr string          `json:"silencedetect_stderr"`
	PSNRStderr          string          `json:"psnr_stderr"`
	FFprobeRender       json.RawMessage `json:"ffprobe_render"`
}

type fakeRunner struct {
	f    fakes
	cmds [][]string
}

func (r *fakeRunner) Run(argv []string) error {
	r.cmds = append(r.cmds, append([]string{}, argv...))
	out := argv[len(argv)-1]
	if argv[0] == "ffmpeg" && filepath.IsAbs(out) {
		if _, err := os.Stat(out); err != nil {
			return os.WriteFile(out, make([]byte, r.f.OutputSize), 0o644)
		}
	}
	return nil
}

func (r *fakeRunner) Output(argv []string) (string, error) {
	r.cmds = append(r.cmds, append([]string{}, argv...))
	if argv[0] == "ffprobe" {
		return string(r.f.FFprobeRender), nil
	}
	return "", nil
}

func (r *fakeRunner) Stderr(argv []string) (string, error) {
	r.cmds = append(r.cmds, append([]string{}, argv...))
	line := strings.Join(argv, " ")
	switch {
	case strings.Contains(line, "silencedetect"):
		return r.f.SilencedetectStderr, nil
	case strings.Contains(line, "psnr"):
		return r.f.PSNRStderr, nil
	}
	return "", nil
}

func readFakes(t *testing.T) fakes {
	b, err := os.ReadFile("../../testdata/golden/fakes.json")
	if err != nil {
		t.Fatal(err)
	}
	var f fakes
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func listFiles(t *testing.T, dir string) map[string]bool {
	out := map[string]bool{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, p)
			out[rel] = true
		}
		return nil
	})
	return out
}

// goArgs turns a render.py-style case into the Go CLI form.
func goArgs(c goldenCase, editPath, rendered string) []string {
	var args []string
	for _, a := range c.Args {
		if a == "FILE" {
			a = rendered
		}
		args = append(args, a)
	}
	if args[0] == "listen" {
		args = append(args, "--video") // audio-only is the Go default; render.py writes mp4
	}
	return append(args, "--edit", editPath)
}

func TestGolden(t *testing.T) {
	f := readFakes(t)
	outs, _ := filepath.Glob("../../testdata/golden/out/*.json")
	if len(outs) == 0 {
		t.Fatal("no golden outputs; run `make golden`")
	}
	total := 0
	for _, out := range outs {
		name := strings.TrimSuffix(filepath.Base(out), ".json")
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var cases []goldenCase
		if err := json.Unmarshal(b, &cases); err != nil {
			t.Fatal(err)
		}
		t.Run(name, func(t *testing.T) {
			for _, c := range cases {
				total++
				runGolden(t, f, name, c)
			}
		})
	}
	t.Logf("%d golden cases", total)
}

func runGolden(t *testing.T, f fakes, fixture string, c goldenCase) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	copyDir(t, filepath.Join("../../testdata/fixtures", fixture), work)
	before := listFiles(t, work)
	runner := &fakeRunner{f: f}
	saved := ffmpeg.R
	ffmpeg.R = runner
	defer func() { ffmpeg.R = saved }()

	var stdout, stderr bytes.Buffer
	args := goArgs(c, filepath.Join(work, "edit.json"), filepath.Join(work, "rendered.mp4"))
	code := Run("test", args, &stdout, &stderr)

	norm := func(s string) string { return strings.ReplaceAll(s, work, "$WORK") }
	label := strings.Join(c.Args, " ")
	if code != c.Exit {
		t.Errorf("%s: exit %d, want %d (stderr %q)", label, code, c.Exit, stderr.String())
	}
	if got := norm(stdout.String()); got != c.Stdout {
		t.Errorf("%s: stdout differs\n got: %q\nwant: %q", label, got, c.Stdout)
	}
	if got := norm(stderr.String()); got != c.Stderr {
		t.Errorf("%s: stderr differs\n got: %q\nwant: %q", label, got, c.Stderr)
	}
	var cmds [][]string
	for _, cmd := range runner.cmds {
		n := make([]string, len(cmd))
		for i, a := range cmd {
			n[i] = norm(a)
		}
		cmds = append(cmds, n)
	}
	if len(cmds) != len(c.Commands) || (len(cmds) > 0 && !reflect.DeepEqual(cmds, c.Commands)) {
		t.Errorf("%s: commands differ\n got: %q\nwant: %q", label, cmds, c.Commands)
	}
	files := map[string]string{}
	for rel := range listFiles(t, work) {
		if before[rel] || !(strings.HasSuffix(rel, ".txt") || strings.HasSuffix(rel, ".json")) {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(work, rel))
		files[rel] = norm(string(b))
	}
	if len(files) != len(c.Files) {
		keys := func(m map[string]string) []string {
			var k []string
			for x := range m {
				k = append(k, x)
			}
			sort.Strings(k)
			return k
		}
		t.Errorf("%s: files %v, want %v", label, keys(files), keys(c.Files))
	}
	for rel, want := range c.Files {
		if files[rel] != want {
			t.Errorf("%s: %s differs\n got: %q\nwant: %q", label, rel, files[rel], want)
		}
	}
}
