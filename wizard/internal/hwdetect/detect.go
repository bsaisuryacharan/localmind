// Package hwdetect classifies the host into a model profile.
//
// The classification is intentionally coarse: we only need enough info to
// pick a default chat + embedding model from models.yml. The wizard always
// shows the choice to the user and lets them override.
package hwdetect

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// Profile is a key into models.yml `profiles`.
type Profile string

const (
	ProfileCPULow       Profile = "cpu_low"
	ProfileCPUMid       Profile = "cpu_mid"
	ProfileNvidia12     Profile = "nvidia_12gb"
	ProfileNvidia24     Profile = "nvidia_24gb"
	ProfileApple16      Profile = "apple_16gb"
	ProfileApple32Plus  Profile = "apple_32gb_plus"
)

// Host describes what we detected.
type Host struct {
	OS         string  // linux, darwin, windows
	Arch       string  // amd64, arm64
	RAMGB      int     // total system RAM
	GPUVendor  string  // "", "nvidia", "apple", "amd"
	GPUVRAMGB  int     // 0 if unknown
}

// Detect inspects the running host. It never errors; unknown fields are
// left at zero so the caller can fall back to a conservative profile.
func Detect() Host {
	h := Host{OS: runtime.GOOS, Arch: runtime.GOARCH}
	h.RAMGB = ramGB()
	h.GPUVendor, h.GPUVRAMGB = gpu()
	return h
}

// Pick selects a profile from a Host. The returned profile is guaranteed
// to exist in the default models.yml.
func Pick(h Host) Profile {
	switch h.GPUVendor {
	case "nvidia":
		if h.GPUVRAMGB >= 22 {
			return ProfileNvidia24
		}
		return ProfileNvidia12
	case "apple":
		if h.RAMGB >= 32 {
			return ProfileApple32Plus
		}
		return ProfileApple16
	}
	if h.RAMGB >= 32 {
		return ProfileCPUMid
	}
	return ProfileCPULow
}

// ramGB returns total physical RAM in GB; 0 on failure.
func ramGB() int {
	switch runtime.GOOS {
	case "linux":
		return linuxRAMGB()
	case "darwin":
		return darwinRAMGB()
	case "windows":
		return windowsRAMGB()
	}
	return 0
}

func linuxRAMGB() int {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.Atoi(fields[1])
		if err != nil {
			return 0
		}
		return kb / 1024 / 1024
	}
	return 0
}

func darwinRAMGB() int {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	bytes, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0
	}
	return int(bytes / (1 << 30))
}

func windowsRAMGB() int {
	// `wmic` is deprecated but ubiquitous; CIM is preferred long-term.
	out, err := exec.Command("wmic", "ComputerSystem", "get", "TotalPhysicalMemory").Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "TotalPhysicalMemory") {
			continue
		}
		bytes, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			continue
		}
		return int(bytes / (1 << 30))
	}
	return 0
}

// gpu returns (vendor, vram_gb). vendor is "" if no discrete GPU.
func gpu() (string, int) {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return "apple", 0 // unified memory; size handled via RAMGB
	}
	if v, ok := nvidiaSMI(); ok {
		return "nvidia", v
	}
	return "", 0
}

func nvidiaSMI() (int, bool) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, false
	}
	first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	mb, err := strconv.Atoi(first)
	if err != nil {
		return 0, false
	}
	return mb / 1024, true
}

// ErrNoCompose is returned by callers when docker compose is unavailable.
var ErrNoCompose = errors.New("docker compose not found")
