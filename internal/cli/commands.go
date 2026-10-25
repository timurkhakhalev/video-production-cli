package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"video-production-cli/internal/audio"
	"video-production-cli/internal/edit"
	"video-production-cli/internal/join"
	"video-production-cli/internal/render"
)

func editCommands() []*cobra.Command {
	return []*cobra.Command{
		newInitCmd(), newStatusCmd(), newModeCmd(), newChunksCmd(), newSuggestChunksCmd(),
		newCutsCmd(), newSpeedupsCmd(),
		newAudioCmd(), newSilencesCmd(), newListenCmd(), newFramesCmd(), newProxyCmd(),
		newJoinsCmd(), newMapCmd(), newPreviewCmd(), newFinalCmd(), newVerifyCmd(),
	}
}

// emit runs a command body: text to stdout, or with --json only the result as JSON.
func emit[T any](cmd *cobra.Command, body func(w io.Writer) (T, error)) error {
	asJSON, _ := cmd.Flags().GetBool("json")
	if !asJSON {
		_, err := body(cmd.OutOrStdout())
		return wrap(err)
	}
	res, err := body(io.Discard)
	if err != nil && !errors.Is(err, render.ErrFailed) {
		writeJSON(cmd.OutOrStdout(), map[string]any{"ok": false, "error": err.Error()})
		return err
	}
	if werr := writeJSON(cmd.OutOrStdout(), res); werr != nil {
		return werr
	}
	return wrap(err)
}

// media resolves what an inspection command reads: --file, or the edit's source.
func media(cmd *cobra.Command, args []string) (render.Media, *edit.Edit, []string, error) {
	file, _ := cmd.Flags().GetString("file")
	if file == "" {
		e, rest, err := loadEdit(cmd, args)
		if err != nil {
			return render.Media{}, nil, nil, err
		}
		return render.SourceMedia(e), e, rest, nil
	}
	// a rendered file needs no edit.json; use its work folder when there is one
	workdir := ""
	path, rest, err := findEdit(cmd, args)
	if err == nil {
		if e, err := edit.Load(path); err == nil {
			workdir = e.Workdir
		}
	}
	m, err := render.FileMedia(file, workdir)
	return m, nil, rest, err
}

func fileFlag(cmd *cobra.Command) {
	cmd.Flags().String("file", "", "inspect a rendered file (preview/final) instead of the source; times are file times")
}

func newInitCmd() *cobra.Command {
	var opt join.Options
	var keeps []string
	cmd := &cobra.Command{
		Use:   "init <source> [source2 ...] [--keep FROM-TO ...]",
		Short: "Create <name>-edit/ with edit.json and encode.json; several parts are joined losslessly first",
		Long: `Creates the work folder next to the source. Several files of one recording are joined
without re-encoding first (video-1.mp4 video-2.mp4 -> video.mp4).

--keep FROM-TO (repeatable) starts keep mode for a short cut from a long recording:
only these fragments stay, chunks are built per fragment.`,
		Example: `  video-production-cli init /path/video.mp4
  video-production-cli init /path/video-1.mp4 /path/video-2.mp4
  video-production-cli init talk.mp4 --keep 35:15.7-36:20.8 --keep 42:50.8-42:57.9`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sources, frags, err := parseKeepArgs(keeps, args)
			if err != nil {
				return err
			}
			opt.Sources, opt.Keep = sources, frags
			return emit(cmd, func(w io.Writer) (map[string]string, error) {
				path, err := join.Init(w, opt)
				if err != nil {
					return nil, err
				}
				dir := filepath.Dir(path)
				return map[string]string{"workdir": dir, "edit": path, "encode": filepath.Join(dir, "encode.json")}, nil
			})
		},
	}
	cmd.Flags().StringVar(&opt.Joined, "joined", "", "path of the joined source (default: next to the first part)")
	cmd.Flags().StringVar(&opt.Workdir, "workdir", "", "work folder (default: <source>-edit)")
	cmd.Flags().Float64Var(&opt.ChunkMinutes, "chunk-minutes", 10, "nominal chunk length")
	cmd.Flags().StringArrayVar(&keeps, "keep", nil, "keep only this source range, FROM-TO (repeatable): keep mode for shorts")
	return cmd
}

