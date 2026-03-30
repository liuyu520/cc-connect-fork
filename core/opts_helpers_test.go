package core

import "testing"

func TestOptsString(t *testing.T) {
	opts := map[string]any{"key": "value", "empty": "", "num": 42}
	if got := OptsString(opts, "key"); got != "value" {
		t.Errorf("OptsString(key) = %q, want %q", got, "value")
	}
	if got := OptsString(opts, "missing"); got != "" {
		t.Errorf("OptsString(missing) = %q, want empty", got)
	}
	if got := OptsString(opts, "num"); got != "" {
		t.Errorf("OptsString(num) = %q, want empty (wrong type)", got)
	}
}

func TestOptsBool(t *testing.T) {
	opts := map[string]any{"yes": true, "no": false, "str": "true"}
	if got := OptsBool(opts, "yes"); !got {
		t.Error("OptsBool(yes) = false, want true")
	}
	if got := OptsBool(opts, "no"); got {
		t.Error("OptsBool(no) = true, want false")
	}
	if got := OptsBool(opts, "missing"); got {
		t.Error("OptsBool(missing) = true, want false")
	}
	if got := OptsBool(opts, "str"); got {
		t.Error("OptsBool(str='true') = true, want false (strict type)")
	}
}
