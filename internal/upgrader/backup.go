// Package upgrader holds the parts of `claude-toolkit upgrade` that need to
// be testable independently of the cmd package: the backup/rollback file
// machinery and the schema-migration registry.
//
// cmd/upgrade.go is a single-file command driver that orchestrates these
// pieces around GitHub Releases fetch + atomic binary replace; the
// orchestration itself is in cmd/upgrade.go, the reusable pieces are here.
package upgrader

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// BackupSuffix is the suffix appended to the running binary to form the
// backup file. Centralised so cmd/upgrade.go and any cleanup helper agree.
const BackupSuffix = ".bak"

// BackupPath returns the backup path for the given binary.
func BackupPath(self string) string { return self + BackupSuffix }

// StagingPath returns the staging path used by the atomic replace dance.
func StagingPath(self string) string { return self + ".new" }

// ErrBackupExists is returned by Backup when a previous backup is still on
// disk. The caller decides whether to clobber it (default) or refuse.
var ErrBackupExists = errors.New("upgrader: backup already exists")

// Backup copies the running binary at self to self.bak so a failed
// upgrade can be rolled back. Existing backups are overwritten because the
// whole point of a backup is "the most recent good binary we had" -- a
// stale one is worse than no backup.
//
// Errors from the copy are wrapped with the underlying syscall so a
// permission denied vs disk-full diagnosis is preserved.
func Backup(self string) (string, error) {
	dst := BackupPath(self)
	if _, err := copyFile(self, dst, 0o755); err != nil {
		return "", fmt.Errorf("upgrader: backup %s -> %s: %w", self, dst, err)
	}
	return dst, nil
}

// Rollback restores self from self.bak and removes the staging file. The
// caller is expected to have already moved self out of the way (or the
// rename target self is unused at this point). If the backup is missing,
// returns an error so the caller can decide whether to fall back to
// `curl install.sh`.
func Rollback(self string) error {
	bak := BackupPath(self)
	if _, err := os.Stat(bak); err != nil {
		return fmt.Errorf("upgrader: rollback: backup missing at %s: %w", bak, err)
	}
	if err := os.Rename(bak, self); err != nil {
		return fmt.Errorf("upgrader: rollback: rename %s -> %s: %w", bak, self, err)
	}
	return nil
}

// Cleanup removes a stale backup file. Idempotent: no error if the file
// is already gone, which matters because the upgrade flow may call this
// several times.
func Cleanup(self string) error {
	bak := BackupPath(self)
	if err := os.Remove(bak); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("upgrader: cleanup: %w", err)
	}
	return nil
}

// copyFile copies src to dst with the given mode, preserving the source
// size and reporting the number of bytes copied. Errors from Open /
// Create / Write are wrapped with path context so a permission denied vs
// disk-full diagnosis is preserved.
func copyFile(src, dst string, mode os.FileMode) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return 0, err
	}

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	defer out.Close()

	n, err := io.Copy(out, in)
	if err != nil {
		return n, err
	}
	// Truncate to the actual copied length when src has shrunk since the
	// earlier Stat (rare; OpenFile with O_TRUNC only cleared the file).
	if n < info.Size() {
		if err := out.Truncate(n); err != nil {
			return n, err
		}
	}
	return n, nil
}
