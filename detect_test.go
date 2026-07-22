package main

import (
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
// detect refuses to guess and names the tips.
func TestDetectTopAmbiguousErrors(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	r.secondStack()
	r.switchTo("master")
	t.Chdir(r.dir)

	_, err := detectTop(opts())
	if err == nil {
		t.Fatal("expected ambiguity error on trunk, got nil")
	}
	for _, tip := range []string{"feature/c", "feature/x"} {
		if !strings.Contains(err.Error(), tip) {
			t.Errorf("error should name %q: %v", tip, err)
		}
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
