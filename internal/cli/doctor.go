package cli

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"video-production-cli/internal/ffmpeg"
)

// filters the renderer and inspection commands rely on
var requiredFilters = []string{
	"trim", "atrim", "setpts", "asetpts", "select", "concat", "afade", "anullsrc",
	"scale", "tile", "vstack", "hstack", "silencedetect", "astats", "psnr", "aresample",
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check ffmpeg/ffprobe versions and required filters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var report bytes.Buffer
			var out io.Writer = cmd.OutOrStdout()
			asJSON, _ := cmd.Flags().GetBool("json")
			if asJSON {
				out = &report
			}
			ok := true
			for _, bin := range []string{ffmpeg.FFmpeg, ffmpeg.FFprobe} {
				major, _, line, err := ffmpeg.Version(bin)
				switch {
				case err != nil:
					fmt.Fprintf(out, "[FAIL] %s: not found on PATH (%v)\n", bin, err)
					ok = false
				case major != 0 && major < 7:
					fmt.Fprintf(out, "[FAIL] %s: version 7+ required: %s\n", bin, line)
					ok = false
				default:
					fmt.Fprintf(out, "[ok]   %s: %s\n", bin, line)
				}
			}
			if ok {
				missing, err := ffmpeg.MissingFilters(requiredFilters...)
				switch {
				case err != nil:
					fmt.Fprintf(out, "[FAIL] filters: %v\n", err)
					ok = false
				case len(missing) > 0:
					fmt.Fprintf(out, "[FAIL] filters missing: %s\n", strings.Join(missing, ", "))
					ok = false
				default:
					fmt.Fprintf(out, "[ok]   filters: %d required, all present\n", len(requiredFilters))
				}
			}
			fmt.Fprintf(out, "video-production-cli %s\n", cmd.Root().Version)
			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), map[string]any{"ok": ok, "version": cmd.Root().Version, "report": report.String()}); err != nil {
					return err
				}
			}
			if !ok {
				return exitError{1}
			}
			return nil
		},
	}
}
