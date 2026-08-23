package llamacpp

import (
	"strings"
	"testing"

	"github.com/Carlboms-Data-AB/myai/internal/config"
)

// realAssets is the asset list of an actual llama.cpp build release.
var realAssets = []string{
	"cudart-llama-bin-win-cuda-12.4-x64.zip",
	"cudart-llama-bin-win-cuda-13.3-x64.zip",
	"cudart-llama-bin-win-cuda-13.4-arm64.zip",
	"llama-b10589-bin-android-arm64.tar.gz",
	"llama-b10589-bin-macos-arm64.tar.gz",
	"llama-b10589-bin-macos-x64.tar.gz",
	"llama-b10589-bin-ubuntu-arm64.tar.gz",
	"llama-b10589-bin-ubuntu-openvino-2026.3-x64.tar.gz",
	"llama-b10589-bin-ubuntu-rocm-7.14-x64.tar.gz",
	"llama-b10589-bin-ubuntu-s390x.tar.gz",
	"llama-b10589-bin-ubuntu-sycl-fp16-x64.tar.gz",
	"llama-b10589-bin-ubuntu-sycl-fp32-x64.tar.gz",
	"llama-b10589-bin-ubuntu-vulkan-arm64.tar.gz",
	"llama-b10589-bin-ubuntu-vulkan-x64.tar.gz",
	"llama-b10589-bin-ubuntu-x64.tar.gz",
	"llama-b10589-bin-win-cpu-arm64.zip",
	"llama-b10589-bin-win-cpu-x64.zip",
	"llama-b10589-bin-win-cuda-12.4-x64.zip",
	"llama-b10589-bin-win-cuda-13.3-x64.zip",
	"llama-b10589-bin-win-cuda-13.4-arm64.zip",
	"llama-b10589-bin-win-opencl-adreno-arm64.zip",
	"llama-b10589-bin-win-openvino-2026.3-x64.zip",
	"llama-b10589-bin-win-rocm-7.14-x64.zip",
	"llama-b10589-bin-win-sycl-x64.zip",
	"llama-b10589-bin-win-vulkan-x64.zip",
	"llama-b10589-ui.tar.gz",
	"llama-b10589-xcframework.zip",
}

func TestAutoSelectionPerPlatform(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
		accel        string
		fallback     string
	}{
		{"windows", "amd64", "llama-b10589-bin-win-vulkan-x64.zip", config.AccelerationVulkan, config.AccelerationCPU},
		{"windows", "arm64", "llama-b10589-bin-win-cpu-arm64.zip", config.AccelerationCPU, ""},
		{"linux", "amd64", "llama-b10589-bin-ubuntu-vulkan-x64.tar.gz", config.AccelerationVulkan, config.AccelerationCPU},
		{"linux", "arm64", "llama-b10589-bin-ubuntu-arm64.tar.gz", config.AccelerationCPU, ""},
		{"darwin", "arm64", "llama-b10589-bin-macos-arm64.tar.gz", config.AccelerationCPU, ""},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			got, err := SelectAsset(realAssets, tt.goos, tt.goarch, config.AccelerationAuto)
			if err != nil {
				t.Fatalf("SelectAsset: %v", err)
			}
			if got.Primary != tt.want {
				t.Errorf("Primary = %q, want %q", got.Primary, tt.want)
			}
			if got.Acceleration != tt.accel {
				t.Errorf("Acceleration = %q, want %q", got.Acceleration, tt.accel)
			}
			if got.Fallback != tt.fallback {
				t.Errorf("Fallback = %q, want %q", got.Fallback, tt.fallback)
			}
		})
	}
}

