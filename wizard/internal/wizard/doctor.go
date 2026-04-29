// Doctor diagnostics. The orchestrator runs each check, prints a
// colored verdict, and aggregates a final exit status.
//
// Stdlib only — the wizard module's go.mod has zero require directives
// and we want to keep it that way.
package wizard

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/localmind/localmind/wizard/internal/hwdetect"
)

// status is the outcome of a single check.
type status int

const (
	statusOK status = iota
	statusWarn
	statusFail
	statusSkip
)

func (s status) label() string {
	switch s {
	case statusOK:
		return "OK   "
	case statusWarn:
		return "WARN "
	case statusFail:
		return "FAIL "
	case statusSkip:
		return "SKIP "
	}
	return "???? "
}

// checkResult is what every check returns. Detail is appended after
// the human name when printed.
type checkResult struct {
	name   string
	status status
	detail string
}

func (r checkResult) print() {
	if r.detail == "" {
		fmt.Printf("%s %s\n", r.status.label(), r.name)
		return
	}
	fmt.Printf("%s %s: %s\n", r.status.label(), r.name, r.detail)
}

// runDoctor is the orchestrator for the new doctor pipeline. Returns
// a non-nil error iff any check returned statusFail.
func runDoctor(ctx context.Context) error {
	root, rootErr := repoRoot()

	envValues := map[string]string{}
	if rootErr == nil {
		envValues = readEnvFile(filepath.Join(root, ".env"))
	}

	results := []checkResult{}

	// 1. Environment
	results = append(results,
		checkDockerOnPath(),
		checkDockerComposePlugin(),
		checkDockerDaemon(ctx),
		checkRepoRoot(rootErr),
	)

	// 2. Configuration
	results = append(results,
		checkEnvFile(root, rootErr),
		checkModelsYML(root, rootErr),
		checkProfileVar(envValues, rootErr),
	)

	// 3. Ports
	results = append(results, checkPorts(ctx, envValues)...)

	// 4. Disk space
	results = append(results, checkDiskSpace(ctx, root, rootErr)...)

	// 5. Ollama reachability
	ollamaURL := ollamaBaseURL(envValues)
	ollamaResult, ollamaUp, modelCount, models := checkOllamaReachable(ctx, ollamaURL)
	results = append(results, ollamaResult)
	_ = modelCount

	// 6. MCP gateway reachability
	results = append(results, checkMCPReachable(ctx, envValues))

	// 7. Active model availability
	results = append(results, checkActiveModel(root, rootErr, ollamaUp, models))

	// 8. Apple GPU note
	if r, ok := checkAppleGPU(envValues); ok {
		results = append(results, r)
	}

	// 9. NVIDIA GPU note
	if r, ok := checkNvidiaGPU(); ok {
		results = append(results, r)
	}

	// Print and aggregate.
	anyFail := false
	for _, r := range results {
		r.print()
		if r.status == statusFail {
			anyFail = true
		}
	}
	if anyFail {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

// ----- environment checks -----

func checkDockerOnPath() checkResult {
	if _, err := exec.LookPath("docker"); err != nil {
		return checkResult{"docker on PATH", statusFail, err.Error()}
	}
	return checkResult{"docker on PATH", statusOK, ""}
}

func checkDockerComposePlugin() checkResult {
	out, err := exec.Command("docker", "compose", "version").CombinedOutput()
	if err != nil {
		return checkResult{"docker compose plugin", statusFail,
			fmt.Sprintf("%v: %s", err, strings.TrimSpace(string(out)))}
	}
	return checkResult{"docker compose plugin", statusOK, ""}
}

func checkDockerDaemon(ctx context.Context) checkResult {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "docker", "info").CombinedOutput()
	if err != nil {
		// Trim noisy multi-line `docker info` output to a single line.
		msg := strings.TrimSpace(string(out))
		if i := strings.Index(msg, "\n"); i > 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return checkResult{"docker daemon reachable", statusFail, msg}
	}
	return checkResult{"docker daemon reachable", statusOK, ""}
}

func checkRepoRoot(rootErr error) checkResult {
	if rootErr != nil {
		return checkResult{"repo root resolvable", statusFail, rootErr.Error()}
	}
	return checkResult{"repo root resolvable", statusOK, ""}
}

// ----- configuration checks -----

func checkEnvFile(root string, rootErr error) checkResult {
	if rootErr != nil {
		return checkResult{".env present", statusSkip, "repo root not found"}
	}
	p := filepath.Join(root, ".env")
	if _, err := os.Stat(p); err != nil {
		return checkResult{".env present", statusWarn, "missing; run `localmind init`"}
	}
	return checkResult{".env present", statusOK, ""}
}

func checkModelsYML(root string, rootErr error) checkResult {
	if rootErr != nil {
		return checkResult{"models.yml present", statusSkip, "repo root not found"}
	}
	p := filepath.Join(root, "models.yml")
	if _, err := os.Stat(p); err != nil {
		return checkResult{"models.yml present", statusFail, "missing at " + p}
	}
	return checkResult{"models.yml present", statusOK, ""}
}

func checkProfileVar(env map[string]string, rootErr error) checkResult {
	if rootErr != nil {
		return checkResult{"LOCALMIND_PROFILE set", statusSkip, "repo root not found"}
	}
	if v := env["LOCALMIND_PROFILE"]; v != "" {
		return checkResult{"LOCALMIND_PROFILE set", statusOK, v}
	}
	return checkResult{"LOCALMIND_PROFILE set", statusWarn, "not set in .env; run `localmind init`"}
}

// ----- port checks -----

type portSpec struct {
	envKey  string
	defPort string
	label   string
}

func checkPorts(ctx context.Context, env map[string]string) []checkResult {
	specs := []portSpec{
		{"WEBUI_PORT", "3000", "webui"},
		{"OLLAMA_PORT", "11434", "ollama"},
		{"WHISPER_PORT", "9000", "whisper"},
		{"PIPER_PORT", "10200", "piper"},
		{"MCP_PORT", "7800", "mcp"},
		{"", "7900", "responder"},
	}
	results := make([]checkResult, 0, len(specs))
	for _, sp := range specs {
		port := sp.defPort
		if sp.envKey != "" {
			if v := env[sp.envKey]; v != "" {
				port = v
			}
		}
		results = append(results, checkOnePort(ctx, sp.label, port))
	}
	return results
}

func checkOnePort(ctx context.Context, label, port string) checkResult {
	name := fmt.Sprintf("port %s (%s)", port, label)
	addr := net.JoinHostPort("127.0.0.1", port)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		// Refused / timeout — port is free, our container is not yet up
		// or we have no collision. Either way: OK from a doctor POV.
		return checkResult{name, statusOK, "free"}
	}
	_ = conn.Close()
	// Something is listening. Find out who.
	owner := portOwnerViaDocker(ctx, port)
	if owner != "" {
		return checkResult{name, statusWarn, "in use by our container " + owner}
	}
	return checkResult{name, statusFail, "in use by another process; localmind up will collide"}
}

