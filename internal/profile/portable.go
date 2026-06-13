package profile

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/Samseys/anthropic-account-switcher/internal/claudejson"
	"github.com/Samseys/anthropic-account-switcher/internal/lock"
	"github.com/Samseys/anthropic-account-switcher/internal/paths"
)

// passEnv lets callers supply the passphrase non-interactively (avoids shell history).
const passEnv = "ACC_CLAUDE_PASSPHRASE"

const bundleVersion = 1

// exportBundle is the plaintext bundle (and the inner payload of an encrypted one).
type exportBundle struct {
	Version  int             `json:"version"`
	Profiles []exportProfile `json:"profiles"`
}

// exportProfile carries one profile. Raw JSON fields round-trip verbatim into profile files.
type exportProfile struct {
	Name         string          `json:"name"`
	Email        string          `json:"email,omitempty"`
	OAuthAccount json.RawMessage `json:"oauthAccount,omitempty"`
	UserID       json.RawMessage `json:"userID,omitempty"`
	Credentials  json.RawMessage `json:"credentials"`
}

// sealed is the encrypted envelope: AES-256-GCM over exportBundle JSON with a
// PBKDF2-derived key. Salt, Nonce, Ciphertext marshal to base64.
type sealed struct {
	Encrypted  bool   `json:"encrypted"`
	KDF        string `json:"kdf"`
	Iter       int    `json:"iter"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

const (
	kdfName = "pbkdf2-sha256"
	kdfIter = 600_000
	saltLen = 16
	keyLen  = 32 // AES-256
)

// Export writes one profile (or all, when all is set) to a portable bundle.
// With encrypt, the bundle is AES-256-GCM encrypted under a passphrase;
// otherwise it is plaintext with a warning. An empty or "-" file writes to stdout.
func Export(name string, all bool, file string, encrypt bool) error {
	var names []string
	if all {
		names = profileNames()
		if len(names) == 0 {
			return fmt.Errorf("no profiles to export%s", availableHint())
		}
	} else {
		n, _, err := resolveProfile(name)
		if err != nil {
			if name == "" {
				return fmt.Errorf("usage: %s export <name|--all> [file] [--passphrase]", paths.Bin)
			}
			return err
		}
		names = []string{n}
	}

	bundle := exportBundle{Version: bundleVersion}
	for _, n := range names {
		dir := profilePath(n)
		creds, err := readProfileCreds(dir)
		if err != nil {
			return fmt.Errorf("profile %q: %w", n, err)
		}
		p := exportProfile{
			Name:        n,
			Email:       profileEmail(dir),
			Credentials: json.RawMessage(creds),
		}
		if o := profileOAuth(dir); o != "" {
			p.OAuthAccount = json.RawMessage(o)
		}
		if u := profileUserID(dir); u != "" {
			p.UserID = json.RawMessage(u)
		}
		bundle.Profiles = append(bundle.Profiles, p)
	}

	plain, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}

	out := plain
	if encrypt {
		pass, err := readPassphrase(true)
		if err != nil {
			return err
		}
		if out, err = seal(plain, pass); err != nil {
			return err
		}
	} else {
		fmt.Fprintln(os.Stderr, "WARNING: this export is PLAINTEXT and contains live OAuth tokens.")
		fmt.Fprintln(os.Stderr, "Anyone who reads the file can sign in as these accounts. Delete it when")
		fmt.Fprintln(os.Stderr, "done, or re-run with --passphrase to encrypt it.")
	}

	if file == "" || file == "-" {
		_, err := os.Stdout.Write(append(out, '\n'))
		return err
	}
	if err := paths.WriteFileAtomic(file, out, 0o600); err != nil {
		return err
	}
	noun := "profile"
	if len(names) != 1 {
		noun = "profiles"
	}
	fmt.Printf("Exported %d %s to %s\n", len(names), noun, file)
	return nil
}

// Import restores profiles from an Export bundle. Encrypted bundles are detected
// automatically and prompt for a passphrase. Same-name profiles are skipped
// unless overwrite is set.
func Import(file string, overwrite bool) error {
	raw, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	bundle, err := openBundle(raw)
	if err != nil {
		return err
	}

	release, err := lock.Acquire()
	if err != nil {
		return err
	}
	defer release()

	imported, skipped := 0, 0
	for _, p := range bundle.Profiles {
		name := paths.Sanitize(p.Name)
		if name == "" {
			fmt.Fprintln(os.Stderr, "Skipping a profile with an empty name.")
			skipped++
			continue
		}
		if profileExists(name) && !overwrite {
			fmt.Printf("Skipping %q: a profile of that name exists (pass --overwrite to replace).\n", name)
			skipped++
			continue
		}
		// One account, one profile. Skip if a different-named profile already
		// tracks this account (--overwrite only covers name collisions).
		if id := claudejson.Field(string(p.OAuthAccount), "accountUuid"); id != "" {
			if existing := profileForAccount(id); existing != "" && existing != name {
				fmt.Printf("Skipping %q: this account is already saved as profile %q (remove or rename it first).\n", name, existing)
				skipped++
				continue
			}
		}
		enc, err := encryptCreds([]byte(p.Credentials))
		if err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
		files := []profileFile{
			{fileCreds, enc},
			{fileEmail, []byte(p.Email)},
		}
		if len(p.OAuthAccount) > 0 {
			files = append(files, profileFile{fileOAuth, []byte(p.OAuthAccount)})
		}
		if len(p.UserID) > 0 {
			files = append(files, profileFile{fileUserID, []byte(p.UserID)})
		}
		if err := writeProfileAtomic(name, files); err != nil {
			return fmt.Errorf("profile %q: %w", name, err)
		}
		fmt.Printf("Imported %q (%s).\n", name, emailOrUnknown(p.Email))
		imported++
	}
	fmt.Printf("Done: %d imported, %d skipped.\n", imported, skipped)
	return nil
}

func openBundle(raw []byte) (exportBundle, error) {
	var probe struct {
		Encrypted bool `json:"encrypted"`
	}
	_ = json.Unmarshal(raw, &probe)

	data := raw
	if probe.Encrypted {
		pass, err := readPassphrase(false)
		if err != nil {
			return exportBundle{}, err
		}
		if data, err = open(raw, pass); err != nil {
			return exportBundle{}, err
		}
	}

	var b exportBundle
	if err := json.Unmarshal(data, &b); err != nil {
		return exportBundle{}, fmt.Errorf("not a valid %s export bundle: %w", paths.Bin, err)
	}
	if len(b.Profiles) == 0 {
		return exportBundle{}, fmt.Errorf("not a valid %s export bundle (no profiles)", paths.Bin)
	}
	return b, nil
}

// seal encrypts plain under a passphrase (AES-256-GCM, PBKDF2-SHA256 key).
func seal(plain []byte, pass string) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	key, err := pbkdf2.Key(sha256.New, pass, salt, kdfIter, keyLen)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	env := sealed{
		Encrypted:  true,
		KDF:        kdfName,
		Iter:       kdfIter,
		Salt:       salt,
		Nonce:      nonce,
		Ciphertext: gcm.Seal(nil, nonce, plain, nil),
	}
	return json.MarshalIndent(env, "", "  ")
}

func open(raw []byte, pass string) ([]byte, error) {
	var env sealed
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("malformed encrypted bundle: %w", err)
	}
	iter := env.Iter
	if iter <= 0 {
		iter = kdfIter
	}
	key, err := pbkdf2.Key(sha256.New, pass, env.Salt, iter, keyLen)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, env.Nonce, env.Ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed (wrong passphrase or corrupted file)")
	}
	return plain, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// readPassphrase reads from $ACC_CLAUDE_PASSPHRASE or prompts the terminal
// (echo-free). When confirm is true it asks twice (export path).
func readPassphrase(confirm bool) (string, error) {
	if v := os.Getenv(passEnv); v != "" {
		return v, nil
	}
	r := bufio.NewReader(os.Stdin)
	prompt := func(label string) (string, error) {
		fmt.Fprintf(os.Stderr, "%s (set %s to avoid this prompt): ", label, passEnv)
		var line string
		var err error
		if fd := int(os.Stdin.Fd()); term.IsTerminal(fd) {
			var b []byte
			b, err = term.ReadPassword(fd)
			fmt.Fprintln(os.Stderr) // ReadPassword suppresses the newline
			line = string(b)
		} else {
			line, err = r.ReadString('\n')
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if err != nil {
				return "", fmt.Errorf("reading passphrase: %w", err)
			}
			return "", fmt.Errorf("empty passphrase")
		}
		return line, nil
	}
	pass, err := prompt("Enter passphrase")
	if err != nil {
		return "", err
	}
	if confirm {
		again, err := prompt("Confirm passphrase")
		if err != nil {
			return "", err
		}
		if again != pass {
			return "", fmt.Errorf("passphrases do not match")
		}
	}
	return pass, nil
}
