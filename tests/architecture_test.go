package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/Cheikh-Nakamoto/Kergui-Fibre-Manager"

// TestDependencyRule_DomainIsPure asserts the innermost ring imports no outer
// package of this module (Clean Architecture Dependency Rule).
func TestDependencyRule_DomainIsPure(t *testing.T) {
	for _, dep := range transitiveDeps(t, "./internal/domain") {
		if internalPkg(dep) && !strings.HasPrefix(dep, modulePath+"/internal/domain") {
			t.Errorf("domain must not depend on outer package %s", dep)
		}
	}
}

// TestDependencyRule_UsecaseStaysInner asserts the use-case layer depends only on
// the domain and its own ports — never on a concrete adapter or framework.
func TestDependencyRule_UsecaseStaysInner(t *testing.T) {
	allowed := []string{
		modulePath + "/internal/usecase", // includes usecase/port
		modulePath + "/internal/domain",
	}
	for _, dep := range transitiveDeps(t, "./internal/usecase") {
		if !internalPkg(dep) {
			continue
		}
		ok := false
		for _, a := range allowed {
			if strings.HasPrefix(dep, a) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("use-case layer must not depend on %s", dep)
		}
	}
}

func internalPkg(p string) bool { return strings.HasPrefix(p, modulePath+"/internal/") }

func transitiveDeps(t *testing.T, pkg string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	return strings.Fields(string(out))
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
