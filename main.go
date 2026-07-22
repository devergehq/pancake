// Command pancake is a stateless stacked-PR manager.
//
// It manages a stack of stacked pull requests without storing any state: the
// stack is derived on every run from the Git commit graph (every remote branch
// reachable from the top branch but not from the trunk). That means it works
// identically in any clone — including throwaway APFS workspaces — with no
// config, no server, and no account.
//
// You provide the top branch and a trunk (default origin/master); everything
// else is computed.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTrunk  = "origin/master"
	defaultRemote = "origin"
)

// Populated at release time via -ldflags -X (see .goreleaser.yaml).
var (
	version = "dev"
	commit  = "none"
)

// Output streams. These are package-level seams so tests can capture command
// output and streamed git I/O without spawning a subprocess. Production uses
// the real terminal streams; nothing else reassigns them.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// tr is the active tracer, or nil when tracing is off. It is package-level so
// the bare git() helper (which takes no options) can feed it. Because pancake
// is a thin wrapper over git, timing every git call is the whole story of where
// a run's wall-clock goes — see DEV-244.
var tr *tracer

// traceCall is one recorded git invocation.
type traceCall struct {
	Cmd      string `json:"cmd"`
	MS       int64  `json:"ms"`
	OK       bool   `json:"ok"`
	Mutating bool   `json:"mutating"`
	Skipped  bool   `json:"skipped"` // gated out by --dry-run
}

// tracer collects per-git-command timings for a single pancake run.
type tracer struct {
	mode  string // "text" | "json"
	calls []traceCall
}

// record logs one git invocation. In text mode it also streams a dim line so a
// hang is visible as it happens; json mode stays silent until finish().
func (t *tracer) record(args []string, d time.Duration, err error, mutating, skipped bool) {
	if t == nil {
		return
	}
	c := traceCall{
		Cmd:      strings.Join(args, " "),
		MS:       d.Milliseconds(),
		OK:       err == nil,
		Mutating: mutating,
		Skipped:  skipped,
	}
	t.calls = append(t.calls, c)
	if t.mode == "text" {
		status := "ok"
		switch {
		case skipped:
			status = "skip"
		case err != nil:
			status = "ERR"
		}
		fmt.Fprintf(stderr, "\033[2m  trace %7dms  %-4s  git %s\033[0m\n", c.MS, status, c.Cmd)
	}
}

// stats aggregates the recorded calls: total wall-clock spent in git, the call
// count, and the slowest git subcommand (by summed duration).
func (t *tracer) stats() (total int64, count int, slowPhase string, slowMS int64) {
	byPhase := map[string]int64{}
	for _, c := range t.calls {
		total += c.MS
		phase := c.Cmd
		if i := strings.IndexByte(phase, ' '); i > 0 {
			phase = phase[:i]
		}
		byPhase[phase] += c.MS
	}
	slowMS = -1
	for p, ms := range byPhase {
		if ms > slowMS {
			slowPhase, slowMS = p, ms
		}
	}
	return total, len(t.calls), slowPhase, slowMS
}

// finish emits the end-of-run trace summary (to stderr, keeping stdout clean for
// the command's real output). json mode emits one machine-readable object.
func (t *tracer) finish() {
	if t == nil {
		return
	}
	total, count, slowPhase, slowMS := t.stats()
	if t.mode == "json" {
		obj := struct {
			TotalMS int64       `json:"total_ms"`
			Count   int         `json:"count"`
			Slowest string      `json:"slowest_phase"`
			SlowMS  int64       `json:"slowest_ms"`
			Calls   []traceCall `json:"calls"`
		}{total, count, slowPhase, slowMS, t.calls}
		enc := json.NewEncoder(stderr)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(obj)
		return
	}
	fmt.Fprintf(stderr, "\033[2mtrace summary: %d git calls, %dms total; slowest: %s (%dms)\033[0m\n",
		count, total, slowPhase, slowMS)
}

// traceMode is a flag.Value so --trace works bare (text) or as --trace=json.
type traceMode struct{ s string }

func (m *traceMode) String() string { return m.s }
func (m *traceMode) Set(v string) error {
	switch v {
	case "true", "", "text":
		m.s = "text"
	case "json":
		m.s = "json"
	default:
		return fmt.Errorf("invalid --trace value %q (want: text or json)", v)
	}
	return nil
}
func (m *traceMode) IsBoolFlag() bool { return true }

// resolveTraceMode picks the trace mode: an explicit flag wins, else the
// PANCAKE_TRACE env var ("json" for json, any other non-empty value for text).
func resolveTraceMode(flagVal, env string) string {
	if flagVal != "" {
		return flagVal
	}
	switch env {
	case "":
		return ""
	case "json":
		return "json"
	default:
		return "text"
	}
}

