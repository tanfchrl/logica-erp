package approvals

import "testing"

func TestNullStr(t *testing.T) {
	if v := nullStr(""); v != nil {
		t.Errorf("empty → nil, got %v", v)
	}
	if v := nullStr("x"); v != "x" {
		t.Errorf("non-empty pass-through, got %v", v)
	}
}