// parseKeepArgs reads --keep values as FROM-TO, FROM..TO or FROM,TO; `--keep FROM TO` also
// works: the TO lands among the positionals and is taken back from there.
func parseKeepArgs(keeps, args []string) ([]string, []join.Fragment, error) {
	var sources []string
	var spare []string
	for _, a := range args {
		if _, err := os.Stat(a); err != nil {
			if _, terr := parseTime(a); terr == nil {
				spare = append(spare, a)
				continue
			}
		}
		sources = append(sources, a)
	}
	var frags []join.Fragment
	for _, k := range keeps {
		var a, b string
		for _, sep := range []string{"..", ",", "-"} {
			if x, y, ok := strings.Cut(k, sep); ok && x != "" {
				a, b = x, y
				break
			}
		}
		if a == "" {
			if len(spare) == 0 {
				return nil, nil, fmt.Errorf("--keep %s: give a range, --keep FROM-TO", k)
			}
			a, b, spare = k, spare[0], spare[1:]
		}
		from, err := parseTime(a)
		if err != nil {
			return nil, nil, err
		}
		to, err := parseTime(b)
		if err != nil {
			return nil, nil, err
		}
		frags = append(frags, join.Fragment{From: from, To: to})
	}
	if len(spare) > 0 {
		return nil, nil, fmt.Errorf("source not found: %s", spare[0])
	}
	return sources, frags, nil
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "status",
		Short:   "Summary: chunks and their status, cuts, speed-ups, notes, and the next step",
		Example: "  video-production-cli status",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (render.StatusResult, error) { return render.Status(w, e) })
		},
	}
}

func newChunksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chunks",
		Short: "Chunk boundaries and statuses",
		Example: `  video-production-cli chunks set 2 in_review
  video-production-cli chunks set all approved
  video-production-cli chunks suggest`,
	}
	set := &cobra.Command{
		Use:     "set <id|all> <draft|in_review|approved>",
		Short:   "Set a chunk status (instead of editing edit.json by hand)",
		Example: "  video-production-cli chunks set 2 approved",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) != 2 {
				return errors.New("chunks set needs a chunk and a status: chunks set <id|all> <draft|in_review|approved>")
			}
			id := 0
			if rest[0] != "all" {
				if id, err = strconv.Atoi(rest[0]); err != nil || id <= 0 {
					return fmt.Errorf("chunk must be a number or all (got %q)", rest[0])
				}
			}
			return emit(cmd, func(w io.Writer) (map[string]any, error) {
				if err := e.SetChunkStatus(id, rest[1]); err != nil {
					return nil, err
				}
				if err := e.Save(); err != nil {
					return nil, err
				}
				which := "all chunks"
				if id != 0 {
					which = fmt.Sprintf("chunk %d", id)
				}
				fmt.Fprintf(w, "%s: %s (%s)\n", which, rest[1], e.Path)
				return map[string]any{"chunk": rest[0], "status": rest[1]}, nil
			})
		},
	}
	cmd.AddCommand(set, newSuggestChunksCmd("suggest"))
	return cmd
}

func newSuggestChunksCmd(name ...string) *cobra.Command {
	var every, window float64
	use := "suggest-chunks"
	if len(name) > 0 {
		use = name[0]
	}
	cmd := &cobra.Command{
		Use:     use,
		Short:   "List the longest pauses near each nominal chunk boundary",
		Example: "  video-production-cli chunks suggest --every 600 --window 120",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) ([]render.ChunkMark, error) {
				return render.SuggestChunks(w, e, every, window)
			})
		},
	}
	cmd.Flags().Float64Var(&every, "every", 600, "nominal chunk length, seconds")
	cmd.Flags().Float64Var(&window, "window", 120, "search window around each mark, seconds")
	return cmd
}

