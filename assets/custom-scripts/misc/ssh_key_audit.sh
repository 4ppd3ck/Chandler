#!/bin/bash

# SSH key toolbox: audit existing keys, generate new ones (interactive key
# type / size / comment / location pickers), and purge stale known_hosts.
#
#   ssh_key_audit.sh                     list keys (+ hint)
#   ssh_key_audit.sh gen | generate      generate a new key (interactive)
#   ssh_key_audit.sh purge-hosts         purge stale known_hosts entries

set -euo pipefail

SSH_DIR="${HOME}/.ssh"

pick() {
  local title="$1" prompt="$2"
  shift 2
  local tui rc=0 out
  tui="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/tui_select.sh"
  if [[ -f "$tui" ]]; then
    out=$(printf '%s\n' "$@" | bash "$tui" --title "$title" --prompt "$prompt") || rc=$?
    if (( rc == 0 )); then
      [[ -n "$out" ]] && printf '%s' "$out"
      return 0
    fi
    return 1
  fi
  # Fallback: numbered prompt straight on the terminal.
  if ! { exec 8<>/dev/tty; } 2>/dev/null; then
    return 1
  fi
  local -a items=("$@")
  local count=$#
  local i n=1
  for i in "${items[@]}"; do printf '%3d) %s\n' "$n" "$i" >&8; n=$(( n + 1 )); done
  printf '#: ' >&8
  local choice sel=""
  IFS= read -r choice <&8 || { exec 8>&- 8<&-; return 1; }
  exec 8>&- 8<&-
  if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= count )); then
    sel="${items[$(( choice - 1 ))]}"
    [[ -n "$sel" ]] && printf '%s' "$sel" && return 0
  fi
  return 1
}

list_keys() {
  if [[ ! -d "$SSH_DIR" ]]; then
    echo "No ~/.ssh directory."
    return 0
  fi
  echo "=== SSH keys in $SSH_DIR ==="
  printf "%-30s %-12s %s\n" "FILE" "TYPE" "FINGERPRINT"
  while IFS= read -r -d '' f; do
    base=$(basename "$f")
    [[ "$base" == *.pub ]] && continue
    [[ "$base" == known_hosts* || "$base" == config || "$base" == authorized_keys ]] && continue
    fp=$(ssh-keygen -lf "$f" 2>/dev/null | head -1 | awk '{print $1, $2}')
    printf "%-30s %-12s %s\n" "$base" "${fp%% *}" "${fp#* }"
  done < <(find "$SSH_DIR" -maxdepth 1 -type f -print0)

  echo ""
  local kh="$SSH_DIR/known_hosts"
  if [[ -f "$kh" ]]; then
    echo "=== known_hosts entries: $(wc -l < "$kh") line(s) ==="
  else
    echo "=== known_hosts entries: 0 line(s) ==="
  fi
}

purge_hosts() {
  if [[ ! -f "$SSH_DIR/known_hosts" ]]; then
    echo "No known_hosts file."
    return 0
  fi
  local before after
  before=$(wc -l < "$SSH_DIR/known_hosts" 2>/dev/null)
  cp "$SSH_DIR/known_hosts" "$SSH_DIR/known_hosts.bkp"
  ssh-keygen -H -f "$SSH_DIR/known_hosts" >/dev/null 2>&1 || true
  after=$(wc -l < "$SSH_DIR/known_hosts" 2>/dev/null)
  echo "Purged/hashed. Before: $before lines, after: $after lines."
  echo "Backup at: $SSH_DIR/known_hosts.bkp"
}

gen_key() {
  mkdir -p "$SSH_DIR"

  # --- 1. key type -----------------------------------------------------
  local type_sel type
  type_sel=$(pick "ssh-keygen | pick a key type" "type> " \
    "ed25519 — modern, fast, small keys (recommended)" \
    "ecdsa   — NIST P-256 / P-384 / P-521" \
    "rsa     — widest compatibility" \
    "dsa     — legacy (1024-bit only)") || { echo "Cancelled."; return 1; }
  case "$type_sel" in
    ed25519*) type="ed25519" ;;
    ecdsa*)   type="ecdsa"   ;;
    rsa*)     type="rsa"     ;;
    dsa*)     type="dsa"     ;;
    *) echo "Cancelled."; return 1 ;;
  esac

  # --- 2. key size (only where it applies) ------------------------------
  local bits=""
  case "$type" in
    rsa)
      bits=$(pick "rsa | pick the key size" "bits> " 2048 3072 4096) || { echo "Cancelled."; return 1; }
      ;;
    ecdsa)
      bits=$(pick "ecdsa | pick the curve size" "bits> " 256 384 521) || { echo "Cancelled."; return 1; }
      ;;
    *)
      ;;
  esac

  # --- 3. comment (empty = no comment) ----------------------------------
  local comment=""
  read -r -p "Key comment (empty = no comment): " comment || comment=""

  # --- 4. file name (cannot be empty) ------------------------------------
  local fname=""
  while [[ -z "$fname" ]]; do
    read -r -p "Key file name (e.g. id_ed25519_github): " fname || return 1
    if [[ -z "$fname" ]]; then
      echo "File name cannot be empty — try again (Ctrl+C to cancel)."
    elif [[ "$fname" == */* ]]; then
      echo "File name must not contain '/' — it is a name, not a path."
      fname=""
    fi
  done

  # --- 5. directory (empty = ~/.ssh) --------------------------------------
  local dir
  read -r -p "Directory (empty = $SSH_DIR): " dir || dir=""
  dir="${dir/#\~/$HOME}"
  [[ -z "$dir" ]] && dir="$SSH_DIR"
  mkdir -p "$dir"

  local key_path="$dir/$fname"
  if [[ -e "$key_path" || -e "$key_path.pub" ]]; then
    local ans
    read -r -p "$key_path already exists. Overwrite? [y/N] " ans || ans=""
    [[ "$ans" == "y" || "$ans" == "Y" ]] || { echo "Aborted — existing key kept."; return 1; }
  fi

  # --- 6. generate ---------------------------------------------------------
  local -a args=(-t "$type")
  [[ -n "$bits" ]] && args+=(-b "$bits")
  args+=(-C "$comment")     # empty string = no comment (skips user@host default)
  args+=(-f "$key_path")

  echo ""
  echo ">>> ssh-keygen ${args[*]}"
  echo "(you will be asked for a passphrase; empty = no passphrase)"
  if ! ssh-keygen "${args[@]}"; then
    echo "Key generation cancelled or failed."
    return 1
  fi

  echo ""
  echo "=== Generated key ==="
  echo "Private : $key_path"
  echo "Public  : $key_path.pub"
  echo "Public key line (for authorized_keys / GitHub / git servers):"
  cat "$key_path.pub"
  ssh-keygen -lf "$key_path" 2>/dev/null || true
  echo ""
  echo "Tip: ssh-add $key_path   # load into the agent"
}

case "${1:-}" in
  ""|list)          list_keys; echo "(tip: '$0 gen' generates a new key)" ;;
  gen|generate)     gen_key ;;
  purge-hosts)      purge_hosts ;;
  -h|--help|help)   sed -n '2,/^$/p' "$0" | sed 's/^# \{0,1\}//' ;;
  *) echo "Usage: $0 [list|gen|purge-hosts]"; exit 1 ;;
esac
