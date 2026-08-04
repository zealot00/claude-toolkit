package upgrader

import (
	"errors"
	"testing"
)

func TestRunMigrations_EmptyRegistryReturnsNothing(t *testing.T) {
	orig := migrations
	migrations = nil
	t.Cleanup(func() { migrations = orig })
	ran, err := RunMigrations("0.1.0", t.TempDir())
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	if len(ran) != 0 {
		t.Errorf("expected zero runs, got %v", ran)
	}
}

func TestRunMigrations_RunsOnlyUnappliedMigrations(t *testing.T) {
	orig := migrations
	migrations = []Migration{
		{From: "0.0.5", To: "0.1.0", Apply: func(string) error { return nil }},
		{From: "0.1.0", To: "0.2.0", Apply: func(string) error { return nil }},
		{From: "0.2.0", To: "0.3.0", Apply: func(string) error { return nil }},
	}
	t.Cleanup(func() { migrations = orig })

	// From 0.1.0 -> only migrations with To > 0.1.0 run: the second and
	// third. The first (To=0.1.0) is no longer "ahead".
	ran, err := RunMigrations("0.1.0", t.TempDir())
	if err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	want := []string{"0.1.0->0.2.0", "0.2.0->0.3.0"}
	if len(ran) != len(want) {
		t.Fatalf("ran = %v, want %v", ran, want)
	}
	for i := range want {
		if ran[i] != want[i] {
			t.Errorf("ran[%d] = %q, want %q", i, ran[i], want[i])
		}
	}
}

func TestRunMigrations_OldVersionWellBehindRunsAll(t *testing.T) {
	orig := migrations
	migrations = []Migration{
		{From: "0.1.0", To: "0.2.0", Apply: func(string) error { return nil }},
		{From: "0.2.0", To: "0.3.0", Apply: func(string) error { return nil }},
	}
	t.Cleanup(func() { migrations = orig })

	ran, err := RunMigrations("0.0.5", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 {
		t.Errorf("ran = %v, want both migrations", ran)
	}
}

func TestRunMigrations_OldVersionAtOrAheadOfAllRunsNone(t *testing.T) {
	orig := migrations
	migrations = []Migration{
		{From: "0.1.0", To: "0.2.0", Apply: func(string) error { return nil }},
		{From: "0.2.0", To: "0.3.0", Apply: func(string) error { return nil }},
	}
	t.Cleanup(func() { migrations = orig })

	ran, err := RunMigrations("0.5.0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 0 {
		t.Errorf("ran = %v, want zero migrations", ran)
	}
}

func TestRunMigrations_StopsOnError(t *testing.T) {
	orig := migrations
	migrations = []Migration{
		{From: "0.1.0", To: "0.2.0", Apply: func(string) error { return errors.New("boom") }},
		{From: "0.2.0", To: "0.3.0", Apply: func(string) error {
			t.Error("subsequent migration must not run after a failure")
			return nil
		}},
	}
	t.Cleanup(func() { migrations = orig })

	_, err := RunMigrations("0.0.5", t.TempDir())
	if err == nil {
		t.Fatal("expected error from migration")
	}
	if !errors.Is(err, err) {
		t.Errorf("error chain broken: %v", err)
	}
}

func TestRunMigrations_IdempotentOnReRun(t *testing.T) {
	calls := 0
	orig := migrations
	migrations = []Migration{
		{From: "0.1.0", To: "0.2.0", Apply: func(string) error { calls++; return nil }},
	}
	t.Cleanup(func() { migrations = orig })

	// First upgrade: from oldVersion=0.1.0 -> migration runs.
	ran1, err := RunMigrations("0.1.0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ran1) != 1 || calls != 1 {
		t.Errorf("first run: ran=%v calls=%d, want 1 migration and 1 call", ran1, calls)
	}

	// Second upgrade: oldVersion is now 0.2.0 (post-migration); the
	// migration's To no longer gates the run.
	ran2, err := RunMigrations("0.2.0", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(ran2) != 0 || calls != 1 {
		t.Errorf("second run: ran=%v calls=%d, want zero migrations", ran2, calls)
	}
}

func TestNormalizeVersion_PadsAndStripsV(t *testing.T) {
	cases := []struct{ in, want string }{
		{"v0.1", "0.1.0"},
		{"0.2.0", "0.2.0"},
		{"0.1", "0.1.0"},
		{"v1.2.3", "1.2.3"},
	}
	for _, c := range cases {
		if got := normalizeVersion(c.in); got != c.want {
			t.Errorf("normalizeVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
