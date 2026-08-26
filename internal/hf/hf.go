// Package hf downloads model artifacts from Hugging Face. Downloads resume
// after an interruption so a failed multi-gigabyte transfer never starts over.
package hf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/carlbomsdata/myai/internal/progress"
)

// Endpoint is the Hugging Face host. It can be overridden by the HF_ENDPOINT
// environment variable, which mirrors the convention of the official tools.
func Endpoint() string {
	if v := strings.TrimSpace(os.Getenv("HF_ENDPOINT")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "https://huggingface.co"
}

// FileURL builds the download URL for a file inside a repository.
func FileURL(repo, file string) string {
	return fmt.Sprintf("%s/%s/resolve/main/%s", Endpoint(), repo, url.PathEscape(file))
}

// Client downloads files from Hugging Face.
type Client struct {
	HTTP *http.Client
}

// New returns a Client with timeouts suited to very large files: no overall
// deadline, but a bounded wait for the response to start.
func New() *Client {
	return &Client{HTTP: &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 60 * time.Second,
			TLSHandshakeTimeout:   30 * time.Second,
			Proxy:                 http.ProxyFromEnvironment,
		},
	}}
}

func (c *Client) client() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// Size reports the size of a remote file in bytes, or zero when the server
// does not disclose it.
func (c *Client) Size(ctx context.Context, repo, file string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, FileURL(repo, file), nil)
	if err != nil {
		return 0, err
	}
	addAuth(req)

	resp, err := c.client().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s: %s", FileURL(repo, file), resp.Status)
	}
	if resp.ContentLength < 0 {
		return 0, nil
	}
	return resp.ContentLength, nil
}

// Download fetches a file into dest, resuming a partial transfer when one is
// present. The file only appears at dest once it has downloaded completely.
func (c *Client) Download(ctx context.Context, repo, file, dest string, r progress.Reporter) error {
	rep := progress.Or(r)

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	part := dest + ".part"
	var resumeFrom int64
	if info, err := os.Stat(part); err == nil {
		resumeFrom = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, FileURL(repo, file), nil)
	if err != nil {
		return err
	}
	addAuth(req)
	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}

	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	total := resp.ContentLength

	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
		if total > 0 {
			total += resumeFrom
		}
	case http.StatusOK:
		// The server ignored the range request, so start again.
		resumeFrom = 0
		flags |= os.O_TRUNC
	case http.StatusRequestedRangeNotSatisfiable:
		// The range was past the end of the file. That happens when the
		// partial download is already complete, but also when it is larger
		// than the file it came from, so the size has to be checked rather
		// than assumed. Promoting a too-large file would install a corrupt
		// model and report success.
		remote, sizeErr := c.Size(ctx, repo, file)
		if sizeErr == nil && remote > 0 && resumeFrom == remote {
			return os.Rename(part, dest)
		}
		if err := os.Remove(part); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return fmt.Errorf("the partial download of %s/%s did not match the file on the server and was discarded; run the install again", repo, file)
	default:
		return fmt.Errorf("download %s/%s: %s", repo, file, resp.Status)
	}

	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}

	name := file
	if name == "" {
		name = repo
	}
	counter := &countingWriter{
		written:  resumeFrom,
		total:    total,
		name:     name,
		reporter: rep,
	}

	_, copyErr := io.Copy(io.MultiWriter(f, counter), resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		// Leave the partial file behind so the next attempt resumes.
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}

	if total > 0 {
		info, err := os.Stat(part)
		if err != nil {
			return err
		}
		if info.Size() != total {
			return fmt.Errorf("download %s/%s: got %d bytes, expected %d", repo, file, info.Size(), total)
		}
	}
	rep.Download(name, counter.written, counter.total)
	return os.Rename(part, dest)
}

// PartialSize reports how many bytes of an interrupted download are on disk.
func PartialSize(dest string) int64 {
	info, err := os.Stat(dest + ".part")
	if err != nil {
		return 0
	}
	return info.Size()
}

func addAuth(req *http.Request) {
	// Public models need no token, but honouring one lets MyAI reach gated or
	// private repositories the user already has access to.
	for _, key := range []string{"HF_TOKEN", "HUGGING_FACE_HUB_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(key)); token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
			return
		}
	}
}

// countingWriter reports download progress without buffering anything.
type countingWriter struct {
	written  int64
	total    int64
	name     string
	reporter progress.Reporter
	lastTick time.Time
}

func (w *countingWriter) Write(p []byte) (int, error) {
	w.written += int64(len(p))
	// Reporting on every chunk would flood any frontend, so throttle.
	if time.Since(w.lastTick) >= 250*time.Millisecond {
		w.lastTick = time.Now()
		w.reporter.Download(w.name, w.written, w.total)
	}
	return len(p), nil
}

// Files lists the file names in a repository.
func (c *Client) Files(ctx context.Context, repo string) ([]string, error) {
	url := fmt.Sprintf("%s/api/models/%s", Endpoint(), repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	addAuth(req)

	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list %s: %s", repo, resp.Status)
	}
	var parsed struct {
		Siblings []struct {
			Name string `json:"rfilename"`
		} `json:"siblings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(parsed.Siblings))
	for _, s := range parsed.Siblings {
		out = append(out, s.Name)
	}
	return out, nil
}

// MatchGGUF picks the GGUF file in a repository matching a quantization
// label such as "Q6_K". Projection files, which hold a vision encoder rather
// than the model, are never a match.
func MatchGGUF(files []string, quant string) (string, error) {
	quant = strings.ToLower(strings.TrimSpace(quant))
	if quant == "" {
		return "", fmt.Errorf("no quantization given")
	}

	var matches []string
	for _, f := range files {
		lower := strings.ToLower(f)
		if !strings.HasSuffix(lower, ".gguf") || strings.Contains(lower, "mmproj") {
			continue
		}
		// Bound the label so "Q4_K" does not match "Q4_K_M".
		if strings.Contains(lower, "-"+quant+".gguf") || strings.Contains(lower, "."+quant+".gguf") {
			matches = append(matches, f)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no %s file found; available: %s", strings.ToUpper(quant), strings.Join(ggufNames(files), ", "))
	case 1:
		return matches[0], nil
	default:
		sort.Strings(matches)
		return matches[0], nil
	}
}

// ggufNames lists the quantizations a repository offers, for error messages.
func ggufNames(files []string) []string {
	var out []string
	for _, f := range files {
		lower := strings.ToLower(f)
		if strings.HasSuffix(lower, ".gguf") && !strings.Contains(lower, "mmproj") {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	if len(out) > 12 {
		out = append(out[:12], "...")
	}
	return out
}
