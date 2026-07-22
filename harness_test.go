package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain pins a hermetic git identity and isolates the tests from the
// developer's real ~/.gitconfig, so runs are reproducible on any machine.
func TestMain(m *testing.M) {
	env := map[string]string{
		"GIT_AUTHOR_NAME":     "pancake-test",
		"GIT_AUTHOR_EMAIL":    "test@pancake.local",
		"GIT_COMMITTER_NAME":  "pancake-test",
		"GIT_COMMITTER_EMAIL": "test@pancake.local",
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_SYSTEM":   os.DevNull,
	}
	for k, v := range env {
		os.Setenv(k, v)
	}
	os.Exit(m.Run())
}

// repo is a throwaway git repo pair: a bare origin plus a working clone. The
// tool under test operates entirely on origin/* remote-tracking refs, so a real
// remote is mandatory — not a convenience.
type repo struct {
	t      *testing.T
	dir    string // working clone; git commands run here
	origin string // bare remote path
}

// newRepo builds a fresh bare origin (default branch master) and clones it.
func newRepo(t *testing.T) *repo {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")
	mustGit(t, root, "init", "--bare", "-b", "master", origin)
	mustGit(t, root, "clone", origin, work)
	return &repo{t: t, dir: work, origin: origin}
}

func tryGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := tryGit(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// git runs a git command in the working clone and fails the test on error.
func (r *repo) git(args ...string) string { return mustGit(r.t, r.dir, args...) }

// tryGit runs a git command in the working clone and returns its exit status,
// for probes like `merge-base --is-ancestor` where non-zero is a valid answer.
func (r *repo) tryGit(args ...string) (string, error) { return tryGit(r.dir, args...) }

func (r *repo) switchNew(name string) { r.git("switch", "-c", name) }
func (r *repo) switchTo(name string)  { r.git("switch", name) }

// writeCommit writes file and commits it, returning the new commit sha.
func (r *repo) writeCommit(file, body, msg string) string {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, file), []byte(body+"\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.git("add", file)
	r.git("commit", "-m", msg)
	return r.git("rev-parse", "HEAD")
}

// buildStack lays down the canonical three-branch stack, one commit per branch:
//
//	master ── base
//	           └─ feature/a ── commit a
//	                            └─ feature/b ── commit b
//	                                            └─ feature/c ── commit c
//
// Everything is pushed to origin and HEAD is left on feature/c.
func (r *repo) buildStack() {
	r.writeCommit("base.txt", "base", "base commit")
	r.switchNew("feature/a")
	r.writeCommit("a.txt", "a", "commit a")
	r.switchNew("feature/b")
	r.writeCommit("b.txt", "b", "commit b")
	r.switchNew("feature/c")
	r.writeCommit("c.txt", "c", "commit c")
	r.git("push", "origin", "master", "feature/a", "feature/b", "feature/c")
}

// squashMergeToMaster simulates a squash-merge of branch into trunk on the
// remote: it lands a single commit on master carrying branch's exact diff (so
// its patch-id matches the original and a later rebase will drop it), deletes
// the branch from origin, and prunes. This is the "merged base" fixture.
func (r *repo) squashMergeToMaster(branch string) {
	r.switchTo("master")
	r.git("merge", "--squash", branch)
	r.git("commit", "-m", "squash merge "+branch)
	r.git("push", "origin", "master")
	r.git("push", "origin", "--delete", branch)
	r.git("fetch", "--prune", "origin")
}
