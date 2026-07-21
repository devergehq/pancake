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
	trunk  string
	remote string
	dryRun bool
}

// branch is one member of the stack.
type branch struct {
	name  string // short name, e.g. feature/dev-67
	sha   string
	depth int // commits between trunk and this branch's tip (for ordering)
}

func main() {
	if len(os.Args) < 2 {
		usage(1)
	}
	cmd := os.Args[1]
	switch cmd {
	case "-h", "--help", "help":
		usage(0)
	}

	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	var o options
	fs.StringVar(&o.trunk, "trunk", defaultTrunk, "trunk branch the stack targets")
	fs.StringVar(&o.remote, "remote", defaultRemote, "remote name")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print mutating git commands instead of running them")
	var traceOpt traceMode
	fs.Var(&traceOpt, "trace", "trace git calls with timings (--trace or --trace=json)")
	_ = fs.Parse(os.Args[2:])

	if mode := resolveTraceMode(traceOpt.s, os.Getenv("PANCAKE_TRACE")); mode != "" {
		tr = &tracer{mode: mode}
	}

	top := fs.Arg(0)
	if top == "" {
		fatal("missing <top> branch — usage: pancake %s [flags] <top> [trunk]", cmd)
	}
	if t := fs.Arg(1); t != "" { // optional positional trunk override
		o.trunk = t
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
	out, err := git("for-each-ref",
		"--merged", ref,
		"--no-merged", o.trunk,
		"--format=%(objectname) %(refname:lstrip=3)",
		"refs/remotes/"+o.remote+"/")
	if err != nil {
		return nil, err
	}
	var bs []branch
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		sha, name := parts[0], parts[1]
		cnt, _ := git("rev-list", "--count", o.trunk+".."+sha)
		depth, _ := strconv.Atoi(cnt)
		bs = append(bs, branch{name: name, sha: sha, depth: depth})
	}
	sort.SliceStable(bs, func(i, j int) bool { return bs[i].depth < bs[j].depth })
	return bs, nil
}

func cmdList(top string, o options) error {
	bs, err := stack(top, o)
	if err != nil {
		return err
	}
	for _, b := range bs {
		fmt.Fprintln(stdout, b.name)
	}
	return nil
}

func cmdLog(top string, o options) error {
	return run(o, false, "log", "--graph", "--oneline", "--decorate", o.trunk+".."+o.remote+"/"+top)
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
	note("materializing %d stack branches locally", len(bs))
	for _, b := range bs {
		if err := run(o, true, "branch", "--force", b.name, o.remote+"/"+b.name); err != nil {
			return err
		}
	}
	if err := run(o, true, "switch", top); err != nil {
		return err
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
  pancake list   [flags] <top> [trunk]   print the stack, bottom -> top
  pancake log    [flags] <top> [trunk]   decorated graph of the stack
  pancake sync   [flags] <top> [trunk]   fetch+prune, restack onto trunk, move all refs
  pancake submit [flags] <top> [trunk]   force-push (with lease) every branch in the stack

Flags (must precede positional args):
  --trunk <ref>    trunk the stack targets (default origin/master)
  --remote <name>  remote name (default origin)
  --dry-run        print mutating git commands instead of running them
  --trace[=json]   time every git call; end-of-run summary (or PANCAKE_TRACE=1)

<top> is a short branch name, e.g. feature/dev-67 (no remote prefix).

Since pancake is a thin wrapper over git, --trace shows exactly where a run's
time goes — set GIT_TRACE2=1 alongside it to see git's own internal phases.

Typical loop after the bottom PR merges:
  pancake sync   feature/dev-67
  pancake submit feature/dev-67
`)
	os.Exit(code)
}
