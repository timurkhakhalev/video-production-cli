// Package audio analyses a stretch of the sound track: a loudness envelope an agent can read
// as text, pauses with exact edges, and where a cut may sit inside each pause.
package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"

	"video-production-cli/internal/ffmpeg"
)

// SampleRate of the analysis: speech needs no more, and it keeps decoding instant.
const SampleRate = 16000

// Join rules (see the cutting section of the skill): leave this much sound around a cut.
const (
	DecayAfter   = 0.10 // after the last sound of the previous phrase (80–120 ms)
	OnsetBefore  = 0.12 // before the first sound of the next phrase (100–150 ms)
	DefaultNoise = -32.0
	DefaultMin   = 0.08
	DefaultStep  = 0.02
)

// Decode returns mono float samples of [from, from+dur) of the first audio stream of path.
func Decode(path string, from, dur float64) ([]float32, error) {
	out, err := ffmpeg.Output(ffmpeg.FFmpeg, "-v", "error", "-ss", fmt.Sprintf("%.4f", from), "-t", fmt.Sprintf("%.4f", dur),
		"-i", path, "-map", "0:a:0", "-ac", "1", "-ar", fmt.Sprint(SampleRate), "-f", "f32le", "-")
	if err != nil {
		return nil, err
	}
	b := []byte(out)
	s := make([]float32, len(b)/4)
	for i := range s {
		s[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return s, nil
}

// Pause is a stretch where every sample stays below the noise threshold.
type Pause struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	// CutFrom is the earliest a cut may start here (decay of the previous sound kept);
	// CutTo the latest a cut may end here (onset of the next sound kept). Nil at the
	// edges of the analysed range, where the sound outside is unknown.
	CutFrom *float64 `json:"cut_from,omitempty"`
	CutTo   *float64 `json:"cut_to,omitempty"`
	// Middle of the safe window, when the pause is long enough for both margins.
	Suggest *float64 `json:"suggest,omitempty"`
}

func (p Pause) Len() float64 { return p.End - p.Start }

// Analysis of one range.
type Analysis struct {
	Source  string    `json:"source"`
	From    float64   `json:"from"`
	To      float64   `json:"to"`
	Step    float64   `json:"step"`
	Noise   float64   `json:"noise_db"`
	Min     float64   `json:"min_pause"`
	DB      []float64 `json:"db"` // RMS level per step, dBFS (-120 for digital silence)
	Pauses  []Pause   `json:"pauses"`
	At      *float64  `json:"at,omitempty"`
	Nearest *int      `json:"nearest,omitempty"` // index into Pauses
}

// Analyze computes the envelope and pauses of samples that start at time `from`.
func Analyze(samples []float32, from, step, noise, min float64) Analysis {
	a := Analysis{From: from, To: from + float64(len(samples))/SampleRate, Step: step, Noise: noise, Min: min}
	win := int(math.Round(step * SampleRate))
	if win < 1 {
		win = 1
	}
	for i := 0; i < len(samples); i += win {
		end := i + win
		if end > len(samples) {
			end = len(samples)
		}
		var sum float64
		for _, s := range samples[i:end] {
			sum += float64(s) * float64(s)
		}
		a.DB = append(a.DB, toDB(math.Sqrt(sum/float64(end-i))))
	}
	thr := math.Pow(10, noise/20)
	minN := int(math.Ceil(min * SampleRate))
	t := func(i int) float64 { return from + float64(i)/SampleRate }
	run := -1
	flush := func(end int) {
		if run >= 0 && end-run >= minN {
			p := Pause{Start: t(run), End: t(end)}
			if run > 0 {
				v := p.Start + DecayAfter
				p.CutFrom = &v
			}
			if end < len(samples) {
				v := p.End - OnsetBefore
				p.CutTo = &v
			}
			if p.CutFrom != nil && p.CutTo != nil && *p.CutTo >= *p.CutFrom {
				v := (*p.CutFrom + *p.CutTo) / 2
				p.Suggest = &v
			}
			a.Pauses = append(a.Pauses, p)
		}
		run = -1
	}
	for i, s := range samples {
		if math.Abs(float64(s)) < thr {
			if run < 0 {
				run = i
			}
		} else {
			flush(i)
		}
	}
	flush(len(samples))
	return a
}

func toDB(rms float64) float64 {
	if rms <= 1e-6 {
		return -120
	}
	return 20 * math.Log10(rms)
}

// SetAt marks the pause nearest to t (distance 0 inside a pause).
func (a *Analysis) SetAt(t float64) {
	a.At = &t
	best, bestD := -1, math.Inf(1)
	for i, p := range a.Pauses {
		d := 0.0
		if t < p.Start {
			d = p.Start - t
		} else if t > p.End {
			d = t - p.End
		}
		if d < bestD {
			best, bestD = i, d
		}
	}
	if best >= 0 {
		a.Nearest = &best
	}
}

// levels: '.' far below the threshold, '▁' below it (a pause), then seven steps up to -6 dBFS.
var levels = []rune("▂▃▄▅▆▇█")

func cell(db, noise float64) rune {
	switch {
	case db < noise-20:
		return '.'
	case db < noise:
		return '▁'
	}
	top := -6.0
	if noise >= top {
		return levels[len(levels)-1]
	}
	i := int((db - noise) / (top - noise) * float64(len(levels)))
	if i >= len(levels) {
		i = len(levels) - 1
	}
	return levels[i]
}

// Print writes the envelope (one row per second), the pauses and the cut windows.
// snap rounds a time to the frame grid (identity when unknown).
func (a Analysis) Print(w io.Writer, what string, snap func(float64) float64) {
	perRow := int(math.Round(1 / a.Step))
	if perRow < 1 {
		perRow = 1
	}
	tick := int(math.Round(0.1 / a.Step))
	fmt.Fprintf(w, "%s %.3f .. %.3f, step %d ms, level per cell (RMS dBFS): '.' < %g, '▁' < %g (pause), ▂..█ up to -6\n",
		what, a.From, a.To, int(math.Round(a.Step*1000)), a.Noise-20, a.Noise)
	if tick >= 1 && perRow > tick {
		var r strings.Builder
		for i := 0; i < perRow; i++ {
			if i%tick == 0 {
				r.WriteByte('|')
			} else {
				r.WriteByte(' ')
			}
		}
		fmt.Fprintf(w, "%-10s %s  (| every 100 ms)\n", "", strings.TrimRight(r.String(), " "))
	}
	for i := 0; i < len(a.DB); i += perRow {
		end := i + perRow
		if end > len(a.DB) {
			end = len(a.DB)
		}
		var row strings.Builder
		for _, db := range a.DB[i:end] {
			row.WriteRune(cell(db, a.Noise))
		}
		fmt.Fprintf(w, "%-10.2f %s\n", a.From+float64(i)*a.Step, row.String())
	}
	fmt.Fprintf(w, "pauses (< %g dB, >= %d ms):\n", a.Noise, int(math.Round(a.Min*1000)))
	if len(a.Pauses) == 0 {
		fmt.Fprintln(w, "  none")
	}
	for i, p := range a.Pauses {
		line := fmt.Sprintf("  %.3f - %.3f (%.2fs)", p.Start, p.End, p.Len())
		var win []string
		if p.CutFrom != nil {
			win = append(win, fmt.Sprintf("cut from >= %.3f", *p.CutFrom))
		}
		if p.CutTo != nil {
			win = append(win, fmt.Sprintf("cut to <= %.3f", *p.CutTo))
		}
		if len(win) > 0 {
			line += "  " + strings.Join(win, ", ")
		}
		if a.Nearest != nil && *a.Nearest == i {
			line += fmt.Sprintf("   <- nearest to --at %.3f", *a.At)
		}
		fmt.Fprintln(w, line)
	}
	if a.Nearest == nil {
		return
	}
	p := a.Pauses[*a.Nearest]
	switch {
	case p.Suggest != nil:
		fmt.Fprintf(w, "suggested cut point: %.3f (frame %.3f), middle of the safe window %.3f .. %.3f inside the pause\n",
			*p.Suggest, snap(*p.Suggest), *p.CutFrom, *p.CutTo)
	case p.CutFrom != nil && p.CutTo != nil:
		fmt.Fprintf(w, "pause %.3f - %.3f is too short for both margins (%.0f ms decay + %.0f ms before onset); "+
			"a cut can start or end here, not both; or use a longer pause\n", p.Start, p.End, DecayAfter*1000, OnsetBefore*1000)
	default:
		fmt.Fprintln(w, "nearest pause touches the edge of the range; widen FROM TO to see the sound beyond it")
	}
}
