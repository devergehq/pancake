package main

import (
	"bytes"
	"strings"
	"testing"
)

func opts() options { return options{trunk: defaultTrunk, remote: defaultRemote} }

// capture redirects the package output streams to buffers for the duration of
// the test, restoring them on cleanup.
func capture(t *testing.T) (out, err *bytes.Buffer) {
	t.Helper()
	out, err = &bytes.Buffer{}, &bytes.Buffer{}
	so, se := stdout, stderr
	stdout, stderr = out, err
	t.Cleanup(func() { stdout, stderr = so, se })
	return out, err
}

func branchNames(bs []branch) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.name
	}
	return out
}

func mustAncestor(t *testing.T, r *repo, ancestor, descendant string) {
	t.Helper()
	if _, err := r.tryGit("merge-base", "--is-ancestor", ancestor, descendant); err != nil {
		t.Fatalf("%s is not an ancestor of %s", ancestor, descendant)
	}
}

// discover: the stack is the origin branches above trunk, ordered bottom -> top.
func TestStackOrderBottomToTop(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	bs, err := stack("feature/c", opts())
	if err != nil {
		t.Fatal(err)
	}
	got := branchNames(bs)
	want := []string{"feature/a", "feature/b", "feature/c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stack order = %v, want %v", got, want)
	}
}

// list prints the stack names, one per line, bottom -> top.
func TestListPrintsNames(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	out, _ := capture(t)
	if err := cmdList("feature/c", opts()); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "feature/a\nfeature/b\nfeature/c\n"; got != want {
		t.Fatalf("list output = %q, want %q", got, want)
	}
}

// discover: a branch that has been merged into trunk and pruned from origin
// disappears from the stack — it is never named.
func TestDiscoverExcludesMergedBranch(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	r.squashMergeToMaster("feature/a")
	t.Chdir(r.dir)

	bs, err := stack("feature/c", opts())
	if err != nil {
		t.Fatal(err)
	}
	got := branchNames(bs)
	want := []string{"feature/b", "feature/c"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("stack after merge = %v, want %v", got, want)
	}
}

// sync: after the bottom PR squash-merges into trunk, restacking drops its
// commit as an empty replay — the range above trunk holds only b and c.
func TestSyncDropsMergedCommit(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	r.squashMergeToMaster("feature/a")
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if count := r.git("rev-list", "--count", "origin/master..feature/c"); count != "2" {
		t.Fatalf("commits above trunk = %s, want 2 (merged commit a dropped)", count)
	}
	subjects := r.git("log", "--format=%s", "origin/master..feature/c")
	if strings.Contains(subjects, "commit a") {
		t.Fatalf("merged commit a still present in stack:\n%s", subjects)
	}
	for _, want := range []string{"commit b", "commit c"} {
		if !strings.Contains(subjects, want) {
			t.Fatalf("missing %q after restack:\n%s", want, subjects)
		}
	}
}

// sync: --update-refs carries every branch in the stack to its new position on
// top of the advanced trunk.
func TestSyncUpdateRefsMovesBranches(t *testing.T) {
	r := newRepo(t)
	r.buildStack()

	before := map[string]string{}
	for _, b := range []string{"feature/a", "feature/b", "feature/c"} {
		before[b] = r.git("rev-parse", b)
	}

	// Advance trunk with an unrelated commit and publish it.
	r.switchTo("master")
	r.writeCommit("m.txt", "m", "master moved")
	r.git("push", "origin", "master")
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	for _, b := range []string{"feature/a", "feature/b", "feature/c"} {
		if now := r.git("rev-parse", b); now == before[b] {
			t.Fatalf("%s did not move under --update-refs (still %s)", b, now)
		}
		mustAncestor(t, r, "origin/master", b)
	}
}

// submit: force-push updates every branch of the stack on the remote to match
// the local (restacked) tips.
func TestSubmitForcePushesStack(t *testing.T) {
	r := newRepo(t)
	r.buildStack()

	// Advance trunk and restack so local tips diverge from origin.
	r.switchTo("master")
	r.writeCommit("m.txt", "m", "master moved")
	r.git("push", "origin", "master")
	t.Chdir(r.dir)

	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := cmdSubmit("feature/c", opts()); err != nil {
		t.Fatalf("submit: %v", err)
	}

	r.git("fetch", "origin")
	for _, b := range []string{"feature/a", "feature/b", "feature/c"} {
		local := r.git("rev-parse", b)
		remote := r.git("rev-parse", "origin/"+b)
		if local != remote {
			t.Fatalf("%s: origin=%s local=%s (force-push did not land)", b, remote, local)
		}
	}
}

// --dry-run mutates nothing: neither local branches nor the remote change, and
// the intended git commands are announced.
func TestDryRunNoMutation(t *testing.T) {
	r := newRepo(t)
	r.buildStack()

	// Advance trunk so a real sync would rewrite the stack.
	r.switchTo("master")
	r.writeCommit("m.txt", "m", "master moved")
	r.git("push", "origin", "master")

	refs := []string{
		"feature/a", "feature/b", "feature/c",
		"origin/feature/a", "origin/feature/b", "origin/feature/c",
	}
	snap := map[string]string{}
	for _, ref := range refs {
		snap[ref] = r.git("rev-parse", ref)
	}
	t.Chdir(r.dir)

	o := opts()
	o.dryRun = true
	_, errBuf := capture(t)
	if err := cmdSync("feature/c", o); err != nil {
		t.Fatalf("dry-run sync: %v", err)
	}
	if err := cmdSubmit("feature/c", o); err != nil {
		t.Fatalf("dry-run submit: %v", err)
	}

	if !strings.Contains(errBuf.String(), "DRY-RUN") {
		t.Fatalf("expected DRY-RUN notices, got:\n%s", errBuf.String())
	}
	for _, ref := range refs {
		if got := r.git("rev-parse", ref); got != snap[ref] {
			t.Fatalf("%s mutated under --dry-run: %s -> %s", ref, snap[ref], got)
		}
	}
}
