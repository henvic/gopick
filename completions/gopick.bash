# Load after bash-completion, if installed:
# source <(gopick -completion bash)

# COMP_WORDS retains shell quotes, including unfinished quotes at the cursor.
# Decode quoting without eval or command/variable expansion. Also record the
# last unquoted word break: Readline replaces only the text after it.
_gopick_unquote() {
  local text=$1 quote='' char next i tilde expanded=''
  REPLY='' breakprefix=''
  # Bash leaves unquoted tilde prefixes in COMP_WORDS. Expand the current
  # user's home and directory-stack shortcuts without evaluating shell input.
  # Seed REPLY so quotes or backslashes in the expanded path stay literal.
  tilde=${text%%/*}
  case $tilde in
    '~') expanded=$HOME ;;
    '~+') expanded=$PWD ;;
    '~-') expanded=$OLDPWD ;;
  esac
  if [[ -n $expanded ]]; then
    REPLY=$expanded
    text=${text:${#tilde}}
  fi
  for (( i=0; i < ${#text}; i++ )); do
    char=${text:i:1}
    if [[ $quote == "'" ]]; then
      if [[ $char == "'" ]]; then quote=''; else REPLY+=$char; fi
    elif [[ $char == \\ ]]; then
      next=${text:i+1:1}
      if [[ -z $quote || $next == [\$\`\"\\] ]]; then
        REPLY+=$next
        (( i++ ))
      else
        REPLY+=$char
      fi
    elif [[ -n $quote ]]; then
      if [[ $char == "$quote" ]]; then quote=''; else REPLY+=$char; fi
    elif [[ $char == \" || $char == "'" ]]; then
      quote=$char
    else
      REPLY+=$char
      [[ $COMP_WORDBREAKS == *"$char"* ]] && breakprefix=$REPLY
    fi
  done
  return 0
}

_gopick_complete() {
  local command=$1
  shift
  local i word index=-1 cursor=0 line kind strip='' current saved REPLY breakprefix
  local remaining=$COMP_LINE gap
  local -a words=() results=() spec=()
  COMPREPLY=()

  # Readline splits punctuation such as '=' and ':' into separate COMP_WORDS.
  # Locate each piece in the original line and join adjacent pieces only, so
  # whitespace-separated punctuation remains a separate argument. Leave
  # COMP_WORDBREAKS unchanged for other completers.
  for (( i=0; i < ${#COMP_WORDS[@]}; i++ )); do
    word=${COMP_WORDS[i]}
    if [[ -n $word ]]; then
      gap=${remaining%%"$word"*}
      remaining=${remaining#*"$word"}
    else
      gap=$remaining
    fi
    if (( index >= 0 )) && [[ -z $gap ]]; then
      words[index]+=$word
    else
      (( index++ ))
      words[index]=$word
    fi
    (( i == COMP_CWORD )) && cursor=$index
  done
  for (( i=0; i < ${#words[@]}; i++ )); do
    _gopick_unquote "${words[i]}"
    words[i]=$REPLY
    if (( i == cursor )); then
      strip=$breakprefix
    fi
  done
  while IFS= read -r line; do
    results[${#results[@]}]=$line
  done < <(gopick -complete shell -cursor "$cursor" -- "${words[@]}" 2>/dev/null)
  kind=${results[0]:-fallback}
  if [[ $kind != fallback ]]; then
    # Don't add filenames when a test regexp has no matching candidates.
    type compopt &>/dev/null && compopt +o default +o bashdefault 2>/dev/null
    # Preserve the backend's package-first order when Bash supports it.
    # Bash 3.2 still completes normally, using Readline's native sorting.
    compopt -o nosort 2>/dev/null || :
    for (( i=1; i < ${#results[@]}; i++ )); do
      current=${results[i]#"$strip"}
      # Readline adds slashes outside its quotes when it can stat the full
      # path. After a word break inside the path it sees only a suffix, so
      # keep our slash in that case.
      if [[ $kind == directories && ( -z $strip || $strip == -C= || $strip == --C= ) ]]; then
        current=${current%/}
      fi
      COMPREPLY[${#COMPREPLY[@]}]=$current
    done
    if [[ $kind == directories ]]; then
      # Bash 3.2 has no compopt. Calling compgen -f in this shell also enables
      # Readline's filename quoting; discard its results and keep ours.
      compopt -o filenames 2>/dev/null || compgen -f /dev/null/ >/dev/null
    fi
    return 0
  fi

  if [[ $command == go ]]; then
    saved=$_gopick_saved_go
  else
    saved=$_gopick_saved_command
  fi
  [[ -n $saved ]] || return 0
  # This is the shell-quoted output of Bash's own 'complete -p', never user
  # command-line text. Preserve the previous completer and its options.
  eval "spec=( ${saved#complete } )"
  unset 'spec[${#spec[@]}-1]'
  for (( i=0; i < ${#spec[@]}; i++ )); do
    if [[ ${spec[i]} == -F ]]; then
      "${spec[i+1]}" "$@"
      return $?
    fi
  done
  while IFS= read -r line; do
    COMPREPLY[${#COMPREPLY[@]}]=$line
  done < <(compgen "${spec[@]}" -- "${COMP_WORDS[COMP_CWORD]}")
}

_gopick() { _gopick_complete gopick "$@"; }
_gopick_go() { _gopick_complete go "$@"; }

_gopick_register() {
  local command=$1 function=$2 enabled=$3 current saved i
  local -a spec=() options=()
  current=$(complete -p "$command" 2>/dev/null)
  if [[ $command == go ]]; then
    saved=$_gopick_saved_go
  else
    saved=$_gopick_saved_command
  fi
  if [[ $current == *"-F $function "* ]]; then
    complete -r "$command"
    [[ -n $saved ]] && eval "$saved"
  fi
  [[ $enabled == yes ]] || return 0
  # Resolve bash-completion's lazy Go registration before wrapping it.
  if [[ $command == go ]] && ! complete -p go &>/dev/null && declare -F _completion_loader &>/dev/null; then
    _completion_loader go &>/dev/null
  fi
  saved=$(complete -p "$command" 2>/dev/null)
  if [[ $command == go ]]; then
    _gopick_saved_go=$saved
  else
    _gopick_saved_command=$saved
  fi
  if [[ -n $saved ]]; then
    eval "spec=( ${saved#complete } )"
    for (( i=0; i < ${#spec[@]}; i++ )); do
      if [[ ${spec[i]} == -o ]]; then
        options+=( -o "${spec[i+1]}" )
        (( i++ ))
      fi
    done
  else
    options=( -o default -o bashdefault )
  fi
  complete "${options[@]}" -F "$function" "$command"
}

if [[ ${_gopick_completion_target:-both} == go ]]; then
  _gopick_register gopick _gopick no
else
  _gopick_register gopick _gopick yes
fi
if [[ ${_gopick_completion_target:-both} == gopick ]]; then
  _gopick_register go _gopick_go no
else
  _gopick_register go _gopick_go yes
fi