type options struct {
	trunk   string
	remote  string
	dryRun  bool
	jsonOut bool
}

// branch is one member of the stack.
type branch struct {
	name    string // short name, e.g. feature/dev-67
	sha     string
	depth   int    // commits between trunk and this branch's tip (for ordering)
	subject string // subject line of the branch tip commit
}

// branchJSON is the machine-readable projection emitted by `list --json`.
type branchJSON struct {
	Branch            string `json:"branch"`
	SHA               string `json:"sha"`
	CommitsAboveTrunk int    `json:"commitsAboveTrunk"`
	Subject           string `json:"subject"`
}

func main() {
	if len(os.Args) < 2 {
		usage(1)
	}
	cmd := os.Args[1]
	switch cmd {
	case "-h", "--help", "help":
		usage(0)
	case "-v", "--version", "version":
		fmt.Fprintf(stdout, "pancake %s (%s)\n", version, commit)
		return
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var o options
	fs.StringVar(&o.trunk, "trunk", defaultTrunk, "trunk branch the stack targets")
	fs.StringVar(&o.remote, "remote", defaultRemote, "remote name")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print mutating git commands instead of running them")
	fs.BoolVar(&o.jsonOut, "json", false, "list: emit the stack as JSON")
	var traceOpt traceMode
	fs.Var(&traceOpt, "trace", "trace git calls with timings (--trace or --trace=json)")
	_ = fs.Parse(os.Args[2:])

	if mode := resolveTraceMode(traceOpt.s, os.Getenv("PANCAKE_TRACE")); mode != "" {
		tr = &tracer{mode: mode}
	}

	if t := fs.Arg(1); t != "" { // optional positional trunk override
		o.trunk = t
	}
	top := fs.Arg(0)
	if top == "" { // no <top> given — infer the tip of the stack from the graph
		detected, derr := detectTop(o)
		if derr != nil {
			fatal("%v", derr)
		}
		top = detected
		note("auto-detected top branch: %s", top)
	}

	var err error
	switch cmd {
	case "list":
		err = cmdList(top, o)
	case "log":
		err = cmdLog(top, o)
	case "sync":
		err = cmdSync(top, o)
	case "submit":
		err = cmdSubmit(top, o)
	default:
		fatal("unknown command %q (try: list, log, sync, submit)", cmd)
	}
	tr.finish()
	if err != nil {
		fatal("%v", err)
	}
}

// git runs a git command and returns its trimmed stdout.
func git(args ...string) (string, error) {
	start := time.Now()
	out, err := exec.Command("git", args...).Output()
	tr.record(args, time.Since(start), err, false, false)
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// run streams a git command to the terminal. When mutating and --dry-run is set,
// it prints the command instead of executing it.
func run(o options, mutating bool, args ...string) error {
	if o.dryRun && mutating {
		fmt.Fprintf(stderr, "DRY-RUN: git %s\n", strings.Join(args, " "))
		tr.record(args, 0, nil, mutating, true)
		return nil
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, os.Stdin
	start := time.Now()
	err := cmd.Run()
	tr.record(args, time.Since(start), err, mutating, false)
	return err
}

func note(format string, a ...any) {
	fmt.Fprintf(stderr, "\033[36m▸ %s\033[0m\n", fmt.Sprintf(format, a...))
}

// stack derives the branches of the stack topped by top, ordered bottom -> top.
func stack(top string, o options) ([]branch, error) {
	ref := o.remote + "/" + top
	if _, err := git("rev-parse", "--verify", "--quiet", ref); err != nil {
		return nil, fmt.Errorf("no remote-tracking branch %q — run a fetch, or check the name", ref)
	}
	// Tab-delimited so the commit subject (which may contain spaces) parses
	// cleanly. sha and refname never contain tabs.
	out, err := git("for-each-ref",
		"--merged", ref,
		"--no-merged", o.trunk,
		"--format=%(objectname)%09%(refname:lstrip=3)%09%(contents:subject)",
		"refs/remotes/"+o.remote+"/")
	if err != nil {
		return nil, err
	}
	var bs []branch
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		sha, name := parts[0], parts[1]
		subject := ""
		if len(parts) == 3 {
			subject = parts[2]
		}
		cnt, _ := git("rev-list", "--count", o.trunk+".."+sha)
		depth, _ := strconv.Atoi(cnt)
		bs = append(bs, branch{name: name, sha: sha, depth: depth, subject: subject})
	}
	sort.SliceStable(bs, func(i, j int) bool { return bs[i].depth < bs[j].depth })
	return bs, nil
}

// detectTop infers the top branch when the user omits it: among the remote
// branches not merged into trunk, the "tip" of a stack is one that no other
// such branch is built on top of. With a single stack there is exactly one tip.
// With several independent stacks, it disambiguates by the current branch —
// the tip whose stack contains HEAD — and otherwise asks the user to be
// explicit rather than guessing.
func detectTop(o options) (string, error) {
	out, err := git("for-each-ref", "--no-merged", o.trunk,
		"--format=%(refname:lstrip=3)", "refs/remotes/"+o.remote+"/")
	if err != nil {
		return "", err
	}
	var cands []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || l == "HEAD" { // skip the origin/HEAD symref
			continue
		}
		cands = append(cands, l)
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("no branches above %s to infer a stack from — pass <top> explicitly", o.trunk)
	}

	// A candidate is a tip if no other candidate contains it.
	var tips []string
	for _, x := range cands {
		isTip := true
		for _, y := range cands {
			if x != y && contains(o.remote+"/"+y, o.remote+"/"+x) {
				isTip = false
				break
			}
		}
		if isTip {
			tips = append(tips, x)
		}
	}
	if len(tips) == 1 {
		return tips[0], nil
	}

	// Multiple stacks: pick the one whose tip contains the current branch.
	if cur, err := git("rev-parse", "--abbrev-ref", "HEAD"); err == nil && cur != "" && cur != "HEAD" {
		var match []string
		for _, tp := range tips {
			if tp == cur || contains(o.remote+"/"+tp, o.remote+"/"+cur) {
				match = append(match, tp)
			}
		}
		if len(match) == 1 {
			return match[0], nil
		}
	}
	sort.Strings(tips)
	return "", fmt.Errorf("multiple stacks found (%s) — pass <top> explicitly", strings.Join(tips, ", "))
}

// contains reports whether ref outer contains inner (inner is an ancestor of
// outer). git merge-base --is-ancestor exits 1 for a plain "no", which is a
// valid answer rather than a failure — so we record it as a successful probe in
// the trace and reserve the ERR marker for genuine errors (exit >1).
func contains(outer, inner string) bool {
	args := []string{"merge-base", "--is-ancestor", inner, outer}
	start := time.Now()
	err := exec.Command("git", args...).Run()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		tr.record(args, time.Since(start), nil, false, false)
		return false
	}
	tr.record(args, time.Since(start), err, false, false)
	return err == nil
}