func newAudioCmd() *cobra.Command {
	var at string
	var step, noise, min float64
	cmd := &cobra.Command{
		Use:   "audio FROM TO [--at T]",
		Short: "Loudness envelope as text, pauses with exact edges, and where a cut fits (look before you listen)",
		Long: `Shows the sound track of a range (up to 60 s) as one row of cells per second, lists the
pauses with the window a cut may use inside each (100 ms decay kept after the previous sound,
120 ms kept before the next onset) and, with --at, the pause nearest to T and a suggested
cut point snapped to a frame.`,
		Example: `  video-production-cli audio 1219 1222 --at 1220.0
  video-production-cli audio 2:03 2:09 --file previews/preview_chunk02.mp4`,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, rest, err := media(cmd, args)
			if err != nil {
				return err
			}
			from, to, rest, ok, err := getRange(cmd, rest)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("audio needs a range: audio FROM TO [--at T]")
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			var atp *float64
			if at != "" {
				t, err := parseTime(at)
				if err != nil {
					return err
				}
				atp = &t
			}
			return emit(cmd, func(w io.Writer) (audio.Analysis, error) {
				return render.Audio(w, m, from, to, atp, step, noise, min)
			})
		},
	}
	rangeFlags(cmd)
	fileFlag(cmd)
	cmd.Flags().StringVar(&at, "at", "", "time of interest: mark the nearest pause and suggest a cut point")
	cmd.Flags().Float64Var(&step, "step", audio.DefaultStep, "seconds per cell")
	cmd.Flags().Float64Var(&noise, "noise", audio.DefaultNoise, "pause threshold, dB")
	cmd.Flags().Float64Var(&min, "min", audio.DefaultMin, "minimum pause length, seconds")
	return cmd
}

func newSilencesCmd() *cobra.Command {
	var noise, min float64
	cmd := &cobra.Command{
		Use:     "silences FROM TO [--noise -32] [--min 0.08]",
		Short:   "List pauses in a range, in source seconds (ffmpeg silencedetect)",
		Example: "  video-production-cli silences 1219 1225",
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, rest, err := media(cmd, args)
			if err != nil {
				return err
			}
			from, to, rest, ok, err := getRange(cmd, rest)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("silences needs a range: silences FROM TO")
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (render.SilencesResult, error) {
				return render.Silences(w, m, from, to, noise, min)
			})
		},
	}
	rangeFlags(cmd)
	fileFlag(cmd)
	cmd.Flags().Float64Var(&noise, "noise", -32, "silence threshold, dB")
	cmd.Flags().Float64Var(&min, "min", 0.08, "minimum pause length, seconds")
	return cmd
}

