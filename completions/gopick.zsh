# Enable zsh completion first: autoload -Uz compinit && compinit
# Load with: source <(gopick -completion zsh)

# Completion words retain quoting and unexpanded tilde prefixes. Expand only
# unquoted home/current/previous-directory shortcuts, without evaluating input.
_gopick_unquote() {
  local word=$1 tilde expanded prefix
  REPLY=${(Q)word}
  # Zsh expands tilde after an unquoted flag assignment only with this option.
  if [[ -o magicequalsubst && $word == -*=\~* ]]; then
    prefix=${word%%=*}=
    [[ $prefix == ${(Q)prefix} ]] || return 0
    word=${word#"$prefix"}
  fi
  tilde=${word%%/*}
  case $tilde in
    '~') expanded=$HOME ;;
    '~+') expanded=$PWD ;;
    '~-') expanded=$OLDPWD ;;
    *) return ;;
  esac
  # Unquote only the suffix: quotes or backslashes in the expanded directory
  # are already literal path characters.
  [[ -n $expanded ]] && REPLY=$prefix$expanded${(Q)${word#"$tilde"}}
  return 0
}

_gopick() {
  local context state state_descr line value kind REPLY
  local curcontext="$curcontext"
  typeset -A opt_args
  local -a args candidates scope directory tags

  # Once Go arguments start, use the user's existing Go completer. Keep the
  # original words local so completion does not change the command line.
  local -a words=("${words[@]}")
  local CURRENT=$CURRENT
  integer i firstpos=0 positional=0 options=1
  scope=(./...)
  for (( i=2; i <= CURRENT; i++ )); do
    value=${(Q)words[i]}
    if (( options && !positional )); then
      case $value in
        -C|--C) directory=(-C "${words[i+1]}"); (( i++ )); continue ;;
        -C=*|--C=*) directory=("${words[i]}"); continue ;;
        -tags|--tags) tags=(-tags "${words[i+1]}"); (( i++ )); continue ;;
        -tags=*|--tags=*) tags=("${words[i]}"); continue ;;
        -format|--format|-completion|--completion|-completion-target|--completion-target|-complete|--complete)
          (( i++ )); continue ;;
        --) options=0; continue ;;
        -*) continue ;;
      esac
    fi
    if (( positional )) && [[ $value == -* && $value != - ]]; then
      [[ $value == -- ]] && (( i++ ))
      (( i > CURRENT )) && return
      args=(go "${directory[@]}" test "${tags[@]}" "${scope[@]}")
      (( CURRENT = CURRENT - i + ${#args} + 1 ))
      words=("${args[@]}" "${(@)words[i,-1]}")
      _normal
      return
    fi
    if (( !positional )); then
      firstpos=$i
      # Match gopick's unambiguous package scopes. With two positional
      # arguments the first is a scope, including names such as net/http.
      if [[ $value == . || $value == .. || $value == ... || $value == ./* || $value == ../* ]]; then
        scope=("${words[i]}")
      elif [[ $value == */* && ${value%%/*} == *.* && $value != Test* && $value != *[\^\$\*\+\?\(\)\[\]\{\}\|\\]* ]]; then
        scope=("${words[i]}")
      fi
    else
      scope=("${words[firstpos]}")
    fi
    (( positional++ ))
  done

  _arguments -0 -S -C -A '-*' \
    '(-C --C)'{-C,--C}'=[search from this directory]:directory:_directories' \
    '(-tags --tags)'{-tags,--tags}'=[Go build tags]:build tags:' \
    '(-cmd --cmd -list --list)'{-list,--list}'[list matching tests without running them]' \
    '(-list --list -format --format -cmd --cmd)'{-cmd,--cmd}'[print the command without running it]' \
    '(-cmd --cmd -format --format)'{-format,--format}'=[customize output]:Go template:(table json)' \
    '(-format-fields --format-fields)'{-format-fields,--format-fields}'[print template field names, types, and descriptions as JSON Lines]' \
    '(-completion --completion)'{-completion,--completion}'=[print a completion script]:shell:(zsh bash fish)' \
    '(-completion-target --completion-target)'{-completion-target,--completion-target}'=[choose completion target]:target:(go gopick both)' \
    '(-h --h -help --help)'{-h,--h,-help,--help}'[show help]' \
    '*:package path or test regexp:->arguments'

  # Flag names are already supplied by _arguments.
  [[ $state == arguments && $PREFIX == -* ]] && return

  case "$state" in
    arguments)
      (( ${#line} > 2 )) && return
      kind=arguments
      scope=()
      if (( ${#line} == 2 )); then
        kind=tests
        scope=("${(Q)line[1]}" '')
      fi
      # Complete an anchored name while preserving the typed ^.
      if [[ $PREFIX == \^* ]]; then
        kind=tests
        compset -P '\^'
      fi
      args=(-complete "$kind")
      if (( ${#directory} )); then
        _gopick_unquote "${directory[-1]}"
        value=$REPLY
        (( ${#directory} == 1 )) && value=${value#*=}
        args+=(-C "$value")
      fi
      if (( ${+opt_args[-tags]} || ${+opt_args[--tags]} )); then
        value=${opt_args[-tags]-${opt_args[--tags]}}
        args+=(-tags "${(Q)value}")
      fi
      # The current word may be an unfinished regexp. Let zsh match name
      # prefixes instead of sending incomplete expressions to the backend.
      args+=(-- "${scope[@]}")
      candidates=("${(@f)$(gopick "${args[@]}" 2>/dev/null)}")
      # Preserve the backend's package-first order within this group.
      (( ${#candidates} )) && compadd -V gopick -a candidates
      ;;
  esac
}

# A pattern hook runs before the existing Go completer. Only claim -run
# values; every other position keeps its existing completion behavior.
_gopick_go() {
  local word strip kind name REPLY
  local -a query response candidates matched anchored

  # Only query the backend at -run values. It interprets flags, their values,
  # package arguments, and build settings for Zsh, Bash, and Fish alike.
  word=${(Q)words[CURRENT-1]}
  case $word in
    -run|--run|-test.run|--test.run) ;;
    *)
      word=${(Q)words[CURRENT]}
      case $word in
        -run=*|--run=*|-test.run=*|--test.run=*) strip=${word%%=*}= ;;
        *) return 1 ;;
      esac
      [[ $PREFIX == "$strip"* ]] || return 1
      ;;
  esac
  for word in "${words[@]}"; do
    _gopick_unquote "$word"
    query+=("$REPLY")
  done
  # Let compadd apply the user's native matching styles to the name prefix.
  # The backend still parses every other word, including packages after it.
  query[CURRENT]=$strip
  response=("${(@f)$(gopick -complete shell -cursor $(( CURRENT - 1 )) -- "${query[@]}" 2>/dev/null)}")
  kind=${response[1]}
  case $kind in
    exact|none) _compskip=all ;;
    *) return 1 ;;
  esac
  [[ $kind == exact && -z $SUFFIX ]] || return 1

  [[ -n $strip ]] && compset -P "$strip"
  # Inside -run='...', zsh represents the quoted caret as an escaped prefix.
  if [[ $PREFIX == '\^'* ]]; then
    PREFIX=${PREFIX#\\}
  fi
  PREFIX=${PREFIX#\^}
  [[ $PREFIX == *[^[:alnum:]_]* ]] && return 1
  for word in "${response[@]:1}"; do
    word=${word#"$strip"}
    word=${word#\^}
    candidates+=("${word%\$}")
  done
  compadd -O matched -M "$_matcher" -a candidates
  (( ${#matched} )) || return 1
  for name in "${matched[@]}"; do
    anchored+=("^${name}$")
  done
  # Match plain names, then let zsh quote the entire exact regexp on insertion.
  compadd -U -i "$IPREFIX" -d matched -a anchored
  if (( compstate[nmatches] > 1 )) && [[ $compstate[insert] == *unambiguous* ]]; then
    # Show choices without anchoring a shared prefix. Menu selection (or a
    # narrower prefix) inserts an actual candidate with both anchors.
    compstate[insert]=''
    compstate[list]=list
  fi
}

# Restore only registrations we own before applying the requested selection.
# In particular, never replace the normal Go completer with the -run hook.
if [[ ${_comps[gopick]} == _gopick ]]; then
  if [[ -n $_gopick_previous_command ]]; then
    _comps[gopick]=$_gopick_previous_command
  else
    unset '_comps[gopick]'
  fi
fi
if [[ ${_patcomps[go]} == _gopick_go ]]; then
  if [[ -n $_gopick_previous_pattern ]]; then
    _patcomps[go]=$_gopick_previous_pattern
  else
    unset '_patcomps[go]'
  fi
fi
if [[ ${_gopick_completion_target:-both} != go ]]; then
  _gopick_previous_command=${_comps[gopick]}
  compdef _gopick gopick
fi
if [[ ${_gopick_completion_target:-both} != gopick ]]; then
  _gopick_previous_pattern=${_patcomps[go]}
  compdef _gopick_go -p go
fi
