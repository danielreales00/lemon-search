package flags

import "testing"

func TestTruthy(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"  true  ", true},
		{"0", false},
		{"false", false},
		{"", false},
		{"yes", false},
		{"on", false},
	}
	for _, tc := range tests {
		if got := truthy(tc.in); got != tc.want {
			t.Errorf("truthy(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("LEMON_FF_INTENT", "true")
	if !FromEnv().Intent {
		t.Fatal("LEMON_FF_INTENT=true should enable Intent")
	}
	t.Setenv("LEMON_FF_INTENT", "")
	if FromEnv().Intent {
		t.Fatal("empty LEMON_FF_INTENT should leave Intent off")
	}
}