func cmdList(top string, o options) error {
	bs, err := stack(top, o)
	if err != nil {
		return err
	}
	if o.jsonOut {
		out := make([]branchJSON, len(bs))
		for i, b := range bs {
			out[i] = branchJSON{Branch: b.name, SHA: b.sha, CommitsAboveTrunk: b.depth, Subject: b.subject}
		}
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for _, b := range bs {
		fmt.Fprintln(stdout, b.name)
	}
	return nil
}

// prInfo is one open/closed pull request, as reported by gh.
type prInfo struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Base   string `json:"baseRefName"`
	Head   string `json:"headRefName"`
}

// fetchPRs is a seam: production shells to gh; tests stub it so the PR-aware
// path is exercised without a network or a real GitHub repo.
var fetchPRs = ghFetchPRs

// ghFetchPRs returns the repo's PRs keyed by head branch. A missing or
// unauthenticated gh is reported as an error so callers can degrade gracefully.
func ghFetchPRs(o options) (map[string]prInfo, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return nil, fmt.Errorf("gh not installed")
	}
	out, err := exec.Command("gh", "pr", "list", "--state", "all",
		"--json", "number,state,baseRefName,headRefName", "--limit", "200").Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var arr []prInfo
	if err := json.Unmarshal(out, &arr); err != nil {
		return nil, err
	}
	m := make(map[string]prInfo, len(arr))
	for _, p := range arr {
		m[p.Head] = p
	}
	return m, nil
}

func cmdLog(top string, o options) error {
	bs, err := stack(top, o)
	if err != nil {
		return err
	}
	if prs, prErr := fetchPRs(o); prErr != nil {
		note("PR annotations unavailable (%v) — showing plain graph", prErr)
	} else {
		printPRTable(bs, o, prs)
	}
	return run(o, false, "log", "--graph", "--oneline", "--decorate", o.trunk+".."+o.remote+"/"+top)
}

