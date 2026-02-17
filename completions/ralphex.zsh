#compdef ralphex

# zsh completion for ralphex (generated via go-flags)
_ralphex() {
    local -a lines
    lines=(${(f)"$(GO_FLAGS_COMPLETION=verbose "${words[1]}" "${(@)words[2,$CURRENT]}" 2>/dev/null)"})
    if (( ${#lines} )); then
        local -a descr
        local line item desc
        for line in "${lines[@]}"; do
            if [[ "$line" = *'  # '* ]]; then
                item="${line%%  *}"
                desc="${line#*  \# }"
                descr+=("${item//:/\\:}:${desc}")
            else
                item="${line%%  *}"
                [[ -z "$item" ]] && item="$line"
                [[ "$item" = *" "* ]] && continue
                descr+=("${item//:/\\:}")
            fi
        done
        _describe 'command' descr
    else
        _files
    fi
}

_ralphex "$@"
