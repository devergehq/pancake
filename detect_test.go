package main

import (
	"fmt"
	"strings"
	"testing"
)

// secondStack branches feature/x off master with one commit and pushes it,
// giving the repo two independent stacks: [a,b,c] and [x].
func (r *repo) secondStack() {
	r.switchTo("master")
	r.switchNew("feature/x")
	r.writeCommit("x.txt", "x", "commit x")
	r.git("push", "origin", "feature/x")
}

// A single stack has exactly one tip — the top branch.
func TestDetectTopSingleStack(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	got, err := detectTop(opts())
	if err != nil {
		t.Fatal(err)
	}
	if got != "feature/c" {
		t.Fatalf("detectTop = %q, want feature/c", got)
	}
}

// With multiple stacks, the current branch disambiguates: detect resolves to
// the tip of whichever stack HEAD sits in.
func TestDetectTopDisambiguatesByCurrentBranch(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	r.secondStack() // leaves HEAD on feature/x
	t.Chdir(r.dir)

	// On the x stack's tip.
	if got, err := detectTop(opts()); err != nil || got != "feature/x" {
		t.Fatalf("on feature/x: detectTop = %q, %v; want feature/x", got, err)
	}
	// Mid the other stack: resolves up to that stack's tip.
	r.switchTo("feature/b")
	if got, err := detectTop(opts()); err != nil || got != "feature/c" {
		t.Fatalf("on feature/b: detectTop = %q, %v; want feature/c", got, err)
	}
}

// Multiple stacks with HEAD on trunk (contained in all of them) is ambiguous —
// Detection is anchored on the current branch, so sitting on the trunk (with no
// stack branch checked out) can't be resolved — it errors clearly instead of
// guessing among unrelated stacks.
func TestDetectTopOnTrunkErrors(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	r.secondStack()
	r.switchTo("master")
	t.Chdir(r.dir)

	_, err := detectTop(opts())
	if err == nil {
		t.Fatal("expected an error when on the trunk, got nil")
	}
	if !strings.Contains(err.Error(), "trunk") {
		t.Errorf("error should mention the trunk: %v", err)
	}
}

// The whole point of DEV-245: detection must scale with the stack, not the repo.
// With many unrelated branches present, the number of merge-base subprocesses
// must stay bounded by the stack size — not fan out across every ref.
func TestDetectTopScalesWithStackNotRepo(t *testing.T) {
	r := newRepo(t)
	r.buildStack() // master <- a <- b <- c
	for i := 0; i < 40; i++ {
		r.switchTo("master")
		name := fmt.Sprintf("noise/b%02d", i)
		r.switchNew(name)
		r.writeCommit(fmt.Sprintf("noise%02d.txt", i), "x", "noise "+name)
		r.git("push", "origin", name)
	}
	r.switchTo("feature/b") // mid-stack, so the tip search actually runs
	t.Chdir(r.dir)

	tc := enableTrace(t, "text")
	capture(t)
	top, err := detectTop(opts())
	if err != nil || top != "feature/c" {
		t.Fatalf("detectTop = %q, %v; want feature/c", top, err)
	}
	mb := 0
	for _, c := range tc.calls {
		if strings.HasPrefix(c.Cmd, "merge-base") {
			mb++
		}
	}
	if mb > 6 {
		t.Errorf("detectTop made %d merge-base calls with 40+ noise branches; must be bounded by stack size", mb)
	}
}

// No branches above trunk -> a clear error, not a panic.
func TestDetectTopNoStack(t *testing.T) {
	r := newRepo(t)
	r.writeCommit("base.txt", "base", "base commit")
	r.git("push", "origin", "master")
	t.Chdir(r.dir)

	if _, err := detectTop(opts()); err == nil {
		t.Fatal("expected error when there is no stack, got nil")
	}
}
