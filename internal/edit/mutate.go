package edit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"video-production-cli/internal/jsonx"
)

// Version is written into edit.json on the first save; older files without it load as is.
const Version = 1

// Save validates the document and writes it atomically, keeping key order and unknown fields.
func (e *Edit) Save() error {
	if _, ok := e.Doc.Get("version"); !ok {
		e.Doc.SetFirst("version", jsonx.Number(fmt.Sprint(Version)))
	}
	if err := e.validate(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(e.Path), ".edit-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(jsonx.Dump(e.Doc)); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), e.Path)
}

// SetChunkStatus sets the status of one chunk (id > 0) or of every chunk (id 0).
func (e *Edit) SetChunkStatus(id int, status string) error {
	if !contains(Statuses, status) {
		return fmt.Errorf("status must be one of draft, in_review, approved (got %q)", status)
	}
	if id == 0 {
		for _, c := range e.Chunks {
			c.Obj.Set("status", status)
		}
		return nil
	}
	c, err := e.Chunk(id)
	if err != nil {
		return err
	}
	c.Obj.Set("status", status)
	return nil
}

// WithCut returns a copy of the edit with one more cut, validated like a real one.
// The document is not touched; it is for previewing a cut (`listen --try`).
func (e *Edit) WithCut(from, to float64, reason string) (*Edit, error) {
	if to <= from {
		return nil, fmt.Errorf("cut %.3f-%.3f has non-positive length", from, to)
	}
	if from < -1e-6 || to > e.Duration+1e-3 {
		return nil, fmt.Errorf("cut %.3f-%.3f outside source", from, to)
	}
	for _, c := range e.Cuts {
		if c.From < to-1e-6 && from < c.To-1e-6 {
			return nil, fmt.Errorf("cut %.3f-%.3f overlaps the existing cut %.3f-%.3f (%s); try a range outside it or edit that cut",
				from, to, c.From, c.To, c.Reason)
		}
	}
	for _, s := range e.Speedups {
		if s.From < to-1e-6 && from < s.To-1e-6 {
			return nil, fmt.Errorf("cut %.3f-%.3f overlaps the speed-up %.3f-%.3f", from, to, s.From, s.To)
		}
	}
	cp := *e
	cp.Cuts = append(append([]Cut{}, e.Cuts...), Cut{From: from, To: to, Reason: reason})
	sort.SliceStable(cp.Cuts, func(i, j int) bool { return cp.Cuts[i].From < cp.Cuts[j].From })
	return &cp, nil
}
