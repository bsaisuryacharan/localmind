package wizard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// dockerVolumes are the named compose volumes (project = "localmind") whose
// contents make up a complete snapshot of user state.
var dockerVolumes = []string{
	"localmind_ollama",
	"localmind_webui",
	"localmind_piper",
	"localmind_mcp_index",
}

// Backup snapshots all docker volumes into a single zstd-compressed tarball.
//
// Strategy: spin up an ephemeral alpine container with each volume mounted
// read-only at /backup/<volume> and the destination directory mounted writable
// at /out, then `tar | zstd` everything in one shot. This avoids any host-side
// dependency on tar/zstd and works identically on Linux, macOS, and Windows
// hosts that have Docker.
//
// TODO: implement `localmind restore <archive>` as a sibling command.
func Backup(ctx context.Context, args []string) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found on PATH: %w", err)
	}

	archivePath := defaultArchivePath()
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		archivePath = args[0]
	}
	abs, err := filepath.Abs(archivePath)
	if err != nil {
		return fmt.Errorf("resolve archive path: %w", err)
	}
	outDir := filepath.Dir(abs)
	outName := filepath.Base(abs)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}

	if err := assertVolumesExist(ctx); err != nil {
		return err
	}

	fmt.Printf("==> backing up to %s\n", abs)

	dockerArgs := []string{"run", "--rm", "-v", outDir + ":/out"}
	for _, vol := range dockerVolumes {
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:/backup/%s:ro", vol, vol))
	}
	dockerArgs = append(dockerArgs, "alpine:3.20", "sh", "-c",
		`set -e
apk add --no-cache --quiet zstd tar >/dev/null
tar -cf - -C /backup . | zstd -T0 -19 -q -o "/out/`+outName+`"`)

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker run: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("stat archive: %w", err)
	}
	fmt.Printf("==> wrote %s (%.1f MB)\n", abs, float64(info.Size())/(1<<20))
	return nil
}

// defaultArchivePath returns ./localmind-backup-<timestamp>.tar.zst with a
// Windows-safe (colon-free) timestamp.
func defaultArchivePath() string {
	stamp := time.Now().UTC().Format("20060102-150405")
	return fmt.Sprintf("./localmind-backup-%s.tar.zst", stamp)
}

// assertVolumesExist checks that the compose stack has been brought up at
// least once. `docker volume inspect` on an unknown volume returns non-zero.
func assertVolumesExist(ctx context.Context) error {
	missing := []string{}
	for _, vol := range dockerVolumes {
		cmd := exec.CommandContext(ctx, "docker", "volume", "inspect", vol)
		if err := cmd.Run(); err != nil {
			missing = append(missing, vol)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing docker volumes: %s\nrun `localmind up` first to create them",
			strings.Join(missing, ", "))
	}
	return nil
}
