package astsum

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// PyFunc is one Python function or method signature.
type PyFunc struct {
	Name       string   `json:"name"`
	Params     string   `json:"params"`
	Decorators []string `json:"decorators,omitempty"`
}

// PyClass is one top-level class with its methods.
type PyClass struct {
	Name       string   `json:"name"`
	Bases      string   `json:"bases,omitempty"`
	Decorators []string `json:"decorators,omitempty"`
	Methods    []PyFunc `json:"methods,omitempty"`
}

// PySummary is the structural shape of a Python file.
type PySummary struct {
	Classes []PyClass `json:"classes,omitempty"`
	Funcs   []PyFunc  `json:"funcs,omitempty"`
}

// SummarizePy scans a Python file with a pure-Go top-level scanner. It
// deliberately extracts only top-level class and def signatures (with their
// decorators); nested functions, bodies and multi-line expressions are
// ignored, which keeps the summary small and the parser dependency-free.
func SummarizePy(path string) (*PySummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("astsum: open %s: %w", path, err)
	}
	defer f.Close()

	s := &PySummary{}
	var cur *PyClass // current top-level class, for method attribution
	var pending []string

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimLeft(line, " \t")
		indent := len(line) - len(trimmed)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		switch {
		case strings.HasPrefix(trimmed, "@"):
			pending = append(pending, strings.TrimSpace(trimmed))
			continue
		case indent == 0 && strings.HasPrefix(trimmed, "class "):
			name, bases := parsePyDef(trimmed, "class ")
			cur = &PyClass{Name: name, Bases: bases, Decorators: pending}
			s.Classes = append(s.Classes, *cur)
			cur = &s.Classes[len(s.Classes)-1]
			pending = nil
		case indent == 0 && strings.HasPrefix(trimmed, "def "):
			name, params := parsePyDef(trimmed, "def ")
			s.Funcs = append(s.Funcs, PyFunc{Name: name, Params: params, Decorators: pending})
			pending = nil
		case indent > 0 && cur != nil && strings.HasPrefix(trimmed, "def "):
			name, params := parsePyDef(trimmed, "def ")
			cur.Methods = append(cur.Methods, PyFunc{Name: name, Params: params, Decorators: pending})
			pending = nil
		case indent == 0 && pending != nil:
			// A decorator with nothing after it (e.g. module-level @overload
			// before something non-def); drop it.
			pending = nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("astsum: read %s: %w", path, err)
	}
	return s, nil
}

// parsePyDef splits "def name(params):" / "class Name(Base):". Params are
// taken to the closing paren on the same line, or elided with "..." when the
// signature spans lines (rare in practice; the summary stays small either
// way).
func parsePyDef(line, prefix string) (name, params string) {
	body := strings.TrimPrefix(line, prefix)
	if open := strings.IndexByte(body, '('); open >= 0 {
		name = strings.TrimSpace(body[:open])
		if close := strings.IndexByte(body[open:], ')'); close >= 0 {
			params = body[open : open+close+1]
		} else {
			params = body[open:] + "..."
		}
		return name, params
	}
	if colon := strings.IndexByte(body, ':'); colon >= 0 {
		name = strings.TrimSpace(body[:colon])
	} else {
		name = strings.TrimSpace(body)
	}
	return name, ""
}
