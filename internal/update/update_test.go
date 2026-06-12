package update

import (
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.2.0", "1.1.9", 1},
		{"v1.0.0", "1.0.0", 0}, // leading v ignored
		{"2.0", "2.0.0", 0},    // missing patch == 0
		{"1.0.0", "2.0.0", -1},
		{"10.0.0", "9.0.0", 1},    // numeric, not lexical
		{"1.0.0-rc1", "1.0.0", 0}, // pre-release suffix dropped
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
