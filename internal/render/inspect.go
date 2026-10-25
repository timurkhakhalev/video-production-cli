// Package render implements the commands that read an edit: joins, map, frames, proxy,
// silences, audio, listen, preview, final, verify. Text output of the commands ported from
// render.py matches it byte for byte; every command also returns a result for --json.
package render

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/ffmpeg"
	"video-production-cli/internal/graph"
	"video-production-cli/internal/jsonx"
	"video-production-cli/internal/pyfmt"
)

// ErrFailed means the command already printed a failed report; exit non-zero quietly.
var ErrFailed = errors.New("failed")

const ProxyMaxBytes = 90 * 1024 * 1024

// MaxSheetFrames limits a contact sheet (10 per row).
const MaxSheetFrames = 120

var tc = pyfmt.TC

// Media is what an inspection command reads: the edit's source, or a rendered file (--file).
type Media struct {
	Path     string
	FPS      *big.Rat
	Duration float64
	Workdir  string // where frames/, proxy/ go
	What     string // "source" or "file"
}

func (m Media) Snap(t float64) float64 { return edit.SnapFPS(m.FPS, t) }
func (m Media) FPSFloat() float64      { return pyfmt.RatFloat(m.FPS) }

// SourceMedia is the edit's source.
func SourceMedia(e *edit.Edit) Media {
	return Media{Path: e.Source, FPS: e.FPS, Duration: e.Duration, Workdir: e.Workdir, What: "source"}
}

// FileMedia probes a rendered file; outputs go to workdir (the edit's, or the file's folder).
func FileMedia(path, workdir string) (Media, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Media{}, err
	}
	info, err := ffmpeg.Probe(abs)
	if err != nil {
		return Media{}, err
	}
	m := Media{Path: abs, Workdir: workdir, What: "file",
		Duration: ffmpeg.Float(ffmpeg.Get(ffmpeg.GetObj(info, "format"), "duration"))}
	if workdir == "" {
		m.Workdir = scratchDir(abs)
	}
	m.FPS = big.NewRat(30, 1)
	if v := ffmpeg.Stream(info, "video"); v != nil {
		if r, ok := new(big.Rat).SetString(jsonx.Str(ffmpeg.Get(v, "r_frame_rate"))); ok && r.Sign() > 0 {
			m.FPS = r
		}
	}
	return m, nil
}