func TestNeverSelectsAndroidOrOtherNoise(t *testing.T) {
	for _, p := range [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}} {
		for _, accel := range []string{config.AccelerationAuto, config.AccelerationCPU} {
			got, err := SelectAsset(realAssets, p[0], p[1], accel)
			if err != nil {
				t.Fatalf("%s/%s %s: %v", p[0], p[1], accel, err)
			}
			for _, bad := range []string{"android", "s390x", "xcframework", "-ui.", "sycl", "rocm", "openvino", "adreno"} {
				if strings.Contains(got.Primary, bad) {
					t.Errorf("%s/%s %s selected %q, which contains %q", p[0], p[1], accel, got.Primary, bad)
				}
			}
		}
	}
}

func TestCPUSelection(t *testing.T) {
	tests := map[string][2]string{
		"llama-b10589-bin-win-cpu-x64.zip":     {"windows", "amd64"},
		"llama-b10589-bin-win-cpu-arm64.zip":   {"windows", "arm64"},
		"llama-b10589-bin-ubuntu-x64.tar.gz":   {"linux", "amd64"},
		"llama-b10589-bin-ubuntu-arm64.tar.gz": {"linux", "arm64"},
	}
	for want, p := range tests {
		got, err := SelectAsset(realAssets, p[0], p[1], config.AccelerationCPU)
		if err != nil {
			t.Fatalf("%s/%s: %v", p[0], p[1], err)
		}
		if got.Primary != want {
			t.Errorf("%s/%s = %q, want %q", p[0], p[1], got.Primary, want)
		}
		if got.Fallback != "" {
			t.Errorf("the CPU build has nothing to fall back to, got %q", got.Fallback)
		}
	}
}

func TestCUDASelectionPicksNewestAndItsRuntime(t *testing.T) {
	got, err := SelectAsset(realAssets, "windows", "amd64", config.AccelerationCUDA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Primary != "llama-b10589-bin-win-cuda-13.3-x64.zip" {
		t.Errorf("Primary = %q, want the newest CUDA build", got.Primary)
	}
	if len(got.Extra) != 1 || got.Extra[0] != "cudart-llama-bin-win-cuda-13.3-x64.zip" {
		t.Errorf("Extra = %v, want the matching CUDA runtime", got.Extra)
	}
}

func TestCUDASelectionOnWindowsARM(t *testing.T) {
	got, err := SelectAsset(realAssets, "windows", "arm64", config.AccelerationCUDA)
	if err != nil {
		t.Fatal(err)
	}
	if got.Primary != "llama-b10589-bin-win-cuda-13.4-arm64.zip" {
		t.Errorf("Primary = %q", got.Primary)
	}
	if len(got.Extra) != 1 || got.Extra[0] != "cudart-llama-bin-win-cuda-13.4-arm64.zip" {
		t.Errorf("Extra = %v", got.Extra)
	}
}

func TestLinuxCUDAIsRefusedRatherThanSubstituted(t *testing.T) {
	// The project publishes no Linux CUDA archive. Installing something else
	// would be worse than saying so.
	_, err := SelectAsset(realAssets, "linux", "amd64", config.AccelerationCUDA)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "does not publish a Linux CUDA build") {
		t.Errorf("err = %v", err)
	}
}

func TestWindowsARMVulkanIsRefused(t *testing.T) {
	_, err := SelectAsset(realAssets, "windows", "arm64", config.AccelerationVulkan)
	if err == nil {
		t.Error("there is no Windows ARM64 Vulkan build, so this must fail loudly")
	}
}

func TestUnsupportedArchitecture(t *testing.T) {
	if _, err := SelectAsset(realAssets, "linux", "riscv64", config.AccelerationAuto); err == nil {
		t.Error("expected an error for an architecture with no builds")
	}
}

func TestUnsupportedOS(t *testing.T) {
	if _, err := SelectAsset(realAssets, "freebsd", "amd64", config.AccelerationAuto); err == nil {
		t.Error("expected an error for an unsupported operating system")
	}
}

func TestEmptyReleaseIsReported(t *testing.T) {
	if _, err := SelectAsset(nil, "windows", "amd64", config.AccelerationCPU); err == nil {
		t.Error("expected an error when the release has no assets")
	}
}
