# Load with: gopick -completion fish | source

function __gopick_query
    set -l words (commandline -xpc)
    set -l current (commandline -ct)
    set -l unquoted (string unescape -- "$current")
    if test (count $unquoted) -gt 0
        # The current token is excluded from -xpc expansion. Expand only a
        # leading, unquoted home prefix; quoted and escaped tildes are literal.
        if string match -qr '^~(/|$)' -- "$current"
            set unquoted (string replace -- '~' "$HOME" "$unquoted")
        end
        set current "$unquoted"
    end
    command gopick -complete shell -cursor (count $words) -- $words "$current" 2>/dev/null
end

function __gopick_active
    switch "$__gopick_completion_target"
        case go
            test "$argv[1]" = go
        case gopick
            test "$argv[1]" = gopick
        case '*'
            return 0
    end
end

function __gopick_go_context
    __gopick_active go; or return 1
    set -g __gopick_result (__gopick_query)
    contains -- "$__gopick_result[1]" exact none
end

function __gopick_command_context
    __gopick_active gopick; or return 1
    set -g __gopick_result (__gopick_query)
    test "$__gopick_result[1]" != fallback
end

function __gopick_option_context
    __gopick_command_context; or return 1
    set -l current (commandline -ct)
    # The backend supplies valid flag names. These rules only describe values.
    # Sharing one condition also lets Fish cache it across all option rules.
    if test "$__gopick_result[1]" = arguments; and string match -q -- '-*' "$current"; and not string match -q -- '*=*' "$current"
        return 1
    end
    return 0
end

function __gopick_candidates
    set -l current (commandline -ct)
    # The condition has already queried discovery for this completion cycle.
    for candidate in $__gopick_result[2..-1]
        # Fish matches candidates against the original tilde-prefixed token.
        if string match -qr '^~(/|$)' -- "$current"
            set candidate (string replace -- "$HOME" '~' "$candidate")
        end
        # Fish's option completion already preserves the -flag= part.
        if string match -q -- '-*=*' "$current"
            set candidate (string replace -r '^[^=]*=' '' -- "$candidate")
        end
        printf '%s\n' "$candidate"
    end
end

# Conditions allow re-sourcing with another selection.
if not string match -q '*__gopick_go_context*' -- (complete -c go)
    for option in run test.run
        complete -c go -n __gopick_go_context -o $option -l $option -x -a '(__gopick_candidates)'
    end
end

# Refresh our rules when upgrading, preserving every unrelated registration.
# Fish's erase operation cannot select a rule by condition. Reapply the shell's
# own serialized definitions for other providers, never command-line input.
set -l previous (complete -c gopick)
if string match -qr '__gopick_(command|option)_context' -- $previous
    complete -e -c gopick
    for rule in $previous
        if not string match -qr '__gopick_(command|option)_context' -- "$rule"
            eval "$rule"
        end
    end
end
for option in C tags format completion completion-target install-completion
    complete -c gopick -n __gopick_option_context -o $option -l $option -x -a '(__gopick_candidates)'
end
complete -c gopick -n __gopick_option_context -o completion-file -l completion-file -r -F
for option in list cmd format-fields yes
    complete -c gopick -n __gopick_option_context -o $option -l $option -a '(__gopick_candidates)'
end
complete -c gopick -n __gopick_option_context -o help -l help
complete -c gopick -n __gopick_command_context -k -f -a '(__gopick_candidates)'
