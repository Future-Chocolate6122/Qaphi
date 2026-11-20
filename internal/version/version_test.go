package version

import "testing"

func TestParseRelease(t *testing.T) {
	tests := []struct {
		in   string
		want Semver
		ok   bool
	}{
		{"v0.1.0", Semver{0, 1, 0}, true},
		{"v1.20.300", Semver{1, 20, 300}, true},
		{"v10.0.0", Semver{10, 0, 0}, true},
		{"dev", Semver{}, false},
		{"0.1.0", Semver{}, false},
		{"v0.1", Semver{}, false},
		{"v0.1.0-3-gabc123", Semver{}, false},
		{"v0.1.0-dirty", Semver{}, false},
		{"v01.1.0", Semver{}, false},
		{"v0..1", Semver{}, false},
		{"v0.1.0.", Semver{}, false},
		{"v0.1.0.4", Semver{}, false},
	}
	for _, tt := range tests {
		got, ok := ParseRelease(tt.in)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseRelease(%q) = %v, %v; se esperaba %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v0.1.0", "v0.1.0", 0},
		{"v0.1.0", "v0.2.0", -1},
		{"v0.10.0", "v0.9.9", 1},
		{"v1.0.0", "v0.99.99", 1},
		{"v0.1.1", "v0.1.2", -1},
	}
	for _, tt := range tests {
		a, _ := ParseRelease(tt.a)
		b, _ := ParseRelease(tt.b)
		if got := a.Compare(b); got != tt.want {
			t.Errorf("%s vs %s = %d, se esperaba %d", tt.a, tt.b, got, tt.want)
		}
	}
}
