package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedFileCompiles(t *testing.T) {
	source, err := generateGoCode([]string{
		"type Counter ads.Int16",
		"type Clock = time.Time",
		"type Delay = time.Duration",
		"type Matrix [2][3]ads.Int16",
		"type Value struct { Items [2]struct { Value ads.Float32 }; Clock time.Time }",
	}, "generated")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(cwd, "../.."))
	module := "module generated\n\ngo 1.26\nrequire github.com/fluxin/go-native-ads v0.1.0\nreplace github.com/fluxin/go-native-ads => " + filepath.ToSlash(root) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", "test", "./...")
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated package: %v\n%s\n%s", err, output, source)
	}
}
func TestGenerateGoCodeRejectsInvalidNames(t *testing.T) {
	for _, defs := range [][]string{{"type X int16", "type X int32"}, {"type X struct { A int; A int }"}} {
		if _, err := generateGoCode(defs, "generated"); err == nil {
			t.Fatal("accepted name collision")
		}
	}
	if _, err := generateGoCode(nil, "package"); err == nil {
		t.Fatal("accepted keyword package")
	}
}
func TestGenerateGoCodeDeterministic(t *testing.T) {
	defs := []string{"type Value struct { N ads.Int16; T time.Time }"}
	a, err := generateGoCode(defs, "generated")
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		b, err := generateGoCode(defs, "generated")
		if err != nil || a != b {
			t.Fatal("unstable generated output")
		}
	}
	if !strings.Contains(a, "import ads") || !strings.Contains(a, "import time") {
		t.Fatal(a)
	}
}
