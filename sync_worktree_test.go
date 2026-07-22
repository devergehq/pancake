package main

import "testing"

// syncSetup builds the stack, advances trunk so sync does real work, and returns
// the repo. HEAD is left on master; each test then checks out the branch under
// test before syncing.
func syncSetup(t *testing.T) *repo {
	t.Helper()
	r := newRepo(t)
	r.buildStack()
	r.switchTo("master")
	r.writeCommit("m.txt", "m", "master moved")
	r.git("push", "origin", "master")
	return r
}

func assertRestacked(t *testing.T, r *repo) {
	t.Helper()
	for _, b := range []string{"feature/a", "feature/b", "feature/c"} {
		if _, err := r.tryGit("merge-base", "--is-ancestor", "origin/master", b); err != nil {
			t.Errorf("%s was not restacked onto the new trunk", b)
		}
	}
}

// Regression: sync while checked out ON the top branch must not fail trying to
// force-update the checked-out ref (git exit 128).
func TestSyncWhileOnTopBranch(t *testing.T) {
	r := syncSetup(t)
	r.switchTo("feature/c") // the top of the stack
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync while on top branch failed: %v", err)
	}
	assertRestacked(t, r)
}

// Sync while checked out on a mid-stack branch: switching to top up front frees
// the mid-stack ref so it can be force-updated.
func TestSyncWhileOnMidStackBranch(t *testing.T) {
	r := syncSetup(t)
	r.switchTo("feature/b") // middle of the stack
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync while on mid-stack branch failed: %v", err)
	}
	assertRestacked(t, r)
}

// Sync from trunk (outside the stack) still works — the original happy path.
func TestSyncFromTrunk(t *testing.T) {
	r := syncSetup(t) // already on master
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync from trunk failed: %v", err)
	}
	assertRestacked(t, r)
}
