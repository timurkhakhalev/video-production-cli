package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	vpc "video-production-cli"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"zeta/SKILL.md":                {Data: []byte("---\nname: zeta\ndescription: last\n---\nZ\n")},
		"core/SKILL.md":                {Data: []byte("---\nname: core\ndescription: >-\n  folded\n  text\n---\n# core\n")},
		"core/references/commands.md":  {Data: []byte("# Commands\n")},
		"core/references/edit-json.md": {Data: []byte("# edit.json")},
		"audio/SKILL.md":               {Data: []byte("---\nname: audio\ndescription: track first\n---\nA\n")},
	}
}

func TestListOrderAndDescriptions(t *testing.T) {
	got, err := List(testFS())
	if err != nil {
		t.Fatal(err)
	}
	want := []Skill{{"core", "folded text"}, {"audio", "track first"}, {"zeta", "last"}}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %v want %v", i, got[i], want[i])
		}
	}
}

func TestGetFull(t *testing.T) {
	short, err := Get(testFS(), "core", false)
	if err != nil || short != "---\nname: core\ndescription: >-\n  folded\n  text\n---\n# core\n" {
		t.Fatalf("short: %q %v", short, err)
	}
	full, _ := Get(testFS(), "core", true)
	want := short + "\n--- references/commands.md ---\n\n# Commands\n" +
		"\n--- references/edit-json.md ---\n\n# edit.json"
	if full != want {
		t.Fatalf("full:\n%q\nwant\n%q", full, want)
	}
}

func TestGetUnknownListsAvailable(t *testing.T) {
	_, err := Get(testFS(), "nope", false)
	if err == nil || !strings.Contains(err.Error(), "core, audio, zeta") {
		t.Fatalf("err = %v", err)
	}
}

func TestExtract(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "x")
	if err := Extract(testFS(), dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "core", "references", "commands.md"))
	if err != nil || string(b) != "# Commands\n" {
		t.Fatalf("%q %v", b, err)
	}
}

// Every embedded section must have a name matching its directory and a description.
func TestEmbeddedSections(t *testing.T) {
	fsys, err := fs.Sub(vpc.SkillData, "skill-data")
	if err != nil {
		t.Fatal(err)
	}
	list, err := List(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < len(Order) {
		t.Fatalf("want at least %v, got %v", Order, list)
	}
	for i, name := range Order {
		if list[i].Name != name || list[i].Description == "" {
			t.Errorf("section %d: %+v", i, list[i])
		}
		b, _ := fs.ReadFile(fsys, name+"/SKILL.md")
		if frontmatter(string(b))["name"] != name {
			t.Errorf("%s: frontmatter name mismatch", name)
		}
	}
}

func TestInstall(t *testing.T) {
	home := t.TempDir()
	paths, err := Install(home, "all", vpc.PointerSkill)
	if err != nil || len(paths) != 4 {
		t.Fatalf("%v %v", paths, err)
	}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || string(b) != string(vpc.PointerSkill) {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if _, err := Install(home, "unknown", vpc.PointerSkill); err == nil {
		t.Fatal("accepted unknown agent")
	}
}
