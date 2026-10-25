package render

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"

	"video-production-cli/internal/audio"
	"video-production-cli/internal/edit"
	"video-production-cli/internal/ffmpeg"
	"video-production-cli/internal/graph"
)

type ListenResult struct {
	File         string  `json:"file"`
	JoinAt       float64 `json:"join_at,omitempty"` // seconds into the clip
	Offset       float64 `json:"offset,omitempty"`  // --range: source time = offset + clip time
	SrcOut       float64 `json:"src_out,omitempty"`
	SrcIn        float64 `json:"src_in,omitempty"`
	EncoderDelay float64 `json:"encoder_delay,omitempty"`
}

// encodeListen renders the kept ranges of e as a listening clip: mp3 (default) or 540p mp4.
// Gapless metadata lets decoders skip encoder delay without cutting source samples.
func encodeListen(e *edit.Edit, keeps []edit.Keep, out string, video bool) error {
	if video {
		g := strings.ReplaceAll(graph.Build(e, keeps), "\n", "") + ";[v]scale=-2:540[vs]"
		argv := []string{ffmpeg.FFmpeg, "-v", "error", "-y", "-i", e.Source, "-filter_complex", g, "-map", "[vs]", "-map", "[a]"}
		argv = append(argv, graph.ProxyArgs[2:]...)
		argv = append(argv, "-movflags", "+faststart", out)
		return ffmpeg.Run(argv...)
	}
	g := strings.ReplaceAll(graph.BuildAudio(e, keeps), "\n", "") + ";[a]aresample=16000[out]"
	argv := []string{ffmpeg.FFmpeg, "-v", "error", "-y", "-i", e.Source, "-filter_complex", g, "-map", "[out]"}
	argv = append(argv, graph.ListenMP3Args...)
	argv = append(argv, out)
	return ffmpeg.Run(argv...)
}

func ext(video bool) string {
	if video {
		return ".mp4"
	}
	return ".mp3"
}

// ListenJoin writes a short clip around join k, cut exactly as in the render.
func ListenJoin(w io.Writer, e *edit.Edit, k int, span float64, video bool) (ListenResult, error) {
	keeps, rows, err := joinRows(e, k)
	if err != nil {
		return ListenResult{}, err
	}
	i := k - 1
	before, after := keeps[i], keeps[i+1]
	seg := []edit.Keep{
		{In: math.Max(before.In, before.Out-span), Out: before.Out, Cut: before.Cut},
		{In: after.In, Out: math.Min(after.Out, after.In+span)},
	}
	fdir, err := ensureDir(e.Workdir, "frames")
	if err != nil {
		return ListenResult{}, err
	}
	out := filepath.Join(fdir, fmt.Sprintf("join%03d_listen%s", k, ext(video)))
	if err := encodeListen(e, seg, out, video); err != nil {
		return ListenResult{}, err
	}
	at := e.OutLen(seg[0].In, seg[0].Out)
	fmt.Fprintf(w, "join #%d: src %s -> %s (%s)\n", k, tc(rows[i].SrcOut), tc(rows[i].SrcIn), rows[i].Reason)
	fmt.Fprintln(w, "listen: "+out)
	delay := listenDelay(w, video)
	fmt.Fprintf(w, "  the join is at %.2fs into the clip; check that no word or breath is clipped on either side\n", at)
	return ListenResult{File: out, EncoderDelay: delay, JoinAt: at, SrcOut: rows[i].SrcOut, SrcIn: rows[i].SrcIn}, nil
}

