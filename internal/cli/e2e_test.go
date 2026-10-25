//go:build e2e

package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"video-production-cli/internal/jsonx"
)

// End-to-end on synthetic media: testsrc2 picture, a 440 Hz tone that is silent during the
// first second of every 3 s (known pauses), h264/aac like a screen recording.

func ffmpegGen(t *testing.T, out string, seconds int, size string, rate string, extra ...string) {
	t.Helper()
	args := []string{"-v", "error", "-y",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%s:rate=%s:duration=%d", size, rate, seconds),
		"-f", "lavfi", "-i", fmt.Sprintf("aevalsrc='0.5*sin(2*PI*440*t)*gte(mod(t\\,3)\\,1)|0.5*sin(2*PI*440*t)*gte(mod(t\\,3)\\,1)':s=48000:d=%d", seconds),
		"-c:v", "libx264", "-profile:v", "high", "-pix_fmt", "yuv420p", "-g", "30",
		"-c:a", "aac", "-b:a", "128k", "-shortest"}
	args = append(args, extra...)
	args = append(args, out)
	if b, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, b)
	}
}

func cli(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run("e2e", args, &out, &errb)
	return out.String(), errb.String(), code
}

func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, errs, code := cli(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d\nstdout:\n%s\nstderr:\n%s", args, code, out, errs)
	}
	return out
}

func readObj(t *testing.T, path string) *jsonx.Object {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonx.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return v.(*jsonx.Object)
}

