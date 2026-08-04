package upgrader

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeBinary lays down a small executable file at path with the given
// contents. Used as the "self" passed to Backup / Rollback.
func writeFakeBinary(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude-toolkit")
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBackup_CreatesBackupFile(t *testing.T) {
	self := writeFakeBinary(t, "old version\n")
	bak, err := Backup(self)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if bak != self+BackupSuffix {
		t.Errorf("Backup returned %q, want %q", bak, self+BackupSuffix)
	}
	data, err := os.ReadFile(bak)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != "old version\n" {
		t.Errorf("backup contents = %q, want %q", data, "old version\n")
	}
}

func TestBackup_OverwritesStaleBackup(t *testing.T) {
	self := writeFakeBinary(t, "current version\n")
	// Lay down a stale backup to confirm Backup clobbers it.
	if err := os.WriteFile(self+BackupSuffix, []byte("STALE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(self); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	got, err := os.ReadFile(self + BackupSuffix)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current version\n" {
		t.Errorf("stale backup not overwritten: %q", got)
	}
}

func TestBackup_SourceMissing(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such-binary")
	_, err := Backup(missing)
	if err == nil {
		t.Fatal("expected error backing up missing source")
	}
	if !strings.Contains(err.Error(), "no-such-binary") {
		t.Errorf("error should mention the missing path, got: %v", err)
	}
}

func TestBackup_DestinationReadOnly(t *testing.T) {
	self := writeFakeBinary(t, "x")
	// Replace self with a symlink into a directory that doesn't allow
	// writes: simplest cross-platform-ish way is to point Backup at a
	// path whose parent is read-only.
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Skipf("Chmod 0o500 unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	target := filepath.Join(parent, "claude-toolkit")
	if err := os.Symlink(self, target); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Backup(target); err == nil {
		t.Fatal("expected error when destination dir is read-only")
	}
}

func TestRollback_RestoresFromBackup(t *testing.T) {
	self := writeFakeBinary(t, "post-upgrade contents\n")
	// Lay down a backup explicitly so Rollback has something to restore.
	if err := os.WriteFile(self+BackupSuffix, []byte("pre-upgrade contents\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Simulate the post-upgrade state: the user already moved self away
	// (the upgrade cmd's job). We simulate by truncating self.
	if err := os.WriteFile(self, []byte(""), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(self); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	got, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "pre-upgrade contents\n" {
		t.Errorf("after rollback: %q, want %q", got, "pre-upgrade contents\n")
	}
	if _, err := os.Stat(self + BackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup should be consumed by rollback; stat err = %v", err)
	}
}

func TestRollback_NoBackupReturnsError(t *testing.T) {
	self := writeFakeBinary(t, "x")
	if err := Rollback(self); err == nil {
		t.Fatal("expected error rolling back without backup")
	}
}

func TestCleanup_RemovesBackup(t *testing.T) {
	self := writeFakeBinary(t, "x")
	if err := os.WriteFile(self+BackupSuffix, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Cleanup(self); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(self + BackupSuffix); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("backup should be gone, stat err = %v", err)
	}
}

func TestCleanup_NoBackupIsNoOp(t *testing.T) {
	self := writeFakeBinary(t, "x")
	if err := Cleanup(self); err != nil {
		t.Errorf("Cleanup with no backup: %v", err)
	}
}
