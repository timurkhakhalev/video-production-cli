// Package join probes sources into encode.json, joins parts of one video without
// re-encoding, and creates the work folder (render.py init).
package join

import (
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"video-production-cli/internal/ffmpeg"
	"video-production-cli/internal/jsonx"
	"video-production-cli/internal/pyfmt"
)

// ProbeEncode captures the source stream parameters (encode.json).
func ProbeEncode(source string) (*jsonx.Object, error) {
	info, err := ffmpeg.Probe(source)
	if err != nil {
		return nil, err
	}
	v, a := ffmpeg.Stream(info, "video"), ffmpeg.Stream(info, "audio")
	if v == nil || a == nil {
		return nil, fmt.Errorf("source must have one video and one audio stream")
	}
	g := ffmpeg.Get
	if rf, af := g(v, "r_frame_rate"), g(v, "avg_frame_rate"); rf != af {
		return nil, fmt.Errorf("source looks like variable frame rate (r_frame_rate=%s avg_frame_rate=%s). "+
			"Frame-accurate cutting is not supported for VFR; stop and ask the user.", jsonx.Str(rf), jsonx.Str(af))
	}
	for _, s := range []*jsonx.Object{v, a} {
		if math.Abs(ffmpeg.Float(g(s, "start_time"))) > 1e-3 {
			return nil, fmt.Errorf("stream %s starts at %s, expected 0", jsonx.Str(g(s, "index")), jsonx.Str(g(s, "start_time")))
		}
	}
	n := func(x int64) jsonx.Number { return jsonx.Number(fmt.Sprint(x)) }
	aBitrate := ffmpeg.Int(g(a, "bit_rate"))
	if aBitrate == 0 {
		aBitrate = 192000
	}
	// never below source, rounded up to 32k, at least 192k
	bitRate := int64(math.Ceil(float64(aBitrate)/32000.0)) * 32000
	if bitRate < 192000 {
		bitRate = 192000
	}
	video := jsonx.NewObject().
		Set("codec", g(v, "codec_name")).
		Set("profile", g(v, "profile")).
		Set("level", g(v, "level")).
		Set("pix_fmt", g(v, "pix_fmt")).
		Set("width", g(v, "width")).
		Set("height", g(v, "height")).
		Set("fps", g(v, "r_frame_rate")).
		Set("time_base", g(v, "time_base")).
		Set("color_range", g(v, "color_range")).
		Set("color_space", g(v, "color_space")).
		Set("color_primaries", g(v, "color_primaries")).
		Set("color_transfer", g(v, "color_transfer")).
		Set("source_bit_rate", n(ffmpeg.Int(g(v, "bit_rate"))))
	audio := jsonx.NewObject().
		Set("codec", g(a, "codec_name")).
		Set("profile", g(a, "profile")).
		Set("sample_rate", n(ffmpeg.Int(g(a, "sample_rate")))).
		Set("channels", g(a, "channels")).
		Set("channel_layout", g(a, "channel_layout")).
		Set("source_bit_rate", n(aBitrate)).
		Set("bit_rate", n(bitRate))
	return jsonx.NewObject().
		Set("video", video).
		Set("audio", audio).
		Set("duration", ffmpeg.Float(ffmpeg.Get(ffmpeg.GetObj(info, "format"), "duration"))), nil
}

// joinKeys are the stream parameters that must match for a lossless join.
var joinKeys = []struct {
	kind string
	keys []string
}{
	{"video", []string{"codec", "profile", "level", "pix_fmt", "width", "height", "fps", "time_base",
		"color_range", "color_space", "color_primaries", "color_transfer"}},
	{"audio", []string{"codec", "profile", "sample_rate", "channels", "channel_layout"}},
}

