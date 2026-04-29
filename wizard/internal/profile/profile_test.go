package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		profile     string
		tps         float64
		wantRec     Recommendation
		wantNext    string
	}{
		{"below-slow downgrades cpu_mid", "cpu_mid", 3, Downgrade, "cpu_low"},
		{"above-fast upgrades cpu_low", "cpu_low", 40, Upgrade, "cpu_mid"},
		{"stays at floor (cpu_low + slow)", "cpu_low", 3, Stay, ""},
		{"stays at ceiling (nvidia_24gb + fast)", "nvidia_24gb", 40, Stay, ""},
		{"in-band stays regardless of profile", "cpu_low", 10, Stay, ""},
		{"in-band stays for nvidia_12gb too", "nvidia_12gb", 10, Stay, ""},
		// Edge: the slow/fast bands are strict (< and >), not <= / >=, so
		// an exact-threshold value should land in Stay.
		{"exact slow threshold stays", "cpu_mid", slowTPS, Stay, ""},
		{"exact fast threshold stays", "cpu_low", fastTPS, Stay, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec, next := classify(tc.profile, tc.tps)
			if rec != tc.wantRec || next != tc.wantNext {
				t.Fatalf("classify(%q, %v) = (%q, %q); want (%q, %q)",
					tc.profile, tc.tps, rec, next, tc.wantRec, tc.wantNext)
			}
		})
	}
}

func TestReadEnvKey_BasicAndQuoted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	body := `# this is a comment
FOO=bar
BAZ="quux"
QUOTED='value'
EMPTY=
SPACED = padded
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write env: %v", err)
	}

	cases := []struct {
		key     string
		want    string
		wantErr bool
	}{
		{"FOO", "bar", false},
		{"BAZ", "quux", false},        // double-quotes stripped
		{"QUOTED", "value", false},    // single-quotes stripped
		{"EMPTY", "", false},          // empty value is fine, no error
		{"SPACED", "padded", false},   // surrounding whitespace trimmed
		{"MISSING", "", true},
	}
	for _, tc := range cases {
		got, err := readEnvKey(path, tc.key)
		if tc.wantErr {
			if err == nil {
				t.Errorf("readEnvKey(%q) expected error, got %q", tc.key, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("readEnvKey(%q) error: %v", tc.key, err)
			continue
		}
		if got != tc.want {
			t.Errorf("readEnvKey(%q)=%q want %q", tc.key, got, tc.want)
		}
	}
}

func TestReadYAMLKey_TopLevel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yml")
	body := `# header
active_profile: cpu_low
something_else: ignored
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	got, err := readYAMLKey(path, "active_profile")
	if err != nil {
		t.Fatalf("readYAMLKey: %v", err)
	}
	if got != "cpu_low" {
		t.Fatalf("readYAMLKey=%q want cpu_low", got)
	}

	if _, err := readYAMLKey(path, "missing_key"); err == nil {
		t.Fatalf("expected error for missing key")
	}
}

// writeRealisticModelsYML drops a models.yml in the tmp dir that mirrors the
// shape of the repo's real one: profiles: block with cpu_low, cpu_mid, etc.
// readChatModel is line-based and indentation-tolerant rather than spec-correct,
// so the indentation here matters.
func writeRealisticModelsYML(t *testing.T, dir string) {
	t.Helper()
	body := `# header comment
profiles:
  cpu_low:
    description: "CPU only"
    chat: qwen2.5:3b
    embedding: nomic-embed-text
  cpu_mid:
    description: "CPU only, more RAM"
    chat: qwen2.5:7b
    embedding: nomic-embed-text
  nvidia_12gb:
    chat: qwen2.5:14b-instruct-q4_K_M

active_profile: cpu_low
`
	if err := os.WriteFile(filepath.Join(dir, "models.yml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write models.yml: %v", err)
	}
}

func TestReadChatModel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRealisticModelsYML(t, dir)

	got, err := readChatModel(dir, "cpu_mid")
	if err != nil {
		t.Fatalf("readChatModel(cpu_mid): %v", err)
	}
	if got != "qwen2.5:7b" {
		t.Fatalf("readChatModel(cpu_mid)=%q want qwen2.5:7b", got)
	}

	// The first profile in the file should also resolve correctly — sanity
	// check that the parser doesn't only find the second one.
	got, err = readChatModel(dir, "cpu_low")
	if err != nil {
		t.Fatalf("readChatModel(cpu_low): %v", err)
	}
	if got != "qwen2.5:3b" {
		t.Fatalf("readChatModel(cpu_low)=%q want qwen2.5:3b", got)
	}
}

func TestReadChatModel_ProfileNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeRealisticModelsYML(t, dir)

	if _, err := readChatModel(dir, "nonexistent"); err == nil {
		t.Fatalf("readChatModel(nonexistent) returned nil error")
	}
}

func TestRound1(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   float64
		want float64
	}{
		{11.444, 11.4},
		{11.45, 11.5}, // round-half-up via the +0.5 trick
		{0, 0},
		{1.0, 1.0},
	}
	for _, tc := range cases {
		if got := round1(tc.in); got != tc.want {
			t.Errorf("round1(%v)=%v want %v", tc.in, got, tc.want)
		}
	}
}
