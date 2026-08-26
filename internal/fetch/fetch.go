// Package fetch downloads files over HTTP with progress reporting. It is used
// for runtime dependencies such as llama.cpp and OpenCode release archives.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/carlbomsdata/myai/internal/progress"
)

// Download saves url to dest, reporting progress under the given label. The
// destination file is only created once the transfer starts.
func Download(ctx context.Context, client *http.Client, url, dest, label string, rep progress.Reporter) error {
	if client == nil {
		client = http.DefaultClient
	}
	reporter := progress.Or(rep)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", label, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}

	written, copyErr := copyReporting(f, resp.Body, label, resp.ContentLength, reporter)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(dest)
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	reporter.Download(label, written, resp.ContentLength)
	return nil
}

// copyReporting copies r into w, reporting progress at a rate a user interface
// can keep up with.
func copyReporting(w io.Writer, r io.Reader, label string, total int64, rep progress.Reporter) (int64, error) {
	var written int64
	last := time.Now()
	buf := make([]byte, 256*1024)

	for {
		n, err := r.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
			if time.Since(last) >= 250*time.Millisecond {
				last = time.Now()
				rep.Download(label, written, total)
			}
		}
		if errors.Is(err, io.EOF) {
			return written, nil
		}
		if err != nil {
			return written, err
		}
	}
}
