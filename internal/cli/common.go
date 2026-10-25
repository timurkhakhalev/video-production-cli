package cli

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/pyfmt"
	"video-production-cli/internal/render"
)

// splitEditArg pulls a positional *.json argument out of args (old render.py syntax:
// `joins video-edit/edit.json --chunk 2`).
func splitEditArg(args []string) (string, []string) {
	var rest []string
	found := ""
	for _, a := range args {
		if found == "" && strings.HasSuffix(strings.ToLower(a), ".json") {
			found = a
			continue
		}
		rest = append(rest, a)
	}
	return found, rest
}

// findEdit resolves edit.json: positional argument, --edit, ./edit.json, ./*-edit/edit.json.
func findEdit(cmd *cobra.Command, args []string) (string, []string, error) {
	pos, rest := splitEditArg(args)
	if pos != "" {
		return pos, rest, nil
	}
	if f, _ := cmd.Flags().GetString("edit"); f != "" {
		return f, rest, nil
	}
	if _, err := os.Stat("edit.json"); err == nil {
		return "edit.json", rest, nil
	}
	matches, _ := filepath.Glob("*-edit/edit.json")
	switch len(matches) {
	case 1:
		return matches[0], rest, nil
	case 0:
		return "", rest, errors.New("no edit.json found: pass it as an argument or with --edit, or run from the work folder (start with `init <source>`)")
	default:
		return "", rest, fmt.Errorf("several edit.json found (%s); pass one with --edit", strings.Join(matches, ", "))
	}
}

func loadEdit(cmd *cobra.Command, args []string) (*edit.Edit, []string, error) {
	path, rest, err := findEdit(cmd, args)
	if err != nil {
		return nil, nil, err
	}
	e, err := edit.Load(path)
	return e, rest, err
}

// parseTime accepts 123.4, 2:03.4 or 1:02:03.4 (render.py parse_tc).
func parseTime(s string) (float64, error) {
	total := 0.0
	parts := strings.Split(s, ":")
	if len(parts) > 3 {
		return 0, fmt.Errorf("bad time %q", s)
	}
	for _, p := range parts {
		v, err := pyfmt.ParseFloat(p)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return 0, fmt.Errorf("bad time %q: use seconds (123.4), m:ss.s (2:03.4) or h:mm:ss.s (1:02:03.4)", s)
		}
		total = total*60 + v
		if math.IsInf(total, 0) {
			return 0, fmt.Errorf("time %q is too large", s)
		}
	}
	return total, nil
}

// parseRange reads FROM TO from `--range FROM TO` (the flag takes FROM, TO stays positional)
// or from two positional arguments; it returns the remaining positionals.
func parseRange(rangeFrom string, args []string) (from, to float64, rest []string, ok bool, err error) {
	var a, b string
	switch {
	case rangeFrom != "":
		if len(args) < 1 {
			return 0, 0, nil, false, errors.New("--range needs two values: --range FROM TO")
		}
		a, b, rest = rangeFrom, args[0], args[1:]
	case len(args) >= 2:
		a, b, rest = args[0], args[1], args[2:]
	default:
		return 0, 0, args, false, nil
	}
	if from, err = parseTime(a); err != nil {
		return
	}
	if to, err = parseTime(b); err != nil {
		return
	}
	return from, to, rest, true, nil
}

// scopeFlags adds --chunk N / --all (the default).
func scopeFlags(cmd *cobra.Command) {
	cmd.Flags().Int("chunk", 0, "chunk id (default: the whole edit)")
	cmd.Flags().Bool("all", false, "the whole edit (default; kept for render.py compatibility)")
}

func scope(cmd *cobra.Command, e *edit.Edit) (edit.Scope, error) {
	id, _ := cmd.Flags().GetInt("chunk")
	all, _ := cmd.Flags().GetBool("all")
	if all && id != 0 {
		return edit.Scope{}, errors.New("use either --chunk N or --all")
	}
	return e.ScopeFor(id)
}

// wrap maps render.ErrFailed (report already printed) to a quiet non-zero exit.
func wrap(err error) error {
	if errors.Is(err, render.ErrFailed) {
		return exitError{1}
	}
	return err
}

func noExtraArgs(rest []string) error {
	if len(rest) > 0 {
		return fmt.Errorf("unexpected argument(s): %s", strings.Join(rest, " "))
	}
	return nil
}

func rangeFlags(cmd *cobra.Command) {
	cmd.Flags().String("range", "", "source range: --range FROM TO (or positional FROM TO)")
}

func getRange(cmd *cobra.Command, args []string) (float64, float64, []string, bool, error) {
	start, _ := cmd.Flags().GetString("range")
	a, b, rest, ok, err := parseRange(start, args)
	if err == nil && ok && b <= a {
		err = fmt.Errorf("range end must be after start")
	}
	return a, b, rest, ok, err
}
