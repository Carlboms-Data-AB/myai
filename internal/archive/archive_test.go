package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func makeZip(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for name, body := range entries {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0o755)
		e, err := w.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		e.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeTarGz(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "release.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return path
}

func TestUnzipExtractsFiles(t *testing.T) {
	src := makeZip(t, map[string]string{
		"build/bin/llama-server": "binary",
		"build/bin/libggml.so":   "library",
	})
	dest := t.TempDir()

	if err := Extract(src, dest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "build", "bin", "llama-server"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary" {
		t.Errorf("content = %q", got)
	}
}

func TestUnzipPreservesExecutableBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not meaningful on Windows")
	}
	src := makeZip(t, map[string]string{"bin/llama-server": "binary"})
	dest := t.TempDir()
	if err := Extract(src, dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "bin", "llama-server"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("mode = %o, want the executable bit set", info.Mode().Perm())
	}
}

func TestUntarExtractsFiles(t *testing.T) {
	src := makeTarGz(t, map[string]string{"build/bin/llama-server": "elf"})
	dest := t.TempDir()

	if err := Extract(src, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "build", "bin", "llama-server")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractRejectsPathTraversal(t *testing.T) {
	for name, src := range map[string]string{
		"zip": makeZip(t, map[string]string{"../escaped.txt": "bad"}),
		"tar": makeTarGz(t, map[string]string{"../escaped.txt": "bad"}),
	} {
		t.Run(name, func(t *testing.T) {
			dest := t.TempDir()
			if err := Extract(src, dest); err == nil {
				t.Fatal("expected an error for an entry outside the destination")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(dest), "escaped.txt")); err == nil {
				t.Fatal("archive wrote outside the destination directory")
			}
		})
	}
}

func TestExtractRejectsAbsolutePaths(t *testing.T) {
	src := makeTarGz(t, map[string]string{"/etc/passwd": "bad"})
	if err := Extract(src, t.TempDir()); err == nil {
		t.Error("expected an error for an absolute entry")
	}
}

func TestExtractRejectsUnknownType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.rar")
	os.WriteFile(path, []byte("x"), 0o644)
	if err := Extract(path, t.TempDir()); err == nil {
		t.Error("expected an error for an unsupported archive")
	}
}

func TestFindExecutable(t *testing.T) {
	src := makeZip(t, map[string]string{
		"llama-b1-bin/build/bin/llama-server": "x",
		"llama-b1-bin/build/bin/llama-cli":    "x",
	})
	dest := t.TempDir()
	if err := Extract(src, dest); err != nil {
		t.Fatal(err)
	}

	got, err := FindExecutable(dest, "llama-server")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "llama-server" {
		t.Errorf("found %q", got)
	}
	if _, err := FindExecutable(dest, "nonexistent"); err == nil {
		t.Error("expected an error when the executable is absent")
	}
}

func TestFindExecutableMatchesWindowsSuffix(t *testing.T) {
	src := makeZip(t, map[string]string{"bin/llama-server.exe": "x"})
	dest := t.TempDir()
	if err := Extract(src, dest); err != nil {
		t.Fatal(err)
	}
	got, err := FindExecutable(dest, "llama-server")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "llama-server.exe" {
		t.Errorf("found %q", got)
	}
}