// portOwnerViaDocker returns the name of a docker container publishing
// the given port, or "" if none. Best-effort; failures yield "".
func portOwnerViaDocker(ctx context.Context, port string) string {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, "docker", "ps",
		"--filter", "publish="+port,
		"--format", "{{.Names}}").Output()
	if err != nil {
		return ""
	}
	names := strings.TrimSpace(string(out))
	if names == "" {
		return ""
	}
	// First line only.
	if i := strings.Index(names, "\n"); i > 0 {
		names = names[:i]
	}
	return names
}

// ----- disk space -----

func checkDiskSpace(ctx context.Context, root string, rootErr error) []checkResult {
	results := []checkResult{}
	if rootErr == nil {
		results = append(results, diskCheck(ctx, "disk space (repo)", root))
	} else {
		results = append(results, checkResult{"disk space (repo)", statusSkip, "repo root not found"})
	}

	home, err := os.UserHomeDir()
	if err == nil {
		lm := filepath.Join(home, ".localmind")
		if _, err := os.Stat(lm); err == nil {
			results = append(results, diskCheck(ctx, "disk space (~/.localmind)", lm))
		}
	}
	return results
}

func diskCheck(ctx context.Context, name, path string) checkResult {
	free, err := freeBytes(ctx, path)
	if err != nil {
		return checkResult{name, statusSkip, err.Error()}
	}
	gb := free / (1 << 30)
	detail := fmt.Sprintf("%d GB free", gb)
	switch {
	case free < (1 << 30): // < 1 GB
		return checkResult{name, statusFail, detail}
	case free < (10 * (1 << 30)): // < 10 GB
		return checkResult{name, statusWarn, detail}
	}
	return checkResult{name, statusOK, detail}
}

