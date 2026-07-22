package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// enableTrace installs a package tracer for the test and removes it on cleanup.
func enableTrace(t *testing.T, mode string) *tracer {
	t.Helper()
	prev := tr
	tr = &tracer{mode: mode}
	t.Cleanup(func() { tr = prev })
	return tr
}

func TestResolveTraceMode(t *testing.T) {
	cases := []struct {
		flag, env, want string
	}{
		{"", "", ""},         // off
		{"text", "", "text"}, // flag wins
		{"json", "", "json"}, // flag wins
		{"", "1", "text"},    // env truthy -> text
		{"", "json", "json"}, // env json
		{"", "anything", "text"},
		{"json", "1", "json"}, // flag beats env
	}
	for _, c := range cases {
		if got := resolveTraceMode(c.flag, c.env); got != c.want {
			t.Errorf("resolveTraceMode(%q, %q) = %q, want %q", c.flag, c.env, got, c.want)
		}
	}
}

func TestTraceModeSet(t *testing.T) {
	var m traceMode
	for _, in := range []string{"", "true", "text"} {
		m = traceMode{}
		if err := m.Set(in); err != nil || m.s != "text" {
			t.Errorf("Set(%q) => (%q, %v), want text", in, m.s, err)
		}
	}
	m = traceMode{}
	if err := m.Set("json"); err != nil || m.s != "json" {
		t.Errorf("Set(json) => (%q, %v), want json", m.s, err)
	}
	m = traceMode{}
	if err := m.Set("bogus"); err == nil {
		t.Errorf("Set(bogus) should error, got mode %q", m.s)
	}
}

func TestTracerStatsSlowestPhase(t *testing.T) {
	tc := &tracer{calls: []traceCall{
		{Cmd: "rev-parse HEAD", MS: 5},
		{Cmd: "fetch --prune origin", MS: 900},
		{Cmd: "rebase --onto x y", MS: 400},
		{Cmd: "rev-parse HEAD~1", MS: 5},
	}}
	total, count, slow, slowMS := tc.stats()
	if total != 1310 || count != 4 {
		t.Fatalf("stats total/count = %d/%d, want 1310/4", total, count)
	}
	if slow != "fetch" || slowMS != 900 {
		t.Fatalf("slowest = %s (%dms), want fetch (900ms)", slow, slowMS)
	}
}

func TestTracerFinishJSON(t *testing.T) {
	tc := enableTrace(t, "json")
	tc.calls = []traceCall{
		{Cmd: "fetch --prune origin", MS: 900, OK: true, Mutating: true},
		{Cmd: "rev-parse HEAD", MS: 5, OK: true},
	}
	_, errBuf := capture(t)
	tc.finish()

	var got struct {
		TotalMS int64       `json:"total_ms"`
		Count   int         `json:"count"`
		Slowest string      `json:"slowest_phase"`
		SlowMS  int64       `json:"slowest_ms"`
		Calls   []traceCall `json:"calls"`
	}
	if err := json.Unmarshal(errBuf.Bytes(), &got); err != nil {
		t.Fatalf("trace json did not parse: %v\n%s", err, errBuf.String())
	}
	if got.TotalMS != 905 || got.Count != 2 || got.Slowest != "fetch" || len(got.Calls) != 2 {
		t.Fatalf("json summary = %+v", got)
	}
}

func TestTracerTextLineAndSummary(t *testing.T) {
	tc := enableTrace(t, "text")
	_, errBuf := capture(t)
	tc.record([]string{"fetch", "--prune", "origin"}, 900*time.Millisecond, nil, true, false)
	tc.finish()
	out := errBuf.String()
	if !strings.Contains(out, "git fetch --prune origin") {
		t.Errorf("missing per-call trace line:\n%s", out)
	}
	if !strings.Contains(out, "trace summary") || !strings.Contains(out, "slowest: fetch") {
		t.Errorf("missing summary:\n%s", out)
	}
}

// Integration: a real sync records the actual git calls it makes, so the trace
// reflects where the run's time went.
func TestTraceRecordsRealGitCalls(t *testing.T) {
	r := newRepo(t)
	r.buildStack()
	r.switchTo("master")
	r.writeCommit("m.txt", "m", "master moved")
	r.git("push", "origin", "master")
	t.Chdir(r.dir)

	tc := enableTrace(t, "text")
	capture(t) // swallow the streamed git + trace output
	if err := cmdSync("feature/c", opts()); err != nil {
		t.Fatalf("sync: %v", err)
	}

	if len(tc.calls) == 0 {
		t.Fatal("tracer recorded no git calls during sync")
	}
	seen := map[string]bool{}
	for _, c := range tc.calls {
		seen[gitPhase(c.Cmd)] = true
	}
	for _, want := range []string{"fetch", "rebase", "merge-base"} {
		if !seen[want] {
			t.Errorf("sync trace missing %q phase; saw %v", want, keys(seen))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
