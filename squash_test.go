package main

import (
	"strings"
	"testing"
)

// buildMultiCommitStack: master <- feature/a (TWO commits) <- feature/b <- feature/c.
// feature/a is deliberately multi-commit — the case that breaks a naive restack
// when it gets squash-merged.
func (r *repo) buildMultiCommitStack() {
	r.writeCommit("base.txt", "base", "base commit")
	r.switchNew("feature/a")
	// Both commits touch the SAME file (create, then modify). A squash collapses
	// them to the final content, so replaying a1's intermediate content onto the
	// squash is a genuine add/add conflict — the case that actually breaks.
	r.writeCommit("shared.txt", "v1", "commit a1")
	r.writeCommit("shared.txt", "v2", "commit a2")
	r.switchNew("feature/b")
	r.writeCommit("b.txt", "b", "commit b")
	r.switchNew("feature/c")
	r.writeCommit("c.txt", "c", "commit c")
	r.git("push", "origin", "master", "feature/a", "feature/b", "feature/c")
}

// The exact workflow the user hits: a MULTI-commit branch is squash-merged, and
// you sync on the machine you merged from (its local ref survives the remote
// prune). The restack must start ABOVE the squashed commits — no add/add
// conflict. Without boundary detection this errors (rebase conflict).
func TestSyncMultiCommitSquashLocalRef(t *testing.T) {
	r := newRepo(t)
	r.buildMultiCommitStack()
	r.squashMergeToMaster("feature/a") // a1+a2 -> one squash commit on master; local feature/a remains
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync must not conflict on a multi-commit squash: %v", err)
	}
	if n := r.git("rev-list", "--count", "origin/master..feature/c"); n != "2" {
		t.Fatalf("commits above trunk = %s, want 2 (a1/a2 skipped, not replayed)", n)
	}
	subjects := r.git("log", "--format=%s", "origin/master..feature/c")
	for _, gone := range []string{"commit a1", "commit a2"} {
		if strings.Contains(subjects, gone) {
			t.Fatalf("a squashed commit was replayed:\n%s", subjects)
		}
	}
	for _, kept := range []string{"commit b", "commit c"} {
		if !strings.Contains(subjects, kept) {
			t.Fatalf("missing %q after restack:\n%s", kept, subjects)
		}
	}
}

// The stateless case (DEV-243 acceptance): same multi-commit squash, but the
// local ref is GONE (fresh / ephemeral clone). patch-id boundary detection must
// still find the boundary — no conflict, no --from needed.
func TestSyncMultiCommitSquashFreshClonePatchID(t *testing.T) {
	r := newRepo(t)
	r.buildMultiCommitStack()
	r.squashMergeToMaster("feature/a")
	r.git("branch", "-D", "feature/a") // simulate a fresh clone: no local ref survives
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync must not conflict via patch-id boundary: %v", err)
	}
	if n := r.git("rev-list", "--count", "origin/master..feature/c"); n != "2" {
		t.Fatalf("commits above trunk = %s, want 2 (a1/a2 skipped via patch-id)", n)
	}
}

// --from names the boundary explicitly and bypasses detection (the escape hatch
// for when neither the local ref nor a clean patch-id match is available).
func TestSyncFromFlag(t *testing.T) {
	r := newRepo(t)
	r.buildMultiCommitStack()
	aTip := r.git("rev-parse", "feature/a") // the squash-merged branch's pre-merge tip
	r.squashMergeToMaster("feature/a")
	r.git("branch", "-D", "feature/a")
	t.Chdir(r.dir)

	o := opts()
	o.from = aTip
	if err := cmdSync("feature/c", o); err != nil {
		t.Fatalf("sync --from failed: %v", err)
	}
	if n := r.git("rev-list", "--count", "origin/master..feature/c"); n != "2" {
		t.Fatalf("commits above trunk = %s, want 2", n)
	}
}