// printPRTable lists the stack top → bottom with each branch's PR number, state,
// and base — flagging any PR whose base is not the branch directly below it (the
// bottom branch's base should be the trunk). That mismatch is the thing you most
// want to catch: a stacked PR pointed at the wrong base.
func printPRTable(bs []branch, o options, prs map[string]prInfo) {
	trunkShort := strings.TrimPrefix(o.trunk, o.remote+"/")
	fmt.Fprintln(stdout, "Stack PRs (top → bottom):")
	for i := len(bs) - 1; i >= 0; i-- {
		b := bs[i]
		wantBase := trunkShort
		if i > 0 {
			wantBase = bs[i-1].name
		}
		pr, ok := prs[b.name]
		if !ok {
			fmt.Fprintf(stdout, "  %-28s (no PR)\n", b.name)
			continue
		}
		mark := "✓"
		if pr.Base != wantBase {
			mark = "✗ base should be " + wantBase
		}
		fmt.Fprintf(stdout, "  %-28s #%-4d %-6s base %-24s %s\n", b.name, pr.Number, pr.State, pr.Base, mark)
	}
	fmt.Fprintln(stdout)
}

func cmdSync(top string, o options) error {
	note("fetching + pruning %s", o.remote)
	if err := run(o, true, "fetch", "--prune", o.remote); err != nil {
		return err
	}
	bs, err := stack(top, o)
	if err != nil {
		return err
	}
	if len(bs) == 0 {
		return fmt.Errorf("no stack branches found above %s", o.trunk)
	}
	// Check out the top branch in THIS worktree first (DWIM-creates the tracking
	// branch in a fresh clone). It becomes HEAD and is rebased below, so it must
	// never be force-updated in the loop — git refuses to move a checked-out
	// branch's ref. Doing this up front also means a sync started from anywhere
	// (trunk, mid-stack, or the tip) proceeds identically.
	if err := run(o, true, "switch", top); err != nil {
		return err
	}
	note("materializing %d stack branches locally", len(bs))
	for _, b := range bs {
		if b.name == top {
			continue // HEAD — rebased below, never force-updated
		}
		// A branch checked out in another worktree also can't be force-updated;
		// warn and skip it rather than aborting the whole sync.
		if err := run(o, true, "branch", "--force", b.name, o.remote+"/"+b.name); err != nil {
			note("! skipped %s (checked out in another worktree — align it there)", b.name)
		}
	}
	// Fork point on the (remote) top so it works even under --dry-run.
	fork, err := git("merge-base", o.trunk, o.remote+"/"+top)
	if err != nil {
		return err
	}
	// Replay onto the new trunk. Commits already contained in trunk (whatever
	// just merged) replay empty and are dropped, so the merged branch never has
	// to be named. --update-refs carries every local branch to its new position.
	note("restacking %s onto %s (--update-refs)", top, o.trunk)
	if err := run(o, true, "rebase", "--update-refs", "--onto", o.trunk, fork); err != nil {
		return err
	}
	note("done — review with: pancake log %s", top)
	return nil
}

func cmdSubmit(top string, o options) error {
	bs, err := stack(top, o)
	if err != nil {
		return err
	}
	if len(bs) == 0 {
		return fmt.Errorf("no stack branches found above %s", o.trunk)
	}
	args := []string{"push", "--force-with-lease", o.remote}
	names := make([]string, 0, len(bs))
	for _, b := range bs {
		args = append(args, b.name)
		names = append(names, b.name)
	}
	note("force-pushing: %s", strings.Join(names, " "))
	return run(o, true, args...)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(stderr, "pancake: "+format+"\n", a...)
	os.Exit(1)
}

func usage(code int) {
	fmt.Fprint(stderr, `pancake — stateless stacked-PR manager

Derives the whole stack from the git graph. No stored state, no server, no account.

Usage:
  pancake list   [flags] [top] [trunk]   print the stack, bottom -> top
  pancake log    [flags] [top] [trunk]   decorated graph of the stack
  pancake sync   [flags] [top] [trunk]   fetch+prune, restack onto trunk, move all refs
  pancake submit [flags] [top] [trunk]   force-push (with lease) every branch in the stack

Flags (must precede positional args):
  --trunk <ref>    trunk the stack targets (default origin/master)
  --remote <name>  remote name (default origin)
  --dry-run        print mutating git commands instead of running them
  --json           list: emit the stack as JSON [{branch,sha,commitsAboveTrunk,subject}]
  --trace[=json]   time every git call; end-of-run summary (or PANCAKE_TRACE=1)

<top> is a short branch name, e.g. feature/dev-67 (no remote prefix). Omit it to
auto-detect the tip of your stack from the graph (the current branch's stack).

Since pancake is a thin wrapper over git, --trace shows exactly where a run's
time goes — set GIT_TRACE2=1 alongside it to see git's own internal phases.

Typical loop after the bottom PR merges:
  pancake sync   feature/dev-67
  pancake submit feature/dev-67
`)
	os.Exit(code)
}
