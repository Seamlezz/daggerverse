package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestMergeWkgLocksDeterministicUnion(t *testing.T) {
	clocks := `version = 1
[[packages]]
name = "wasi:clocks"
registry = "wasi.dev"
[[packages.versions]]
requirement = "=0.2.0"
version = "0.2.0"
digest = "sha256:clocks"
`
	http := `version = 1
[[packages]]
name = "wasi:http"
registry = "wasi.dev"
[[packages.versions]]
requirement = "=0.2.2"
version = "0.2.2"
digest = "sha256:http"
`
	first, err := mergeWkgLocks([]string{http, clocks})
	if err != nil {
		t.Fatal(err)
	}
	second, err := mergeWkgLocks([]string{clocks, http})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("merge depends on input order")
	}
	if !strings.Contains(first, "wasi:clocks") || !strings.Contains(first, "wasi:http") {
		t.Fatalf("merged lock does not contain union:\n%s", first)
	}
}

func TestMergeWkgLocksRejectsConflictingDigest(t *testing.T) {
	lock := `version = 1
[[packages]]
name = "wasi:http"
registry = "wasi.dev"
[[packages.versions]]
requirement = "=0.2.2"
version = "0.2.2"
digest = "sha256:first"
`
	conflict := strings.Replace(lock, "sha256:first", "sha256:second", 1)
	if _, err := mergeWkgLocks([]string{lock, conflict}); err == nil {
		t.Fatal("merge accepted conflicting digest")
	}
}

func TestResolveDirs(t *testing.T) {
	got, err := resolveDirs([]string{"backend/z", "/backend/a/", "backend/z"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"backend/a", "backend/z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveDirs() = %v, want %v", got, want)
	}
}

func TestResolveDirsRequiresComponent(t *testing.T) {
	if _, err := resolveDirs(nil); err == nil {
		t.Fatal("resolveDirs() returned nil error")
	}
}

func TestResolveDirsRejectsSourceRootAndTraversal(t *testing.T) {
	for _, componentDir := range []string{".", "/", "../backend/component"} {
		t.Run(componentDir, func(t *testing.T) {
			if _, err := resolveDirs([]string{componentDir}); err == nil {
				t.Fatalf("resolveDirs(%q) returned nil error", componentDir)
			}
		})
	}
}
