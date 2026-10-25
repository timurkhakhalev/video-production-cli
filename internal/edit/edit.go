// Package edit is the edit.json model: loading, validation, frame snapping, kept ranges,
// speed-up splitting and the output timeline. Arithmetic mirrors render.py exactly
// (rational snapping, float64 afterwards) so outputs match it byte for byte.
package edit

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"

	"video-production-cli/internal/jsonx"
	"video-production-cli/internal/pyfmt"
)

// Statuses a chunk may have.
var Statuses = []string{"draft", "in_review", "approved"}

// MinKeep: shorter kept fragments flash on screen.
const MinKeep = 0.3

type Chunk struct {
	ID     jsonx.Value // printed with %s like render.py
	Start  float64
	End    float64
	Status string
	Obj    *jsonx.Object
}

// IDString is the chunk id as render.py prints it.
func (c Chunk) IDString() string { return jsonx.Str(c.ID) }

// IDInt is the numeric id (0 if not a number).
func (c Chunk) IDInt() int {
	if n, ok := c.ID.(jsonx.Number); ok {
		return int(n.Float())
	}
	return 0
}

type Cut struct {
	From, To float64
	Reason   string
	// Implicit cuts are the gaps outside keep fragments (keep mode), not entries of "cuts".
	Implicit bool
}

// Fragment is one entry of "keep": a source range that stays in the edit (keep mode).
type Fragment struct {
	From, To float64
	Reason   string
}

type Speedup struct {
	From, To float64
	Speed    int
	Reason   string
}

// Keep is a kept source range; Cut is the cut that follows it (nil for the last one).
type Keep struct {
	In, Out float64
	Cut     *Cut
}

// Piece is a part of a kept range at one speed, placed on the output timeline.
type Piece struct {
	In, Out  float64
	Speed    int
	OutStart float64
	Frames   int64
}

type Edit struct {
	Path     string
	Workdir  string
	Doc      *jsonx.Object
	Enc      *jsonx.Object
	FPS      *big.Rat
	Duration float64
	Source   string
	Chunks   []Chunk
	Cuts     []Cut // explicit and implicit, sorted
	Speedups []Speedup
	Keep     []Fragment // keep mode: everything outside these fragments is cut
}

// Load reads edit.json and the encode.json next to it, then validates.
func Load(path string) (*Edit, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	doc, err := readObject(abs)
	if err != nil {
		return nil, err
	}
	e := &Edit{Path: abs, Workdir: filepath.Dir(abs), Doc: doc}
	encPath := filepath.Join(e.Workdir, "encode.json")
	if _, err := os.Stat(encPath); err != nil {
		return nil, fmt.Errorf("encode.json missing next to edit.json; run init")
	}
	if e.Enc, err = readObject(encPath); err != nil {
		return nil, err
	}
	fps, ok := new(big.Rat).SetString(str(get(e.Enc, "video", "fps")))
	if !ok || fps.Sign() <= 0 {
		return nil, fmt.Errorf("encode.json: bad video fps %v", get(e.Enc, "video", "fps"))
	}
	e.FPS = fps
	e.Duration = num(get(e.Enc, "duration"))
	e.Source = str(get(doc, "source"))
	if err := e.validate(); err != nil {
		return nil, err
	}
	return e, nil
}

func readObject(path string) (*jsonx.Object, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	v, err := jsonx.Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, fmt.Errorf("%s: expected a JSON object", path)
	}
	return o, nil
}

// get walks nested objects; missing keys give nil.
func get(o *jsonx.Object, keys ...string) jsonx.Value {
	var v jsonx.Value = o
	for _, k := range keys {
		obj, ok := v.(*jsonx.Object)
		if !ok {
			return nil
		}
		v, _ = obj.Get(k)
	}
	return v
}

func num(v jsonx.Value) float64 {
	if n, ok := v.(jsonx.Number); ok {
		return n.Float()
	}
	return 0
}

