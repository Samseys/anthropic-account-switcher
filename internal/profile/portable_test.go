package profile

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportToFile runs Export with stdout muted, failing the test on error.
func exportToFile(t *testing.T, name string, all bool, file string, encrypt bool) {
	t.Helper()
	var err error
	captureStdout(t, func() { err = Export(name, all, file, encrypt) })
	if err != nil {
		t.Fatalf("export: %v", err)
	}
}

func importFromFile(t *testing.T, file string, overwrite bool) {
	t.Helper()
	var err error
	captureStdout(t, func() { err = Import(file, overwrite) })
	if err != nil {
		t.Fatalf("import: %v", err)
	}
}

func TestExportImportPlaintextRoundTrip(t *testing.T) {
	setupEnv(t)
	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	writeAccount(t, "b@example.com", "id-b", "tok-b")
	if _, err := snapshot("personal", true); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(t.TempDir(), "bundle.json")
	exportToFile(t, "", true, file, false)

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("tok-a")) || !bytes.Contains(raw, []byte("tok-b")) {
		t.Fatalf("plaintext export missing tokens: %s", raw)
	}

	if err := os.RemoveAll(profilePath("work")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(profilePath("personal")); err != nil {
		t.Fatal(err)
	}
	importFromFile(t, file, false)

	dir := profilePath("work")
	pc, err := readProfileCreds(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pc), "tok-a") {
		t.Fatalf("imported credentials wrong: %s", pc)
	}
	if e := profileEmail(dir); e != "a@example.com" {
		t.Fatalf("imported email = %q", e)
	}
	if u := profileUserID(dir); u != fmt.Sprintf("%q", machineUserID) {
		t.Fatalf("imported userID = %q", u)
	}
	if a := profileAccountID(dir); a != "id-a" {
		t.Fatalf("imported accountUuid = %q", a)
	}
}

func TestExportImportEncryptedRoundTrip(t *testing.T) {
	setupEnv(t)
	t.Setenv(passEnv, "correct horse battery staple")
	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}

	file := filepath.Join(t.TempDir(), "bundle.enc.json")
	exportToFile(t, "work", false, file, true)

	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("tok-a")) {
		t.Fatalf("encrypted export leaked the plaintext token: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"encrypted": true`)) {
		t.Fatalf("encrypted bundle missing its marker: %s", raw)
	}

	if err := os.RemoveAll(profilePath("work")); err != nil {
		t.Fatal(err)
	}
	importFromFile(t, file, false)

	pc, err := readProfileCreds(profilePath("work"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pc), "tok-a") {
		t.Fatalf("decrypted credentials wrong: %s", pc)
	}
}

func TestImportWrongPassphraseFails(t *testing.T) {
	setupEnv(t)
	t.Setenv(passEnv, "right")
	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "b.json")
	exportToFile(t, "work", false, file, true)

	t.Setenv(passEnv, "wrong")
	var err error
	captureStdout(t, func() { err = Import(file, false) })
	if err == nil || !strings.Contains(err.Error(), "decryption failed") {
		t.Fatalf("want a decryption failure, got %v", err)
	}
}

func TestImportRespectsOverwrite(t *testing.T) {
	setupEnv(t)
	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "b.json")
	exportToFile(t, "work", false, file, false)

	// Simulate token rotation and re-save.
	writeAccount(t, "a2@example.com", "id-a", "tok-a-new")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}

	importFromFile(t, file, false) // no --overwrite: existing profile must be untouched
	pc, err := readProfileCreds(profilePath("work"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pc), "tok-a-new") {
		t.Fatalf("import without --overwrite clobbered the existing profile: %s", pc)
	}

	importFromFile(t, file, true) // --overwrite: bundle's snapshot replaces the profile
	pc, err = readProfileCreds(profilePath("work"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pc), "tok-a") || strings.Contains(string(pc), "tok-a-new") {
		t.Fatalf("import --overwrite did not replace the profile: %s", pc)
	}
}

func TestImportSkipsDuplicateAccount(t *testing.T) {
	setupEnv(t)
	writeAccount(t, "a@example.com", "id-a", "tok-a")
	if _, err := snapshot("work", true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "b.json")
	exportToFile(t, "work", false, file, false)

	// Account now lives under a different name; --overwrite only covers name collisions,
	// so importing must not create a second profile for it.
	if err := os.Rename(profilePath("work"), profilePath("renamed")); err != nil {
		t.Fatal(err)
	}
	importFromFile(t, file, true)
	if profileExists("work") {
		t.Fatal("import created a duplicate profile for an account already saved under another name")
	}
}

func TestImportRejectsBadBundle(t *testing.T) {
	setupEnv(t)
	file := filepath.Join(t.TempDir(), "junk.json")
	if err := os.WriteFile(file, []byte(`{"hello":"world"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var err error
	captureStdout(t, func() { err = Import(file, false) })
	if err == nil || !strings.Contains(err.Error(), "not a valid") {
		t.Fatalf("want an invalid-bundle error, got %v", err)
	}
}
