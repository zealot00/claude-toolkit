package upgrader

import (
	"fmt"
	"strconv"
	"strings"
)

// Migration is one schema-bump the toolkit performs after a successful
// binary upgrade. From is the version *before* the bump; To is the
// version that introduces the new schema. Apply runs against the toolkit
// root (resolved by pkg/dir in the caller) and returns an error if the
// migration cannot complete cleanly.
//
// Migrations are best-effort: Apply errors are returned to the caller but
// do not roll back the binary. The reasoning: a failed migration may be a
// real bug, but the upgrade itself is sound -- the user's old binary
// already worked, and the new binary works too, just with a fresh schema
// for the next session.
type Migration struct {
	From  string
	To    string
	Apply func(homeDir string) error
}

// Registered migrations. Ordered by To (semver). Add new entries at the
// end of the slice -- the run order matters when one migration's output
// is the next migration's input.
//
// v0.1 -> v0.2: no schema changes requiring a migration. New state files
// (autoproxy.pid, hud.json) are additive and old binaries safely ignore
// them. The placeholder keeps the registry wired for future bumps.
var migrations = []Migration{
	{
		From: "0.1.0",
		To:   "0.2.0",
		Apply: func(homeDir string) error {
			// No-op. New state files are additive; old binaries do not
			// read them and new binaries tolerate their absence.
			return nil
		},
	},
}

// RunMigrations executes every migration whose To is newer than
// oldVersion, in slice order. The homeDir argument is the toolkit root
// that Apply hooks can write into.
//
// The version comparison is a dotted-numeric semver: missing segments are
// treated as 0, non-numeric segments sort low (matching the existing
// cmd/upgrade.go compareVersions convention).
//
// A migration applies when "oldVersion < migration.To" -- i.e. the user
// is coming from a version that does not yet have this migration's
// schema. The From field is informational: it records the version that
// introduced the new schema, but the gate is only on To because we never
// want to re-run a migration the user has already received.
//
// Returns the list of migrations that ran (for logging) plus the first
// error encountered, if any. A partial run is possible: subsequent
// migrations are skipped when an earlier one errors.
func RunMigrations(oldVersion, homeDir string) (ran []string, err error) {
	oldNorm := normalizeVersion(oldVersion)
	for _, m := range migrations {
		if compareVersions(oldNorm, normalizeVersion(m.To)) >= 0 {
			continue
		}
		ran = append(ran, m.From+"->"+m.To)
		if applyErr := m.Apply(homeDir); applyErr != nil {
			return ran, fmt.Errorf("upgrader: migration %s -> %s: %w", m.From, m.To, applyErr)
		}
	}
	return ran, nil
}

// normalizeVersion strips a leading "v" and pads missing segments with
// zeros so a partial version like "0.1" compares equal to "0.1.0".
func normalizeVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	for len(parts) < 3 {
		parts = append(parts, "0")
	}
	return strings.Join(parts, ".")
}

// compareVersions compares two normalised dotted-numeric versions.
// Returns -1, 0, or 1. Missing segments (already padded to 0) compare
// as equal. This intentionally matches cmd/upgrade.go's compareVersions
// so the migration range logic and the upgrade version gate agree.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		av, bv := 0, 0
		if i < len(as) {
			if n, err := strconv.Atoi(as[i]); err == nil {
				av = n
			} else {
				av = -1
			}
		}
		if i < len(bs) {
			if n, err := strconv.Atoi(bs[i]); err == nil {
				bv = n
			} else {
				bv = -1
			}
		}
		if av != bv {
			if av > bv {
				return 1
			}
			return -1
		}
	}
	return 0
}
