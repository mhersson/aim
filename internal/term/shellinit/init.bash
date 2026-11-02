# AIM's shell integration for bash. bash --posix reads this file through ENV
# because AIM named it there. It leaves posix mode, puts the user's ENV back,
# reads the login files bash would have read, then puts AIM's claude first.
if [ -n "${AIM_ENV+x}" ]; then
  ENV=$AIM_ENV
else
  unset ENV
fi
set +o posix
# Posix mode changed these at startup; an exported value is the user's own.
shopt -u inherit_errexit 2>/dev/null
# The attribute word of declare -p ("-x", "--") is cut before the value, so a value cannot match.
__aim_a=$(declare -p HISTFILE 2>/dev/null)
__aim_a=${__aim_a#declare }
[[ ${__aim_a%% *} == *x* ]] || HISTFILE=~/.bash_history
__aim_a=$(declare -p MAILCHECK 2>/dev/null)
__aim_a=${__aim_a#declare }
[[ ${__aim_a%% *} == *x* ]] || MAILCHECK=60
unset __aim_a

if [ -r /etc/profile ]; then
  . /etc/profile
fi
if [ -r ~/.bash_profile ]; then
  . ~/.bash_profile
elif [ -r ~/.bash_login ]; then
  . ~/.bash_login
elif [ -r ~/.profile ]; then
  . ~/.profile
fi

if [ -n "$AIM_SHELL_DIR" ]; then
  __aim_path=":$PATH:"
  while [[ $__aim_path == *":$AIM_SHELL_DIR/bin:"* ]]; do
    __aim_path=${__aim_path//":$AIM_SHELL_DIR/bin:"/:}
  done
  __aim_path=${__aim_path#:}
  __aim_path=${__aim_path%:}
  PATH=$AIM_SHELL_DIR/bin${__aim_path:+:$__aim_path}
  unset __aim_path
  if ! declare -F claude >/dev/null; then
    function claude {
      if [[ -x $AIM_SHELL_DIR/bin/claude ]]; then
        "$AIM_SHELL_DIR/bin/claude" "$@"
      else
        hash -d claude 2>/dev/null
        command claude "$@"
      fi
    }
  fi
fi
