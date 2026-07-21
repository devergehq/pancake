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

Because `sync` rebases from the fork point onto the latest trunk, any commit
already contained in trunk (i.e. whatever just merged) replays empty and is
dropped — so you never have to tell it which branch merged.

## Commands

```
pancake list   <top> [trunk]   print the stack, bottom -> top
pancake log    <top> [trunk]   decorated graph of the stack
pancake sync   <top> [trunk]   fetch+prune, restack onto trunk, move every ref
pancake submit <top> [trunk]   force-push (--force-with-lease) every branch

Flags (before positionals): --trunk <ref>  --remote <name>  --dry-run
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
