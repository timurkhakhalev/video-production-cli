// Package jsonx is an order-preserving JSON model with Python json.dump(indent=2,
// ensure_ascii=False) output, so edit.json keeps its key order and unknown fields
// and is written exactly like render.py writes it.
package jsonx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"video-production-cli/internal/pyfmt"
)

// Value is nil, bool, Number, string, []Value or *Object.
type Value = any

// Number keeps the JSON literal; Int reports whether it was written as an integer.
type Number string

func (n Number) Float() float64 { f, _ := strconv.ParseFloat(string(n), 64); return f }
func (n Number) IsInt() bool    { return !strings.ContainsAny(string(n), ".eE") }

// Object is a JSON object with its key order.
type Object struct {
	keys []string
	vals map[string]Value
}

func NewObject() *Object { return &Object{vals: map[string]Value{}} }

func (o *Object) Keys() []string { return o.keys }

func (o *Object) Get(k string) (Value, bool) {
	v, ok := o.vals[k]
	return v, ok
}

// Set replaces a value in place or appends the key at the end.
func (o *Object) Set(k string, v Value) *Object {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
	return o
}

// SetFirst inserts a new key at the front (or replaces it in place).
func (o *Object) SetFirst(k string, v Value) *Object {
	if _, ok := o.vals[k]; !ok {
		o.keys = append([]string{k}, o.keys...)
	}
	o.vals[k] = v
	return o
}

// SetAfter inserts a new key right after `after` (or at the end); existing keys stay in place.
func (o *Object) SetAfter(after, k string, v Value) *Object {
	if _, ok := o.vals[k]; ok {
		o.vals[k] = v
		return o
	}
	o.vals[k] = v
	for i, key := range o.keys {
		if key == after {
			o.keys = append(o.keys[:i+1], append([]string{k}, o.keys[i+1:]...)...)
			return o
		}
	}
	o.keys = append(o.keys, k)
	return o
}

func (o *Object) Delete(k string) {
	if _, ok := o.vals[k]; !ok {
		return
	}
	delete(o.vals, k)
	for i, key := range o.keys {
		if key == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Parse decodes JSON keeping object key order and number literals.
func Parse(data []byte) (Value, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("unexpected data after the JSON value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (Value, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := NewObject()
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(kt.(string), v)
			}
			_, err := dec.Token()
			return o, err
		case '[':
			arr := []Value{}
			for dec.More() {
				v, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			_, err := dec.Token()
			return arr, err
		}
	case json.Number:
		return Number(t), nil
	}
	return tok, nil // string, bool, nil
}

// Dump writes v like Python json.dump(v, indent=2, ensure_ascii=False) plus a trailing newline.
func Dump(v Value) []byte {
	var b bytes.Buffer
	dump(&b, v, 0)
	b.WriteByte('\n')
	return b.Bytes()
}

func dump(b *bytes.Buffer, v Value, depth int) {
	pad := func(d int) { b.WriteByte('\n'); b.WriteString(strings.Repeat("  ", d)) }
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case Number:
		b.WriteString(string(t)) // literal kept as read: rewrites stay minimal diffs
	case float64:
		b.WriteString(floatJSON(t))
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case string:
		writeString(b, t)
	case []Value:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			pad(depth + 1)
			dump(b, e, depth+1)
		}
		pad(depth)
		b.WriteByte(']')
	case *Object:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, k := range t.keys {
			if i > 0 {
				b.WriteByte(',')
			}
			pad(depth + 1)
			writeString(b, k)
			b.WriteString(": ")
			dump(b, t.vals[k], depth+1)
		}
		pad(depth)
		b.WriteByte('}')
	default:
		panic(fmt.Sprintf("jsonx: unsupported type %T", v))
	}
}

func floatJSON(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case math.IsNaN(f):
		return "NaN"
	}
	return pyfmt.Repr(f)
}

// writeString escapes like Python's json with ensure_ascii=False.
func writeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// Repr formats a value like Python's repr/%r of the decoded JSON value.
func Repr(v Value) string {
	switch t := v.(type) {
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	case Number:
		if t.IsInt() {
			return string(t)
		}
		return pyfmt.Repr(t.Float())
	case string:
		return "'" + strings.ReplaceAll(strings.ReplaceAll(t, `\`, `\\`), "'", `\'`) + "'"
	}
	return fmt.Sprint(v)
}

// Str formats a value like Python's str()/%s.
func Str(v Value) string {
	if s, ok := v.(string); ok {
		return s
	}
	return Repr(v)
}
