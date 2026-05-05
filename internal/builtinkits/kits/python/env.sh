#!/usr/bin/env bash
# python kit env. Exports and PATH only.
export PYENV_ROOT="/opt/pyenv"
export PATH="${PYENV_ROOT}/bin:${PATH}"
# Note: `eval "$(pyenv init -)"` is intentionally NOT here — that's a command,
# not an export. Users who need pyenv shims run it in their interactive shell.
