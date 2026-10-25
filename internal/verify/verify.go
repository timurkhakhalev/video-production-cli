// Package verify checks a rendered preview or final against the edit and the source.
package verify

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/ffmpeg"
	"video-production-cli/internal/jsonx"
	"video-production-cli/internal/pyfmt"
)

// MinPSNR: CRF 15 is ~54 dB against the source; 45 dB catches a wrong frame or broken picture.
const MinPSNR = 45

type Check struct {
	Name   string `json:"check"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

type Report struct {
	File   string  `json:"file"`
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

// JSON renders the report like render.py's save_json.
func (r Report) JSON() []byte {
	checks := make([]jsonx.Value, len(r.Checks))
	for i, c := range r.Checks {
		checks[i] = jsonx.NewObject().Set("check", c.Name).Set("ok", c.OK).Set("detail", c.Detail)
	}
	return jsonx.Dump(jsonx.NewObject().Set("file", r.File).Set("ok", r.OK).Set("checks", checks))
}

// Print writes the human-readable report.
func (r Report) Print(w io.Writer) {
	if r.OK {
		fmt.Fprintln(w, "verify: OK")
	} else {
		fmt.Fprintln(w, "verify: FAILED")
	}
	for _, c := range r.Checks {
		mark := "ok"
		if !c.OK {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "  [%s] %s: %s\n", mark, c.Name, c.Detail)
	}
}

// Run verifies a rendered file of the given kept ranges.
func Run(e *edit.Edit, path string, keeps []edit.Keep, final bool) (Report, error) {
	info, err := ffmpeg.Probe(path)
	if err != nil {
		return Report{}, err
	}
	v, a := ffmpeg.Stream(info, "video"), ffmpeg.Stream(info, "audio")
	if v == nil || a == nil {
		return Report{}, fmt.Errorf("%s: expected one video and one audio stream", path)
	}
	fps := e.FPSFloat()
	var frames int64
	for _, p := range e.Timeline(keeps) {
		frames += p.Frames
	}
	expected := float64(frames) / fps
	vdur, adur := ffmpeg.Float(ffmpeg.Get(v, "duration")), ffmpeg.Float(ffmpeg.Get(a, "duration"))
	nb := ffmpeg.Get(v, "nb_frames")
	checks := []Check{
		{"video duration ~= expected", math.Abs(vdur-expected) <= 1.5/fps, fmt.Sprintf("%.3f vs %.3f", vdur, expected)},
		{"audio ~= video duration", math.Abs(adur-vdur) <= 0.05, fmt.Sprintf("a=%.3f v=%.3f", adur, vdur)},
		{"frame count", absInt(ffmpeg.Int(nb)-frames) <= 1, fmt.Sprintf("%s vs %d", jsonx.Str(nb), frames)},
	}
	if final {
		ev, ea := e.EncVideo(), e.EncAudio()
		g := ffmpeg.Get
		size := func(o *jsonx.Object) string {
			return jsonx.Str(g(o, "width")) + "x" + jsonx.Str(g(o, "height"))
		}
		asInt := func(x jsonx.Value) jsonx.Value { return jsonx.Number(strconv.FormatInt(ffmpeg.Int(x), 10)) }
		want := []struct {
			name     string
			got, exp jsonx.Value
		}{
			{"video codec", g(v, "codec_name"), g(ev, "codec")},
			{"profile", g(v, "profile"), g(ev, "profile")},
			{"level", g(v, "level"), g(ev, "level")},
			{"pix_fmt", g(v, "pix_fmt"), g(ev, "pix_fmt")},
			{"size", size(v), size(ev)},
			{"fps", g(v, "r_frame_rate"), g(ev, "fps")},
			{"time_base", g(v, "time_base"), g(ev, "time_base")},
			{"color_range", g(v, "color_range"), g(ev, "color_range")},
			{"color_space", g(v, "color_space"), g(ev, "color_space")},
			{"color_primaries", g(v, "color_primaries"), g(ev, "color_primaries")},
			{"color_transfer", g(v, "color_transfer"), g(ev, "color_transfer")},
			{"audio codec", g(a, "codec_name"), g(ea, "codec")},
			{"sample_rate", asInt(g(a, "sample_rate")), g(ea, "sample_rate")},
			{"channels", g(a, "channels"), g(ea, "channels")},
		}
		for _, w := range want {
			checks = append(checks, Check{w.name, pyEqual(w.got, w.exp),
				fmt.Sprintf("%s vs source %s", jsonx.Str(w.got), jsonx.Str(w.exp))})
		}
		// ffmpeg aac spends fewer bits on silence than requested, so allow some slack
		ab := ffmpeg.Int(g(a, "bit_rate"))
		src := ffmpeg.Int(g(ea, "source_bit_rate"))
		checks = append(checks, Check{"audio bitrate not far below source", float64(ab) >= float64(src)*0.8,
			fmt.Sprintf("%d vs source %d (requested %d)", ab, src, ffmpeg.Int(g(ea, "bit_rate")))})
		// bitrate says little (screen recorders often encode in hardware, x264 is far more efficient); compare pixels
		psnr, err := SamplePSNR(e, path, keeps, 10.0)
		if err != nil {
			return Report{}, err
		}
		checks = append(checks, Check{fmt.Sprintf("picture matches source (PSNR >= %d dB)", MinPSNR), psnr >= MinPSNR,
			fmt.Sprintf("%s dB, worst of first/middle/last kept 1x segments", pyfmt.F(psnr, 1))})
	}
	r := Report{File: path, OK: true, Checks: checks}
	for _, c := range checks {
		r.OK = r.OK && c.OK
	}
	return r, nil
}

func absInt(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// pyEqual compares decoded JSON values like Python's ==.
func pyEqual(a, b jsonx.Value) bool {
	na, aNum := a.(jsonx.Number)
	nb, bNum := b.(jsonx.Number)
	if aNum && bNum {
		return na.Float() == nb.Float()
	}
	if aNum || bNum {
		return false
	}
	return a == b
}

func psnr(out string, outT float64, source string, srcT, dur float64) (float64, error) {
	stderr, err := ffmpeg.Stderr(ffmpeg.FFmpeg, "-hide_banner", "-nostats", "-ss", fmt.Sprintf("%.6f", outT), "-i", out,
		"-ss", fmt.Sprintf("%.6f", srcT), "-i", source,
		"-lavfi", fmt.Sprintf("[0:v]trim=0:%.6f,setpts=PTS-STARTPTS[x];[1:v]trim=0:%.6f,setpts=PTS-STARTPTS[y];[x][y]psnr", dur, dur),
		"-f", "null", "-")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "PSNR") && strings.Contains(line, "average:") {
			val := strings.Fields(strings.SplitN(line, "average:", 2)[1])
			if len(val) == 0 {
				continue
			}
			if val[0] == "inf" {
				return math.Inf(1), nil
			}
			f, err := pyfmt.ParseFloat(val[0])
			if err != nil {
				return 0, err
			}
			return f, nil
		}
	}
	return 0, nil
}

// SamplePSNR is the lowest PSNR over the first, middle and last kept 1x pieces.
// Float seeks can land one frame apart on the two files, so each sample tries offsets
// of -1/0/+1 frame and keeps the best alignment.
func SamplePSNR(e *edit.Edit, path string, keeps []edit.Keep, seconds float64) (float64, error) {
	var pieces []edit.Piece
	for _, p := range e.Timeline(keeps) {
		if p.Speed == 1 {
			pieces = append(pieces, p)
		}
	}
	if len(pieces) == 0 {
		return 0, nil
	}
	picks := []int{0} // sorted(set([0, mid, last])): the values never decrease
	for _, i := range []int{len(pieces) / 2, len(pieces) - 1} {
		if i != picks[len(picks)-1] {
			picks = append(picks, i)
		}
	}
	frame := 1.0 / e.FPSFloat()
	worst := math.Inf(1)
	for _, i := range picks {
		p := pieces[i]
		dur := math.Min(seconds, p.Out-p.In-2*frame)
		if dur <= frame {
			continue
		}
		best := math.Inf(-1)
		for _, d := range []float64{-1, 0, 1} {
			v, err := psnr(path, math.Max(0, p.OutStart+frame+d*frame), e.Source, p.In+frame, dur)
			if err != nil {
				return 0, err
			}
			best = math.Max(best, v)
		}
		worst = math.Min(worst, best)
	}
	if math.IsInf(worst, 1) {
		return 0, nil
	}
	return worst, nil
}