// firstPTS is the earliest packet pts of the first video ("v") or audio ("a") stream.
func firstPTS(path, kind string) (*big.Rat, error) {
	out, err := ffmpeg.Output(ffmpeg.FFprobe, "-v", "error", "-select_streams", kind+":0", "-read_intervals", "%+#8",
		"-show_entries", "packet=pts:stream=time_base", "-of", "json", path)
	if err != nil {
		return nil, err
	}
	v, err := jsonx.Parse([]byte(out))
	if err != nil {
		return nil, err
	}
	info, _ := v.(*jsonx.Object)
	streams, _ := ffmpeg.Get(info, "streams").([]jsonx.Value)
	if len(streams) == 0 {
		return nil, fmt.Errorf("%s: no %s stream", path, kind)
	}
	tb, ok := new(big.Rat).SetString(jsonx.Str(ffmpeg.Get(streams[0].(*jsonx.Object), "time_base")))
	if !ok {
		return nil, fmt.Errorf("%s: bad time_base", path)
	}
	packets, _ := ffmpeg.Get(info, "packets").([]jsonx.Value)
	var min *int64
	for _, p := range packets {
		pts := ffmpeg.Get(p.(*jsonx.Object), "pts")
		if pts == nil {
			continue
		}
		x := ffmpeg.Int(pts)
		if min == nil || x < *min {
			min = &x
		}
	}
	if min == nil {
		return nil, fmt.Errorf("%s: no packets with pts", path)
	}
	return new(big.Rat).Mul(new(big.Rat).SetInt64(*min), tb), nil
}

func videoFrames(path string) (int64, error) {
	info, err := ffmpeg.Probe(path)
	if err != nil {
		return 0, err
	}
	return ffmpeg.Int(ffmpeg.Get(ffmpeg.Stream(info, "video"), "nb_frames")), nil
}

func streamTimeBase(path, kind string) (*big.Rat, error) {
	info, err := ffmpeg.Probe(path)
	if err != nil {
		return nil, err
	}
	streams, _ := ffmpeg.Get(info, "streams").([]jsonx.Value)
	for _, s := range streams {
		o := s.(*jsonx.Object)
		if t, _ := ffmpeg.Get(o, "codec_type").(string); strings.HasPrefix(t, kind) {
			tb, ok := new(big.Rat).SetString(jsonx.Str(ffmpeg.Get(o, "time_base")))
			if !ok {
				return nil, fmt.Errorf("%s: bad time_base", path)
			}
			return tb, nil
		}
	}
	return nil, fmt.Errorf("%s: no %s stream", path, kind)
}

// JoinedPath is the default path of the joined source: the common prefix of the part names.
func JoinedPath(sources []string) string {
	stems := make([]string, len(sources))
	for i, p := range sources {
		stems[i] = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
	}
	prefix := stems[0]
	for _, s := range stems[1:] {
		prefix = commonPrefix(prefix, s)
	}
	stem := strings.TrimRight(prefix, " -_.")
	for _, s := range stems {
		if stem == s {
			stem = ""
		}
	}
	if stem == "" {
		stem = stems[0] + "-joined"
	}
	return filepath.Join(filepath.Dir(sources[0]), stem+filepath.Ext(sources[0]))
}

func commonPrefix(a, b string) string {
	ra, rb := []rune(a), []rune(b)
	i := 0
	for i < len(ra) && i < len(rb) && ra[i] == rb[i] {
		i++
	}
	return string(ra[:i])
}

