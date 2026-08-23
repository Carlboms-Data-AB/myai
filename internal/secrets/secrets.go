// Package secrets manages the credentials MyAI generates for OpenCode Web.
// Credentials live only on the local machine and are never written into the
// repository or into any file MyAI publishes.
package secrets

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Credentials are the OpenCode Web basic-auth details.
type Credentials struct {
	Username string `toml:"username"`
	Password string `toml:"password"`
}

// Complete reports whether both fields are present.
func (c Credentials) Complete() bool {
	return strings.TrimSpace(c.Username) != "" && strings.TrimSpace(c.Password) != ""
}

// Load reads stored credentials. A missing file yields empty credentials
// rather than an error, so callers can generate on demand.
func Load(path string) (Credentials, error) {
	var creds Credentials
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return creds, nil
	}
	if err != nil {
		return creds, err
	}
	if err := toml.Unmarshal(data, &creds); err != nil {
		return Credentials{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return creds, nil
}

// Save writes credentials with the tightest permissions the OS supports.
func Save(path string, creds Credentials) error {
	if !creds.Complete() {
		return errors.New("refusing to store incomplete credentials")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".web-auth-*.toml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if err := toml.NewEncoder(tmp).Encode(creds); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return Restrict(path)
}

// Ensure returns the stored credentials, generating and persisting a password
// the first time. An existing password is preserved so upgrades do not
// invalidate bookmarks or saved logins.
func Ensure(path, username string) (Credentials, error) {
	creds, err := Load(path)
	if err != nil {
		return creds, err
	}
	changed := false
	if strings.TrimSpace(creds.Username) != username {
		creds.Username = username
		changed = true
	}
	if strings.TrimSpace(creds.Password) == "" {
		password, err := GeneratePassword()
		if err != nil {
			return creds, err
		}
		creds.Password = password
		changed = true
	}
	if changed {
		if err := Save(path, creds); err != nil {
			return creds, err
		}
	}
	return creds, nil
}

// Rotate replaces the stored password with a freshly generated one.
func Rotate(path, username string) (Credentials, error) {
	password, err := GeneratePassword()
	if err != nil {
		return Credentials{}, err
	}
	creds := Credentials{Username: username, Password: password}
	if err := Save(path, creds); err != nil {
		return Credentials{}, err
	}
	return creds, nil
}

// GeneratePassword returns 18 bytes of cryptographic randomness, hex encoded,
// matching the strength the prototype used.
func GeneratePassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
