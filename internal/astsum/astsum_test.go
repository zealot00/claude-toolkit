package astsum

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSum(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSummarizeGo(t *testing.T) {
	src := writeSum(t, "widget.go", `package widget

import (
	"context"
	"io"
)

// Widget is a thing.
type Widget struct {
	Name string
}

// Stringer is an interface.
type Stringer interface {
	String() string
}

func (w *Widget) Size(ctx context.Context, r io.Reader) (int, error) { return 0, nil }
func (w *Widget) String() string                                     { return w.Name }
func New(name string) *Widget                                        { return &Widget{Name: name} }
func unexportedHelper()                                              {}
`)

	s, err := SummarizeGo(src)
	if err != nil {
		t.Fatal(err)
	}
	if s.Package != "widget" {
		t.Errorf("package = %q", s.Package)
	}
	if len(s.Imports) != 2 {
		t.Errorf("imports = %v", s.Imports)
	}
	if len(s.Types) != 2 || s.Types[0].Name != "Widget" || s.Types[0].Kind != "struct" {
		t.Fatalf("types = %+v", s.Types)
	}
	if s.Types[1].Kind != "interface" {
		t.Errorf("Stringer kind = %q", s.Types[1].Kind)
	}
	// Methods attach to their type.
	if len(s.Types[0].Methods) != 2 || s.Types[0].Methods[0].Name != "Size" {
		t.Errorf("Widget methods = %+v", s.Types[0].Methods)
	}
	// Only exported package funcs.
	if len(s.Funcs) != 1 || s.Funcs[0].Name != "New" {
		t.Errorf("funcs = %+v", s.Funcs)
	}
	if !strings.Contains(s.Types[0].Methods[0].Params, "context.Context") {
		t.Errorf("params not rendered: %q", s.Types[0].Methods[0].Params)
	}
}

func TestSummarizePy(t *testing.T) {
	src := writeSum(t, "app.py", `import os
import requests

BASE = "https://api.example.com"

class Client:
    """A client."""

    def __init__(self, token: str, timeout: float = 5.0):
        self.token = token

    @property
    def headers(self) -> dict:
        return {}

def make_client(token: str) -> Client:
    return Client(token)

def _private_helper():
    pass
`)

	s, err := SummarizePy(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Classes) != 1 || s.Classes[0].Name != "Client" {
		t.Fatalf("classes = %+v", s.Classes)
	}
	if len(s.Classes[0].Methods) != 2 {
		t.Fatalf("Client methods = %+v", s.Classes[0].Methods)
	}
	if s.Classes[0].Methods[0].Name != "__init__" {
		t.Errorf("first method = %q", s.Classes[0].Methods[0].Name)
	}
	// Only top-level defs; nested/private at top level is still top-level.
	if len(s.Funcs) != 2 {
		t.Fatalf("funcs = %+v", s.Funcs)
	}
	if s.Funcs[0].Name != "make_client" {
		t.Errorf("first func = %q", s.Funcs[0].Name)
	}
}

func TestSummarizePyDecoratorOnMethod(t *testing.T) {
	src := writeSum(t, "dec.py", `class Svc:
    @staticmethod
    def ping():
        return "pong"
`)
	s, err := SummarizePy(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Classes) != 1 || len(s.Classes[0].Methods) != 1 {
		t.Fatalf("summary = %+v", s)
	}
	if len(s.Classes[0].Methods[0].Decorators) != 1 || s.Classes[0].Methods[0].Decorators[0] != "@staticmethod" {
		t.Errorf("decorators = %v", s.Classes[0].Methods[0].Decorators)
	}
}

func TestParsePyDef(t *testing.T) {
	name, params := parsePyDef("def foo(a: int, b: str) -> bool:", "def ")
	if name != "foo" || params != "(a: int, b: str)" {
		t.Errorf("parsePyDef = %q, %q", name, params)
	}
	name, params = parsePyDef("class Client(BaseClient):", "class ")
	if name != "Client" || params != "(BaseClient)" {
		t.Errorf("parsePyDef class = %q, %q", name, params)
	}
	name, params = parsePyDef("def noargs():", "def ")
	if name != "noargs" || params != "()" {
		t.Errorf("parsePyDef noargs = %q, %q", name, params)
	}
}
