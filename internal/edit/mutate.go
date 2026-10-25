package edit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"video-production-cli/internal/jsonx"
	"video-production-cli/internal/pyfmt"
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

// Modes an edit may have.
var Modes = []string{"step", "full"}

// SetMode sets "mode" (step: review each chunk; full: one review at the end).
func (e *Edit) SetMode(mode string) error {
	if !contains(Modes, mode) {
		return fmt.Errorf("mode must be step or full (got %q)", mode)
	}
	e.Doc.Set("mode", mode)
	return nil
}

func numLit(x float64) jsonx.Number { return jsonx.Number(pyfmt.Repr(pyfmt.Round(x, 3))) }

// insertSorted adds obj to the array under key, before the first entry that starts later.
func (e *Edit) insertSorted(key string, from float64, obj *jsonx.Object) {
	arr, _ := e.Doc.Get(key)
	items, _ := arr.([]jsonx.Value)
	i := 0
	for ; i < len(items); i++ {
		if o, ok := items[i].(*jsonx.Object); ok {
			if f, ok := ffrom(o); ok && f > from {
				break
			}
		}
	}
	out := append(append(append([]jsonx.Value{}, items[:i]...), obj), items[i:]...)
	if _, exists := e.Doc.Get(key); exists {
		e.Doc.Set(key, out)
	} else if _, ok := e.Doc.Get("cuts"); ok && key != "cuts" {
		e.Doc.SetAfter("cuts", key, out)
	} else {
		e.Doc.SetAfter("chunks", key, out)
	}
}

func ffrom(o *jsonx.Object) (float64, bool) {
	v, _ := o.Get("from")
	n, ok := v.(jsonx.Number)
	return n.Float(), ok
}

// AddCut adds a cut in source seconds; Save validates it against chunks, keep and speed-ups.
func (e *Edit) AddCut(from, to float64, reason string) error {
	if reason == "" {
		return fmt.Errorf("a cut needs a reason (--reason), in the language of the video")
	}
	if _, err := e.WithCut(from, to, reason); err != nil {
		return err
	}
	e.insertSorted("cuts", from, jsonx.NewObject().Set("from", numLit(from)).Set("to", numLit(to)).Set("reason", reason))
	return nil
}

// AddSpeedup adds a muted speed-up of a whole-number factor.
func (e *Edit) AddSpeedup(from, to float64, speed int, reason string) error {
	if reason == "" {
		return fmt.Errorf("a speed-up needs a reason (--reason)")
	}
	e.insertSorted("speedups", from, jsonx.NewObject().Set("from", numLit(from)).Set("to", numLit(to)).
		Set("speed", jsonx.Number(fmt.Sprint(speed))).Set("reason", reason))
	return nil
}

// Remove deletes entry n (1-based, in source order as listed) of "cuts" or "speedups"
// and returns it.
func (e *Edit) Remove(key string, n int) (*jsonx.Object, error) {
	arr, _ := e.Doc.Get(key)
	items, _ := arr.([]jsonx.Value)
	type entry struct {
		i    int
		from float64
	}
	var order []entry
	for i, v := range items {
		if o, ok := v.(*jsonx.Object); ok {
			f, _ := ffrom(o)
			order = append(order, entry{i, f})
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return order[a].from < order[b].from })
	if n < 1 || n > len(order) {
		return nil, fmt.Errorf("no %s entry #%d (there are %d)", key, n, len(order))
	}
	i := order[n-1].i
	removed := items[i].(*jsonx.Object)
	e.Doc.Set(key, append(append([]jsonx.Value{}, items[:i]...), items[i+1:]...))
	return removed, nil
}
