package ffmpeg

import (
	"strings"

	"video-production-cli/internal/jsonx"
	"video-production-cli/internal/pyfmt"
)

// Probe runs ffprobe -show_streams -show_format and returns the parsed JSON.
func Probe(path string) (*jsonx.Object, error) {
	out, err := Output(FFprobe, "-v", "error", "-show_streams", "-show_format", "-of", "json", path)
	if err != nil {
		return nil, err
	}
	v, err := jsonx.Parse([]byte(out))
	if err != nil {
		return nil, err
	}
	o, _ := v.(*jsonx.Object)
	if o == nil {
		o = jsonx.NewObject()
	}
	return o, nil
}

// Stream returns the first stream of codec_type kind ("video"/"audio"), or nil.
func Stream(info *jsonx.Object, kind string) *jsonx.Object {
	streams, _ := Get(info, "streams").([]jsonx.Value)
	for _, s := range streams {
		if o, ok := s.(*jsonx.Object); ok && Get(o, "codec_type") == kind {
			return o
		}
	}
	return nil
}

// Get reads a key of an object (nil-safe).
func Get(o *jsonx.Object, k string) jsonx.Value {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

// GetObj reads a nested object (nil if missing).
func GetObj(o *jsonx.Object, k string) *jsonx.Object {
	v, _ := Get(o, k).(*jsonx.Object)
	return v
}

// Float is Python's float(v or 0) for probe values (numbers or numeric strings).
func Float(v jsonx.Value) float64 {
	switch t := v.(type) {
	case jsonx.Number:
		return t.Float()
	case float64:
		return t
	case int64:
		return float64(t)
	case string:
		if t == "" {
			return 0
		}
		f, _ := pyfmt.ParseFloat(t)
		return f
	}
	return 0
}

// Int is Python's int(v or 0).
func Int(v jsonx.Value) int64 { return int64(Float(v)) }

// Silence is a detected pause in seconds relative to the analysed input.
type Silence struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Open  bool    `json:"open,omitempty"` // no silence_end before the input ended
}

// ParseSilences parses silencedetect output like render.py does.
func ParseSilences(stderr string) []Silence {
	var out []Silence
	var s0 *float64
	for _, line := range strings.Split(stderr, "\n") {
		if i := strings.Index(line, "silence_start:"); i >= 0 {
			if f, ok := firstFloat(line[i+len("silence_start:"):]); ok {
				s0 = &f
			}
		} else if i := strings.Index(line, "silence_end:"); i >= 0 && s0 != nil {
			if f, ok := firstFloat(line[i+len("silence_end:"):]); ok {
				out = append(out, Silence{Start: *s0, End: f})
				s0 = nil
			}
		}
	}
	if s0 != nil {
		out = append(out, Silence{Start: *s0, Open: true})
	}
	return out
}

func firstFloat(s string) (float64, bool) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, false
	}
	v, err := pyfmt.ParseFloat(f[0])
	return v, err == nil
}
