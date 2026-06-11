package store

import (
	"bytes"
	"runtime"
	"testing"
)

func TestProtectCredsRoundTrip(t *testing.T) {
	in := []byte(`{"claudeAiOauth":{"accessToken":"secret"}}`)
	enc, err := ProtectCreds(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := UnprotectCreds(enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(in, out) {
		t.Fatalf("round trip mismatch: %q", out)
	}
	if runtime.GOOS == "windows" && bytes.Equal(in, enc) {
		t.Fatal("DPAPI did not encrypt on Windows")
	}
}
