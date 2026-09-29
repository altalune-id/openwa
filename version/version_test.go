package version

import (
	"strings"
	"testing"
)

func TestString_Default(t *testing.T) {
	if got := String(); got == "" {
		t.Fatal("version.String() must never be empty")
	}
}

func TestString_ReflectsVersionVar(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "9.9.9"
	if !strings.Contains(String(), "9.9.9") {
		t.Fatalf("String() should include %q, got %q", Version, String())
	}
}

func TestInfo_HasAllFields(t *testing.T) {
	i := Get()
	if i.Version == "" {
		t.Error("Info.Version empty")
	}
}

// SECURITY: regression guard against function-call initializers on Version — those silently ignore ldflags -X.
func TestDefault_FallsBackToEmbedded(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = ""
	got := Default()
	if got == "" {
		t.Fatal("Default() must fall back to embedded VERSION when Version unset")
	}
	if strings.Contains(got, "\n") || strings.TrimSpace(got) != got {
		t.Errorf("Default() must be trimmed, got %q", got)
	}
}

func TestDefault_PrefersLdflagsStamp(t *testing.T) {
	orig := Version
	t.Cleanup(func() { Version = orig })
	Version = "v1.2.3-stamped"
	if got := Default(); got != "v1.2.3-stamped" {
		t.Errorf("Default() = %q, want ldflags value", got)
	}
}

func TestTriple(t *testing.T) {
	tests := []struct {
		in   string
		want [3]uint32
	}{
		{"v1.2.3", [3]uint32{1, 2, 3}},
		{"0.1.0-dev", [3]uint32{0, 1, 0}},
		{"garbage", [3]uint32{0, 0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			orig := Version
			t.Cleanup(func() { Version = orig })
			Version = tt.in
			if got := Triple(); got != tt.want {
				t.Fatalf("Triple() = %v, want %v", got, tt.want)
			}
		})
	}
}
