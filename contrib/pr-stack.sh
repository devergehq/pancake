# pr-stack.sh — convenience shell wrappers around `git stack`.
#
# Sourced from ~/.zshrc. These are thin aliases over the git-stack executable
# (on PATH via ~/.local/bin), so the logic lives in exactly one place.
#
#   prs-list   <top> [trunk]   # print the stack, bottom -> top
#   prs-log    <top> [trunk]   # decorated graph of the stack
#   prs-sync   <top> [trunk]   # fetch+prune, restack onto trunk, move every ref
#   prs-submit <top> [trunk]   # force-push every branch in the stack

prs-list()   { git stack list   "$@"; }
prs-log()    { git stack log    "$@"; }
prs-sync()   { git stack sync   "$@"; }
prs-submit() { git stack submit "$@"; }
