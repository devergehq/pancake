package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallShims(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "pancake")
	if err := os.WriteFile(self, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	capture(t)
	if err := installShims(self, dir, opts()); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, n := range shimNames {
		target, err := os.Readlink(filepath.Join(dir, n))
		if err != nil {
			t.Fatalf("readlink %s: %v", n, err)
		}
		if target != self {
			t.Errorf("%s -> %s, want %s", n, target, self)
		}
	}
	// Idempotent: a second install replaces the links without error.
	if err := installShims(self, dir, opts()); err != nil {
		t.Fatalf("re-install should be idempotent: %v", err)
	}
}

func TestInstallShimsDryRun(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "pancake")
	if err := os.WriteFile(self, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	o := opts()
	o.dryRun = true
	capture(t)
	if err := installShims(self, dir, o); err != nil {
		t.Fatalf("dry-run install: %v", err)
	}
	for _, n := range shimNames {
		if _, err := os.Lstat(filepath.Join(dir, n)); !os.IsNotExist(err) {
			t.Errorf("dry-run should not create %s", n)
		}
	}
}
