package wizard

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Restore reverses a backup archive into the four named compose volumes.
// Destructive: any existing contents in those volumes are overwritten.
//
// Mirrors the backup pattern: an ephemeral alpine container with the four
// named volumes mounted writable at /restore/<volume> and the archive
// directory mounted read-only at /in. Inside the container,
// `zstd -dc | tar -xf -` writes the archive's contents back over the
// volumes. The localmind stack is brought down first so containers don't
// hold open files in the volumes during overwrite.
//
// Usage:
//
//	localmind restore [--yes] <archive>
//
// Without --yes the user is prompted to confirm before any destructive
// action. `localmind backup` is the inverse.
func Restore(ctx context.Context, args []string) error {
	autoYes, archivePath, err := parseRestoreArgs(args)
	if err != nil {
		return err
	}
	if archivePath == "" {
		return fmt.Errorf("usage: localmind restore [--yes] <archive>")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found on PATH: %w", err)
	}

	abs, err := filepath.Abs(archivePath)
	if err != nil {
		return fmt.Errorf("resolve archive: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("archive not readable: %w", err)
	}
	inDir := filepath.Dir(abs)
	inName := filepath.Base(abs)

	if !autoYes {
		fmt.Printf("This will OVERWRITE the contents of these docker volumes:\n")
		for _, v := range dockerVolumes {
			fmt.Printf("  %s\n", v)
		}
		fmt.Printf("\nAny existing chat history, models, and RAG index will be replaced\n")
		fmt.Printf("with the contents of:\n  %s\n\n", abs)
		fmt.Print("Type 'yes' to continue: ")
		reader := bufio.NewReader(os.Stdin)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) != "yes" {
			return fmt.Errorf("aborted")
		}
	}

	// Bring the stack down so no container is holding the volumes.
	fmt.Printf("==> stopping stack so volumes can be replaced\n")
	if err := composeRun(ctx, []string{"down"}); err != nil {
		// Don't fail on `down` errors — the stack may not be running.
		fmt.Printf("warning: docker compose down returned: %v (continuing)\n", err)
	}

	// Make sure the volumes exist; create empty ones if not.
	for _, vol := range dockerVolumes {
		if err := exec.CommandContext(ctx, "docker", "volume", "inspect", vol).Run(); err != nil {
			fmt.Printf("==> creating volume %s\n", vol)
			if err := exec.CommandContext(ctx, "docker", "volume", "create", vol).Run(); err != nil {
				return fmt.Errorf("create volume %s: %w", vol, err)
			}
		}
	}

	fmt.Printf("==> restoring from %s\n", abs)
	dockerArgs := []string{"run", "--rm", "-v", inDir + ":/in:ro"}
	for _, vol := range dockerVolumes {
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:/restore/%s", vol, vol))
	}
	dockerArgs = append(dockerArgs, "alpine:3.20", "sh", "-c",
		`set -e
apk add --no-cache --quiet zstd tar >/dev/null
zstd -dc "/in/`+inName+`" | tar -xf - -C /restore`)

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker run: %w", err)
	}

	fmt.Printf("==> restored. run `localmind up` to start the stack with restored data.\n")
	return nil
}

func parseRestoreArgs(args []string) (autoYes bool, archive string, err error) {
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			autoYes = true
		case "-h", "--help":
			err = fmt.Errorf("usage: localmind restore [--yes] <archive>")
			return
		default:
			if strings.HasPrefix(a, "-") {
				err = fmt.Errorf("unknown flag: %s", a)
				return
			}
			if archive != "" {
				err = fmt.Errorf("expected one archive path, got multiple")
				return
			}
			archive = a
		}
	}
	return
}
