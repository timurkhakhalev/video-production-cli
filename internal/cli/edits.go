package cli

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"

	"github.com/spf13/cobra"

	"video-production-cli/internal/edit"
	"video-production-cli/internal/jsonx"
)

// Commands that change edit.json, so agents never rewrite the file by hand.

type EntryResult struct {
	N      int     `json:"n"`
	From   float64 `json:"from"`
	To     float64 `json:"to"`
	Speed  int     `json:"speed,omitempty"`
	Reason string  `json:"reason"`
	Join   int     `json:"join,omitempty"` // global join this cut creates
}

func listCuts(e *edit.Edit) []EntryResult {
	var out []EntryResult
	for _, c := range e.Cuts {
		if !c.Implicit {
			out = append(out, EntryResult{N: len(out) + 1, From: c.From, To: c.To, Reason: c.Reason, Join: joinAt(e, c.From)})
		}
	}
	return out
}

// joinAt is the global join number at source time t (the end of a kept range), or 0.
func joinAt(e *edit.Edit, t float64) int {
	keeps := e.Keeps(nil, nil)
	a := e.Snap(t)
	for i := 0; i+1 < len(keeps); i++ {
		if math.Abs(keeps[i].Out-a) < 1e-3 {
			return i + 1
		}
	}
	return 0
}

func printEntries(w io.Writer, what string, list []EntryResult) {
	if len(list) == 0 {
		fmt.Fprintf(w, "no %s\n", what)
		return
	}
	for _, c := range list {
		extra := ""
		if c.Speed > 0 {
			extra = fmt.Sprintf(" x%d", c.Speed)
		}
		if c.Join > 0 {
			extra += fmt.Sprintf("  join #%d", c.Join)
		}
		fmt.Fprintf(w, "#%-2d %.3f - %.3f (%.2fs)%s  %s\n", c.N, c.From, c.To, c.To-c.From, extra, c.Reason)
	}
}

// saveCut adds a cut, saves and points at the join to check.
func saveCut(w io.Writer, e *edit.Edit, from, to float64, reason string) (EntryResult, error) {
	if err := e.AddCut(from, to, reason); err != nil {
		return EntryResult{}, err
	}
	if err := e.Save(); err != nil {
		return EntryResult{}, err
	}
	res := EntryResult{From: from, To: to, Reason: reason, Join: joinAt(e, from)}
	for _, c := range listCuts(e) {
		if math.Abs(c.From-from) < 1e-6 {
			res.N = c.N
		}
	}
	fmt.Fprintf(w, "cut #%d added: %.3f - %.3f (%s) -> %s\n", res.N, from, to, reason, e.Path)
	if res.Join > 0 {
		fmt.Fprintf(w, "  now check join #%d: frames --join %d, listen --join %d\n", res.Join, res.Join, res.Join)
	}
	warnReviewed(w, e, from)
	return res, nil
}

// warnReviewed reminds that a change inside a chunk already handed over needs a new review.
func warnReviewed(w io.Writer, e *edit.Edit, t float64) {
	for _, c := range e.Chunks {
		if t >= c.Start-1e-6 && t < c.End-1e-6 && c.Status != "draft" {
			fmt.Fprintf(w, "  chunk %s was %s: rebuild its preview and hand it over again (chunks set %s draft until then)\n",
				c.IDString(), c.Status, c.IDString())
		}
	}
}

func newCutsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cuts [add FROM TO --reason R | rm N]",
		Short: "List, add or remove cuts (source seconds); edit.json is never written by hand",
		Example: `  video-production-cli cuts
  video-production-cli cuts add 312.48 315.12 --reason "фальстарт"
  video-production-cli cuts rm 3`,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) ([]EntryResult, error) {
				list := listCuts(e)
				printEntries(w, "cuts", list)
				if len(e.Keep) > 0 {
					fmt.Fprintf(w, "(keep mode: everything outside %d keep fragment(s) is cut as well)\n", len(e.Keep))
				}
				return list, nil
			})
		},
	}
	var reason string
	add := &cobra.Command{
		Use:     "add FROM TO --reason R",
		Short:   "Add a cut after checking it with listen --try; validates and saves edit.json",
		Example: `  video-production-cli cuts add 312.48 315.12 --reason "фальстарт"`,
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
				return errors.New("cuts add needs a range: cuts add FROM TO --reason R")
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (EntryResult, error) { return saveCut(w, e, from, to, reason) })
		},
	}
	rangeFlags(add)
	add.Flags().StringVar(&reason, "reason", "", "why it is cut, in the language of the video (required)")
	cmd.AddCommand(add, newRemoveCmd("cuts"))
	return cmd
}