// freeBytes shells out to platform-appropriate tools to get free space
// on the volume containing path. Stdlib has no portable disk-free.
func freeBytes(ctx context.Context, path string) (int64, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "linux", "darwin":
		// `df -kP <path>` -> POSIX format. Second line, 4th field is free 1K-blocks.
		out, err := exec.CommandContext(cctx, "df", "-kP", path).Output()
		if err != nil {
			return 0, fmt.Errorf("df failed: %w", err)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) < 2 {
			return 0, fmt.Errorf("df: unexpected output")
		}
		fields := strings.Fields(lines[len(lines)-1])
		if len(fields) < 4 {
			return 0, fmt.Errorf("df: short row")
		}
		kb, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("df parse: %w", err)
		}
		return kb * 1024, nil
	case "windows":
		// Resolve drive letter of path and ask powershell for FreeSpace via Get-PSDrive.
		abs, err := filepath.Abs(path)
		if err != nil {
			return 0, err
		}
		vol := filepath.VolumeName(abs) // e.g. "C:"
		if len(vol) < 2 {
			return 0, fmt.Errorf("no drive letter in %s", abs)
		}
		drive := strings.TrimSuffix(vol, ":")
		ps := fmt.Sprintf("(Get-PSDrive -Name %s).Free", drive)
		out, err := exec.CommandContext(cctx, "powershell.exe",
			"-NoProfile", "-NonInteractive", "-Command", ps).Output()
		if err != nil {
			return 0, fmt.Errorf("powershell failed: %w", err)
		}
		s := strings.TrimSpace(string(out))
		if s == "" {
			return 0, fmt.Errorf("powershell: empty output")
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse free: %w", err)
		}
		return n, nil
	}
	return 0, fmt.Errorf("unsupported os: %s", runtime.GOOS)
}

// ----- ollama -----

func checkOllamaReachable(ctx context.Context, baseURL string) (checkResult, bool, int, []string) {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	url := strings.TrimRight(baseURL, "/") + "/api/tags"
	req, err := http.NewRequestWithContext(cctx, "GET", url, nil)
	if err != nil {
		return checkResult{"ollama reachable", statusWarn, err.Error()}, false, 0, nil
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return checkResult{"ollama reachable", statusWarn,
			"stack not running; that's expected if you haven't run `localmind up` yet"}, false, 0, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return checkResult{"ollama reachable", statusWarn,
			fmt.Sprintf("status %d from %s", resp.StatusCode, url)}, false, 0, nil
	}
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return checkResult{"ollama reachable", statusWarn, "bad json: " + err.Error()}, false, 0, nil
	}
	names := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		names = append(names, m.Name)
	}
	return checkResult{"ollama reachable", statusOK,
		fmt.Sprintf("%d model(s)", len(names))}, true, len(names), names
}

// ----- MCP -----

