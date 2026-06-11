package claudejson

import (
	"strings"
	"testing"
)

// A document that mirrors the real hazards: duplicate keys differing only in
// case, and userID/oauthAccount-shaped keys nested inside other values.
const doc = `{
  "projects": {"C:\\Foo": {"userID": "nested-A"}, "c:\\foo": {"userID": "nested-B"}},
  "oauthAccount": {"emailAddress": "a@b.com", "organizationName": "Acme", "seatTier": "max"},
  "userID": "old-id",
  "other": [1, 2, {"userID": "nested-C"}, "a string with } and \" inside"]
}`

func TestTopLevelValue(t *testing.T) {
	o, ok := TopLevelValue(doc, "oauthAccount")
	if !ok {
		t.Fatal("oauthAccount not found")
	}
	if got := Field(o, "emailAddress"); got != "a@b.com" {
		t.Fatalf("emailAddress = %q", got)
	}
	if got := Field(o, "seatTier"); got != "max" {
		t.Fatalf("seatTier = %q", got)
	}
	u, ok := TopLevelValue(doc, "userID")
	if !ok || u != `"old-id"` {
		t.Fatalf("userID = %q ok=%v", u, ok)
	}
}

func TestSpliceIsByteExactAndDepthAware(t *testing.T) {
	out := SetTopLevelValue(doc, "userID", `"new-id"`)

	// The top-level userID changed...
	if u, _ := TopLevelValue(out, "userID"); u != `"new-id"` {
		t.Fatalf("top-level userID = %q", u)
	}
	if strings.Contains(out, `"old-id"`) {
		t.Fatal("old top-level id still present")
	}
	// ...but the nested look-alikes did not.
	for _, nested := range []string{"nested-A", "nested-B", "nested-C"} {
		if !strings.Contains(out, nested) {
			t.Fatalf("nested value %q was lost", nested)
		}
	}

	// Everything outside the replaced span must be byte-identical.
	span, _ := TopLevelSpan(doc, "userID")
	wantPrefix, wantSuffix := doc[:span.Start], doc[span.End:]
	if !strings.HasPrefix(out, wantPrefix) {
		t.Fatal("prefix not preserved byte-for-byte")
	}
	if !strings.HasSuffix(out, wantSuffix) {
		t.Fatal("suffix not preserved byte-for-byte")
	}
}

func TestMissingKeyLeavesDocumentUnchanged(t *testing.T) {
	if out := SetTopLevelValue(doc, "doesNotExist", `"x"`); out != doc {
		t.Fatal("document changed for an absent key")
	}
	if _, ok := TopLevelValue(doc, "doesNotExist"); ok {
		t.Fatal("absent key reported as found")
	}
}
