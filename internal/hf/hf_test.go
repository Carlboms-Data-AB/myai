package hf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func serveBytes(t *testing.T, body []byte, allowRange bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			return
		}
		rangeHeader := r.Header.Get("Range")
		if allowRange && strings.HasPrefix(rangeHeader, "bytes=") {
			startStr := strings.TrimSuffix(strings.TrimPrefix(rangeHeader, "bytes="), "-")
			start, err := strconv.Atoi(startStr)
			if err != nil || start > len(body) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)-start))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(body[start:])
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("HF_ENDPOINT", srv.URL)
	return srv
}

func TestDownloadWritesCompleteFile(t *testing.T) {
	body := []byte(strings.Repeat("model-weights", 1000))
	serveBytes(t, body, true)

	dest := filepath.Join(t.TempDir(), "model.gguf")
	if err := New().Download(context.Background(), "org/repo", "model.gguf", dest, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("downloaded %d bytes, want %d", len(got), len(body))
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error("partial file should be gone after a successful download")
	}
}

func TestDownloadResumesFromPartial(t *testing.T) {
	body := []byte(strings.Repeat("abcdefgh", 500))
	serveBytes(t, body, true)

	dir := t.TempDir()
	dest := filepath.Join(dir, "model.gguf")
	// Simulate an interrupted transfer.
	if err := os.WriteFile(dest+".part", body[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	if got := PartialSize(dest); got != 1000 {
		t.Fatalf("PartialSize = %d, want 1000", got)
	}

	if err := New().Download(context.Background(), "org/repo", "model.gguf", dest, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Errorf("resumed file does not match: got %d bytes, want %d", len(got), len(body))
	}
}

func TestDownloadRestartsWhenServerIgnoresRange(t *testing.T) {
	body := []byte(strings.Repeat("xyz", 700))
	serveBytes(t, body, false)

	dir := t.TempDir()
	dest := filepath.Join(dir, "model.gguf")
	if err := os.WriteFile(dest+".part", []byte("stale partial data"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := New().Download(context.Background(), "org/repo", "model.gguf", dest, nil); err != nil {
		t.Fatalf("Download: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Error("file should have been re-downloaded from the start")
	}
}

func TestDownloadKeepsPartialOnFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("HF_ENDPOINT", srv.URL)

	dest := filepath.Join(t.TempDir(), "model.gguf")
	if err := New().Download(context.Background(), "org/repo", "model.gguf", dest, nil); err == nil {
		t.Fatal("expected an error on HTTP 500")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("a failed download must not create the destination file")
	}
}

func TestSizeReportsContentLength(t *testing.T) {
	body := []byte(strings.Repeat("q", 4096))
	serveBytes(t, body, true)

	got, err := New().Size(context.Background(), "org/repo", "model.gguf")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if got != int64(len(body)) {
		t.Errorf("Size = %d, want %d", got, len(body))
	}
}

func TestFileURLEscapesNames(t *testing.T) {
	t.Setenv("HF_ENDPOINT", "https://example.test")
	got := FileURL("org/repo", "a b.gguf")
	if got != "https://example.test/org/repo/resolve/main/a%20b.gguf" {
		t.Errorf("FileURL = %q", got)
	}
}

func TestMatchGGUFPicksTheNamedQuantization(t *testing.T) {
	files := []string{
		"README.md",
		"Qwen3.5-9B-Q4_K_M.gguf",
		"Qwen3.5-9B-Q4_K_S.gguf",
		"Qwen3.5-9B-Q6_K.gguf",
		"Qwen3.5-9B-Q8_0.gguf",
		"mmproj-F16.gguf",
	}
	tests := map[string]string{
		"Q6_K":   "Qwen3.5-9B-Q6_K.gguf",
		"q6_k":   "Qwen3.5-9B-Q6_K.gguf",
		"Q4_K_M": "Qwen3.5-9B-Q4_K_M.gguf",
		"Q8_0":   "Qwen3.5-9B-Q8_0.gguf",
	}
	for quant, want := range tests {
		got, err := MatchGGUF(files, quant)
		if err != nil {
			t.Fatalf("%s: %v", quant, err)
		}
		if got != want {
			t.Errorf("MatchGGUF(%q) = %q, want %q", quant, got, want)
		}
	}
}

func TestMatchGGUFDoesNotConfuseNestedLabels(t *testing.T) {
	// "Q4_K" must not silently pick up "Q4_K_M".
	files := []string{"m-Q4_K_M.gguf", "m-Q4_K_S.gguf"}
	if got, err := MatchGGUF(files, "Q4_K"); err == nil {
		t.Errorf("MatchGGUF returned %q; Q4_K is not one of these files", got)
	}
}

func TestMatchGGUFIgnoresProjectionFiles(t *testing.T) {
	// mmproj files hold a vision encoder, not the model.
	files := []string{"mmproj-F16.gguf", "model-F16.gguf"}
	got, err := MatchGGUF(files, "F16")
	if err != nil {
		t.Fatal(err)
	}
	if got != "model-F16.gguf" {
		t.Errorf("MatchGGUF = %q", got)
	}
}

func TestMatchGGUFReportsWhatIsAvailable(t *testing.T) {
	files := []string{"m-Q6_K.gguf", "m-Q8_0.gguf"}
	_, err := MatchGGUF(files, "Q3_K_L")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "m-Q6_K.gguf") {
		t.Errorf("err = %v, want it to list what is available", err)
	}
}

func TestMatchGGUFRejectsEmptyQuant(t *testing.T) {
	if _, err := MatchGGUF([]string{"m-Q6_K.gguf"}, ""); err == nil {
		t.Error("expected an error")
	}
}

func TestFilesListsRepositoryContents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/models/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"siblings":[{"rfilename":"a-Q6_K.gguf"},{"rfilename":"README.md"}]}`))
	}))
	defer srv.Close()
	t.Setenv("HF_ENDPOINT", srv.URL)

	got, err := New().Files(context.Background(), "org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "a-Q6_K.gguf" {
		t.Errorf("Files = %v", got)
	}
}
