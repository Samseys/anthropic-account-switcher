package main

import (
	"crypto/sha256"
	"encoding/hex"
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

func TestVerifyChecksum(t *testing.T) {
	data := []byte("hello world")
	sum := sha256.Sum256(data)
	hexsum := hex.EncodeToString(sum[:])
	name := "claude-acc_linux_amd64"

	sums := []byte("deadbeef  other_file\n" + hexsum + "  " + name + "\n")
	if err := verifyChecksum(data, name, sums); err != nil {
		t.Errorf("verifyChecksum matched entry should pass: %v", err)
	}

	// A "*name" (binary-mode) entry must also match.
	star := []byte(hexsum + " *" + name + "\n")
	if err := verifyChecksum(data, name, star); err != nil {
		t.Errorf("verifyChecksum binary-mode entry should pass: %v", err)
	}

	if err := verifyChecksum([]byte("tampered"), name, sums); err == nil {
		t.Error("verifyChecksum should fail on mismatched content")
	}

	if err := verifyChecksum(data, "missing_asset", sums); err == nil {
		t.Error("verifyChecksum should fail when the asset is absent")
	}
}