// ListenTry renders how a cut FROM-TO would sound, with the edit's other cuts and speed-ups
// applied, before it goes into edit.json.
func ListenTry(w io.Writer, e *edit.Edit, from, to, span float64, video bool) (ListenResult, error) {
	tried, err := e.WithCut(from, to, "try")
	if err != nil {
		return ListenResult{}, err
	}
	ws, we := math.Max(0, from-span), math.Min(e.Duration, to+span)
	keeps := tried.Keeps(&ws, &we)
	if len(keeps) == 0 {
		return ListenResult{}, fmt.Errorf("nothing would be left around %.3f-%.3f", from, to)
	}
	a := e.Snap(from)
	joinAt, found := 0.0, false
	for _, k := range keeps {
		if k.In >= a {
			found = true
			break
		}
		joinAt += tried.OutLen(k.In, k.Out)
	}
	fdir, err := ensureDir(e.Workdir, "frames")
	if err != nil {
		return ListenResult{}, err
	}
	out := filepath.Join(fdir, fmt.Sprintf("try_%.3f_%.3f%s", from, to, ext(video)))
	if err := encodeListen(tried, keeps, out, video); err != nil {
		return ListenResult{}, err
	}
	b := e.Snap(to)
	fmt.Fprintf(w, "try cut %.3f - %.3f (frames %.3f - %.3f, -%.2fs), not written to edit.json\n", from, to, a, b, b-a)
	fmt.Fprintln(w, "listen: "+out)
	delay := listenDelay(w, video)
	if found {
		fmt.Fprintf(w, "  the join is at %.2fs into the clip (%.1fs of context on each side, other cuts applied)\n", joinAt, span)
	} else {
		fmt.Fprintln(w, "  the cut reaches the end of the source: the clip ends where it starts")
	}
	fmt.Fprintf(w, "  open the clip and listen to it; if it sounds clean, save it: listen --try %.3f %.3f --apply --reason \"...\"\n", from, to)
	return ListenResult{File: out, EncoderDelay: delay, JoinAt: joinAt, SrcOut: a, SrcIn: b}, nil
}

// ListenRange writes a range of the source (or a rendered file) as is.
func ListenRange(w io.Writer, m Media, from, to float64, video bool) (ListenResult, error) {
	a, b := math.Max(0, from), math.Min(m.Duration, to)
	if b <= a {
		return ListenResult{}, fmt.Errorf("empty range")
	}
	if b-a > 300 {
		return ListenResult{}, fmt.Errorf("range is %.0f s; listen to at most 300 s at a time (use proxy to watch longer stretches)", b-a)
	}
	fdir, err := ensureDir(m.Workdir, "frames")
	if err != nil {
		return ListenResult{}, err
	}
	name := fmt.Sprintf("range_%.3f_%.3f%s", a, b, ext(video))
	if m.What == "file" {
		name = "file_" + name
	}
	out := filepath.Join(fdir, name)
	var argv []string
	if video {
		argv = append([]string{ffmpeg.FFmpeg, "-v", "error", "-y", "-ss", f4(a), "-t", f4(b - a), "-i", m.Path}, graph.ProxyArgs...)
		argv = append(argv, "-movflags", "+faststart", out)
	} else {
		argv = []string{ffmpeg.FFmpeg, "-v", "error", "-y", "-ss", f4(a), "-t", f4(b - a), "-i", m.Path,
			"-map", "0:a:0", "-ar", "16000"}
		argv = append(argv, graph.ListenMP3Args...)
		argv = append(argv, out)
	}
	if err := ffmpeg.Run(argv...); err != nil {
		return ListenResult{}, err
	}
	fmt.Fprintln(w, "listen: "+out)
	delay := listenDelay(w, video)
	fmt.Fprintf(w, "%s time = %.3f + time in clip  (%s .. %s)\n", m.What, a, tc(a), tc(b))
	return ListenResult{File: out, EncoderDelay: delay, Offset: a}, nil
}

// Audio prints the loudness envelope and pauses of a range, and where a cut fits.
func Audio(w io.Writer, m Media, from, to float64, at *float64, step, noise, min float64) (audio.Analysis, error) {
	a, b := math.Max(0, from), math.Min(m.Duration, to)
	if b <= a {
		return audio.Analysis{}, fmt.Errorf("empty range")
	}
	if b-a > 60 {
		return audio.Analysis{}, fmt.Errorf("range is %.1f s; audio shows at most 60 s (narrow it to the area of the cut)", b-a)
	}
	samples, err := audio.Decode(m.Path, a, b-a)
	if err != nil {
		return audio.Analysis{}, err
	}
	res := audio.Analyze(samples, a, step, noise, min)
	res.Source = m.Path
	if at != nil {
		res.SetAt(*at)
	}
	res.Print(w, m.What, m.Snap)
	return res, nil
}

func listenDelay(w io.Writer, video bool) float64 {
	if video {
		return 0
	}
	fmt.Fprintln(w, "  MP3 times use gapless decoding; players ignoring LAME/Xing metadata add about 0.069s to clip times")
	return graph.MP3Delay
}
