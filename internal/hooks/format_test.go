package hooks

import (
	"os"
	"testing"
)

// withTools stubs the tool resolver so tests can pin which binaries "exist"
// without depending on the host machine.
func withTools(t *testing.T, tools map[string]string) {
	t.Helper()
	old := lookPath
	lookPath = func(name string) (string, error) {
		if p, ok := tools[name]; ok {
			return p, nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { lookPath = old })
}

func TestFormatterForGoPrefersGoimports(t *testing.T) {
	withTools(t, map[string]string{"goimports": "/usr/local/bin/goimports", "gofmt": "/usr/bin/gofmt"})
	steps, ok := formatterFor("x.go")
	if !ok || len(steps) != 1 || steps[0].name != "/usr/local/bin/goimports" {
		t.Fatalf("go pipeline = %+v, %v; want goimports only", steps, ok)
	}
}

func TestFormatterForGoFallsBackToGofmt(t *testing.T) {
	withTools(t, map[string]string{"gofmt": "/usr/bin/gofmt"})
	steps, ok := formatterFor("x.go")
	if !ok || len(steps) != 1 || steps[0].name != "/usr/bin/gofmt" {
		t.Fatalf("go fallback = %+v, %v; want gofmt", steps, ok)
	}
}

func TestFormatterForGoNone(t *testing.T) {
	withTools(t, map[string]string{})
	if _, ok := formatterFor("x.go"); ok {
		t.Error("no Go tools installed; pipeline must be empty")
	}
}

func TestFormatterForPyRunsRuffCheckThenFormat(t *testing.T) {
	withTools(t, map[string]string{"ruff": "/opt/ruff"})
	steps, ok := formatterFor("x.py")
	if !ok || len(steps) != 2 {
		t.Fatalf("py pipeline = %+v, %v; want ruff check + ruff format", steps, ok)
	}
	if len(steps[0].args) == 0 || steps[0].args[0] != "check" {
		t.Errorf("first step = %+v, want ruff check (fix before format)", steps[0])
	}
	if len(steps[1].args) == 0 || steps[1].args[0] != "format" {
		t.Errorf("second step = %+v, want ruff format", steps[1])
	}
}

func TestFormatterForPyFallsBackToBlack(t *testing.T) {
	withTools(t, map[string]string{"black": "/opt/black"})
	steps, ok := formatterFor("x.py")
	if !ok || len(steps) != 1 || steps[0].name != "/opt/black" {
		t.Fatalf("py fallback = %+v, %v; want black only", steps, ok)
	}
}

func TestFormatterForPyNone(t *testing.T) {
	withTools(t, map[string]string{})
	if _, ok := formatterFor("x.py"); ok {
		t.Error("no Python tools installed; pipeline must be empty")
	}
}

func TestFormatterForUnknownExt(t *testing.T) {
	withTools(t, map[string]string{"gofmt": "/usr/bin/gofmt"})
	if _, ok := formatterFor("x.unknownext"); ok {
		t.Error("unknown extension must yield no pipeline")
	}
}