func newListenCmd() *cobra.Command {
	var k int
	var try, reason string
	var span float64
	var video, apply bool
	cmd := &cobra.Command{
		Use:   "listen --join K | --range FROM TO | --try FROM TO [--apply --reason R]",
		Short: "Write a listening clip (mp3, 16 kHz mono; --video for mp4)",
		Long: `--join K   the last seconds before join K and the first after it, cut exactly as in the render
--try F T  how a cut F..T would sound, with the other cuts applied, before writing it to edit.json;
           after you listened to the clip, --apply --reason R saves the cut (same as cuts add)
--range    a stretch of the source (or of --file) as is

MP3 includes gapless timing metadata. Players that ignore it add about 69 ms; see output.`,
		Example: `  video-production-cli listen --join 3
  video-production-cli listen --try 312.48 315.12
  video-production-cli listen --try 312.48 315.12 --apply --reason "фальстарт"
  video-production-cli listen --range 1219 1225`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if apply && try == "" {
				return errors.New("--apply saves a tried cut: listen --try FROM TO --apply --reason R")
			}
			if try != "" {
				e, rest, err := loadEdit(cmd, args)
				if err != nil {
					return err
				}
				from, to, rest, _, err := parseRange(try, rest)
				if err != nil {
					return err
				}
				if err := noExtraArgs(rest); err != nil {
					return err
				}
				if apply {
					return emit(cmd, func(w io.Writer) (EntryResult, error) { return saveCut(w, e, from, to, reason) })
				}
				return emit(cmd, func(w io.Writer) (render.ListenResult, error) {
					return render.ListenTry(w, e, from, to, span, video)
				})
			}
			if k != 0 {
				e, rest, err := loadEdit(cmd, args)
				if err != nil {
					return err
				}
				if err := noExtraArgs(rest); err != nil {
					return err
				}
				return emit(cmd, func(w io.Writer) (render.ListenResult, error) {
					return render.ListenJoin(w, e, k, span, video)
				})
			}
			m, _, rest, err := media(cmd, args)
			if err != nil {
				return err
			}
			from, to, rest, ok, err := getRange(cmd, rest)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("specify --join K, --range FROM TO or --try FROM TO")
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (render.ListenResult, error) {
				return render.ListenRange(w, m, from, to, video)
			})
		},
	}
	cmd.Flags().IntVar(&k, "join", 0, "global join number from `joins`")
	cmd.Flags().StringVar(&try, "try", "", "a cut to try: --try FROM TO")
	rangeFlags(cmd)
	fileFlag(cmd)
	cmd.Flags().Float64Var(&span, "span", 3.0, "seconds of context on each side (--join, --try)")
	cmd.Flags().BoolVar(&video, "video", false, "write a 540p mp4 with picture instead of mp3")
	cmd.Flags().BoolVar(&apply, "apply", false, "with --try: save the cut you have listened to (needs --reason)")
	cmd.Flags().StringVar(&reason, "reason", "", "with --try --apply: why it is cut, in the language of the video")
	cmd.MarkFlagsMutuallyExclusive("join", "try", "range")
	cmd.MarkFlagsMutuallyExclusive("file", "join")
	cmd.MarkFlagsMutuallyExclusive("file", "try")
	return cmd
}

func newFramesCmd() *cobra.Command {
	var k int
	var at []string
	var span, every float64
	cmd := &cobra.Command{
		Use:   "frames --join K | --range FROM TO [--fps N] | --at T [T ...]",
		Short: "Contact sheets around a join or over a range, or single full-resolution frames",
		Example: `  video-production-cli frames --join 3
  video-production-cli frames --range 312.4 314.0
  video-production-cli frames --range 300 330 --fps 2
  video-production-cli frames --at 312.48 315.12`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if k != 0 {
				e, rest, err := loadEdit(cmd, args)
				if err != nil {
					return err
				}
				if err := noExtraArgs(rest); err != nil {
					return err
				}
				return emit(cmd, func(w io.Writer) (render.FramesResult, error) { return render.FramesJoin(w, e, k, span) })
			}
			m, _, rest, err := media(cmd, args)
			if err != nil {
				return err
			}
			if len(at) > 0 {
				// --at T1 T2: extra times arrive as positionals
				var times []float64
				for _, a := range append(append([]string{}, at...), rest...) {
					for _, part := range strings.Split(a, ",") {
						t, err := parseTime(part)
						if err != nil {
							return err
						}
						times = append(times, t)
					}
				}
				return emit(cmd, func(w io.Writer) (render.FramesResult, error) { return render.FramesAt(w, m, times) })
			}
			from, to, rest, ok, err := getRange(cmd, rest)
			if err != nil {
				return err
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			if !ok {
				return errors.New("specify --join K, --range FROM TO or --at T")
			}
			return emit(cmd, func(w io.Writer) (render.FramesResult, error) { return render.FramesRange(w, m, from, to, every) })
		},
	}
	cmd.Flags().IntVar(&k, "join", 0, "global join number from `joins`")
	rangeFlags(cmd)
	fileFlag(cmd)
	cmd.Flags().StringArrayVar(&at, "at", nil, "single frames at these times (--at T [T ...])")
	cmd.Flags().Float64Var(&span, "span", 0.5, "seconds on each side of a join")
	cmd.Flags().Float64Var(&every, "fps", 0, "sample the range at N frames per second (default: every frame, max 120)")
	cmd.MarkFlagsMutuallyExclusive("join", "range", "at")
	cmd.MarkFlagsMutuallyExclusive("file", "join")
	return cmd
}

func newProxyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "proxy FROM TO",
		Short:   "Write a small 540p copy of a source range to proxy/ (source time = FROM + proxy time)",
		Example: "  video-production-cli proxy 0 300",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			from, to, rest, ok, err := getRange(cmd, rest)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("proxy needs a source range: proxy FROM TO")
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (render.ProxyResult, error) { return render.Proxy(w, e, from, to) })
		},
	}
	rangeFlags(cmd)
	return cmd
}

func newJoinsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "joins [--chunk N]",
		Short: "List joins (output time, removed source range, reason) and speed-ups",
		Long: `Join numbers are global, so they match frames --join K and listen --join K.
Warns about kept fragments shorter than 0.3 s.`,
		Example: "  video-production-cli joins --chunk 2",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			sc, err := scope(cmd, e)
			if err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (render.JoinsResult, error) { return render.Joins(w, e, sc) })
		},
	}
	scopeFlags(cmd)
	return cmd
}

func newMapCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "map <time> [--chunk N]",
		Short:   "Convert a preview/final time to a source time",
		Example: "  video-production-cli map 1:28.5 --chunk 2",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) != 1 {
				return errors.New("map needs one time: map <time> [--chunk N]")
			}
			t, err := parseTime(rest[0])
			if err != nil {
				return err
			}
			sc, err := scope(cmd, e)
			if err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (render.MapResult, error) { return render.Map(w, e, sc, t) })
		},
	}
	scopeFlags(cmd)
	return cmd
}

func newPreviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "preview [--chunk N]",
		Short:   "Render a fast preview of a chunk (or everything) into previews/ and verify it",
		Example: "  video-production-cli preview --chunk 2",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			sc, err := scope(cmd, e)
			if err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (render.RenderResult, error) { return render.Preview(w, e, sc) })
		},
	}
	scopeFlags(cmd)
	return cmd
}

func newFinalCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:     "final [--force]",
		Short:   "Render the whole edit once with the source parameters, verify it, write .verify.json",
		Example: "  video-production-cli final",
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (render.RenderResult, error) { return render.Final(w, e, force) })
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "render although some chunks are not approved (only if the user asked)")
	return cmd
}

func newVerifyCmd() *cobra.Command {
	var final bool
	cmd := &cobra.Command{
		Use:   "verify [FILE] [--chunk N | --final]",
		Short: "Re-run the verification on an existing render (default: the matching preview or final file)",
		Example: `  video-production-cli verify --final
  video-production-cli verify previews/preview_chunk02.mp4 --chunk 2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) > 1 {
				return noExtraArgs(rest[1:])
			}
			sc, err := scope(cmd, e)
			if err != nil {
				return err
			}
			if final && sc.Chunk != nil {
				return errors.New("use either --chunk N or --final")
			}
			file := ""
			if len(rest) == 1 {
				file = rest[0]
			} else if final {
				file = render.FinalPath(e)
			} else {
				file = filepath.Join(e.Workdir, "previews", fmt.Sprintf("preview_%s.mp4", sc.Label))
			}
			return emit(cmd, func(w io.Writer) (any, error) { return render.Verify(w, e, sc, file, final) })
		},
	}
	scopeFlags(cmd)
	cmd.Flags().BoolVar(&final, "final", false, "also check codec parameters and picture against the source")
	return cmd
}
