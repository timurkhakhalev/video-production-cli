package audio

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

// tone with gaps: [0,0.5) tone, [0.5,0.8) silence, [0.8,1.0) tone, [1.0,1.05) silence (too short), [1.05,1.5) tone,
// [1.5,1.6) silence at the end of the range
func synth() []float32 {
	var s []float32
	for i := 0; i < int(1.6*SampleRate); i++ {
		t := float64(i) / SampleRate
		silent := (t >= 0.5 && t < 0.8) || (t >= 1.0 && t < 1.05) || t >= 1.5
		v := 0.0
		if !silent {
			v = 0.5 * math.Sin(2*math.Pi*440*t)
		}
		s = append(s, float32(v))
	}
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) < 2e-3 }

func TestAnalyzePauses(t *testing.T) {
	a := Analyze(synth(), 100, DefaultStep, DefaultNoise, DefaultMin)
	if len(a.DB) != 80 {
		t.Fatalf("cells: %d", len(a.DB))
	}
	if len(a.Pauses) != 2 {
		t.Fatalf("pauses: %+v", a.Pauses)
	}
	p := a.Pauses[0]
	if !near(p.Start, 100.5) || !near(p.End, 100.8) {
		t.Fatalf("pause 0: %+v", p)
	}
	if !near(*p.CutFrom, 100.6) || !near(*p.CutTo, 100.68) || !near(*p.Suggest, 100.64) {
		t.Fatalf("cut window: %v %v %v", *p.CutFrom, *p.CutTo, *p.Suggest)
	}
	// the trailing pause touches the end of the range: the onset after it is unknown
	last := a.Pauses[1]
	if !near(last.Start, 101.5) || last.CutTo != nil || last.Suggest != nil || last.CutFrom == nil {
		t.Fatalf("pause 1: %+v", last)
	}
	// tone cells are loud, silent cells digital silence
	if a.DB[0] < -10 || a.DB[30] != -120 {
		t.Fatalf("levels: %v %v", a.DB[0], a.DB[30])
	}
}

func TestPrintMarksNearestAndSuggests(t *testing.T) {
	a := Analyze(synth(), 100, DefaultStep, DefaultNoise, DefaultMin)
	a.SetAt(100.9)
	var b bytes.Buffer
	a.Print(&b, "source", func(x float64) float64 { return math.Round(x*30) / 30 })
	out := b.String()
	for _, want := range []string{
		"source 100.000 .. 101.600, step 20 ms",
		"100.00     █████████████████████████...............██████████",
		"101.00     ..▇██████████████████████.....", // the 50 ms gap shows but is no pause
		"  100.500 - 100.800 (0.30s)  cut from >= 100.600, cut to <= 100.680   <- nearest to --at 100.900",
		"suggested cut point: 100.640 (frame 100.633)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}
