package testloc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocateGoUsesSiblingTestFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "widget.go")
	write(t, src, `package widget

func New() *Widget { return &Widget{} }
`)
	// The sibling test file's actual test names drive the -run filter.
	write(t, filepath.Join(dir, "widget_test.go"), `package widget

func TestNewWidget(t *testing.T) {}
func ExampleNewWidget() {}
`)
	res, err := Locate(src)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindGo || res.Dir != dir {
		t.Fatalf("res = %+v", res)
	}
	if res.TestPattern != "^(?:TestNewWidget|ExampleNewWidget)$" {
		t.Errorf("TestPattern = %q, want the sibling test file's actual test names", res.TestPattern)
	}
}

func TestLocateGoNoSiblingRunsWholePackage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	write(t, src, "package main\n\nfunc main() {}\n")
	res, err := Locate(src)
	if err != nil {
		t.Fatal(err)
	}
	if res.TestPattern != "" {
		t.Errorf("no sibling _test.go should mean no -run filter, got %q", res.TestPattern)
	}
}

func TestLocateGoTestFileNarrowsToItsTests(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "widget_test.go")
	write(t, src, `package widget

func TestNew(t *testing.T) {}
func ExampleNew() {}
func helper() {}
`)
	res, err := Locate(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.TestPattern, "TestNew") || !strings.Contains(res.TestPattern, "ExampleNew") {
		t.Errorf("test file should narrow to its own tests, got %q", res.TestPattern)
	}
	if strings.Contains(res.TestPattern, "helper") {
		t.Errorf("non-test helper leaked into pattern: %q", res.TestPattern)
	}
}

func TestLocateGoUnparsable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "broken.go")
	write(t, src, "package broken\n\nfunc {")
	if _, err := Locate(src); err == nil {
		t.Error("unparsable Go should error")
	}
}

func TestLocatePySiblingTestFile(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "test_util.py"), "def test_x(): pass\n")
	res, err := Locate(filepath.Join(dir, "util.py"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != KindPython || filepath.Base(res.TestFile) != "test_util.py" {
		t.Fatalf("res = %+v", res)
	}
}

func TestLocatePyTestsSubdir(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "tests", "test_api.py"), "def test_x(): pass\n")
	res, err := Locate(filepath.Join(dir, "api.py"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(res.TestFile) != "test_api.py" || !strings.Contains(res.TestFile, "tests") {
		t.Fatalf("res = %+v", res)
	}
}

func TestLocatePySelfIsTest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "test_thing.py")
	write(t, src, "def test_x(): pass\n")
	res, err := Locate(src)
	if err != nil {
		t.Fatal(err)
	}
	if res.TestFile != src {
		t.Fatalf("a test file should run itself, got %q", res.TestFile)
	}
}

func TestLocatePyMissing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "util.py"), "x = 1\n")
	if _, err := Locate(filepath.Join(dir, "util.py")); err == nil {
		t.Error("no test file should error")
	}
}

func TestLocateUnsupportedExt(t *testing.T) {
	if _, err := Locate("x.ts"); err == nil {
		t.Error("non .go/.py should error")
	}
}