func writeObj(t *testing.T, path string, o *jsonx.Object) {
	t.Helper()
	if err := os.WriteFile(path, jsonx.Dump(o), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sha(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func TestE2E(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := filepath.Join(dir, "lesson-1.mp4"), filepath.Join(dir, "lesson-2.mp4")
	ffmpegGen(t, p1, 10, "320x240", "30")
	ffmpegGen(t, p2, 10, "320x240", "30")

	out := mustCLI(t, "init", p1, p2, "--chunk-minutes", "0.2")
	joined := filepath.Join(dir, "lesson.mp4")
	editPath := filepath.Join(dir, "lesson-edit", "edit.json")
	for _, want := range []string{"joining 2 parts without re-encoding -> " + joined, "2 chunk(s)", "part 2 starts at source 0:10."} {
		if !strings.Contains(out, want) {
			t.Fatalf("init output lacks %q:\n%s", want, out)
		}
	}

	// render.py on the same parts must produce the same joined file and encode.json, and the
	// same edit.json apart from the version field.
	if _, err := exec.LookPath("python3"); err == nil {
		pyDir := filepath.Join(dir, "py")
		os.MkdirAll(pyDir, 0o755)
		for _, p := range []string{p1, p2} {
			b, _ := os.ReadFile(p)
			os.WriteFile(filepath.Join(pyDir, filepath.Base(p)), b, 0o644)
		}
		cmd := exec.Command("python3", "../../testdata/golden/render.py", "init",
			filepath.Join(pyDir, "lesson-1.mp4"), filepath.Join(pyDir, "lesson-2.mp4"), "--chunk-minutes", "0.2")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("render.py init: %v\n%s", err, b)
		}
		if sha(t, joined) != sha(t, filepath.Join(pyDir, "lesson.mp4")) {
			t.Errorf("joined file differs from render.py's")
		}
		goEnc, _ := os.ReadFile(filepath.Join(dir, "lesson-edit", "encode.json"))
		pyEnc, _ := os.ReadFile(filepath.Join(pyDir, "lesson-edit", "encode.json"))
		if string(goEnc) != string(pyEnc) {
			t.Errorf("encode.json differs:\n%s\nvs render.py:\n%s", goEnc, pyEnc)
		}
		goEdit := readObj(t, editPath)
		goEdit.Delete("version")
		pyEdit, _ := os.ReadFile(filepath.Join(pyDir, "lesson-edit", "edit.json"))
		if got := strings.ReplaceAll(string(jsonx.Dump(goEdit)), dir, pyDir); got != string(pyEdit) {
			t.Errorf("edit.json differs:\n%s\nvs render.py:\n%s", got, pyEdit)
		}
	}

	// cut inside chunk 1, speed-up inside chunk 2
	doc := readObj(t, editPath)
	doc.Set("cuts", []jsonx.Value{jsonx.NewObject().Set("from", 3.0).Set("to", 4.0).Set("reason", "пауза")})
	doc.Set("speedups", []jsonx.Value{jsonx.NewObject().Set("from", 13.0).Set("to", 17.0).Set("speed", jsonx.Number("4")).Set("reason", "ожидание")})
	writeObj(t, editPath, doc)

	out = mustCLI(t, "joins", editPath)
	for _, want := range []string{"all: source 0:20.000 -> output 0:16.000, 1 join(s)",
		"#1   out 0:03.000 | src 0:03.000 -> 0:04.000 | -1.00s | пауза",
		"x4   out 0:12.000 - 0:13.000 | src 0:13.000 - 0:17.000 | 4.0s -> 1.0s, muted | ожидание"} {
		if !strings.Contains(out, want) {
			t.Fatalf("joins lacks %q:\n%s", want, out)
		}
	}
	if out := mustCLI(t, "map", "12.5", "--edit", editPath); !strings.Contains(out, "-> source 0:15.000 (15.000)  (inside x4") {
		t.Fatalf("map: %s", out)
	}

	// the edit folder can be found from the source folder
	wd, _ := os.Getwd()
	os.Chdir(dir)
	out = mustCLI(t, "preview", "--chunk", "1")
	os.Chdir(wd)
	if !strings.Contains(out, "verify: OK") {
		t.Fatalf("preview chunk 1:\n%s", out)
	}
	if out := mustCLI(t, "preview", "--chunk", "2", "--edit", editPath); !strings.Contains(out, "verify: OK") {
		t.Fatalf("preview chunk 2:\n%s", out)
	}

	_, errs, code := cli(t, "final", "--edit", editPath)
	if code == 0 || !strings.Contains(errs, "chunks not approved: [1, 2]") {
		t.Fatalf("final must refuse unapproved chunks: %d %s", code, errs)
	}
	chunks, _ := doc.Get("chunks")
	for _, c := range chunks.([]jsonx.Value) {
		c.(*jsonx.Object).Set("status", "approved")
	}
	writeObj(t, editPath, doc)
	out = mustCLI(t, "final", "--edit", editPath)
	if !strings.Contains(out, "verify: OK") || !strings.Contains(out, "picture matches source") {
		t.Fatalf("final:\n%s", out)
	}
	var report struct {
		OK bool `json:"ok"`
	}
	b, _ := os.ReadFile(filepath.Join(dir, "lesson-edit", "lesson_edit.verify.json"))
	if json.Unmarshal(b, &report) != nil || !report.OK {
		t.Fatalf("verify.json: %s", b)
	}
	if out := mustCLI(t, "verify", "--final", "--edit", editPath); !strings.Contains(out, "verify: OK") {
		t.Fatalf("verify --final:\n%s", out)
	}

	// inspection commands produce their files
	mustCLI(t, "frames", "--join", "1", "--edit", editPath)
	mustCLI(t, "frames", "--range", "1", "2", "--edit", editPath)
	mustCLI(t, "listen", "--join", "1", "--video", "--edit", editPath)
	mustCLI(t, "proxy", "0", "5", "--edit", editPath)
	for _, f := range []string{"frames/join001_sheet.jpg", "frames/join001_pair.jpg", "frames/range_1.000_2.000.jpg",
		"frames/join001_listen.mp4", "proxy/proxy_0.000_5.000.mp4"} {
		if st, err := os.Stat(filepath.Join(dir, "lesson-edit", f)); err != nil || st.Size() == 0 {
			t.Errorf("%s not written", f)
		}
	}
	// the tone is silent in [3k, 3k+1): silences must find those pauses
	out = mustCLI(t, "silences", "2", "8", "--edit", editPath)
	for _, want := range []string{"  3.0", "  6.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("silences lacks a pause at %s:\n%s", want, out)
		}
	}
}

func TestE2ERefusals(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.mp4"), filepath.Join(dir, "b.mp4")
	ffmpegGen(t, a, 3, "320x240", "30")
	ffmpegGen(t, b, 3, "640x480", "30")
	_, errs, code := cli(t, "init", a, b)
	if code == 0 || !strings.Contains(errs, "parts must be recorded with the same settings") {
		t.Fatalf("mismatched parts: %d %s", code, errs)
	}

	vfr := filepath.Join(dir, "vfr.mp4")
	ffmpegGen(t, vfr, 3, "320x240", "30", "-vf", "setpts='if(lt(N,30),N,N*2)/30/TB'", "-fps_mode", "vfr")
	_, errs, code = cli(t, "init", vfr)
	if code == 0 || !strings.Contains(errs, "variable frame rate") {
		t.Fatalf("vfr: %d %s", code, errs)
	}
}

func TestE2EInspectionAndKeep(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "talk.mp4")
	work := filepath.Join(dir, "short-edit")
	ep := filepath.Join(work, "edit.json")
	ffmpegGen(t, src, 9, "320x240", "30")
	mustCLI(t, "init", src, "--workdir", work, "--keep", "1", "4", "--keep", "6-8", "--json")
	e := readObj(t, ep)
	e.Set("mode", "full")
	writeObj(t, ep, e)
	mustCLI(t, "chunks", "set", "all", "approved", "--edit", ep)
	out := mustCLI(t, "final", "--edit", ep, "--json")
	var result struct {
		File   string `json:"file"`
		Verify struct {
			OK bool `json:"ok"`
		} `json:"verify"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || !result.Verify.OK {
		t.Fatalf("final: %v %s", err, out)
	}
	mustCLI(t, "audio", "2", "7", "--at", "3.5", "--edit", ep, "--json")
	mustCLI(t, "audio", "0", "3", "--file", result.File, "--json")
	mustCLI(t, "silences", "--range", "0", "3", "--file", result.File, "--json")
	mustCLI(t, "frames", "--at", "1", "2", "--edit", ep, "--json")
	mustCLI(t, "frames", "--range", "0", "8", "--fps", "2", "--edit", ep, "--json")
	mustCLI(t, "listen", "--try", "2", "3", "--edit", ep, "--json")
	mustCLI(t, "listen", "--join", "1", "--edit", ep, "--json")
	mustCLI(t, "listen", "--range", "1", "4", "--edit", ep, "--json")
	mustCLI(t, "listen", "--range", "0", "2", "--file", result.File, "--json")
	for _, name := range []string{"join001_listen.mp3", "range_1.000_4.000.mp3", "try_2.000_3.000.mp3"} {
		path := filepath.Join(work, "frames", name)
		b, err := exec.Command("ffprobe", "-v", "error", "-show_streams", "-of", "json", path).Output()
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Streams []struct {
				Codec    string `json:"codec_name"`
				Rate     string `json:"sample_rate"`
				Channels int    `json:"channels"`
			}
		}
		if err := json.Unmarshal(b, &p); err != nil || len(p.Streams) != 1 || p.Streams[0].Codec != "mp3" || p.Streams[0].Rate != "16000" || p.Streams[0].Channels != 1 {
			t.Fatalf("mp3 params: %s %v", b, err)
		}
	}
	// Gapless decoding must preserve the source onset: silence [3,4) starts 2 s into range 1..4.
	b, err := exec.Command("ffmpeg", "-v", "error", "-i", filepath.Join(work, "frames", "range_1.000_4.000.mp3"), "-ac", "1", "-ar", "16000", "-f", "f32le", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	var lastLoud float64
	for i := 0; i+4 <= len(b); i += 4 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(b[i:]))
		if math.Abs(float64(v)) > 0.05 {
			lastLoud = float64(i/4) / 16000
		}
	}
	if math.Abs(lastLoud-2) > 0.03 {
		t.Fatalf("decoded MP3 silence starts at %.4f; expected 2s", lastLoud)
	}
}
