package app

import (
	"context"
	"io"
	"net/http"
	"time"
)

// probeWeb reports whether the OpenCode Web UI answers an authenticated
// request on the given address.
func probeWeb(ctx context.Context, url, username, password string) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/", nil)
	if err != nil {
		return false
	}
	if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 400
}
