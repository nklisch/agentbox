# Agentbox base kit env. Sourced from /etc/profile.d/agentbox.sh at login.
# Per KITS.md, this file is exports/PATH only — interactive-shell init lives
# in /etc/zsh/zshrc (written by install.sh).

export PATH="/usr/local/bin:${PATH}"
export EDITOR="${EDITOR:-vi}"
export PAGER="${PAGER:-less}"
export LESS="-R --mouse"
export ZELLIJ_DEFAULT_LAYOUT="agentbox"
