package main

import (
	"strings"
	"testing"
)

// A branch with MULTIPLE commits, sitting in the middle of the stack (not
// merged), must keep all of its commits through a segmented restack. This is the
// case the user flagged: don't rebase each branch assuming a single commit.
func TestSyncSegmentedPreservesMultiCommitBranch(t *testing.T) {
	r := newRepo(t)
	r.writeCommit("base.txt", "base", "base commit")
	r.switchNew("feature/a")
	r.writeCommit("a.txt", "a", "commit a")
	r.switchNew("feature/b")
	r.writeCommit("b1.txt", "b1", "commit b1")
	r.writeCommit("b2.txt", "b2", "commit b2") // feature/b has TWO commits
	r.switchNew("feature/c")
	r.writeCommit("c.txt", "c", "commit c")
	r.git("push", "origin", "master", "feature/a", "feature/b", "feature/c")

	// Advance trunk so the restack actually replays commits.
	r.switchTo("master")
	r.writeCommit("m.txt", "m", "master moved")
	r.git("push", "origin", "master")
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	// a + b1 + b2 + c = 4 commits above the new trunk — none lost, none duplicated.
	if n := r.git("rev-list", "--count", "origin/master..feature/c"); n != "4" {
		t.Fatalf("commits above trunk = %s, want 4 (multi-commit branch preserved)", n)
	}
	subjects := r.git("log", "--format=%s", "origin/master..feature/c")
	for _, want := range []string{"commit a", "commit b1", "commit b2", "commit c"} {
		if !strings.Contains(subjects, want) {
			t.Fatalf("missing %q — a commit was lost in the segmented restack:\n%s", want, subjects)
		}
	}
	for _, b := range []string{"feature/a", "feature/b", "feature/c"} {
		if _, err := r.tryGit("merge-base", "--is-ancestor", "origin/master", b); err != nil {
			t.Errorf("%s not restacked onto the new trunk", b)
		}
	}
	// feature/b still owns exactly its two commits above feature/a.
	if n := r.git("rev-list", "--count", "feature/a..feature/b"); n != "2" {
		t.Errorf("feature/b has %s commits above feature/a, want 2", n)
	}
}