func newSpeedupsCmd() *cobra.Command {
	list := func(e *edit.Edit) []EntryResult {
		var out []EntryResult
		for i, s := range e.Speedups {
			out = append(out, EntryResult{N: i + 1, From: s.From, To: s.To, Speed: s.Speed, Reason: s.Reason})
		}
		return out
	}
	cmd := &cobra.Command{
		Use:   "speedups [add FROM TO --speed N --reason R | rm N]",
		Short: "List, add or remove muted speed-ups (only when the user asked for them)",
		Example: `  video-production-cli speedups
  video-production-cli speedups add 600 720 --speed 8 --reason "установка зависимостей"
  video-production-cli speedups rm 1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) ([]EntryResult, error) {
				l := list(e)
				printEntries(w, "speed-ups", l)
				return l, nil
			})
		},
	}
	var reason string
	var speed int
	add := &cobra.Command{
		Use:     "add FROM TO --speed N --reason R",
		Short:   "Add a muted speed-up (whole-number factor >= 2); validates and saves edit.json",
		Example: `  video-production-cli speedups add 600 720 --speed 8 --reason "установка зависимостей"`,
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
				return errors.New("speedups add needs a range: speedups add FROM TO --speed N --reason R")
			}
			if err := noExtraArgs(rest); err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) (EntryResult, error) {
				if err := e.AddSpeedup(from, to, speed, reason); err != nil {
					return EntryResult{}, err
				}
				if err := e.Save(); err != nil {
					return EntryResult{}, err
				}
				fmt.Fprintf(w, "speed-up added: %.3f - %.3f x%d (%s) -> %s\n", from, to, speed, reason, e.Path)
				warnReviewed(w, e, from)
				return EntryResult{From: from, To: to, Speed: speed, Reason: reason}, nil
			})
		},
	}
	rangeFlags(add)
	add.Flags().IntVar(&speed, "speed", 0, "whole-number speed factor >= 2 (required)")
	add.Flags().StringVar(&reason, "reason", "", "why it is sped up (required)")
	cmd.AddCommand(add, newRemoveCmd("speedups"))
	return cmd
}

func newRemoveCmd(key string) *cobra.Command {
	return &cobra.Command{
		Use:     "rm N",
		Aliases: []string{"remove"},
		Short:   "Remove entry N as numbered by the list command; saves edit.json",
		Example: fmt.Sprintf("  video-production-cli %s rm 2", key),
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) != 1 {
				return fmt.Errorf("%s rm needs one number from `%s`", key, key)
			}
			n, err := strconv.Atoi(rest[0])
			if err != nil {
				return fmt.Errorf("%s rm: %q is not a number", key, rest[0])
			}
			return emit(cmd, func(w io.Writer) (map[string]any, error) {
				o, err := e.Remove(key, n)
				if err != nil {
					return nil, err
				}
				if err := e.Save(); err != nil {
					return nil, err
				}
				from, _ := o.Get("from")
				to, _ := o.Get("to")
				reason, _ := o.Get("reason")
				fmt.Fprintf(w, "%s #%d removed: %s - %s (%s) -> %s\n", key, n, jsonx.Str(from), jsonx.Str(to), jsonx.Str(reason), e.Path)
				if f, ok := from.(jsonx.Number); ok {
					warnReviewed(w, e, f.Float())
				}
				return map[string]any{"removed": n}, nil
			})
		},
	}
}

func newModeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "mode <step|full>",
		Short:   "Set the review mode the user chose: step reviews each chunk, full reviews the whole edit once",
		Example: `  video-production-cli mode full`,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, rest, err := loadEdit(cmd, args)
			if err != nil {
				return err
			}
			if len(rest) != 1 {
				return errors.New("mode needs one value: mode step|full")
			}
			return emit(cmd, func(w io.Writer) (map[string]string, error) {
				if err := e.SetMode(rest[0]); err != nil {
					return nil, err
				}
				if err := e.Save(); err != nil {
					return nil, err
				}
				fmt.Fprintf(w, "mode: %s (%s)\n", rest[0], e.Path)
				return map[string]string{"mode": rest[0]}, nil
			})
		},
	}
}
