# 🥞 pancake

**Stateless stacked-PR management, derived from your git graph.**

No server. No account. No stored lineage. `pancake` figures out your whole stack
every time from the commit graph itself, so it works identically in any clone —
including throwaway worktrees / APFS workspaces — with zero setup.

You give it two things you already know: the **top** branch of your stack and the
**trunk** it targets (default `origin/master`). It derives the rest.

```
$ pancake list feature/dev-67
feature/dev-43
feature/dev-37
feature/dev-38
…
feature/dev-67
```

## Why

Stacked PRs are great; managing them by hand (rebasing every branch onto `master`
after each merge, moving refs, force-pushing the lot) is tedious. The existing
tools each ask for something we don't want:

| Tool | Trade-off |
|---|---|
| **Graphite** | Proprietary; requires an account and server-side stack state. |
| **git-town** | OSS & good, but stores lineage in *local git config* that doesn't travel to fresh/ephemeral clones, and expects you to adopt its `create` commands. |
| **pancake** | **Stateless.** Nothing stored — the stack is read from the graph. Use it on any branch, in any clone, immediately. |

## How it works

Four git plumbing calls behind a nice UX:

- **discover** the stack → `git for-each-ref --merged <top> --no-merged <trunk>`
- **fork point** → `git merge-base <trunk> <top>`
- **restack** → `git rebase --update-refs --onto <trunk> <fork>`
- **submit** → `git push --force-with-lease`

Because `sync` rebases onto the latest trunk, any commit already contained in
trunk (i.e. whatever just merged) replays empty and is dropped — so you never
have to tell it which branch merged.

### Squash-merges of multi-commit branches

Squash-merging a **single-commit** branch is trivial: it drops as an empty replay.
A **multi-commit** branch is the tricky case — its N commits collapse into one
commit on trunk, so naively replaying the individual N conflicts. `sync` finds
the boundary and starts the replay *above* the squashed branch automatically:

1. via the merged branch's leftover **local ref** (git prunes only the remote), and
2. failing that, via **patch-id** — a squash commit shares its patch-id with the
   range it collapsed, so it works even in a fresh clone with no local ref.

If a squash can't be matched (e.g. conflict resolution was baked into the merge),
`sync` tells you and you name the boundary explicitly:

```sh
pancake sync feature/top --from <the squash-merged branch's pre-merge tip>
```

## Commands

```
pancake list   [top] [trunk]   print the stack, bottom -> top
pancake log    [top] [trunk]   PR-aware stack (via gh) + decorated graph
pancake sync   [top] [trunk]   fetch+prune, restack onto trunk, move every ref
pancake submit [top] [trunk]   force-push (--force-with-lease) every branch
pancake doctor                 check the GitHub prerequisites for stacked PRs
pancake install                symlink git-stack/git-pancake so `git stack …` works

Flags (before positionals): --trunk <ref>  --remote <name>  --dry-run  --json  --trace[=json]
```

`list --json` emits `[{branch, sha, commitsAboveTrunk, subject}]`, ordered
bottom → top — for scripting and for piping into other tools.

`log` annotates each branch with its PR number/state/base (via `gh`) and flags
any PR whose base isn't the branch directly below it — the misconfiguration you
most want to catch in a stack. If `gh` is missing or unauthenticated it degrades
to the plain decorated graph.

## `git stack` — the native-feeling alias

`pancake` is a mouthful to type. `pancake install` symlinks `git-stack` and
`git-pancake` next to the binary, so git picks them up as subcommands:

```sh
pancake install          # one time (the binary's dir must be on PATH)
git stack sync           # == pancake sync — auto-detects the top branch
git stack submit
git stack log
```

git runs `git-stack` for `git stack …`, and pancake ignores `argv[0]`, so the
symlink is the whole trick — no wrapper, no extra process.

## Per-repo defaults: `.pancake`

pancake defaults its trunk to `origin/master`, but plenty of repos default to
`main`, `dev`, or `develop` — and stacking onto the wrong branch is a silent,
maddening failure. Rather than pass `--trunk` every time, commit an optional
`.pancake` file at the repo root:

```ini
# .pancake — pancake defaults for this repo
trunk  = origin/dev
remote = origin
```

- **Precedence:** `--trunk` (flag/positional) → `.pancake` → built-in `origin/master`.
- It's committed and versioned, so a fresh or ephemeral clone gets the right
  default from the first second. It configures the *default target only* — the
  stack itself is still derived from the graph, so this doesn't touch the
  stateless contract.
- `pancake doctor` shows where the trunk came from and, if you *didn't* configure
  one, warns when pancake's built-in default doesn't match the repo's default
  branch (the real trap). A deliberate override is reported as intentional, not
  an error.

## Prerequisite: auto-delete head branches

Stacked PRs on GitHub depend on one repo setting pancake cannot substitute for —
**Settings → General → "Automatically delete head branches"** (`delete_branch_on_merge`).
Without it, when a base PR merges, GitHub does **not** retarget the PR stacked
above it: the merged branch lingers, the dependent PR keeps the wrong base, and
the stack silently rots. pancake won't flip org settings for you, but it will
tell you when it's off:

```sh
pancake doctor          # ✓/✗ gh auth, delete_branch_on_merge, trunk == default branch
pancake doctor --fix    # enable delete_branch_on_merge for you (via gh)
```

`submit` also warns if the setting is off.

Omit `[top]` and pancake infers it: the tip of your stack (the unmerged branch
nothing else is built on). With one stack that's unambiguous; with several it
picks the one containing your current branch, else asks you to name it.

### Seeing where the time goes

pancake is a thin wrapper over git, so a slow run means a slow *git* command —
and which one is wildly repo-dependent (history depth, remote ref count, whether
a `commit-graph` exists, working-tree size, hooks). `--trace` times every git
call and prints an end-of-run summary; `--trace=json` emits a machine-readable
object. Set `GIT_TRACE2=1` alongside it to see git's own internal phases.

```sh
pancake sync feature/dev-67 --trace        # per-call timings + slowest phase
pancake sync feature/dev-67 --trace=json    # structured, for collecting/diffing
PANCAKE_TRACE=1 pancake sync feature/dev-67 # same, via env
```

### Typical loop, after the bottom PR merges

```sh
pancake sync   feature/dev-67   # fetch+prune, restack the rest onto master, move all refs
pancake submit feature/dev-67   # force-push every branch → every PR updates at once
```

## Install

```sh
go install github.com/devergehq/pancake@latest
```

Optionally expose it as a git subcommand (`git stack …`) by symlinking a shim
onto your `PATH`:

```sh
ln -s "$(go env GOPATH)/bin/pancake" ~/.local/bin/git-stack
```

## Shell prototype

`contrib/git-stack` is the original POSIX-shell prototype these commands grew
from — handy if you want the workflow without building the binary. Source
`contrib/pr-stack.sh` from your shell rc for `prs-list` / `prs-sync` / `prs-submit`.

## Status

v0 — see the [Pancake project in Linear](https://linear.app/deverge/project/pancake-764a86584362).

## License

MIT © Deverge
