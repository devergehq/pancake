package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigParsing(t *testing.T) {
	dir := t.TempDir()
	body := "# pancake defaults\ntrunk = origin/dev\n\nremote =  upstream \nbogus = whatever\n"
	if err := os.WriteFile(filepath.Join(dir, ".pancake"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	fc := loadConfig(dir)
	if fc.trunk != "origin/dev" {
		t.Errorf("trunk = %q, want origin/dev", fc.trunk)
	}
	if fc.remote != "upstream" {
		t.Errorf("remote = %q, want upstream (trimmed)", fc.remote)
	}
	if fc.path == "" {
		t.Error("path should be set when the file exists")
	}
	if len(fc.unknown) != 1 || fc.unknown[0] != "bogus" {
		t.Errorf("unknown keys = %v, want [bogus]", fc.unknown)
	}
}

func TestLoadConfigMissing(t *testing.T) {
	fc := loadConfig(t.TempDir())
	if fc.path != "" || fc.trunk != "" || fc.remote != "" {
		t.Errorf("missing config should be empty, got %+v", fc)
	}
}

func TestLoadConfigEmptyDir(t *testing.T) {
	if fc := loadConfig(""); fc.path != "" {
		t.Errorf("empty dir should yield no config, got %+v", fc)
	}
}
