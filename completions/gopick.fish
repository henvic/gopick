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

# Conditions allow re-sourcing with another selection without deleting any
# completions installed by Fish or other tools. Register each rule only once;
# function definitions and the target selection are refreshed on every source.
if not string match -q '*__gopick_go_context*' -- (complete -c go)
    for option in run test.run
        complete -c go -n __gopick_go_context -o $option -l $option -x -a '(__gopick_candidates)'
    end
end

if not string match -q '*__gopick_command_context*' -- (complete -c gopick)
    complete -c gopick -n __gopick_command_context -f -a '(__gopick_candidates)'
    for option in C tags format completion completion-target
        complete -c gopick -n __gopick_command_context -o $option -l $option -x -a '(__gopick_candidates)'
    end
    for option in list cmd format-fields help
        complete -c gopick -n __gopick_command_context -o $option -l $option
    end
end
