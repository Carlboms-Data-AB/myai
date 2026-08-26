package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/carlbomsdata/myai/internal/fetch"
)

// releaseAPI is where MyAI looks for its own releases.
const releaseAPI = "https://api.github.com/repos/carlbomsdata/myai/releases/latest"

// UpdateResult describes what a self-update did.
type UpdateResult struct {
	// Checked is the version that was available.
	Available string
	// Installed is the version now on disk.
	Installed string
	// Updated reports whether anything changed.
	Updated bool
	// Path is where the binary was written.
	Path string
}

// IsNewer reports whether the released version is newer than the one running.
// A version that is not a plain release tag, such as a build from a working
// tree, is left alone: replacing it with a release would be a downgrade.
func IsNewer(release, running string) bool {
	if release == "" || release == running {
		return false
	}
	if !releaseTag.MatchString(running) {
		// A development build is ahead of any release by definition.
		return false
	}
	if !releaseTag.MatchString(release) {
		return false
	}
	return compareVersions(release, running) > 0
}

// releaseTag matches a plain version tag such as v1.2.3.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)

// compareVersions compares two vX.Y.Z strings.
func compareVersions(a, b string) int {
	left := versionParts(a)
	right := versionParts(b)
	for i := 0; i < 3; i++ {
		if left[i] != right[i] {
			if left[i] > right[i] {
				return 1
			}
			return -1
		}
	}
	return 0
}

func versionParts(v string) [3]int {
	var out [3]int
	for i, part := range strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3) {
		if i > 2 {
			break
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return out
		}
		out[i] = n
	}
	return out
}

// AssetName returns the release asset for a platform, matching the names the
// build produces.
func AssetName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux":
		if goarch != "amd64" && goarch != "arm64" {
			return "", fmt.Errorf("no MyAI build for %s/%s", goos, goarch)
		}
		return fmt.Sprintf("myai-%s-%s", goos, goarch), nil
	case "windows":
		if goarch != "amd64" && goarch != "arm64" {
			return "", fmt.Errorf("no MyAI build for %s/%s", goos, goarch)
		}
		return fmt.Sprintf("myai-windows-%s.exe", goarch), nil
	default:
		return "", fmt.Errorf("no MyAI build for %s", goos)
	}
}

// SelfUpdate replaces the installed myai binary with the newest release, if
// there is a newer one. The download is checked against the release
// checksums before anything is replaced.
func (a *App) SelfUpdate(ctx context.Context) (UpdateResult, error) {
	result := UpdateResult{Installed: Version}

	tag, assets, err := latestRelease(ctx)
	if err != nil {
		return result, err
	}
	result.Available = tag

	if !IsNewer(tag, Version) {
		a.reporter.Info(fmt.Sprintf("myai %s is current; the latest release is %s", Version, tag))
		return result, nil
	}

	want, err := AssetName(a.host.OS, a.host.Arch)
	if err != nil {
		return result, err
	}
	url, ok := assets[want]
	if !ok {
		return result, fmt.Errorf("release %s has no asset named %s", tag, want)
	}

	a.reporter.Step(fmt.Sprintf("Updating myai from %s to %s", Version, tag))

	dir, err := os.MkdirTemp("", "myai-update-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(dir)

	downloaded := filepath.Join(dir, want)
	if err := fetch.Download(ctx, nil, url, downloaded, want, a.reporter); err != nil {
		return result, err
	}

	// Verify before replacing anything. A truncated or tampered download must
	// not become the command the operator runs.
	if sums, ok := assets["SHA256SUMS"]; ok {
		if err := verifyChecksum(ctx, sums, want, downloaded); err != nil {
			return result, err
		}
		a.reporter.Info("checksum verified")
	} else {
		a.reporter.Warn("release " + tag + " publishes no checksums, so the download could not be verified")
	}

	target := a.env.Executable()
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return result, err
	}
	if err := replaceExecutable(downloaded, target); err != nil {
		return result, err
	}

	result.Updated = true
	result.Installed = tag
	result.Path = target
	a.reporter.Info("myai " + tag + " installed at " + target)
	a.reporter.Info("the new version takes effect the next time you run myai")
	return result, nil
}

// latestRelease returns the newest release tag and its assets by name.
func latestRelease(ctx context.Context) (string, map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseAPI, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("look up the latest MyAI release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("look up the latest MyAI release: %s", resp.Status)
	}

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", nil, err
	}
	assets := make(map[string]string, len(release.Assets))
	for _, a := range release.Assets {
		assets[a.Name] = a.URL
	}
	return release.TagName, assets, nil
}

// verifyChecksum checks a downloaded file against the release's SHA256SUMS.
func verifyChecksum(ctx context.Context, sumsURL, name, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sumsURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch checksums: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}

	want, err := ExpectedSum(string(body), name)
	if err != nil {
		return err
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, expected %s", name, got, want)
	}
	return nil
}

// ExpectedSum finds a file's checksum in the contents of a SHA256SUMS file.
func ExpectedSum(sums, name string) (string, error) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum published for %s", name)
}

// replaceExecutable puts a downloaded binary in place of the installed one.
func replaceExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	staged := target + ".new"
	out, err := os.OpenFile(staged, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(staged)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(staged)
		return err
	}

	// Moving the old one aside first lets this work even on Windows, where a
	// running executable cannot simply be overwritten.
	previous := target + ".old"
	os.Remove(previous)
	if _, err := os.Stat(target); err == nil {
		if err := os.Rename(target, previous); err != nil {
			os.Remove(staged)
			return err
		}
	}
	if err := os.Rename(staged, target); err != nil {
		os.Rename(previous, target)
		return err
	}
	os.Remove(previous)
	return nil
}
