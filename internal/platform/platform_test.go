package platform

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestFreeSpaceOnExistingDirectory(t *testing.T) {
	got, err := FreeSpace(t.TempDir())
	if err != nil {
		t.Fatalf("FreeSpace: %v", err)
	}
	if got <= 0 {
		t.Errorf("FreeSpace = %d, want a positive figure", got)
	}
}

func TestFreeSpaceWalksUpToExistingParent(t *testing.T) {
	// Model directories are measured before they are created.
	missing := filepath.Join(t.TempDir(), "not", "yet", "created")
	got, err := FreeSpace(missing)
	if err != nil {
		t.Fatalf("FreeSpace: %v", err)
	}
	if got <= 0 {
		t.Errorf("FreeSpace = %d, want a positive figure", got)
	}
}

func TestIsCarrierGradeNAT(t *testing.T) {
	tests := map[string]bool{
		"100.64.0.1":    true,
		"100.100.5.9":   true,
		"100.127.255.1": true,
		"100.128.0.1":   false,
		"100.63.0.1":    false,
		"192.168.1.5":   false,
		"10.0.0.1":      false,
	}
	for addr, want := range tests {
		if got := isCarrierGradeNAT(net.ParseIP(addr)); got != want {
			t.Errorf("%s: got %v, want %v", addr, got, want)
		}
	}
}

func TestReachableAddressAlwaysReturnsSomething(t *testing.T) {
	if got := ReachableAddress(); got == "" {
		t.Error("ReachableAddress returned an empty string")
	}
}

func TestSupportedPlatforms(t *testing.T) {
	tests := []struct {
		os, arch string
		want     bool
	}{
		{"darwin", "arm64", true},
		{"darwin", "amd64", false},
		{"windows", "amd64", true},
		{"windows", "arm64", true},
		{"linux", "amd64", true},
		{"linux", "arm64", true},
		{"freebsd", "amd64", false},
	}
	for _, tt := range tests {
		h := Host{OS: tt.os, Arch: tt.arch}
		if got := h.Supported(); got != tt.want {
			t.Errorf("%s: Supported = %v, want %v", h.Label(), got, tt.want)
		}
	}
}

func TestSupportsMLX(t *testing.T) {
	if !(Host{OS: "darwin", Arch: "arm64"}).SupportsMLX() {
		t.Error("Apple Silicon should support MLX")
	}
	for _, h := range []Host{{OS: "darwin", Arch: "amd64"}, {OS: "linux", Arch: "arm64"}, {OS: "windows", Arch: "arm64"}} {
		if h.SupportsMLX() {
			t.Errorf("%s should not report MLX support", h.Label())
		}
	}
}

func TestHumanBytes(t *testing.T) {
	tests := map[int64]string{
		0:          "0 B",
		512:        "512 B",
		1024:       "1.0 KiB",
		8215258652: "7.7 GiB",
		7458301152: "6.9 GiB",
	}
	for n, want := range tests {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestExistingAncestorStopsAtRoot(t *testing.T) {
	if got := existingAncestor(os.TempDir()); got == "" {
		t.Error("temp dir should be found")
	}
}
