package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsureGeneratesThenPreservesPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-auth.toml")

	first, err := Ensure(path, "opencode")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !first.Complete() {
		t.Fatalf("credentials incomplete: %+v", first)
	}
	if len(first.Password) != 36 {
		t.Errorf("password length = %d, want 36 hex chars", len(first.Password))
	}

	second, err := Ensure(path, "opencode")
	if err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	if second.Password != first.Password {
		t.Error("password changed on second Ensure; upgrades must not invalidate saved logins")
	}
}

func TestEnsureUpdatesUsernameKeepingPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-auth.toml")
	first, err := Ensure(path, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Ensure(path, "tobias")
	if err != nil {
		t.Fatal(err)
	}
	if second.Username != "tobias" {
		t.Errorf("username = %q", second.Username)
	}
	if second.Password != first.Password {
		t.Error("password should survive a username change")
	}
}

func TestRotateReplacesPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-auth.toml")
	first, err := Ensure(path, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := Rotate(path, "opencode")
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Password == first.Password {
		t.Error("Rotate did not change the password")
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Password != rotated.Password {
		t.Error("rotated password was not persisted")
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "web-auth.toml")
	if _, err := Ensure(path, "opencode"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestSaveRejectsIncompleteCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "web-auth.toml")
	if err := Save(path, Credentials{Username: "opencode"}); err == nil {
		t.Error("expected an error when the password is empty")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("incomplete credentials should not create a file")
	}
}

func TestGeneratePasswordIsUnique(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		p, err := GeneratePassword()
		if err != nil {
			t.Fatal(err)
		}
		if seen[p] {
			t.Fatalf("duplicate password generated: %q", p)
		}
		seen[p] = true
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	creds, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if creds.Complete() {
		t.Errorf("expected empty credentials, got %+v", creds)
	}
}
