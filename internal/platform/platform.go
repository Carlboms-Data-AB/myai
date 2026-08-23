// Package platform exposes the host facts MyAI needs: free disk space, the
// address other machines can reach this one on, and the current user. Each
// function has one behaviour per operating system and no user interface.
package platform

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"runtime"
	"strings"
)

// Host describes the machine MyAI is running on.
type Host struct {
	OS       string
	Arch     string
	Hostname string
	User     string
}

// Current returns facts about this machine.
func Current() Host {
	h := Host{OS: runtime.GOOS, Arch: runtime.GOARCH}
	h.Hostname, _ = os.Hostname()
	if u, err := user.Current(); err == nil {
		h.User = u.Username
	}
	return h
}

// Label renders the platform as "darwin/arm64".
func (h Host) Label() string { return h.OS + "/" + h.Arch }

// SupportsMLX reports whether this machine can run the MLX backend.
func (h Host) SupportsMLX() bool { return h.OS == "darwin" && h.Arch == "arm64" }

// Supported reports whether MyAI runs natively on this platform.
func (h Host) Supported() bool {
	switch h.OS {
	case "darwin":
		return h.Arch == "arm64"
	case "windows", "linux":
		return h.Arch == "amd64" || h.Arch == "arm64"
	}
	return false
}

// FreeSpace reports the bytes available on the filesystem holding path. If the
// path does not exist yet, the nearest existing parent is measured instead.
func FreeSpace(path string) (int64, error) {
	probe := existingAncestor(path)
	if probe == "" {
		return 0, fmt.Errorf("no existing directory found for %s", path)
	}
	return freeSpace(probe)
}

func existingAncestor(path string) string {
	for p := path; ; {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := parentDir(p)
		if parent == p {
			return ""
		}
		p = parent
	}
}

// ReachableAddress returns the address another machine should use to reach
// this one. An overlay network address in 100.64.0.0/10, as used by NetBird
// and similar tools, is preferred because it works from anywhere the overlay
// reaches; otherwise the primary LAN address is used.
func ReachableAddress() string {
	addrs := localIPv4()
	for _, ip := range addrs {
		if isCarrierGradeNAT(ip) {
			return ip.String()
		}
	}
	for _, ip := range addrs {
		if ip.IsPrivate() {
			return ip.String()
		}
	}
	if len(addrs) > 0 {
		return addrs[0].String()
	}
	return "localhost"
}

func localIPv4() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipNet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ip)
		}
	}
	return out
}

// isCarrierGradeNAT reports whether ip falls in 100.64.0.0/10.
func isCarrierGradeNAT(ip net.IP) bool {
	ip = ip.To4()
	if ip == nil {
		return false
	}
	return ip[0] == 100 && ip[1] >= 64 && ip[1] <= 127
}

// QualifiedUser returns the current user in the form Windows service
// configuration expects, such as `DOMAIN\user` or `.\user`.
func QualifiedUser() (string, error) {
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	name := u.Username
	if runtime.GOOS != "windows" {
		return name, nil
	}
	if strings.Contains(name, `\`) {
		return name, nil
	}
	if domain := strings.TrimSpace(os.Getenv("USERDOMAIN")); domain != "" {
		return domain + `\` + name, nil
	}
	return `.\` + name, nil
}

// HumanBytes formats a byte count for display.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