// Sources concatenates parts of one video without re-encoding and returns the parts list.
func Sources(sources []string, out string) ([]jsonx.Value, error) {
	encs := make([]*jsonx.Object, len(sources))
	for i, p := range sources {
		e, err := ProbeEncode(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		encs[i] = e
	}
	for i := 1; i < len(sources); i++ {
		for _, jk := range joinKeys {
			got, want := ffmpeg.GetObj(encs[i], jk.kind), ffmpeg.GetObj(encs[0], jk.kind)
			for _, k := range jk.keys {
				if g, w := ffmpeg.Get(got, k), ffmpeg.Get(want, k); jsonx.Repr(g) != jsonx.Repr(w) {
					return nil, fmt.Errorf("%s: %s %s is %s, but %s in %s; parts must be recorded with the same settings",
						sources[i], jk.kind, k, jsonx.Repr(g), jsonx.Repr(w), sources[0])
				}
			}
		}
	}
	var frames int64
	for _, p := range sources {
		n, err := videoFrames(p)
		if err != nil {
			return nil, err
		}
		frames += n
	}
	tmp, err := os.MkdirTemp(filepath.Dir(out), ".join-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	listing := filepath.Join(tmp, "parts.txt")
	var sb strings.Builder
	for _, p := range sources {
		fmt.Fprintf(&sb, "file '%s'\n", strings.ReplaceAll(p, "'", `'\''`))
	}
	if err := os.WriteFile(listing, []byte(sb.String()), 0o644); err != nil {
		return nil, err
	}
	raw := filepath.Join(tmp, "raw"+filepath.Ext(out))
	if err := ffmpeg.Run(ffmpeg.FFmpeg, "-v", "error", "-y", "-f", "concat", "-safe", "0", "-i", listing,
		"-map", "0:v:0", "-map", "0:a:0", "-c", "copy", raw); err != nil {
		return nil, err
	}
	// AAC priming (first audio packet before 0) makes the muxer shift the streams by ~20 ms,
	// not by the same amount; put every stream's first packet back where it was in part 1
	var bsf []string
	for _, kind := range []string{"v", "a"} {
		tb, err := streamTimeBase(raw, kind)
		if err != nil {
			return nil, err
		}
		got, err := firstPTS(raw, kind)
		if err != nil {
			return nil, err
		}
		want, err := firstPTS(sources[0], kind)
		if err != nil {
			return nil, err
		}
		shift := pyfmt.RoundRat(new(big.Rat).Quo(new(big.Rat).Sub(got, want), tb))
		if shift != 0 {
			bsf = append(bsf, "-bsf:"+kind, fmt.Sprintf("setts=pts=PTS-%d:dts=DTS-%d", shift, shift))
		}
	}
	if len(bsf) > 0 {
		argv := append([]string{ffmpeg.FFmpeg, "-v", "error", "-y", "-i", raw, "-map", "0", "-c", "copy"}, bsf...)
		argv = append(argv, "-movflags", "+faststart", out)
		if err := ffmpeg.Run(argv...); err != nil {
			return nil, err
		}
	} else if err := os.Rename(raw, out); err != nil {
		return nil, err
	}
	joined, err := ProbeEncode(out)
	if err != nil {
		return nil, err
	}
	got, err := videoFrames(out)
	if err != nil {
		return nil, err
	}
	if got != frames {
		return nil, fmt.Errorf("joined file has %d frames, parts have %d; check %s", got, frames, out)
	}
	var parts []jsonx.Value
	t := 0.0
	for i, p := range sources {
		d := ffmpeg.Float(ffmpeg.Get(encs[i], "duration"))
		parts = append(parts, jsonx.NewObject().Set("path", p).Set("start", pyfmt.Round(t, 6)).Set("duration", d))
		t += d
	}
	fps, _ := new(big.Rat).SetString(jsonx.Str(ffmpeg.Get(ffmpeg.GetObj(encs[0], "video"), "fps")))
	jd := ffmpeg.Float(ffmpeg.Get(joined, "duration"))
	if math.Abs(jd-t) > 2.0/pyfmt.RatFloat(fps) {
		return nil, fmt.Errorf("joined duration %.3f differs from the parts' sum %.3f", jd, t)
	}
	return parts, nil
}

type Fragment struct{ From, To float64 }

// Options of Init.
type Options struct {
	Sources      []string
	Joined       string
	Workdir      string
	ChunkMinutes float64
	Keep         []Fragment
}

// Init creates <name>-edit/ with encode.json and edit.json.
func Init(w io.Writer, opt Options) (string, error) {
	var sources []string
	for _, p := range opt.Sources {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return "", fmt.Errorf("source not found: %s", abs)
		}
		sources = append(sources, abs)
	}
	if len(sources) == 0 {
		return "", fmt.Errorf("no source given")
	}
	source := sources[0]
	if len(sources) > 1 {
		j := opt.Joined
		if j == "" {
			j = JoinedPath(sources)
		}
		var err error
		if source, err = filepath.Abs(j); err != nil {
			return "", err
		}
		for _, s := range sources {
			if s == source {
				return "", fmt.Errorf("--joined must differ from the sources")
			}
		}
		if _, err := os.Stat(source); err == nil {
			return "", fmt.Errorf("%s already exists; delete it or pass --joined PATH", source)
		}
	}
	workdir := opt.Workdir
	if workdir == "" {
		workdir = strings.TrimSuffix(source, filepath.Ext(source)) + "-edit"
	}
	workdir, err := filepath.Abs(workdir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return "", err
	}
	editPath := filepath.Join(workdir, "edit.json")
	encodePath := filepath.Join(workdir, "encode.json")
	if _, err := os.Stat(editPath); err == nil {
		return "", fmt.Errorf("edit.json already exists in %s; refusing to overwrite", workdir)
	}
	var parts []jsonx.Value
	if len(sources) > 1 {
		fmt.Fprintf(w, "joining %d parts without re-encoding -> %s\n", len(sources), source)
		if parts, err = Sources(sources, source); err != nil {
			return "", err
		}
	}
	enc, err := ProbeEncode(source)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(encodePath, jsonx.Dump(enc), 0o644); err != nil {
		return "", err
	}
	duration := ffmpeg.Float(ffmpeg.Get(enc, "duration"))
	minutes := opt.ChunkMinutes
	if minutes <= 0 {
		minutes = 10
	}
	chunks := NominalChunks(0, duration, minutes*60.0, 1)
	var keeps []jsonx.Value
	if len(opt.Keep) > 0 {
		sort.Slice(opt.Keep, func(i, j int) bool { return opt.Keep[i].From < opt.Keep[j].From })
		chunks = nil
		last := -1.0
		for _, f := range opt.Keep {
			if math.IsNaN(f.From) || math.IsNaN(f.To) || f.From < 0 || f.To > duration || f.To <= f.From || f.From < last {
				return "", fmt.Errorf("invalid or overlapping keep range %.3f-%.3f (duration %.3f)", f.From, f.To, duration)
			}
			keeps = append(keeps, jsonx.NewObject().Set("from", f.From).Set("to", f.To).Set("reason", "selected fragment"))
			chunks = append(chunks, NominalChunks(f.From, f.To, minutes*60, len(chunks)+1)...)
			last = f.To
		}
	}
	data := jsonx.NewObject().Set("version", jsonx.Number("1")).Set("source", source)
	if parts != nil {
		data.Set("parts", parts)
	}
	if keeps != nil {
		data.Set("keep", keeps)
	}
	data.Set("mode", nil).
		Set("markers", []jsonx.Value{"Удалить"}).
		Set("chunks", chunks).
		Set("cuts", []jsonx.Value{}).
		Set("review_flags", []jsonx.Value{}).
		Set("lessons", []jsonx.Value{})
	if err := os.WriteFile(editPath, jsonx.Dump(data), 0o644); err != nil {
		return "", err
	}
	fmt.Fprintln(w, "workdir:     "+workdir)
	fmt.Fprintln(w, "edit.json:   "+editPath)
	fmt.Fprintln(w, "encode.json: "+encodePath)
	fmt.Fprintf(w, "duration %s, %d chunk(s) at nominal %d min; refine boundaries with suggest-chunks\n",
		pyfmt.TC(duration), len(chunks), int64(minutes))
	for i, p := range parts {
		o := p.(*jsonx.Object)
		start := ffmpeg.Float(ffmpeg.Get(o, "start"))
		fmt.Fprintf(w, "part %d starts at source %s (%.3f): %s\n", i+1, pyfmt.TC(start), start, jsonx.Str(ffmpeg.Get(o, "path")))
	}
	return editPath, nil
}

// NominalChunks splits [start, end) every step seconds without leaving a tail shorter than
// 30% of a step; ids start at firstID.
func NominalChunks(start, end, step float64, firstID int) []jsonx.Value {
	var chunks []jsonx.Value
	n := firstID
	for start < end-1e-6 {
		e := math.Min(end, start+step)
		if end-e < step*0.3 { // do not leave a tiny tail chunk
			e = end
		}
		chunks = append(chunks, jsonx.NewObject().
			Set("id", jsonx.Number(fmt.Sprint(n))).
			Set("start", pyfmt.Round(start, 3)).
			Set("end", pyfmt.Round(e, 3)).
			Set("status", "draft"))
		start, n = e, n+1
	}
	return chunks
}
