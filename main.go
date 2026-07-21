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
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
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
	_ = fs.Parse(os.Args[2:])

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
	if err != nil {
		fatal("%v", err)
	}
}

// git runs a git command and returns its trimmed stdout.
func git(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
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
		return nil
	}
	cmd := exec.Command("git", args...)
	cmd.Stdout, cmd.Stderr, cmd.Stdin = stdout, stderr, os.Stdin
	return cmd.Run()
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

<top> is a short branch name, e.g. feature/dev-67 (no remote prefix).

Typical loop after the bottom PR merges:
  pancake sync   feature/dev-67
  pancake submit feature/dev-67
`)
	os.Exit(code)
}
