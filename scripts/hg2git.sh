#!/usr/bin/env bash
# hg2git.sh — translates git subcommands to Mercurial equivalents.
#
# usage: configure ralphex with vcs_command = /path/to/hg2git.sh
# the script receives the same arguments as git and dispatches on the
# first argument (subcommand) via a case statement.
#
# phase-based commit logic:
#   - on a public commit (master-equivalent): hg commit creates a new draft
#   - on a draft commit (unsent): hg amend folds changes into existing commit
#   this produces a single-commit-per-diff workflow.
#
# IMPORTANT: several subcommands intentionally use non-zero exit codes
# (check-ignore exit 1, show-ref exit 1, symbolic-ref exit 128).
# each probe-style command is wrapped in if/|| guards so set -e does not
# terminate the script prematurely.

set -euo pipefail

subcmd="${1:-}"
shift || true

case "$subcmd" in

# ---------------------------------------------------------------------------
# repository info commands
# ---------------------------------------------------------------------------

rev-parse)
    if [[ "${1:-}" == "--show-toplevel" ]]; then
        hg root
    elif [[ "${1:-}" == "HEAD" ]]; then
        hg id -r . --template '{node}\n'
    elif [[ "${1:-}" == "--verify" && "${2:-}" == "--quiet" ]]; then
        ref="${3:-}"
        if hg log -r "$ref" --template '' 2>/dev/null; then
            exit 0
        else
            exit 1
        fi
    else
        # fallback: try to resolve arbitrary ref
        ref="${*}"
        if [[ -n "$ref" ]]; then
            hg log -r "$ref" --template '{node}\n' 2>/dev/null || exit 1
        else
            echo "hg2git: rev-parse: unsupported arguments" >&2
            exit 1
        fi
    fi
    ;;

symbolic-ref)
    if [[ "${1:-}" == "--short" && "${2:-}" == "HEAD" ]]; then
        phase=$(hg log -r . --template '{phase}')
        if [[ "$phase" == "public" ]]; then
            echo "master"
        else
            # draft phase: return active bookmark or "draft"
            bookmark=$(hg log -r . --template '{activebookmark}')
            if [[ -n "$bookmark" ]]; then
                echo "$bookmark"
            else
                echo "draft"
            fi
        fi
    elif [[ "${1:-}" == "refs/remotes/origin/HEAD" ]]; then
        # no remote refs concept in hg
        exit 1
    else
        echo "hg2git: symbolic-ref: unsupported arguments: $*" >&2
        exit 128
    fi
    ;;

show-ref)
    if [[ "${1:-}" == "--verify" && "${2:-}" == "--quiet" ]]; then
        ref="${3:-}"
        # extract bookmark name from refs/heads/<name>
        # note: single quotes in $name would break the revset, but branch names
        # come from Go code which doesn't allow special characters
        name="${ref#refs/heads/}"
        if hg log -r "bookmark('$name')" --template '' 2>/dev/null; then
            exit 0
        else
            exit 1
        fi
    else
        echo "hg2git: show-ref: unsupported arguments: $*" >&2
        exit 1
    fi
    ;;

# ---------------------------------------------------------------------------
# status command (format conversion required)
# ---------------------------------------------------------------------------

status)
    porcelain=false
    paths=()
    dashdash=false

    for arg in "$@"; do
        if [[ "$dashdash" == true ]]; then
            paths+=("$arg")
        elif [[ "$arg" == "--" ]]; then
            dashdash=true
        elif [[ "$arg" == "--porcelain" ]]; then
            porcelain=true
        elif [[ "$arg" == "-uall" ]]; then
            : # no-op for hg (already shows individual files)
        fi
    done

    if [[ "$porcelain" == true ]]; then
        hg_output=$(hg status "${paths[@]+"${paths[@]}"}" 2>/dev/null) || true
        if [[ -z "$hg_output" ]]; then
            exit 0
        fi
        while IFS= read -r line; do
            if [[ -z "$line" ]]; then
                continue
            fi
            hg_status="${line:0:1}"
            file="${line:2}"
            case "$hg_status" in
                M) echo " M $file" ;;   # modified, unstaged
                A) echo "A  $file" ;;   # added
                R) echo "D  $file" ;;   # removed
                !) echo " D $file" ;;   # missing/deleted in worktree
                \?) echo "?? $file" ;;  # untracked
                *) echo "?? $file" ;;   # unknown status, treat as untracked
            esac
        done <<< "$hg_output"
    else
        hg status "${paths[@]+"${paths[@]}"}"
    fi
    ;;

# ---------------------------------------------------------------------------
# branch commands
# ---------------------------------------------------------------------------

checkout)
    if [[ "${1:-}" == "-b" ]]; then
        shift
        name="${1:-}"
        hg bookmark "$name"
    else
        name="${1:-}"
        hg update "$name"
    fi
    ;;

# ---------------------------------------------------------------------------
# staging and file operations
# ---------------------------------------------------------------------------

add)
    if [[ "${1:-}" == "-A" ]]; then
        hg addremove
    else
        # ignore stderr for already-tracked files
        hg add "$@" 2>/dev/null || true
    fi
    ;;

mv)
    hg mv "$@"
    ;;

# ---------------------------------------------------------------------------
# commit command (phase-based amend logic)
# ---------------------------------------------------------------------------

