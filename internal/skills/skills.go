// Package skills serves the skill sections embedded in the binary: list, get, path, install.
package skills

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is one section under skill-data/<name>/SKILL.md.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Order is the reading order of sections; unknown sections go after, sorted by name.
var Order = []string{"core", "cutting", "audio", "speedups", "shorts", "review"}

// List returns every section with its frontmatter description.
func List(fsys fs.FS) ([]Skill, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	var out []Skill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		b, err := fs.ReadFile(fsys, path.Join(e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		out = append(out, Skill{Name: e.Name(), Description: frontmatter(string(b))["description"]})
	}
	rank := func(n string) int {
		for i, o := range Order {
			if o == n {
				return i
			}
		}
		return len(Order)
	}
	sort.Slice(out, func(i, j int) bool {
		ri, rj := rank(out[i].Name), rank(out[j].Name)
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Get returns SKILL.md of a section; full appends every file under references/ and templates/.
func Get(fsys fs.FS, name string, full bool) (string, error) {
	b, err := fs.ReadFile(fsys, path.Join(name, "SKILL.md"))
	if err != nil {
		return "", unknown(fsys, name)
	}
	var sb strings.Builder
	sb.Write(b)
	if !full {
		return sb.String(), nil
	}
	for _, dir := range []string{"references", "templates"} {
		files, err := fs.ReadDir(fsys, path.Join(name, dir))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			rel := path.Join(dir, f.Name())
			body, err := fs.ReadFile(fsys, path.Join(name, rel))
			if err != nil {
				return "", err
			}
			if !strings.HasSuffix(sb.String(), "\n") {
				sb.WriteString("\n")
			}
			fmt.Fprintf(&sb, "\n--- %s ---\n\n", rel)
			sb.Write(body)
		}
	}
	return sb.String(), nil
}

// Extract writes the embedded sections to dir (replacing it) and returns dir.
func Extract(fsys fs.FS, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

func unknown(fsys fs.FS, name string) error {
	list, _ := List(fsys)
	names := make([]string, len(list))
	for i, s := range list {
		names[i] = s.Name
	}
	return fmt.Errorf("unknown skill %q; available: %s", name, strings.Join(names, ", "))
}

// frontmatter parses the simple `key: value` lines between the leading --- markers,
// including folded `>-` values.
func frontmatter(doc string) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(doc))
	if !sc.Scan() || strings.TrimSpace(sc.Text()) != "---" {
		return out
	}
	var key string
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if key != "" && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			out[key] = strings.TrimSpace(out[key] + " " + strings.TrimSpace(line))
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if v == ">-" || v == ">" || v == "|" {
			v = ""
		}
		out[key] = v
	}
	return out
}

// Install writes only the pointer skill, leaving the agent's other skills untouched.
func Install(home, agent string, stub []byte) ([]string, error) {
	targets := map[string]string{
		"claude": ".claude/skills",
		"gemini": ".gemini/config/skills",
		"agy2":   ".config/agy2/home/.gemini/config/skills",
		"codex":  ".agents/skills",
	}
	names := []string{agent}
	if agent == "all" {
		names = []string{"claude", "gemini", "agy2", "codex"}
	}
	var paths []string
	for _, name := range names {
		rel, ok := targets[name]
		if !ok {
			return nil, fmt.Errorf("unknown agent %q; use claude, gemini, agy2, codex or all", name)
		}
		dir := filepath.Join(home, rel, "video-production-cli")
		if err := os.MkdirAll(dir, 0755); err != nil {
			return paths, err
		}
		p := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(p, stub, 0644); err != nil {
			return paths, err
		}
		paths = append(paths, p)
	}
	return paths, nil
}