func checkMCPReachable(ctx context.Context, env map[string]string) checkResult {
	port := env["MCP_PORT"]
	if port == "" {
		port = "7800"
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	url := fmt.Sprintf("http://localhost:%s/healthz", port)
	req, err := http.NewRequestWithContext(cctx, "GET", url, nil)
	if err != nil {
		return checkResult{"mcp gateway reachable", statusWarn, err.Error()}
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return checkResult{"mcp gateway reachable", statusWarn,
			"stack not running; that's expected if you haven't run `localmind up` yet"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return checkResult{"mcp gateway reachable", statusWarn,
			fmt.Sprintf("status %d from %s", resp.StatusCode, url)}
	}
	return checkResult{"mcp gateway reachable", statusOK, ""}
}

// ----- active model -----

func checkActiveModel(root string, rootErr error, ollamaUp bool, models []string) checkResult {
	const name = "active chat model pulled"
	if rootErr != nil {
		return checkResult{name, statusSkip, "repo root not found"}
	}
	if !ollamaUp {
		return checkResult{name, statusSkip, "ollama unreachable"}
	}
	chat, profileName, err := activeChatModel(filepath.Join(root, "models.yml"))
	if err != nil {
		return checkResult{name, statusWarn, err.Error()}
	}
	if chat == "" {
		return checkResult{name, statusWarn, "could not resolve chat model for active profile"}
	}
	if modelInList(chat, models) {
		return checkResult{name, statusOK, fmt.Sprintf("%s (profile %s)", chat, profileName)}
	}
	return checkResult{name, statusWarn,
		fmt.Sprintf("%s not pulled; run `docker exec ollama ollama pull %s`", chat, chat)}
}

// modelInList does a forgiving compare: ollama tags include `:tag` and
// the bare name should still match if the user shortened it.
func modelInList(want string, have []string) bool {
	for _, h := range have {
		if h == want {
			return true
		}
		// Match if `want` lacks an explicit tag and `h` is `want:something`.
		if !strings.Contains(want, ":") && strings.HasPrefix(h, want+":") {
			return true
		}
		// Or if `h` lacks a tag and `want` is `h:something`.
		if !strings.Contains(h, ":") && strings.HasPrefix(want, h+":") {
			return true
		}
	}
	return false
}

// activeChatModel parses models.yml just enough to find
// profiles.<active_profile>.chat. Returns chat, profile, error.
func activeChatModel(path string) (string, string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(string(body), "\n")

	active := ""
	for _, raw := range lines {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "active_profile:") {
			active = strings.TrimSpace(strings.TrimPrefix(l, "active_profile:"))
			active = strings.Trim(active, `"'`)
			break
		}
	}
	if active == "" {
		return "", "", fmt.Errorf("active_profile not set in %s", path)
	}

	// Walk into profiles.<active>.chat. We rely on indentation: profile
	// names live two spaces in, fields four.
	inProfiles := false
	inActive := false
	for _, raw := range lines {
		// Detect leaving with a non-indented top-level key.
		if len(raw) > 0 && raw[0] != ' ' && raw[0] != '#' && raw[0] != '\t' {
			if strings.HasPrefix(strings.TrimSpace(raw), "profiles:") {
				inProfiles = true
				inActive = false
				continue
			}
			inProfiles = false
			inActive = false
			continue
		}
		if !inProfiles {
			continue
		}
		// 2-space-indented profile name: "  cpu_low:"
		if strings.HasPrefix(raw, "  ") && !strings.HasPrefix(raw, "    ") {
			t := strings.TrimSpace(raw)
			if strings.HasSuffix(t, ":") {
				name := strings.TrimSuffix(t, ":")
				inActive = (name == active)
			}
			continue
		}
		if !inActive {
			continue
		}
		// 4+ space indented field under active profile.
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "chat:") {
			val := strings.TrimSpace(strings.TrimPrefix(t, "chat:"))
			val = strings.Trim(val, `"'`)
			if val == "null" || val == "~" {
				val = ""
			}
			return val, active, nil
		}
	}
	return "", active, fmt.Errorf("chat key not found for profile %s", active)
}

// ----- GPU notes -----

func checkAppleGPU(env map[string]string) (checkResult, bool) {
	if runtime.GOOS != "darwin" {
		return checkResult{}, false
	}
	base := env["OLLAMA_BASE_URL"]
	if strings.Contains(base, "host.docker.internal") {
		return checkResult{"apple gpu acceleration", statusOK,
			"OLLAMA_BASE_URL routes to host (Apple Metal active)"}, true
	}
	return checkResult{"apple gpu acceleration", statusWarn,
		"Ollama running in container; Apple GPU not exposed. Use compose.gpu.apple.yml overlay."}, true
}

func checkNvidiaGPU() (checkResult, bool) {
	if runtime.GOOS == "darwin" {
		return checkResult{}, false
	}
	host := hwdetect.Detect()
	if _, err := exec.LookPath("nvidia-smi"); err == nil {
		out, err := exec.Command("nvidia-smi",
			"--query-gpu=name", "--format=csv,noheader").CombinedOutput()
		if err != nil {
			return checkResult{"nvidia gpu", statusWarn,
				fmt.Sprintf("nvidia-smi present but failed: %s", strings.TrimSpace(string(out)))}, true
		}
		first := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		if first == "" {
			return checkResult{"nvidia gpu", statusWarn, "nvidia-smi reported no GPU"}, true
		}
		return checkResult{"nvidia gpu", statusOK, first}, true
	}
	if host.GPUVendor == "nvidia" {
		return checkResult{"nvidia gpu", statusWarn,
			"hardware detected but nvidia-smi missing; install NVIDIA Container Toolkit"}, true
	}
	// No nvidia hardware; emit nothing.
	return checkResult{}, false
}

// ----- helpers -----

// readEnvFile parses KEY=VALUE lines from a .env file. Returns an
// empty map on any error.
func readEnvFile(path string) map[string]string {
	out := map[string]string{}
	body, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return out
}

// ollamaBaseURL builds the URL we should hit. Mirrors ollamaURLFromEnv
// but operates on a pre-parsed env map so we don't read .env twice.
func ollamaBaseURL(env map[string]string) string {
	if v := env["OLLAMA_BASE_URL"]; v != "" {
		return v
	}
	port := env["OLLAMA_PORT"]
	if port == "" {
		port = "11434"
	}
	return "http://localhost:" + port
}
