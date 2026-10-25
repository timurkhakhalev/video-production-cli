package edit

import (
	"fmt"
	"sort"

	"video-production-cli/internal/jsonx"
)

// Keep mode (shorts from a long talk): "keep" lists the fragments that stay; everything
// outside them is cut. Chunks tile the fragments instead of the whole source, cuts and
// speed-ups inside fragments work as usual.

func (e *Edit) parseKeep() ([]Fragment, error) {
	raw, exists := e.Doc.Get("keep")
	if !exists {
		return nil, nil
	}
	items, ok := raw.([]jsonx.Value)
	if !ok || len(items) == 0 {
		return nil, fmt.Errorf("keep must be a non-empty array of fragments")
	}
	var objs []*jsonx.Object
	for _, item := range items {
		obj, ok := item.(*jsonx.Object)
		if !ok {
			return nil, fmt.Errorf("keep fragments must be objects with from and to")
		}
		objs = append(objs, obj)
	}
	var out []Fragment
	for _, o := range objs {
		from, err := requireNum(o, "keep fragment", "from")
		if err != nil {
			return nil, err
		}
		to, err := requireNum(o, "keep fragment", "to")
		if err != nil {
			return nil, err
		}
		r, _ := o.Get("reason")
		f := Fragment{From: from, To: to}
		if truthy(r) {
			f.Reason = jsonx.Str(r)
		}
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].From < out[j].From })
	lastTo := -1.0
	for _, f := range out {
		if f.To <= f.From {
			return nil, fmt.Errorf("keep fragment %.3f-%.3f has non-positive length", f.From, f.To)
		}
		if f.From < -1e-6 || f.To > e.Duration+1e-3 {
			return nil, fmt.Errorf("keep fragment %.3f-%.3f outside source (0 .. %.3f)", f.From, f.To, e.Duration)
		}
		if f.From < lastTo-1e-6 {
			return nil, fmt.Errorf("keep fragments overlap near %.3f", f.From)
		}
		lastTo = f.To
	}
	return out, nil
}

// parseKeepChunks checks that chunks tile every keep fragment: the first chunk of a fragment
// starts at its start, each next one where the previous ends, the last ends at its end.
func (e *Edit) parseKeepChunks(objs []*jsonx.Object) error {
	fi := 0
	prevEnd := e.Keep[0].From
	for _, o := range objs {
		id, _ := o.Get("id")
		status, _ := o.Get("status")
		st, _ := status.(string)
		if !contains(Statuses, st) {
			return fmt.Errorf("chunk %s: status must be one of ('draft', 'in_review', 'approved')", jsonx.Str(id))
		}
		what := "chunk " + jsonx.Str(id)
		start, err := requireNum(o, what, "start")
		if err != nil {
			return err
		}
		end, err := requireNum(o, what, "end")
		if err != nil {
			return err
		}
		if fi >= len(e.Keep) {
			return fmt.Errorf("chunk %s (%.3f-%.3f) is outside every keep fragment; in keep mode chunks tile the fragments",
				jsonx.Str(id), start, end)
		}
		f := e.Keep[fi]
		if abs(start-prevEnd) > 1e-3 {
			return fmt.Errorf("chunk %s must start at %.3f (%s); in keep mode chunks tile the keep fragments",
				jsonx.Str(id), prevEnd, map[bool]string{true: "start of keep fragment", false: "where the previous chunk ends"}[prevEnd == f.From])
		}
		if end <= start {
			return fmt.Errorf("chunk %s has non-positive length", jsonx.Str(id))
		}
		if end > f.To+1e-3 {
			return fmt.Errorf("chunk %s ends at %.3f, past the end of keep fragment %.3f-%.3f; split it at %.3f",
				jsonx.Str(id), end, f.From, f.To, f.To)
		}
		e.Chunks = append(e.Chunks, Chunk{ID: id, Start: start, End: end, Status: st, Obj: o})
		prevEnd = end
		if abs(end-f.To) <= 1e-3 {
			fi++
			if fi < len(e.Keep) {
				prevEnd = e.Keep[fi].From
			}
		}
	}
	if fi < len(e.Keep) {
		f := e.Keep[fi]
		return fmt.Errorf("keep fragment %.3f-%.3f is not covered by chunks (covered up to %.3f); add a chunk", f.From, f.To, prevEnd)
	}
	return nil
}

// withImplicitCuts checks that explicit cuts lie inside keep fragments and adds the gaps
// between fragments as implicit cuts.
func (e *Edit) withImplicitCuts(cuts []Cut) ([]Cut, error) {
	for _, c := range cuts {
		ok := false
		for _, f := range e.Keep {
			if c.From >= f.From-1e-6 && c.To <= f.To+1e-6 {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("cut %.3f-%.3f is not inside a keep fragment; everything outside keep is cut already, "+
				"so trim the fragment instead", c.From, c.To)
		}
	}
	n := len(e.Keep)
	gap := func(from, to float64, reason string) {
		if to-from > 1e-6 {
			cuts = append(cuts, Cut{From: from, To: to, Reason: reason, Implicit: true})
		}
	}
	gap(0, e.Keep[0].From, "outside keep: before fragment 1")
	for i := 1; i < n; i++ {
		gap(e.Keep[i-1].To, e.Keep[i].From, fmt.Sprintf("outside keep: between fragments %d and %d", i, i+1))
	}
	gap(e.Keep[n-1].To, e.Duration, fmt.Sprintf("outside keep: after fragment %d", n))
	sort.SliceStable(cuts, func(i, j int) bool { return cuts[i].From < cuts[j].From })
	return cuts, nil
}