func str(v jsonx.Value) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truthy(v jsonx.Value) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case jsonx.Number:
		return t.Float() != 0
	case []jsonx.Value:
		return len(t) > 0
	case *jsonx.Object:
		return len(t.Keys()) > 0
	}
	return true
}

func list(o *jsonx.Object, key string) []*jsonx.Object {
	arr, _ := get(o, key).([]jsonx.Value)
	out := make([]*jsonx.Object, 0, len(arr))
	for _, v := range arr {
		if obj, ok := v.(*jsonx.Object); ok {
			out = append(out, obj)
		}
	}
	return out
}

func requireNum(o *jsonx.Object, what, key string) (float64, error) {
	v, _ := o.Get(key)
	n, ok := v.(jsonx.Number)
	if !ok {
		return 0, fmt.Errorf("%s: %q must be a number (got %s)", what, key, jsonx.Repr(v))
	}
	return n.Float(), nil
}

func (e *Edit) validate() error {
	chunkObjs := list(e.Doc, "chunks")
	if len(chunkObjs) == 0 {
		return fmt.Errorf("edit.json has no chunks")
	}
	keep, err := e.parseKeep()
	if err != nil {
		return err
	}
	e.Keep = keep
	e.Chunks = nil
	if len(keep) > 0 {
		if err := e.parseKeepChunks(chunkObjs); err != nil {
			return err
		}
	}
	prevEnd := 0.0
	for _, o := range chunkObjs {
		if len(keep) > 0 {
			break
		}
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
		if abs(start-prevEnd) > 1e-3 {
			return fmt.Errorf("chunk %s must start where previous ends (%.3f)", jsonx.Str(id), prevEnd)
		}
		if end <= start {
			return fmt.Errorf("chunk %s has non-positive length", jsonx.Str(id))
		}
		prevEnd = end
		e.Chunks = append(e.Chunks, Chunk{ID: id, Start: start, End: end, Status: st, Obj: o})
	}
	if len(keep) == 0 && abs(prevEnd-e.Duration) > 0.05 {
		return fmt.Errorf("last chunk must end at source duration %.3f", e.Duration)
	}

	var cuts []Cut
	for _, o := range list(e.Doc, "cuts") {
		from, err := requireNum(o, "cut", "from")
		if err != nil {
			return err
		}
		to, err := requireNum(o, "cut", "to")
		if err != nil {
			return err
		}
		r, _ := o.Get("reason")
		c := Cut{From: from, To: to}
		if truthy(r) {
			c.Reason = jsonx.Str(r)
		}
		cuts = append(cuts, c)
	}
	sort.SliceStable(cuts, func(i, j int) bool { return cuts[i].From < cuts[j].From })
	lastTo := -1.0
	for _, c := range cuts {
		if c.To <= c.From {
			return fmt.Errorf("cut %.3f-%.3f has non-positive length", c.From, c.To)
		}
		if c.From < -1e-6 || c.To > e.Duration+1e-3 {
			return fmt.Errorf("cut %.3f-%.3f outside source", c.From, c.To)
		}
		if c.From < lastTo-1e-6 {
			return fmt.Errorf("cuts overlap near %.3f", c.From)
		}
		if c.Reason == "" {
			return fmt.Errorf("cut %.3f-%.3f has no reason", c.From, c.To)
		}
		for _, ch := range e.Chunks {
			if c.From < ch.End-1e-6 && ch.End-1e-6 < c.To-2e-6 {
				return fmt.Errorf("cut %.3f-%.3f crosses chunk %s boundary %.3f; move the boundary",
					c.From, c.To, ch.IDString(), ch.End)
			}
		}
		lastTo = c.To
	}
	if len(keep) > 0 {
		if cuts, err = e.withImplicitCuts(cuts); err != nil {
			return err
		}
	}
	e.Cuts = cuts

	type rawSpeedup struct {
		Speedup
		speed jsonx.Value
	}
	var sps []rawSpeedup
	for _, o := range list(e.Doc, "speedups") {
		from, err := requireNum(o, "speed-up", "from")
		if err != nil {
			return err
		}
		to, err := requireNum(o, "speed-up", "to")
		if err != nil {
			return err
		}
		r, _ := o.Get("reason")
		sp, _ := o.Get("speed")
		s := rawSpeedup{Speedup: Speedup{From: from, To: to}, speed: sp}
		if truthy(r) {
			s.Reason = jsonx.Str(r)
		}
		sps = append(sps, s)
	}
	sort.SliceStable(sps, func(i, j int) bool { return sps[i].From < sps[j].From })
	lastTo = -1.0
	e.Speedups = nil
	for _, s := range sps {
		where := fmt.Sprintf("speed-up %.3f-%.3f", s.From, s.To)
		if s.To <= s.From {
			return fmt.Errorf("%s has non-positive length", where)
		}
		if s.From < -1e-6 || s.To > e.Duration+1e-3 {
			return fmt.Errorf("%s outside source", where)
		}
		if s.From < lastTo-1e-6 {
			return fmt.Errorf("speed-ups overlap near %.3f", s.From)
		}
		if s.Reason == "" {
			return fmt.Errorf("%s has no reason", where)
		}
		n, ok := s.speed.(jsonx.Number)
		if !ok || n.Float() != float64(int64(n.Float())) || n.Float() < 2 {
			return fmt.Errorf("%s: speed must be a whole number >= 2 (got %s)", where, jsonx.Repr(s.speed))
		}
		s.Speed = int(n.Float())
		for _, ch := range e.Chunks {
			if s.From < ch.End-1e-6 && ch.End-1e-6 < s.To-2e-6 {
				return fmt.Errorf("%s crosses chunk %s boundary %.3f; move the boundary", where, ch.IDString(), ch.End)
			}
		}
		for _, c := range cuts {
			if c.From < s.To-1e-6 && s.From < c.To-1e-6 {
				return fmt.Errorf("%s overlaps cut %.3f-%.3f; a range is either cut or sped up", where, c.From, c.To)
			}
		}
		lastTo = s.To
		e.Speedups = append(e.Speedups, s.Speedup)
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// FPSFloat is float(fps).
func (e *Edit) FPSFloat() float64 { return pyfmt.RatFloat(e.FPS) }

// Snap rounds a time to the nearest frame boundary (render.py Edit.snap).
func (e *Edit) Snap(t float64) float64 { return SnapFPS(e.FPS, t) }

// SnapFPS rounds a time to the nearest boundary of a frame grid.
func SnapFPS(fps *big.Rat, t float64) float64 {
	r := new(big.Rat)
	r.SetFloat64(t)
	r = pyfmt.LimitDenominator(r, 100000)
	r.Mul(r, fps)
	frame := pyfmt.RoundRat(r)
	q := new(big.Rat).SetInt64(frame)
	q.Quo(q, fps)
	return pyfmt.RatFloat(q)
}

// Chunk finds a chunk by numeric id.
func (e *Edit) Chunk(id int) (*Chunk, error) {
	for i := range e.Chunks {
		if n, ok := e.Chunks[i].ID.(jsonx.Number); ok && n.Float() == float64(id) {
			return &e.Chunks[i], nil
		}
	}
	return nil, fmt.Errorf("no chunk %d", id)
}

// Keeps returns the frame-snapped kept ranges within [start, end]; nil bounds mean the whole source.
func (e *Edit) Keeps(start, end *float64) []Keep {
	s, en := 0.0, e.Duration
	if start != nil {
		s = *start
	}
	if end != nil {
		en = *end
	}
	s, en = e.Snap(s), e.Snap(en)
	var out []Keep
	cur := s
	for i := range e.Cuts {
		c := &e.Cuts[i]
		a, b := e.Snap(c.From), e.Snap(c.To)
		if b <= s || a >= en {
			continue
		}
		if a < s {
			a = s
		}
		if b > en {
			b = en
		}
		if a > cur {
			out = append(out, Keep{cur, a, c})
		}
		if b > cur {
			cur = b
		}
	}
	if en > cur {
		out = append(out, Keep{cur, en, nil})
	}
	min := 0.5 / e.FPSFloat()
	res := out[:0]
	for _, k := range out {
		if k.Out-k.In >= min {
			res = append(res, k)
		}
	}
	return res
}

type span struct {
	in, out float64
	speed   int
}

// Split cuts a kept range at speed-up boundaries.
func (e *Edit) Split(a, b float64) []span {
	var out []span
	cur := a
	for _, s := range e.Speedups {
		sa, sb := e.Snap(s.From), e.Snap(s.To)
		if sb <= cur || sa >= b {
			continue
		}
		if sa < cur {
			sa = cur
		}
		if sb > b {
			sb = b
		}
		if sa > cur {
			out = append(out, span{cur, sa, 1})
		}
		out = append(out, span{sa, sb, s.Speed})
		cur = sb
	}
	if b > cur {
		out = append(out, span{cur, b, 1})
	}
	return out
}

// OutFrames is the output frame count of a source range: every speed-th frame is kept.
func (e *Edit) OutFrames(a, b float64, speed int) int64 {
	n := pyfmt.RoundInt((b - a) * e.FPSFloat())
	return ceilDiv(n, int64(speed))
}

func ceilDiv(n, d int64) int64 { return -floorDiv(-n, d) }

func floorDiv(n, d int64) int64 {
	q := n / d
	if (n%d != 0) && ((n < 0) != (d < 0)) {
		q--
	}
	return q
}

// OutLen is the output duration of a kept range.
func (e *Edit) OutLen(a, b float64) float64 {
	var n int64
	for _, p := range e.Split(a, b) {
		n += e.OutFrames(p.in, p.out, p.speed)
	}
	return float64(n) / e.FPSFloat()
}

// Timeline places the pieces of the kept ranges on the output.
func (e *Edit) Timeline(keeps []Keep) []Piece {
	var out []Piece
	var n int64
	fps := e.FPSFloat()
	for _, k := range keeps {
		for _, p := range e.Split(k.In, k.Out) {
			f := e.OutFrames(p.in, p.out, p.speed)
			out = append(out, Piece{p.in, p.out, p.speed, float64(n) / fps, f})
			n += f
		}
	}
	return out
}

// Join is one splice between two kept ranges.
type Join struct {
	OutTime float64
	SrcOut  float64
	SrcIn   float64
	Removed float64
	Reason  string
}

// JoinTable lists the joins inside [start, end].
func (e *Edit) JoinTable(start, end *float64) ([]Keep, []Join) {
	keeps := e.Keeps(start, end)
	var rows []Join
	t := 0.0
	for i, k := range keeps {
		t += e.OutLen(k.In, k.Out)
		if i+1 < len(keeps) {
			r := Join{OutTime: t, SrcOut: k.Out, SrcIn: keeps[i+1].In, Removed: keeps[i+1].In - k.Out}
			if k.Cut != nil {
				r.Reason = k.Cut.Reason
			}
			rows = append(rows, r)
		}
	}
	return keeps, rows
}

// Scope is a chunk or the whole source.
type Scope struct {
	Start, End *float64
	Label      string
	Chunk      *Chunk
}

// ScopeFor returns the scope of chunk id, or the whole source when id is 0.
func (e *Edit) ScopeFor(id int) (Scope, error) {
	if id == 0 {
		return Scope{Label: "all"}, nil
	}
	c, err := e.Chunk(id)
	if err != nil {
		return Scope{}, err
	}
	s, en := c.Start, c.End
	return Scope{Start: &s, End: &en, Label: fmt.Sprintf("chunk%02d", c.IDInt()), Chunk: c}, nil
}

// EncVideo / EncAudio give the encode.json sections.
func (e *Edit) EncVideo() *jsonx.Object { o, _ := get(e.Enc, "video").(*jsonx.Object); return o }
func (e *Edit) EncAudio() *jsonx.Object { o, _ := get(e.Enc, "audio").(*jsonx.Object); return o }
