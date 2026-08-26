package llamacpp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/carlbomsdata/myai/internal/config"
)

// Selection is the set of release assets to download for one platform and
// acceleration choice.
type Selection struct {
	// Primary is the archive holding llama-server.
	Primary string
	// Extra holds companion archives, such as the CUDA runtime.
	Extra []string
	// Acceleration is the choice this selection represents.
	Acceleration string
	// Fallback is the acceleration to try if this build will not run, or
	// empty when there is nothing further to fall back to.
	Fallback string
}

// archSuffix maps a Go architecture onto the name llama.cpp releases use.
func archSuffix(goarch string) (string, error) {
	switch goarch {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("llama.cpp does not publish builds for %s", goarch)
	}
}

// SelectAsset picks the release assets for a platform. Names are the asset
// file names of one release, which is all that distinguishes the variants.
func SelectAsset(names []string, goos, goarch, acceleration string) (Selection, error) {
	arch, err := archSuffix(goarch)
	if err != nil {
		return Selection{}, err
	}
	if acceleration == "" || acceleration == config.AccelerationAuto {
		acceleration = autoAcceleration(goos, goarch)
	}

	sel := Selection{Acceleration: acceleration}
	if acceleration != config.AccelerationCPU {
		// Anything GPU-specific can fail to start on a machine without the
		// matching runtime, so CPU is always the safety net.
		sel.Fallback = config.AccelerationCPU
	}

	switch goos {
	case "windows":
		return selectWindows(names, arch, sel)
	case "linux":
		return selectLinux(names, arch, sel)
	case "darwin":
		// Metal is built into the macOS release.
		name, ok := findAsset(names, "-bin-macos-"+arch+".tar.gz")
		if !ok {
			return Selection{}, fmt.Errorf("no macOS %s build in this release", arch)
		}
		sel.Primary = name
		sel.Acceleration = config.AccelerationCPU
		sel.Fallback = ""
		return sel, nil
	default:
		return Selection{}, fmt.Errorf("llama.cpp is not supported on %s", goos)
	}
}

func autoAcceleration(goos, goarch string) string {
	if goarch == "amd64" && (goos == "windows" || goos == "linux") {
		// Vulkan covers NVIDIA, AMD and Intel on a normal desktop driver
		// install, so it is the best default that is not vendor-specific.
		return config.AccelerationVulkan
	}
	return config.AccelerationCPU
}

func selectWindows(names []string, arch string, sel Selection) (Selection, error) {
	switch sel.Acceleration {
	case config.AccelerationCPU:
		name, ok := findAsset(names, "-bin-win-cpu-"+arch+".zip")
		if !ok {
			return Selection{}, fmt.Errorf("no Windows %s CPU build in this release", arch)
		}
		sel.Primary = name
		sel.Fallback = ""
		return sel, nil

	case config.AccelerationVulkan:
		name, ok := findAsset(names, "-bin-win-vulkan-"+arch+".zip")
		if !ok {
			return Selection{}, fmt.Errorf("no Windows %s Vulkan build in this release", arch)
		}
		sel.Primary = name
		return sel, nil

	case config.AccelerationCUDA:
		// CUDA archives carry the toolkit version in the name, and the
		// matching runtime ships separately.
		name, version, ok := findCUDA(names, "llama-", "-bin-win-cuda-", "-"+arch+".zip")
		if !ok {
			return Selection{}, fmt.Errorf("no Windows %s CUDA build in this release", arch)
		}
		sel.Primary = name
		if runtimeName, ok := findAsset(names, "cudart-llama-bin-win-cuda-"+version+"-"+arch+".zip"); ok {
			sel.Extra = []string{runtimeName}
		}
		return sel, nil
	}
	return Selection{}, fmt.Errorf("unknown acceleration %q", sel.Acceleration)
}

func selectLinux(names []string, arch string, sel Selection) (Selection, error) {
	switch sel.Acceleration {
	case config.AccelerationCPU:
		name, ok := findAsset(names, "-bin-ubuntu-"+arch+".tar.gz")
		if !ok {
			return Selection{}, fmt.Errorf("no Linux %s build in this release", arch)
		}
		sel.Primary = name
		sel.Fallback = ""
		return sel, nil

	case config.AccelerationVulkan:
		name, ok := findAsset(names, "-bin-ubuntu-vulkan-"+arch+".tar.gz")
		if !ok {
			return Selection{}, fmt.Errorf("no Linux %s Vulkan build in this release", arch)
		}
		sel.Primary = name
		return sel, nil

	case config.AccelerationCUDA:
		// The project does not publish a Linux CUDA archive, so saying so is
		// better than silently installing something else.
		return Selection{}, fmt.Errorf("llama.cpp does not publish a Linux CUDA build; use vulkan or cpu")
	}
	return Selection{}, fmt.Errorf("unknown acceleration %q", sel.Acceleration)
}

func findAsset(names []string, suffix string) (string, bool) {
	matches := make([]string, 0, 2)
	for _, n := range names {
		if strings.HasSuffix(n, suffix) && !strings.HasPrefix(n, "cudart-") {
			matches = append(matches, n)
		}
		if suffix == n {
			return n, true
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	sort.Strings(matches)
	return matches[0], true
}

// findCUDA locates a CUDA asset and reports the toolkit version embedded in
// its name, so the matching runtime archive can be fetched too.
func findCUDA(names []string, prefix, middle, suffix string) (name, version string, ok bool) {
	var best string
	var bestVersion string
	for _, n := range names {
		if !strings.HasPrefix(n, prefix) || !strings.HasSuffix(n, suffix) {
			continue
		}
		idx := strings.Index(n, middle)
		if idx < 0 {
			continue
		}
		v := n[idx+len(middle) : len(n)-len(suffix)]
		if v == "" || strings.Contains(v, "-") {
			continue
		}
		if v > bestVersion {
			best, bestVersion = n, v
		}
	}
	if best == "" {
		return "", "", false
	}
	return best, bestVersion, true
}
