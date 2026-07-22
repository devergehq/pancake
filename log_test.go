package main

import (
	"strings"
	"testing"
)

// stubPRs installs a fake fetchPRs for the test and restores it on cleanup.
func stubPRs(t *testing.T, fn func(o options) (map[string]prInfo, error)) {
	t.Helper()
	prev := fetchPRs
	fetchPRs = fn
	t.Cleanup(func() { fetchPRs = prev })
}

// log annotates each stack branch with its PR and marks a correctly-based stack.
func TestLogPRAnnotationsWellFormed(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	stubPRs(t, func(options) (map[string]prInfo, error) {
		return map[string]prInfo{
			"feature/a": {Number: 12, State: "OPEN", Base: "master", Head: "feature/a"},
			"feature/b": {Number: 13, State: "OPEN", Base: "feature/a", Head: "feature/b"},
			"feature/c": {Number: 14, State: "OPEN", Base: "feature/b", Head: "feature/c"},
		}, nil
	})

	out, _ := capture(t)
	if err := cmdLog("feature/c", opts()); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"#14", "#13", "#12", "base master", "base feature/a", "base feature/b"} {
		if !strings.Contains(s, want) {
			t.Errorf("log output missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "should be") {
		t.Errorf("well-formed stack should show no base warnings:\n%s", s)
	}
	// Top → bottom ordering: feature/c line precedes feature/a line.
	if strings.Index(s, "feature/c") > strings.Index(s, "feature/a") {
		t.Errorf("expected top→bottom order:\n%s", s)
	}
}

// A PR whose base is not the branch below it is flagged.
func TestLogFlagsWrongBase(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	stubPRs(t, func(options) (map[string]prInfo, error) {
		return map[string]prInfo{
			"feature/a": {Number: 12, State: "OPEN", Base: "master", Head: "feature/a"},
			"feature/b": {Number: 13, State: "OPEN", Base: "master", Head: "feature/b"}, // wrong: should be feature/a
			"feature/c": {Number: 14, State: "OPEN", Base: "feature/b", Head: "feature/c"},
		}, nil
	})

	out, _ := capture(t)
	if err := cmdLog("feature/c", opts()); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "should be feature/a") {
		t.Errorf("expected wrong-base warning for feature/b:\n%s", s)
	}
}

// A branch with no PR is shown as such, not skipped.
func TestLogMissingPR(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	stubPRs(t, func(options) (map[string]prInfo, error) {
		return map[string]prInfo{
			"feature/a": {Number: 12, State: "OPEN", Base: "master", Head: "feature/a"},
		}, nil
	})

	out, _ := capture(t)
	if err := cmdLog("feature/c", opts()); err != nil {
		t.Fatal(err)
	}
	if s := out.String(); !strings.Contains(s, "(no PR)") {
		t.Errorf("expected (no PR) for unlisted branches:\n%s", s)
	}
}

// When gh is unavailable the command degrades gracefully: it notes the reason
// and still succeeds (falling back to the plain git graph).
func TestLogGracefulDegradation(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	stubPRs(t, func(options) (map[string]prInfo, error) {
		return nil, errStub
	})

	out, errBuf := capture(t)
	if err := cmdLog("feature/c", opts()); err != nil {
		t.Fatalf("log should not fail when gh is unavailable: %v", err)
	}
	if !strings.Contains(errBuf.String(), "PR annotations unavailable") {
		t.Errorf("expected graceful-degradation note:\n%s", errBuf.String())
	}
	if strings.Contains(out.String(), "Stack PRs") {
		t.Errorf("should not print a PR table when gh failed:\n%s", out.String())
	}
}

type stubErr struct{}

func (stubErr) Error() string { return "gh not installed" }

var errStub = stubErr{}