// scratchDir is where outputs for a file outside any edit go: the work folder the file
// lives in (previews/, the final), else a per-file folder in the user cache, never next
// to a shared source.
func scratchDir(abs string) string {
	for d, i := filepath.Dir(abs), 0; i < 2; d, i = filepath.Dir(d), i+1 {
		if _, err := os.Stat(filepath.Join(d, "edit.json")); err == nil {
			return d
		}
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	sum := sha1.Sum([]byte(abs))
	name := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	return filepath.Join(base, "video-production-cli", "files", fmt.Sprintf("%s-%x", name, sum[:4]))
}

func ensureDir(workdir, name string) (string, error) {
	dir := filepath.Join(workdir, name)
	return dir, os.MkdirAll(dir, 0o755)
}

func f4(x float64) string { return fmt.Sprintf("%.4f", x) }

// ---------------------------------------------------------------- joins / map

type JoinRow struct {
	N       int     `json:"n"`
	OutTime float64 `json:"out_time"`
	SrcOut  float64 `json:"src_out"`
	SrcIn   float64 `json:"src_in"`
	Removed float64 `json:"removed"`
	Reason  string  `json:"reason"`
}

type SpeedupRow struct {
	Speed    int     `json:"speed"`
	OutStart float64 `json:"out_start"`
	OutEnd   float64 `json:"out_end"`
	SrcIn    float64 `json:"src_in"`
	SrcOut   float64 `json:"src_out"`
	Reason   string  `json:"reason"`
}

type JoinsResult struct {
	Scope     string       `json:"scope"`
	SourceLen float64      `json:"source_len"`
	OutputLen float64      `json:"output_len"`
	Joins     []JoinRow    `json:"joins"`
	Speedups  []SpeedupRow `json:"speedups"`
	Warnings  []string     `json:"warnings"`
}

// Joins prints the join table of a scope (render.py cmd_joins).
func Joins(w io.Writer, e *edit.Edit, sc edit.Scope) (JoinsResult, error) {
	keeps, rows := e.JoinTable(sc.Start, sc.End)
	// number joins globally so they match `frames --join K`
	_, all := e.JoinTable(nil, nil)
	offset := 0
	if sc.Start != nil {
		for _, r := range all {
			if r.SrcOut <= *sc.Start+1e-6 {
				offset++
			}
		}
	}
	res := JoinsResult{Scope: sc.Label, Joins: []JoinRow{}, Speedups: []SpeedupRow{}, Warnings: []string{}}
	for _, k := range keeps {
		res.OutputLen += e.OutLen(k.In, k.Out)
	}
	end, start := e.Duration, 0.0
	if sc.End != nil {
		end = *sc.End
	}
	if sc.Start != nil {
		start = *sc.Start
	}
	res.SourceLen = end - start
	fmt.Fprintf(w, "%s: source %s -> output %s, %d join(s)\n", sc.Label, tc(res.SourceLen), tc(res.OutputLen), len(rows))
	for i, r := range rows {
		row := JoinRow{offset + 1 + i, r.OutTime, r.SrcOut, r.SrcIn, r.Removed, r.Reason}
		res.Joins = append(res.Joins, row)
		fmt.Fprintf(w, "#%-3d out %s | src %s -> %s | -%.2fs | %s\n",
			row.N, tc(r.OutTime), tc(r.SrcOut), tc(r.SrcIn), r.Removed, r.Reason)
	}
	reasons := map[[2]float64]string{}
	for _, s := range e.Speedups {
		reasons[[2]float64{e.Snap(s.From), e.Snap(s.To)}] = s.Reason
	}
	fps := e.FPSFloat()
	for _, p := range e.Timeline(keeps) {
		if p.Speed > 1 {
			outDur := float64(p.Frames) / fps
			row := SpeedupRow{p.Speed, p.OutStart, p.OutStart + outDur, p.In, p.Out, reasons[[2]float64{p.In, p.Out}]}
			res.Speedups = append(res.Speedups, row)
			fmt.Fprintf(w, "x%-3d out %s - %s | src %s - %s | %.1fs -> %.1fs, muted | %s\n",
				p.Speed, tc(row.OutStart), tc(row.OutEnd), tc(p.In), tc(p.Out), p.Out-p.In, outDur, row.Reason)
		}
	}
	for _, k := range keeps {
		if k.Out-k.In < edit.MinKeep {
			msg := fmt.Sprintf("kept fragment %s-%s is only %.2fs and will flash on screen; widen a cut to remove it",
				tc(k.In), tc(k.Out), k.Out-k.In)
			res.Warnings = append(res.Warnings, msg)
			fmt.Fprintln(w, "WARNING: "+msg)
		}
	}
	return res, nil
}

// MapResult is an output time converted to a source time.
type MapResult struct {
	Scope   string  `json:"scope"`
	Out     float64 `json:"out"`
	Source  float64 `json:"source"`
	Speed   int     `json:"speed"`
	PieceIn float64 `json:"piece_in"`
	PieceTo float64 `json:"piece_out"`
}

// MapTime converts a preview/final time of a scope to a source time.
func MapTime(e *edit.Edit, sc edit.Scope, t float64) (MapResult, error) {
	keeps, _ := e.JoinTable(sc.Start, sc.End)
	acc := 0.0
	fps := e.FPSFloat()
	for _, p := range e.Timeline(keeps) {
		acc = p.OutStart
		accEnd := acc + float64(p.Frames)/fps
		if t <= accEnd+1e-9 {
			src := math.Min(p.Out, p.In+(t-acc)*float64(p.Speed))
			return MapResult{Scope: sc.Label, Out: t, Source: src, Speed: p.Speed, PieceIn: p.In, PieceTo: p.Out}, nil
		}
		acc = accEnd
	}
	return MapResult{}, fmt.Errorf("time %s beyond %s output length %s", tc(t), sc.Label, tc(acc))
}

// Map prints MapTime like render.py cmd_map.
func Map(w io.Writer, e *edit.Edit, sc edit.Scope, t float64) (MapResult, error) {
	r, err := MapTime(e, sc, t)
	if err != nil {
		return r, err
	}
	note := ""
	if r.Speed > 1 {
		note = fmt.Sprintf("  (inside x%d speed-up %s - %s)", r.Speed, tc(r.PieceIn), tc(r.PieceTo))
	}
	fmt.Fprintf(w, "%s %s -> source %s (%.3f)%s\n", sc.Label, tc(t), tc(r.Source), r.Source, note)
	return r, nil
}

// joinRows returns join number k (1-based) over the whole edit.
func joinRows(e *edit.Edit, k int) ([]edit.Keep, []edit.Join, error) {
	keeps, rows := e.JoinTable(nil, nil)
	if k < 1 || k > len(rows) {
		return nil, nil, fmt.Errorf("join must be 1..%d (see `joins --all`)", len(rows))
	}
	return keeps, rows, nil
}

// ---------------------------------------------------------------- frames

type FramesResult struct {
	Sheet  string    `json:"sheet,omitempty"`
	Pair   string    `json:"pair,omitempty"`
	Frames []string  `json:"frames,omitempty"`
	Times  []float64 `json:"times,omitempty"`
	From   float64   `json:"from,omitempty"`
	To     float64   `json:"to,omitempty"`
	Cols   int64     `json:"cols,omitempty"`
	FPS    float64   `json:"fps,omitempty"`
}

// FramesJoin writes the contact sheet and the full-resolution pair around join k.
func FramesJoin(w io.Writer, e *edit.Edit, k int, span float64) (FramesResult, error) {
	fdir, err := ensureDir(e.Workdir, "frames")
	if err != nil {
		return FramesResult{}, err
	}
	fps := e.FPSFloat()
	_, rows, err := joinRows(e, k)
	if err != nil {
		return FramesResult{}, err
	}
	r := rows[k-1]
	n := pyfmt.RoundInt(span * fps)
	a0 := math.Max(0, r.SrcOut-span)
	sheet := filepath.Join(fdir, fmt.Sprintf("join%03d_sheet.jpg", k))
	pair := filepath.Join(fdir, fmt.Sprintf("join%03d_pair.jpg", k))
	if err := ffmpeg.Run(ffmpeg.FFmpeg, "-v", "error", "-y",
		"-ss", f4(a0), "-t", f4(r.SrcOut-a0), "-i", e.Source,
		"-ss", f4(r.SrcIn), "-t", f4(span), "-i", e.Source,
		"-filter_complex", fmt.Sprintf("[0:v]scale=384:-2,tile=%dx1[a];[1:v]scale=384:-2,tile=%dx1[b];[a][b]vstack[s]", n, n),
		"-map", "[s]", "-frames:v", "1", "-q:v", "3", sheet); err != nil {
		return FramesResult{}, err
	}
	last := e.Snap(r.SrcOut) - 1.0/fps
	if err := ffmpeg.Run(ffmpeg.FFmpeg, "-v", "error", "-y",
		"-ss", f4(last), "-i", e.Source,
		"-ss", f4(r.SrcIn), "-i", e.Source,
		"-filter_complex", "[0:v]trim=end_frame=1[a];[1:v]trim=end_frame=1[b];[a][b]hstack[s]",
		"-map", "[s]", "-frames:v", "1", "-q:v", "2", pair); err != nil {
		return FramesResult{}, err
	}
	fmt.Fprintf(w, "join #%d: src %s -> %s (%s)\n", k, tc(r.SrcOut), tc(r.SrcIn), r.Reason)
	fmt.Fprintln(w, "sheet: "+sheet)
	fmt.Fprintf(w, "  top row: last %d frames before the cut (left->right, rightmost is the last kept frame)\n", n)
	fmt.Fprintf(w, "  bottom row: first %d frames after the cut (leftmost is the first kept frame)\n", n)
	fmt.Fprintln(w, "pair:  "+pair+"  (left: last frame before, right: first frame after; full resolution)")
	return FramesResult{Sheet: sheet, Pair: pair, From: r.SrcOut, To: r.SrcIn}, nil
}

// FramesRange writes a sheet of a range: every frame, or `every` frames per second when > 0.
func FramesRange(w io.Writer, m Media, from, to, every float64) (FramesResult, error) {
	fdir, err := ensureDir(m.Workdir, "frames")
	if err != nil {
		return FramesResult{}, err
	}
	fps := m.FPSFloat()
	a, b := m.Snap(from), m.Snap(to)
	if every <= 0 {
		n := pyfmt.RoundInt((b - a) * fps)
		if n <= 0 || n > MaxSheetFrames {

			return FramesResult{}, fmt.Errorf("range must cover 1..%d frames (got %d)", MaxSheetFrames, n)
		}
		cols := n
		if cols > 10 {
			cols = 10
		}
		rows := int64(math.Ceil(float64(n) / float64(cols)))
		name := fmt.Sprintf("range_%.3f_%.3f.jpg", a, b)
		if m.What == "file" {
			name = "file_" + name
		}
		sheet := filepath.Join(fdir, name)
		if err := ffmpeg.Run(ffmpeg.FFmpeg, "-v", "error", "-y", "-ss", f4(a), "-t", f4(b-a), "-i", m.Path,
			"-vf", fmt.Sprintf("scale=384:-2,tile=%dx%d", cols, rows), "-frames:v", "1", "-q:v", "3", sheet); err != nil {
			return FramesResult{}, err
		}
		fmt.Fprintln(w, "sheet: "+sheet)
		fmt.Fprintf(w, "frames left->right, top->bottom; cell (row r, col c) = %s %.3f + ((r-1)*%d + (c-1)) / %s s\n",
			m.What, a, cols, pyfmt.G(fps))
		return FramesResult{Sheet: sheet, From: a, To: b, Cols: cols, FPS: fps}, nil
	}
	n := int64(math.Floor((b-a)*every + 1e-9))
	if n <= 0 || n > MaxSheetFrames {
		return FramesResult{}, fmt.Errorf("--fps %s over %.3f s gives %d frames; the sheet takes 1..%d", pyfmt.G(every), b-a, n, MaxSheetFrames)
	}
	cols := n
	if cols > 10 {
		cols = 10
	}
	rows := int64(math.Ceil(float64(n) / float64(cols)))
	name := fmt.Sprintf("range_%.3f_%.3f_fps%s.jpg", a, b, pyfmt.G(every))
	if m.What == "file" {
		name = "file_" + name
	}
	sheet := filepath.Join(fdir, name)
	// one frame at a, a+1/every, ...: fps filter with start time 0 picks the frame nearest each tick
	if err := ffmpeg.Run(ffmpeg.FFmpeg, "-v", "error", "-y", "-ss", f4(a), "-t", f4(b-a), "-i", m.Path,
		"-vf", fmt.Sprintf("fps=%s:round=near,scale=384:-2,tile=%dx%d", pyfmt.G(every), cols, rows),
		"-frames:v", "1", "-q:v", "3", sheet); err != nil {
		return FramesResult{}, err
	}
	fmt.Fprintln(w, "sheet: "+sheet)
	fmt.Fprintf(w, "%d frames left->right, top->bottom; cell (row r, col c) ~= %s %.3f + ((r-1)*%d + (c-1)) / %s s (sampled, not every frame)\n",
		n, m.What, a, cols, pyfmt.G(every))
	return FramesResult{Sheet: sheet, From: a, To: b, Cols: cols, FPS: every}, nil
}

// FramesAt writes single full-resolution frames at the given times (snapped to frames).
func FramesAt(w io.Writer, m Media, times []float64) (FramesResult, error) {
	fdir, err := ensureDir(m.Workdir, "frames")
	if err != nil {
		return FramesResult{}, err
	}
	res := FramesResult{}
	for _, t := range times {
		s := m.Snap(t)
		if s < 0 || s >= m.Duration {
			return res, fmt.Errorf("time %.3f outside the %s (0 .. %.3f)", t, m.What, m.Duration)
		}
		name := fmt.Sprintf("at_%.3f.jpg", s)
		if m.What == "file" {
			name = "file_" + name
		}
		out := filepath.Join(fdir, name)
		if err := ffmpeg.Run(ffmpeg.FFmpeg, "-v", "error", "-y", "-ss", f4(s), "-i", m.Path,
			"-frames:v", "1", "-q:v", "2", out); err != nil {
			return res, err
		}
		res.Frames = append(res.Frames, out)
		res.Times = append(res.Times, s)
		fmt.Fprintf(w, "%s %.3f (%s): %s\n", m.What, s, tc(s), out)
	}
	return res, nil
}

// ---------------------------------------------------------------- proxy

type ProxyResult struct {
	Proxy  string  `json:"proxy"`
	Offset float64 `json:"offset"`
	To     float64 `json:"to"`
	Bytes  int64   `json:"bytes"`
}

// Proxy writes a small 540p copy of a source range.
func Proxy(w io.Writer, e *edit.Edit, from, to float64) (ProxyResult, error) {
	a, b := math.Max(0, from), math.Min(e.Duration, to)
	if b <= a {
		return ProxyResult{}, fmt.Errorf("empty range")
	}
	pdir, err := ensureDir(e.Workdir, "proxy")
	if err != nil {
		return ProxyResult{}, err
	}
	out := filepath.Join(pdir, fmt.Sprintf("proxy_%.3f_%.3f.mp4", a, b))
	argv := append([]string{ffmpeg.FFmpeg, "-v", "error", "-y", "-ss", f4(a), "-t", f4(b - a), "-i", e.Source},
		graph.ProxyArgs...)
	argv = append(argv, "-movflags", "+faststart", out)
	if err := ffmpeg.Run(argv...); err != nil {
		return ProxyResult{}, err
	}
	st, err := os.Stat(out)
	if err != nil {
		return ProxyResult{}, err
	}
	size := st.Size()
	fmt.Fprintln(w, "proxy: "+out)
	fmt.Fprintf(w, "source time = %.3f + time in proxy  (proxy %s .. %s of source)\n", a, tc(a), tc(b))
	fmt.Fprintf(w, "size: %.1f MB\n", float64(size)/1048576.0)
	if size > ProxyMaxBytes {
		fmt.Fprintf(w, "WARNING: larger than %d MB; split the range\n", ProxyMaxBytes/1048576)
	}
	return ProxyResult{Proxy: out, Offset: a, To: b, Bytes: size}, nil
}

// ---------------------------------------------------------------- silences

// DetectSilences runs silencedetect over a range; times are in the media's seconds.
func DetectSilences(path string, from, to, noise, min float64) ([]ffmpeg.Silence, error) {
	stderr, err := ffmpeg.Stderr(ffmpeg.FFmpeg, "-hide_banner", "-nostats", "-ss", f4(from), "-t", f4(to-from),
		"-i", path, "-map", "0:a:0",
		"-af", fmt.Sprintf("silencedetect=noise=%sdB:d=%s", pyfmt.G(noise), pyfmt.G(min)), "-f", "null", "-")
	if err != nil {
		return nil, err
	}
	sil := ffmpeg.ParseSilences(stderr)
	for i := range sil {
		sil[i].Start += from
		if sil[i].Open {
			sil[i].End = to
		} else {
			sil[i].End += from
		}
	}
	return sil, nil
}

type SilencesResult struct {
	What   string           `json:"what"`
	From   float64          `json:"from"`
	To     float64          `json:"to"`
	Noise  float64          `json:"noise_db"`
	Min    float64          `json:"min"`
	Pauses []ffmpeg.Silence `json:"pauses"`
}

// Silences lists pauses in a range of the source (or a rendered file).
func Silences(w io.Writer, m Media, from, to, noise, min float64) (SilencesResult, error) {
	a, b := math.Max(0, from), math.Min(m.Duration, to)
	sil, err := DetectSilences(m.Path, a, b, noise, min)
	if err != nil {
		return SilencesResult{}, err
	}
	res := SilencesResult{What: m.What, From: a, To: b, Noise: noise, Min: min, Pauses: sil}
	if res.Pauses == nil {
		res.Pauses = []ffmpeg.Silence{}
	}
	fmt.Fprintf(w, "pauses in %s %s .. %s (noise %sdB, min %ss), %s seconds:\n", m.What, tc(a), tc(b), pyfmt.G(noise), pyfmt.G(min), m.What)
	for _, s := range sil {
		if s.Open {
			fmt.Fprintf(w, "  %.3f - %.3f  (open)\n", s.Start, s.End)
			continue
		}
		fmt.Fprintf(w, "  %.3f - %.3f  (%.2fs)  %s\n", s.Start, s.End, s.End-s.Start, tc(s.Start))
	}
	if len(sil) == 0 {
		fmt.Fprintln(w, "  none")
	}
	return res, nil
}

type ChunkMark struct {
	Mark   float64          `json:"mark"`
	Pauses []ffmpeg.Silence `json:"pauses"`
}

// SuggestChunks lists the longest pauses near each nominal chunk mark.
func SuggestChunks(w io.Writer, e *edit.Edit, every, window float64) ([]ChunkMark, error) {
	stderr, err := ffmpeg.Stderr(ffmpeg.FFmpeg, "-hide_banner", "-nostats", "-i", e.Source, "-map", "0:a:0",
		"-af", "silencedetect=noise=-40dB:d=0.4", "-f", "null", "-")
	if err != nil {
		return nil, err
	}
	var sil []ffmpeg.Silence
	for _, s := range ffmpeg.ParseSilences(stderr) {
		if !s.Open {
			sil = append(sil, s)
		}
	}
	res := []ChunkMark{}
	for mark := every; mark < e.Duration-window; mark += every {
		near := []ffmpeg.Silence{}
		for _, s := range sil {
			if math.Abs((s.Start+s.End)/2-mark) <= window {
				near = append(near, s)
			}
		}
		sort.SliceStable(near, func(i, j int) bool { return near[i].End-near[i].Start > near[j].End-near[j].Start })
		if len(near) > 5 {
			near = near[:5]
		}
		res = append(res, ChunkMark{Mark: mark, Pauses: near})
		fmt.Fprintf(w, "around %s:\n", tc(mark))
		for _, s := range near {
			fmt.Fprintf(w, "  pause %s - %s (%.2fs), boundary candidate %.3f\n",
				tc(s.Start), tc(s.End), s.End-s.Start, (s.Start+s.End)/2)
		}
		if len(near) == 0 {
			fmt.Fprintln(w, "  no pause >= 0.4s in window")
		}
	}
	return res, nil
}
