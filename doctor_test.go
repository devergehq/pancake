package main

import (
	"strings"
	"testing"
)

func stubRepoConfig(t *testing.T, cfg repoConfig, err error) {
	t.Helper()
	prev := fetchRepoConfig
	fetchRepoConfig = func(options) (repoConfig, error) { return cfg, err }
	t.Cleanup(func() { fetchRepoConfig = prev })
}

func goodConfig() repoConfig {
	return repoConfig{NameWithOwner: "acme/widget", DeleteBranchOnMerge: true, DefaultBranch: "master"}
}

func TestDoctorAllGood(t *testing.T) {
	stubRepoConfig(t, goodConfig(), nil)
	out, _ := capture(t)
	if err := cmdDoctor(opts()); err != nil {
		t.Fatalf("doctor should pass: %v", err)
	}
	if !strings.Contains(out.String(), "✓") || strings.Contains(out.String(), "✗") {
		t.Errorf("expected all-pass output:\n%s", out.String())
	}
}

func TestDoctorFailsWhenAutoDeleteOff(t *testing.T) {
	cfg := goodConfig()
	cfg.DeleteBranchOnMerge = false
	stubRepoConfig(t, cfg, nil)

	out, _ := capture(t)
	if err := cmdDoctor(opts()); err == nil {
		t.Fatal("doctor should fail when delete_branch_on_merge is off")
	}
	if !strings.Contains(out.String(), "✗") || !strings.Contains(out.String(), "auto-delete") {
		t.Errorf("expected a failing auto-delete check:\n%s", out.String())
	}
}

func TestDoctorFixEnablesSetting(t *testing.T) {
	cfg := goodConfig()
	cfg.DeleteBranchOnMerge = false
	stubRepoConfig(t, cfg, nil)

	called := false
	prev := enableAutoDelete
	enableAutoDelete = func(options, repoConfig) error { called = true; return nil }
	t.Cleanup(func() { enableAutoDelete = prev })

	o := opts()
	o.fix = true
	capture(t)
	if err := cmdDoctor(o); err != nil {
		t.Fatalf("doctor --fix should pass after enabling: %v", err)
	}
	if !called {
		t.Error("doctor --fix did not call enableAutoDelete")
	}
}

func TestDoctorTrunkMismatch(t *testing.T) {
	cfg := goodConfig()
	cfg.DefaultBranch = "main" // repo default is main, but trunk is origin/master
	stubRepoConfig(t, cfg, nil)

	out, _ := capture(t)
	if err := cmdDoctor(opts()); err == nil {
		t.Fatal("doctor should flag a trunk/default-branch mismatch")
	}
	if !strings.Contains(out.String(), "default=main") {
		t.Errorf("expected the mismatch detail:\n%s", out.String())
	}
}

func TestDoctorErrorsWithoutGh(t *testing.T) {
	stubRepoConfig(t, repoConfig{}, errStub)
	capture(t)
	if err := cmdDoctor(opts()); err == nil {
		t.Fatal("doctor should error when repo config is unreadable")
	}
}

// submit warns (but does not fail) when the repo won't auto-retarget the stack.
func TestSubmitWarnsWhenAutoDeleteOff(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	cfg := goodConfig()
	cfg.DeleteBranchOnMerge = false
	stubRepoConfig(t, cfg, nil)

	_, errBuf := capture(t)
	if err := cmdSubmit("feature/c", opts()); err != nil {
		t.Fatalf("submit should still succeed: %v", err)
	}
	if !strings.Contains(errBuf.String(), "auto-delete head branches OFF") {
		t.Errorf("expected submit to warn about the setting:\n%s", errBuf.String())
	}
}
