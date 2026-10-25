// Package cli wires the cobra commands.
package cli

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/spf13/cobra"
)

// exitError carries a non-zero exit code without an extra ERROR line
// (the command already printed its report, e.g. a failed verification).
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// Execute runs the CLI and returns the process exit code.
func Execute(version string) int {
	return Run(version, os.Args[1:], os.Stdout, os.Stderr)
}

// Run executes one command line with the given writers and returns the exit code.
func Run(version string, args []string, out, errOut io.Writer) int {
	root := NewRoot(version, out, errOut)
	root.SetArgs(args)
	err := root.Execute()
	if err == nil {
		return 0
	}
	var ee exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	fmt.Fprintln(errOut, "ERROR: "+err.Error())
	return 1
}

// NewRoot builds the command tree; out/errOut are injectable for tests.
func NewRoot(version string, out, errOut io.Writer) *cobra.Command {
	root := &cobra.Command{
		Use:   "video-production-cli",
		Short: "Frame-accurate video editing driven by edit.json, built for agents",
		Long: `video-production-cli cuts videos from one edit.json.

Agents: run ` + "`video-production-cli skills get core`" + ` before editing.`,
		Version: version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			for _, name := range []string{"step", "min", "span", "fps", "every", "window", "chunk-minutes", "noise"} {
				f := cmd.Flags().Lookup(name)
				if f == nil {
					continue
				}
				v, err := cmd.Flags().GetFloat64(name)
				if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
					return fmt.Errorf("--%s must be finite", name)
				}
				if name != "noise" && (v < 0 || (v == 0 && name != "fps") || (v == 0 && f.Changed)) {
					return fmt.Errorf("--%s must be positive", name)
				}
				if name == "step" && (v < 1.0/16000 || v > 1) {
					return fmt.Errorf("--step must be between 0.0000625 and 1 second")
				}
			}
			return nil
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetFlagErrorFunc(flagHint)
	root.PersistentFlags().Bool("json", false, "JSON output")
	root.PersistentFlags().String("edit", "", "path to edit.json (default: argument, ./edit.json or ./*-edit/edit.json)")
	root.AddCommand(editCommands()...)
	root.AddCommand(newSkillsCmd(), newDoctorCmd())
	return root
}

// flagHint turns an unknown/invalid flag into the correct syntax plus an example:
// agents guess flags, so the error must teach the right call.
func flagHint(cmd *cobra.Command, err error) error {
	msg := err.Error() + "\nusage: " + cmd.UseLine()
	if cmd.Example != "" {
		msg += "\nexample:\n" + cmd.Example
	}
	return errors.New(msg)
}
