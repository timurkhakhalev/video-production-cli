package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScratchDir(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "talk-edit")
	os.MkdirAll(filepath.Join(work, "previews"), 0o755)
	os.WriteFile(filepath.Join(work, "edit.json"), []byte("{}"), 0o644)

	if got := scratchDir(filepath.Join(work, "previews", "preview_all.mp4")); got != work {
		t.Fatalf("preview: %s, want the work folder %s", got, work)
	}
	if got := scratchDir(filepath.Join(work, "talk_edit.mp4")); got != work {
		t.Fatalf("final: %s, want %s", got, work)
	}
	src := filepath.Join(root, "talk.mp4")
	got := scratchDir(src)
	if strings.HasPrefix(got, root) || !strings.Contains(got, filepath.Join("video-production-cli", "files", "talk-")) {
		t.Fatalf("source outside an edit: %s, want a cache folder", got)
	}
	if scratchDir(filepath.Join(root, "other", "talk.mp4")) == got {
		t.Fatal("same-named files in different folders share a scratch folder")
	}
}
