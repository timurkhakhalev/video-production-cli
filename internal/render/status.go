package render

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/jsonx"
	"video-production-cli/internal/pyfmt"
)

type ChunkStatus struct {
	ID        int     `json:"id"`
	Start     float64 `json:"start"`
	End       float64 `json:"end"`
	Status    string  `json:"status"`
	Cuts      int     `json:"cuts"`
	Speedups  int     `json:"speedups"`
	OutputLen float64 `json:"output_len"`
}

type StatusResult struct {
	Source      string        `json:"source"`
	Workdir     string        `json:"workdir"`
	Duration    float64       `json:"duration"`
	FPS         string        `json:"fps"`
	Mode        string        `json:"mode"`
	Keep        int           `json:"keep_fragments,omitempty"`
	Chunks      []ChunkStatus `json:"chunks"`
	OutputLen   float64       `json:"output_len"`
	Cuts        int           `json:"cuts"`
	Speedups    int           `json:"speedups"`
	Warnings    int           `json:"short_fragments"`
	ReviewFlags int           `json:"review_flags"`
	Lessons     int           `json:"lessons"`
	Final       string        `json:"final,omitempty"`
	FinalOK     *bool         `json:"final_ok,omitempty"`
	Next        string        `json:"next"`
}

func inside(t, a, b float64) bool { return t >= a-1e-6 && t < b-1e-6 }

// Status summarises the edit and says what to do next.
func Status(w io.Writer, e *edit.Edit) (StatusResult, error) {
	res := StatusResult{Source: e.Source, Workdir: e.Workdir, Duration: e.Duration, FPS: e.FPS.RatString()}
	if m, ok := e.Doc.Get("mode"); ok {
		if s, ok := m.(string); ok {
			res.Mode = s
		}
	}
	res.Keep = len(e.Keep)
	for _, c := range e.Chunks {
		cs := ChunkStatus{ID: c.IDInt(), Start: c.Start, End: c.End, Status: c.Status}
		for _, cut := range e.Cuts {
			if inside(cut.From, c.Start, c.End) && !cut.Implicit {
				cs.Cuts++
			}
		}
		for _, s := range e.Speedups {
			if inside(s.From, c.Start, c.End) {
				cs.Speedups++
			}
		}
		start, end := c.Start, c.End
		for _, k := range e.Keeps(&start, &end) {
			cs.OutputLen += e.OutLen(k.In, k.Out)
		}
		res.Chunks = append(res.Chunks, cs)
	}
	for _, k := range e.Keeps(nil, nil) {
		res.OutputLen += e.OutLen(k.In, k.Out)
		if k.Out-k.In < edit.MinKeep {
			res.Warnings++
		}
	}
	for _, c := range e.Cuts {
		if !c.Implicit {
			res.Cuts++
		}
	}
	res.Speedups = len(e.Speedups)
	if v, ok := e.Doc.Get("review_flags"); ok {
		if a, ok := v.([]jsonx.Value); ok {
			res.ReviewFlags = len(a)
		}
	}
	if v, ok := e.Doc.Get("lessons"); ok {
		if a, ok := v.([]jsonx.Value); ok {
			res.Lessons = len(a)
		}
	}
	if _, err := os.Stat(FinalPath(e)); err == nil {
		res.Final = FinalPath(e)
		if b, err := os.ReadFile(stripExt(res.Final) + ".verify.json"); err == nil {
			var r struct {
				OK bool `json:"ok"`
			}
			if json.Unmarshal(b, &r) == nil {
				res.FinalOK = &r.OK
			}
		}
	}
	res.Next = nextStep(res)

	tc := pyfmt.TC
	fmt.Fprintf(w, "source:  %s (%s, %s fps)\n", e.Source, tc(e.Duration), res.FPS)
	fmt.Fprintf(w, "workdir: %s\n", e.Workdir)
	mode := res.Mode
	if mode == "" {
		mode = "not set"
	}
	fmt.Fprintf(w, "mode:    %s\n", mode)
	if res.Keep > 0 {
		fmt.Fprintf(w, "keep:    %d fragment(s); everything outside them is cut\n", res.Keep)
	}
	fmt.Fprintln(w, "chunks:")
	for _, c := range res.Chunks {
		extra := fmt.Sprintf("%d cut(s)", c.Cuts)
		if c.Speedups > 0 {
			extra += fmt.Sprintf(", %d speed-up(s)", c.Speedups)
		}
		fmt.Fprintf(w, "  #%-2d %s - %s  %-9s  %s -> output %s\n", c.ID, tc(c.Start), tc(c.End), c.Status, extra, tc(c.OutputLen))
	}
	srcLen := e.Duration
	fmt.Fprintf(w, "total:   source %s -> output %s, %d cut(s), %d speed-up(s)\n", tc(srcLen), tc(res.OutputLen), res.Cuts, res.Speedups)
	if res.Warnings > 0 {
		fmt.Fprintf(w, "WARNING: %d kept fragment(s) shorter than %.1fs (see joins)\n", res.Warnings, edit.MinKeep)
	}
	fmt.Fprintf(w, "notes:   %d review flag(s), %d lesson(s)\n", res.ReviewFlags, res.Lessons)
	if res.Final != "" {
		state := "not verified"
		if res.FinalOK != nil {
			state = map[bool]string{true: "verify OK", false: "verify FAILED"}[*res.FinalOK]
		}
		fmt.Fprintf(w, "final:   %s (%s)\n", res.Final, state)
	}
	fmt.Fprintf(w, "next:    %s\n", res.Next)
	return res, nil
}

func nextStep(r StatusResult) string {
	if r.Mode == "" {
		return `ask the user for the mode (step: review each chunk; full: one review at the end) and set "mode" in edit.json`
	}
	var draft, review []string
	for _, c := range r.Chunks {
		switch c.Status {
		case "draft":
			draft = append(draft, fmt.Sprint(c.ID))
		case "in_review":
			review = append(review, fmt.Sprint(c.ID))
		}
	}
	if len(draft) == 0 && len(review) == 0 {
		switch {
		case r.Final == "":
			return "all chunks approved: final"
		case r.FinalOK != nil && *r.FinalOK:
			return "done: report the final file, the old and new length and the verification to the user"
		default:
			return "final exists but did not verify: report the failed checks to the user (do not fix them with ffmpeg)"
		}
	}
	if r.Mode == "full" {
		if len(draft) > 0 {
			return fmt.Sprintf("edit chunk(s) %s, check every join, then preview and `chunks set all in_review`", strings.Join(draft, ", "))
		}
		return "waiting for the user's review of the preview: apply remarks (map T), or on approval `chunks set all approved`"
	}
	for _, c := range r.Chunks {
		switch c.Status {
		case "draft":
			return fmt.Sprintf("edit chunk %d: watch it (proxy), add cuts, check every join (joins --chunk %d, frames/listen --join K), "+
				"then preview --chunk %d and `chunks set %d in_review`", c.ID, c.ID, c.ID, c.ID)
		case "in_review":
			return fmt.Sprintf("chunk %d waits for the user's review: apply remarks (map T --chunk %d), or on approval `chunks set %d approved`",
				c.ID, c.ID, c.ID)
		}
	}
	return ""
}
