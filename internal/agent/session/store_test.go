package session

import (
	"reflect"
	"testing"
)

func TestNullStr(t *testing.T) {
	if v := nullStr(""); v != nil {
		t.Errorf("empty → nil, got %v", v)
	}
	if v := nullStr("hello"); v != "hello" {
		t.Errorf("non-empty pass-through, got %v", v)
	}
}

func TestRawJSON_Scan(t *testing.T) {
	cases := []struct {
		name string
		src  any
		want map[string]any
	}{
		{"nil source", nil, nil},
		{"sql NULL literal bytes", []byte("null"), nil},
		{"empty bytes", []byte{}, nil},
		{"valid object", []byte(`{"a":1,"b":"x"}`), map[string]any{"a": float64(1), "b": "x"}},
		{"string source", `{"k":"v"}`, map[string]any{"k": "v"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dst map[string]any
			r := &rawJSON{dst: &dst}
			if err := r.Scan(c.src); err != nil {
				t.Fatalf("Scan: %v", err)
			}
			if !reflect.DeepEqual(dst, c.want) {
				t.Errorf("dst = %#v, want %#v", dst, c.want)
			}
		})
	}
}

func TestRawJSON_Scan_BadJSON(t *testing.T) {
	var dst map[string]any
	r := &rawJSON{dst: &dst}
	if err := r.Scan([]byte("not-json")); err == nil {
		t.Fatal("expected unmarshal error on invalid JSON")
	}
}
