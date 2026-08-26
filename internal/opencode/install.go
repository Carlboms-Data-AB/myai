package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/carlbomsdata/myai/internal/archive"
	"github.com/carlbomsdata/myai/internal/fetch"
	"github.com/carlbomsdata/myai/internal/progress"
)

// releaseURL is the OpenCode release feed.
const releaseURL = "https://api.github.com/repos/anomalyco/opencode/releases/latest"

// AssetName returns the OpenCode release archive for a platform. The project
// publishes native builds for every platform MyAI supports, Windows included,
// so nothing here needs WSL.
func AssetName(goos, goarch string) (string, error) {
	arch := ""
	switch goarch {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	default:
		return "", fmt.Errorf("OpenCode does not publish builds for %s", goarch)
	}

	switch goos {
	case "darwin":
		return "opencode-darwin-" + arch + ".zip", nil
	case "windows":
		return "opencode-windows-" + arch + ".zip", nil
	case "linux":
		return "opencode-linux-" + arch + ".tar.gz", nil
	default:
		return "", fmt.Errorf("OpenCode is not supported on %s", goos)
	}
}

// Install downloads the official OpenCode build unless one is already present.
func (o *OpenCode) Install(ctx context.Context, rep progress.Reporter) error {
	reporter := progress.Or(rep)

	if info := o.Detect(ctx); info.Installed {
		reporter.Info("OpenCode is already installed: " + info.Summary())
		return nil
	}

	want, err := AssetName(o.GOOS, o.GOARCH)
	if err != nil {
		return err
	}

	reporter.Step("Installing OpenCode")
	url, tag, err := o.assetURL(ctx, want)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(o.ToolDir, 0o755); err != nil {
		return err
	}
	archivePath := filepath.Join(o.ToolDir, want)
	if err := fetch.Download(ctx, http.DefaultClient, url, archivePath, want, reporter); err != nil {
		return err
	}
	if err := archive.Extract(archivePath, o.ToolDir); err != nil {
		return fmt.Errorf("unpack OpenCode: %w", err)
	}
	os.Remove(archivePath)

	// Release archives may nest the binary in a directory, so find it and put
	// it where Detect looks.
	found, err := archive.FindExecutable(o.ToolDir, Executable)
	if err != nil {
		return fmt.Errorf("OpenCode %s did not contain %s", tag, Executable)
	}
	target := filepath.Join(o.ToolDir, filepath.Base(found))
	if found != target {
		if err := os.Rename(found, target); err != nil {
			return err
		}
	}
	if o.GOOS != "windows" {
		if err := os.Chmod(target, 0o755); err != nil {
			return err
		}
	}

	if !o.Detect(ctx).Installed {
		return fmt.Errorf("installed OpenCode %s but it does not run", tag)
	}
	return nil
}

func (o *OpenCode) assetURL(ctx context.Context, want string) (url, tag string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("look up the OpenCode release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("look up the OpenCode release: %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", err
	}
	for _, a := range release.Assets {
		if a.Name == want {
			return a.URL, release.TagName, nil
		}
	}
	return "", "", fmt.Errorf("OpenCode release %s has no asset named %s", release.TagName, want)
}
