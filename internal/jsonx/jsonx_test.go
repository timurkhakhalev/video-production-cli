package jsonx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures were written by Python json.dump(indent=2, ensure_ascii=False), some then edited
// by hand: parsing and dumping must reproduce them (key order, unknown fields, number
// literals), up to the trailing newline Dump always adds.
func TestRoundTripFixtures(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/fixtures/*/*.json")
	if len(files) == 0 {
		t.Fatal("no fixtures")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		v, err := Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if got := string(Dump(v)); strings.TrimSuffix(got, "\n") != strings.TrimSuffix(string(b), "\n") {
			t.Errorf("%s: round trip differs", f)
		}
	}
}

func TestDumpPythonStyle(t *testing.T) {
	o := NewObject().Set("a", 600.0).Set("b", 1e-5).Set("c", []Value{}).Set("d", NewObject()).
		Set("e", "кириллица \"q\"\n\x01").Set("f", nil).Set("g", Number("1e3")).Set("h", Number("7"))
	want := "{\n  \"a\": 600.0,\n  \"b\": 1e-05,\n  \"c\": [],\n  \"d\": {},\n" +
		"  \"e\": \"кириллица \\\"q\\\"\\n\\u0001\",\n  \"f\": null,\n  \"g\": 1e3,\n  \"h\": 7\n}\n"
	if got := string(Dump(o)); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSetAfterAndDelete(t *testing.T) {
	o := NewObject().Set("source", "x").Set("mode", "step")
	o.SetAfter("source", "keep", []Value{})
	o.Delete("mode")
	o.Set("source", "y")
	if got := string(Dump(o)); got != "{\n  \"source\": \"y\",\n  \"keep\": []\n}\n" {
		t.Fatal(got)
	}
}

func TestRepr(t *testing.T) {
	for v, want := range map[Value]string{nil: "None", true: "True", Number("2.50"): "2.5", Number("3"): "3", "it's": `'it\'s'`} {
		if got := Repr(v); got != want {
			t.Errorf("Repr(%v) = %s, want %s", v, got, want)
		}
	}
}
