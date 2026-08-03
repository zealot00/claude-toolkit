// Package testloc maps a source file to the incremental tests that cover it.
// It is pure logic with no I/O beyond reading the source file, so the mapping
// rules are unit-testable without a Go or Python toolchain.
package testloc

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// Kind is the toolchain a located test belongs to.
type Kind int

const (
	// KindGo runs `go test -count=1` in the package directory.
	KindGo Kind = iota
	// KindPython runs pytest against a located test file.
	KindPython
)

// Result describes how to run the incremental tests for a source file.
type Result struct {
	Kind Kind
	// Dir is the directory `go test` runs in (KindGo).
	Dir string
	// TestPattern is the -run regex narrowing which tests run; "" runs the
	// whole package.
	TestPattern string
	// TestFile is the pytest target (KindPython); a directory runs the whole
	// test directory.
	TestFile string
}

// Locate finds the incremental test target for src. It returns an error for
// file types with no test story or when no Python test file exists.
func Locate(src string) (Result, error) {
	switch strings.ToLower(filepath.Ext(src)) {
	case ".go":
		return locateGo(src)
	case ".py":
		return locatePy(src)
	default:
		return Result{}, fmt.Errorf("testloc: %s is not a .go or .py file; no incremental test story", src)
	}
}

// locateGo maps a .go file to `go test` in its package directory. The -run
// filter is derived from the actual test functions in the sibling
// <base>_test.go (or, for a _test.go input, its own test functions), because
// Go test names are descriptive (TestCreateWidget) and cannot be inferred
// from the source's exported names. With no sibling test file the whole
// package runs.
func locateGo(src string) (Result, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, 0)
	if err != nil {
		return Result{}, fmt.Errorf("testloc: parse %s: %w", src, err)
	}

	var names []string
	if strings.HasSuffix(src, "_test.go") {
		names = testNamesIn(f)
	} else if sibling := strings.TrimSuffix(src, ".go") + "_test.go"; fileExists(sibling) {
		if tf, terr := parser.ParseFile(token.NewFileSet(), sibling, nil, 0); terr == nil {
			names = testNamesIn(tf)
		}
	}

	pat := ""
	if len(names) > 0 {
		pat = "^(?:" + strings.Join(names, "|") + ")$"
	}
	return Result{Kind: KindGo, Dir: filepath.Dir(src), TestPattern: pat}, nil
}

// testNamesIn collects the package-level Test*/Example* functions a test file
// defines.
func testNamesIn(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv != nil {
			continue
		}
		if strings.HasPrefix(fd.Name.Name, "Test") || strings.HasPrefix(fd.Name.Name, "Example") {
			names = append(names, fd.Name.Name)
		}
	}
	return names
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// locatePy maps a .py file to its pytest target: test_<base>.py,
// tests/test_<base>.py, or the test_<base> directory, in that order. A file
// that is itself a test (test_*.py / *_test.py) runs directly.
func locatePy(src string) (Result, error) {
	base := filepath.Base(src)
	stem := strings.TrimSuffix(base, ".py")
	if strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test") {
		return Result{Kind: KindPython, TestFile: src}, nil
	}

	dir := filepath.Dir(src)
	for _, c := range []string{
		filepath.Join(dir, "test_"+stem+".py"),
		filepath.Join(dir, "tests", "test_"+stem+".py"),
		filepath.Join(dir, "test_"+stem),
	} {
		if st, err := os.Stat(c); err == nil {
			if st.IsDir() {
				return Result{Kind: KindPython, TestFile: c}, nil
			}
			return Result{Kind: KindPython, TestFile: c}, nil
		}
	}
	return Result{}, fmt.Errorf(
		"testloc: no test file for %s (looked for test_%s.py and tests/test_%s.py)", src, stem, stem)
}
