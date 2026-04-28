// Package profile benchmarks the active chat model on the host's hardware
// and recommends a smaller / larger profile based on the measured throughput.
//
// Stdlib only — wizard/go.mod must stay dependency-free.
package profile

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Recommendation is one of "stay", "downgrade", "upgrade".
type Recommendation string

const (
	Stay      Recommendation = "stay"
	Downgrade Recommendation = "downgrade"
	Upgrade   Recommendation = "upgrade"
)

// Result is what gets persisted to .localmind/profile.json.
type Result struct {
	Model          string         `json:"model"`
	Profile        string         `json:"profile"`
	TokensPerSec   float64        `json:"tokens_per_sec"`
	Recommendation Recommendation `json:"recommendation"`
	NextProfile    string         `json:"next_profile,omitempty"`
	Timestamp      time.Time      `json:"timestamp"`
}

// Config controls a single profiler run.
type Config struct {
	RepoRoot       string
	OllamaBaseURL  string
	WaitForOllama  time.Duration // default 60s
	GenerateBudget time.Duration // default 5m
}

// thresholds for recommendations.
const (
	slowTPS = 5.0
	fastTPS = 30.0
)

// downgradeMap and upgradeMap define the linear ordering within each family.
// Apple Silicon and CPU paths share a downgrade target because dropping below
// nvidia_12gb on a CPU-only fallback means giving up the GPU entirely.
var downgradeMap = map[string]string{
	"cpu_mid":         "cpu_low",
	"nvidia_12gb":     "cpu_mid",
	"nvidia_24gb":     "nvidia_12gb",
	"apple_32gb_plus": "apple_16gb",
}

var upgradeMap = map[string]string{
	"cpu_low":     "cpu_mid",
	"cpu_mid":     "nvidia_12gb",
	"nvidia_12gb": "nvidia_24gb",
	"apple_16gb":  "apple_32gb_plus",
}

// Run executes the profiler end-to-end: wait for Ollama, pull the model,
// time a generation, persist result, return it.
//
// Caller decides whether to skip; see ShouldSkip and ProfilePath.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	if cfg.OllamaBaseURL == "" {
		cfg.OllamaBaseURL = "http://localhost:11434"
	}
	if cfg.WaitForOllama == 0 {
		cfg.WaitForOllama = 60 * time.Second
	}
	if cfg.GenerateBudget == 0 {
		cfg.GenerateBudget = 5 * time.Minute
	}
	cfg.OllamaBaseURL = strings.TrimRight(cfg.OllamaBaseURL, "/")

	profileName, err := readActiveProfile(cfg.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("read active profile: %w", err)
	}
	model, err := readChatModel(cfg.RepoRoot, profileName)
	if err != nil {
		return nil, fmt.Errorf("read chat model: %w", err)
	}

	if err := waitForOllama(ctx, cfg.OllamaBaseURL, cfg.WaitForOllama); err != nil {
		return nil, err
	}

	fmt.Printf("==> profiler: ensuring model %s is available\n", model)
	if err := pullModel(ctx, cfg.OllamaBaseURL, model); err != nil {
		return nil, fmt.Errorf("pull %s: %w", model, err)
	}

	fmt.Printf("==> profiler: timing a 50-token generation\n")
	tps, err := timeGeneration(ctx, cfg.OllamaBaseURL, model, cfg.GenerateBudget)
	if err != nil {
		return nil, fmt.Errorf("generate: %w", err)
	}

	rec, next := classify(profileName, tps)
	res := &Result{
		Model:          model,
		Profile:        profileName,
		TokensPerSec:   round1(tps),
		Recommendation: rec,
		NextProfile:    next,
		Timestamp:      time.Now().UTC(),
	}
	if err := persist(cfg.RepoRoot, res); err != nil {
		return nil, fmt.Errorf("persist: %w", err)
	}
	report(res)
	return res, nil
}

// ShouldSkip reports whether a previous profile result already exists.
// Callers pass force=true to override.
func ShouldSkip(repoRoot string, force bool) bool {
	if force {
		return false
	}
	_, err := os.Stat(ProfilePath(repoRoot))
	return err == nil
}

// ProfilePath returns the on-disk location of the persisted result.
func ProfilePath(repoRoot string) string {
	return filepath.Join(repoRoot, ".localmind", "profile.json")
}

func report(r *Result) {
	switch r.Recommendation {
	case Stay:
		fmt.Printf("==> profiler: %.1f tok/s with %s — looks good\n", r.TokensPerSec, r.Model)
	case Downgrade:
		fmt.Printf("==> profiler: %.1f tok/s with %s is slow; consider switching to profile %q in models.yml\n",
			r.TokensPerSec, r.Model, r.NextProfile)
	case Upgrade:
		fmt.Printf("==> profiler: %.1f tok/s with %s — your hardware can handle a larger profile (%q)\n",
			r.TokensPerSec, r.Model, r.NextProfile)
	}
}

