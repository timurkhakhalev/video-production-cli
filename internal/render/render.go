package render

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/ffmpeg"
	"video-production-cli/internal/graph"
	"video-production-cli/internal/verify"
)

func stripExt(p string) string { return strings.TrimSuffix(p, filepath.Ext(p)) }

// renderKeeps writes the graph next to the output and runs ffmpeg once on the source.
func renderKeeps(w io.Writer, e *edit.Edit, sc edit.Scope, out string, final bool) ([]edit.Keep, error) {
	keeps := e.Keeps(sc.Start, sc.End)
	if len(keeps) == 0 {
		return nil, fmt.Errorf("nothing left to render in this range")
	}
	codec := append(append([]string{}, graph.PreviewVideo...), graph.PreviewAudio...)
	if final {
		var err error
		if codec, err = graph.FinalCodecArgs(e); err != nil {
			return nil, err
		}
	}
	graphPath := stripExt(out) + ".graph.txt"
	if err := os.WriteFile(graphPath, []byte(graph.Build(e, keeps)), 0o644); err != nil {
		return nil, err
	}
	argv := []string{ffmpeg.FFmpeg, "-hide_banner", "-v", "error", "-stats", "-y", "-i", e.Source,
		"-/filter_complex", graphPath, "-map", "[v]", "-map", "[a]", "-fps_mode", "passthrough"}
	argv = append(argv, codec...)
	argv = append(argv, "-movflags", "+faststart", out)
	fmt.Fprintln(w, "graph: "+graphPath)
	fmt.Fprintln(w, "render: "+strings.Join(argv, " "))
	if err := ffmpeg.Run(argv...); err != nil {
		return nil, err
	}
	return keeps, nil
}

// Preview renders a fast x264 preview of a scope into previews/ and verifies its durations.
func Preview(w io.Writer, e *edit.Edit, sc edit.Scope) (RenderResult, error) {
	pdir, err := ensureDir(e.Workdir, "previews")
	if err != nil {
		return RenderResult{}, err
	}
	out := filepath.Join(pdir, "preview_"+sc.Label+".mp4")
	keeps, err := renderKeeps(w, e, sc, out, false)
	if err != nil {
		return RenderResult{}, err
	}
	report, err := verify.Run(e, out, keeps, false)
	if err != nil {
		return RenderResult{}, err
	}
	fmt.Fprintln(w, "preview: "+out)
	report.Print(w)
	res := RenderResult{File: out, Graph: stripExt(out) + ".graph.txt", Verify: report}
	if !report.OK {
		return res, ErrFailed
	}
	return res, nil
}

// RenderResult is the --json result of preview and final.
type RenderResult struct {
	File   string        `json:"file"`
	Graph  string        `json:"graph"`
	Verify verify.Report `json:"verify"`
}

// FinalPath is where `final` writes the video.
func FinalPath(e *edit.Edit) string {
	stem := stripExt(filepath.Base(e.Source))
	return filepath.Join(e.Workdir, stem+"_edit.mp4")
}

// Final renders the whole edit once with the source parameters, verifies it and writes .verify.json.
func Final(w io.Writer, e *edit.Edit, force bool) (RenderResult, error) {
	var pending []string
	for _, c := range e.Chunks {
		if c.Status != "approved" {
			pending = append(pending, c.IDString())
		}
	}
	if len(pending) > 0 && !force {
		return RenderResult{}, fmt.Errorf("chunks not approved: [%s] (use --force only if the user asked to render anyway)",
			strings.Join(pending, ", "))
	}
	out := FinalPath(e)
	keeps, err := renderKeeps(w, e, edit.Scope{Label: "all"}, out, true)
	if err != nil {
		return RenderResult{}, err
	}
	report, err := verify.Run(e, out, keeps, true)
	if err != nil {
		return RenderResult{}, err
	}
	if err := os.WriteFile(stripExt(out)+".verify.json", report.JSON(), 0o644); err != nil {
		return RenderResult{}, err
	}
	fmt.Fprintln(w, "final: "+out)
	report.Print(w)
	res := RenderResult{File: out, Graph: stripExt(out) + ".graph.txt", Verify: report}
	if !report.OK {
		return res, ErrFailed
	}
	return res, nil
}

// Verify re-runs the checks on an existing render of a scope (final adds the source checks).
func Verify(w io.Writer, e *edit.Edit, sc edit.Scope, file string, final bool) (verify.Report, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return verify.Report{}, err
	}
	if final {
		sc = edit.Scope{Label: "all"}
	}
	report, err := verify.Run(e, abs, e.Keeps(sc.Start, sc.End), final)
	if err != nil {
		return report, err
	}
	report.Print(w)
	if !report.OK {
		return report, ErrFailed
	}
	return report, nil
}
