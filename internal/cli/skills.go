package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	vpc "video-production-cli"
	"video-production-cli/internal/skills"
)

func skillFS() (fs.FS, error) { return fs.Sub(vpc.SkillData, "skill-data") }

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "List and print the skill sections bundled with this binary",
		Long: `The skill content is embedded in the binary, so it always matches the installed version.
Agents should read it from here instead of a cached copy.`,
		Example: `  video-production-cli skills list
  video-production-cli skills get core cutting
  video-production-cli skills get core --full
  video-production-cli skills path core
  video-production-cli skills install --agent gemini`,
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List skill sections",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			fsys, err := skillFS()
			if err != nil {
				return err
			}
			all, err := skills.List(fsys)
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return writeJSON(cmd.OutOrStdout(), all)
			}
			for _, s := range all {
				fmt.Fprintf(cmd.OutOrStdout(), "  %-10s %s\n", s.Name, s.Description)
			}
			return nil
		},
	}

	var full bool
	get := &cobra.Command{
		Use:     "get <name> [name...]",
		Short:   "Print a skill section (--full adds references)",
		Args:    cobra.MinimumNArgs(1),
		Example: "  video-production-cli skills get core --full",
		RunE: func(cmd *cobra.Command, args []string) error {
			fsys, err := skillFS()
			if err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				sections := map[string]string{}
				for _, name := range args {
					body, err := skills.Get(fsys, name, full)
					if err != nil {
						return err
					}
					sections[name] = body
				}
				return writeJSON(cmd.OutOrStdout(), sections)
			}
			for i, name := range args {
				text, err := skills.Get(fsys, name, full)
				if err != nil {
					return err
				}
				if i > 0 {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				fmt.Fprint(cmd.OutOrStdout(), text)
			}
			return nil
		},
	}
	get.Flags().BoolVar(&full, "full", false, "include references (command reference, edit.json schema)")

	path := &cobra.Command{
		Use:   "path [name]",
		Short: "Extract the sections to the user cache and print the directory",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fsys, err := skillFS()
			if err != nil {
				return err
			}
			base, err := os.UserCacheDir()
			if err != nil {
				return err
			}
			dir := filepath.Join(base, "video-production-cli", "skill-data-"+cmd.Root().Version)
			if err := skills.Extract(fsys, dir); err != nil {
				return err
			}
			if len(args) == 1 {
				if _, err := skills.Get(fsys, args[0], false); err != nil {
					return err
				}
				dir = filepath.Join(dir, args[0])
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return writeJSON(cmd.OutOrStdout(), map[string]string{"path": dir})
			}
			fmt.Fprintln(cmd.OutOrStdout(), dir)
			return nil
		},
	}
	var agent string
	install := &cobra.Command{
		Use:   "install [--agent claude|gemini|agy2|codex|all]",
		Short: "Install the pointer skill in agent skill directories",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			return emit(cmd, func(w io.Writer) ([]string, error) {
				paths, err := skills.Install(home, agent, vpc.PointerSkill)
				for _, p := range paths {
					fmt.Fprintln(w, p)
				}
				return paths, err
			})
		},
	}
	install.Flags().StringVar(&agent, "agent", "all", "claude, gemini, agy2, codex or all")
	cmd.AddCommand(list, get, path, install)
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("unexpected arguments: %s\nto read sections, use:\n  video-production-cli skills get %s\nsee also: video-production-cli skills --help", strings.Join(args, " "), strings.Join(args, " "))
		}
		return nil
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := list.RunE(cmd, args); err != nil {
			return err
		}
		if asJSON, _ := cmd.Flags().GetBool("json"); !asJSON {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "\nExamples:\n%s\n", cmd.Example)
			return err
		}
		return nil
	}
	return cmd
}

func writeJSON(w interface{ Write([]byte) (int, error) }, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