func classify(profile string, tps float64) (Recommendation, string) {
	if tps < slowTPS {
		if next, ok := downgradeMap[profile]; ok {
			return Downgrade, next
		}
	}
	if tps > fastTPS {
		if next, ok := upgradeMap[profile]; ok {
			return Upgrade, next
		}
	}
	return Stay, ""
}

// readActiveProfile reads the LOCALMIND_PROFILE key from <root>/.env, falling
// back to active_profile from <root>/models.yml.
func readActiveProfile(root string) (string, error) {
	if v, err := readEnvKey(filepath.Join(root, ".env"), "LOCALMIND_PROFILE"); err == nil && v != "" {
		return v, nil
	}
	return readYAMLKey(filepath.Join(root, "models.yml"), "active_profile")
}

// readChatModel finds the chat model for the named profile in models.yml.
// Parser is line-based and tolerant of typical YAML indentation; it does not
// implement the full spec.
func readChatModel(root, profile string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, "models.yml"))
	if err != nil {
		return "", err
	}
	scan := bufio.NewScanner(bytes.NewReader(body))
	inProfiles := false
	inThis := false
	for scan.Scan() {
		raw := scan.Text()
		line := strings.TrimRight(raw, " \t")
		if strings.HasPrefix(line, "profiles:") {
			inProfiles = true
			continue
		}
		if inProfiles && len(line) > 0 && !startsWithSpace(line) {
			break // left the profiles: block
		}
		if !inProfiles {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, profile+":") {
			inThis = true
			continue
		}
		if inThis {
			if strings.HasPrefix(trimmed, "chat:") {
				val := strings.TrimSpace(strings.TrimPrefix(trimmed, "chat:"))
				return strings.Trim(val, `"'`), nil
			}
			// Detect that we've left the profile (next sibling key at same indent).
			if !startsWithSpace(line) || (len(line) > 0 && line[0] != ' ' && line[0] != '\t') {
				inThis = false
			}
		}
	}
	if err := scan.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("chat model not found for profile %q", profile)
}

func startsWithSpace(s string) bool {
	return len(s) > 0 && (s[0] == ' ' || s[0] == '\t')
}

// readEnvKey extracts KEY=VALUE from a dotenv-style file. Lines starting with
// '#' and empty lines are ignored. Quotes around the value are stripped.
func readEnvKey(path, key string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	scan := bufio.NewScanner(bytes.NewReader(body))
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(v), `"'`), nil
	}
	return "", fmt.Errorf("key %s not found in %s", key, path)
}

// readYAMLKey extracts a top-level scalar key from a tiny YAML file.
func readYAMLKey(path, key string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	scan := bufio.NewScanner(bytes.NewReader(body))
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, key+":") {
			val := strings.TrimSpace(strings.TrimPrefix(line, key+":"))
			return strings.Trim(val, `"'`), nil
		}
	}
	return "", fmt.Errorf("key %s not found in %s", key, path)
}

// waitForOllama polls /api/tags until it returns 200 or the budget is spent.
func waitForOllama(ctx context.Context, base string, budget time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(budget)
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/tags", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("ollama at %s did not become ready within %s", base, budget)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// pullModel POSTs /api/pull and drains the streaming response.
func pullModel(ctx context.Context, base, model string) error {
	body, _ := json.Marshal(map[string]string{"name": model})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	// Drain. Ollama emits NDJSON status updates; we don't need them but the
	// connection stays open until the pull finishes.
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var msg struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if msg.Error != "" {
			return errors.New(msg.Error)
		}
	}
	return nil
}

// timeGeneration sends one /api/generate (stream=false) and returns
// tokens-per-second derived from the response's eval_count + eval_duration.
func timeGeneration(ctx context.Context, base, model string, budget time.Duration) (float64, error) {
	body, _ := json.Marshal(map[string]any{
		"model":   model,
		"prompt":  "Write a haiku about open source.",
		"stream":  false,
		"options": map[string]any{"num_predict": 50},
	})
	gctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	req, err := http.NewRequestWithContext(gctx, http.MethodPost, base+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return 0, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		EvalCount    int   `json:"eval_count"`
		EvalDuration int64 `json:"eval_duration"` // nanoseconds
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	if out.EvalDuration == 0 {
		return 0, errors.New("eval_duration was zero in response")
	}
	return float64(out.EvalCount) / (float64(out.EvalDuration) / 1e9), nil
}

func persist(root string, r *Result) error {
	dir := filepath.Join(root, ".localmind")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "profile.json.tmp")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, ProfilePath(root))
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}