commit)
    msg=""
    files=()
    dashdash=false

    while [[ $# -gt 0 ]]; do
        if [[ "$dashdash" == true ]]; then
            files+=("$1")
            shift
        elif [[ "$1" == "--" ]]; then
            dashdash=true
            shift
        elif [[ "$1" == "-m" ]]; then
            shift
            msg="${1:-}"
            shift
        else
            shift
        fi
    done

    phase=$(hg log -r . --template '{phase}')

    if [[ "$phase" == "public" ]]; then
        # on public commit: create a new draft commit
        if [[ ${#files[@]} -gt 0 ]]; then
            hg commit -m "$msg" "${files[@]}"
        else
            hg commit -m "$msg"
        fi
    else
        # on draft commit: amend existing commit (preserves original message)
        if [[ ${#files[@]} -gt 0 ]]; then
            hg amend "${files[@]}"
        else
            hg amend
        fi
    fi
    ;;

# ---------------------------------------------------------------------------
# diff command
# ---------------------------------------------------------------------------

diff)
    # parse --numstat <base>...HEAD
    numstat=false
    base=""

    for arg in "$@"; do
        if [[ "$arg" == "--numstat" ]]; then
            numstat=true
        elif [[ "$arg" == *"...HEAD" ]]; then
            base="${arg%...HEAD}"
        elif [[ "$arg" == *"..."* ]]; then
            base="${arg%%...*}"
        fi
    done

    if [[ "$numstat" == true && -n "$base" ]]; then
        # produce per-file numstat format: added\tremoved\tfile
        # use -r ... -r . to compare committed changes only (match git's base...HEAD semantics)
        diff_output=$(hg diff -r "ancestor(., $base)" -r . 2>/dev/null) || true
        if [[ -z "$diff_output" ]]; then
            exit 0
        fi

        declare -A added
        declare -A removed
        current_file=""

        while IFS= read -r line; do
            if [[ "$line" =~ ^\+\+\+\ b/(.+)$ ]]; then
                current_file="${BASH_REMATCH[1]}"
            elif [[ "$line" =~ ^---\ a/(.+)$ ]]; then
                # use --- a/ as fallback for deleted files (+++ /dev/null)
                current_file="${BASH_REMATCH[1]}"
            elif [[ -n "$current_file" ]]; then
                if [[ "$line" =~ ^\+[^+] ]] || [[ "$line" =~ ^\+$ ]]; then
                    added[$current_file]=$(( ${added[$current_file]:-0} + 1 ))
                elif [[ "$line" =~ ^-[^-] ]] || [[ "$line" =~ ^-$ ]]; then
                    removed[$current_file]=$(( ${removed[$current_file]:-0} + 1 ))
                fi
            fi
        done <<< "$diff_output"

        # collect all unique files
        declare -A all_files
        for f in "${!added[@]}"; do all_files[$f]=1; done
        for f in "${!removed[@]}"; do all_files[$f]=1; done

        for f in "${!all_files[@]}"; do
            printf '%s\t%s\t%s\n' "${added[$f]:-0}" "${removed[$f]:-0}" "$f"
        done
    else
        # plain diff, pass through
        hg diff "$@"
    fi
    ;;

# ---------------------------------------------------------------------------
# ignore check
# ---------------------------------------------------------------------------

check-ignore)
    # parse: check-ignore -q -- <path>
    path=""
    for arg in "$@"; do
        if [[ "$arg" == "-q" || "$arg" == "--" ]]; then
            continue
        fi
        path="$arg"
    done

    if [[ -z "$path" ]]; then
        exit 1
    fi

    if [[ -e "$path" ]]; then
        # file exists: use hg status -i
        ignored_output=$(hg status -i "$path" 2>/dev/null) || true
        if [[ -n "$ignored_output" ]]; then
            exit 0  # ignored
        else
            exit 1  # not ignored
        fi
    else
        # file does not exist: fall back to pattern matching against .hgignore.
        # WARNING: only supports regex syntax (hg default). if .hgignore uses
        # "syntax: glob", patterns like *.pyc will be misinterpreted as regex.
        # for glob-mode .hgignore files, add an explicit regex section for
        # .ralphex paths or switch the file to regex syntax.
        if [[ -f .hgignore ]]; then
            while IFS= read -r pattern; do
                # skip empty lines, comments, and syntax directives
                [[ -z "$pattern" || "$pattern" =~ ^# || "$pattern" =~ ^syntax: ]] && continue
                if echo "$path" | grep -qE "$pattern" 2>/dev/null; then
                    exit 0  # matches an ignore pattern
                fi
            done < .hgignore
        fi
        exit 1  # not ignored
    fi
    ;;

# ---------------------------------------------------------------------------
# worktree (not supported)
# ---------------------------------------------------------------------------

worktree)
    echo "worktree not supported with hg backend" >&2
    exit 1
    ;;

# ---------------------------------------------------------------------------
# log command
# ---------------------------------------------------------------------------

log)
    # parse: log <base>..HEAD --oneline
    base=""
    oneline=false

    for arg in "$@"; do
        if [[ "$arg" == "--oneline" ]]; then
            oneline=true
        elif [[ "$arg" == *"..HEAD" ]]; then
            base="${arg%..HEAD}"
        fi
    done

    if [[ "$oneline" == true && -n "$base" ]]; then
        hg log -r "::. and not ::$base" --template '{node|short} {desc|firstline}\n'
    else
        hg log "$@"
    fi
    ;;

# ---------------------------------------------------------------------------
# unknown command
# ---------------------------------------------------------------------------

*)
    echo "hg2git: unsupported git command: $subcmd" >&2
    exit 1
    ;;

esac
