package main

import (
	"encoding/json"
	"testing"
)

// list --json emits the full machine-readable projection, ordered bottom -> top.
func TestListJSON(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	t.Chdir(r.dir)

	out, _ := capture(t)
	o := opts()
	o.jsonOut = true
	if err := cmdList("feature/c", o); err != nil {
		t.Fatal(err)
	}

	var got []branchJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("list --json did not parse: %v\n%s", err, out.String())
	}

	want := []struct {
		branch, subject string
		depth           int
	}{
		{"feature/a", "commit a", 1},
		{"feature/b", "commit b", 2},
		{"feature/c", "commit c", 3},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Branch != w.branch || g.Subject != w.subject || g.CommitsAboveTrunk != w.depth {
			t.Errorf("entry %d = %+v, want branch=%s subject=%q depth=%d", i, g, w.branch, w.subject, w.depth)
		}
		if len(g.SHA) != 40 {
			t.Errorf("entry %d sha = %q, want a 40-char object name", i, g.SHA)
		}
	}
}

// A commit subject containing spaces survives the tab-delimited for-each-ref
// round-trip intact.
func TestListJSONSubjectWithSpaces(t *testing.T) {
	r := newRepo(t)
	r.writeCommit("base.txt", "base", "base commit")
	r.switchNew("feature/a")
	r.writeCommit("a.txt", "a", "commit a: with spaces and: punctuation")
	r.git("push", "origin", "master", "feature/a")
	t.Chdir(r.dir)

	out, _ := capture(t)
	o := opts()
	o.jsonOut = true
	if err := cmdList("feature/a", o); err != nil {
		t.Fatal(err)
	}
	var got []branchJSON
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 1 || got[0].Subject != "commit a: with spaces and: punctuation" {
		t.Fatalf("subject not preserved: %+v", got)
	}
}
