#!/usr/bin/env bash
# Install a verified Relay release without elevated privileges.
set -euo pipefail

readonly REPOSITORY='blak0p-dev/relay-mcp'
readonly DEFAULT_RELEASE_BASE_URL="https://github.com/$REPOSITORY/releases"
readonly DEFAULT_API_URL="https://api.github.com/repos/$REPOSITORY/releases/latest"

version=''
workspace=''
stage_dir=''
client_failures=0

cleanup() {
  [[ -z "$stage_dir" ]] || rm -rf "$stage_dir"
  [[ -z "$workspace" ]] || rm -rf "$workspace"
}

die() { printf 'relay installer: %s\n' "$*" >&2; exit 1; }

usage() {
  printf 'Usage: %s [--version vX.Y.Z]\n' "${0##*/}"
}

resolve_version() {
  if [[ -n "$version" ]]; then
    return
  fi

  local response
  response="$(curl --fail --silent --show-error --location "${RELAY_RELEASE_API_URL:-$DEFAULT_API_URL}")" \
    || die 'could not resolve the latest release; pass --version vX.Y.Z to retry'
  version="$(printf '%s' "$response" | tr -d '\n' | sed -nE 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p')"
  [[ -n "$version" ]] || die 'latest release response did not contain a tag_name'
}

validate_version() {
  [[ "$version" =~ ^v[0-9][0-9A-Za-z._-]*$ ]] || die "invalid release tag: $version"
}

platform() {
  local detected_os detected_arch
  detected_os="$(uname -s)"
  detected_arch="$(uname -m)"
  case "$detected_os" in
    Linux) os='linux' ;;
    Darwin) os='darwin' ;;
    *) die "unsupported operating system: $detected_os" ;;
  esac
  case "$detected_arch" in
    x86_64|amd64) arch='amd64' ;;
    arm64|aarch64) arch='arm64' ;;
    *) die "unsupported architecture: $detected_arch" ;;
  esac
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die 'SHA-256 utility not found (need sha256sum or shasum)'
  fi
}

download() {
  curl --fail --silent --show-error --location --proto '=https,file' "$1" --output "$2"
}

verify_checksum() {
  local manifest="$1" archive="$2" asset_name expected actual matches
  asset_name="$(basename "$archive")"
  matches="$(awk -v name="$asset_name" '$2 == name && length($1) == 64 && $1 ~ /^[0-9A-Fa-f]+$/ { print tolower($1) }' "$manifest")"
  [[ "$(printf '%s\n' "$matches" | sed '/^$/d' | wc -l | tr -d ' ')" == 1 ]] \
    || die "checksum manifest must contain exactly one valid entry for $asset_name"
  expected="$(printf '%s\n' "$matches" | sed '/^$/d')"
  actual="$(sha256_file "$archive" | tr '[:upper:]' '[:lower:]')"
  [[ "$actual" == "$expected" ]] || die "SHA-256 mismatch for $asset_name"
}

extract_binary() {
  local archive="$1" member count
  tar -tzf "$archive" >/dev/null 2>&1 || die "invalid archive: $(basename "$archive")"
  while IFS= read -r member; do
    [[ "$member" != /* && "$member" != '..' && "$member" != *'../'* ]] \
      || die "unsafe archive member: $member"
  done < <(tar -tzf "$archive")
  count="$(tar -tzf "$archive" | awk '$0 == "relay" { count++ } END { print count + 0 }')"
  [[ "$count" == 1 ]] || die 'archive must contain exactly one relay binary member'
  tar -xzf "$archive" -C "$stage_dir" relay >/dev/null 2>&1 || die 'could not extract relay from archive'
  [[ -f "$stage_dir/relay" ]] || die 'archive relay member was not a regular file'
  chmod 755 "$stage_dir/relay"
}

is_path_entry() {
  case ":${PATH:-}:" in *":$1:"*) return 0 ;; *) return 1 ;; esac
}

missing_client() {
  local command="$1"
  printf 'Skipped %s (not found). Run: %s\n' "$2" "$command"
}

configure_client() {
  local client="$1" remove_scope="$2" add_scope="$3" remediation="$4"
  if ! command -v "$client" >/dev/null 2>&1; then
    missing_client "$remediation" "$client"
    return
  fi

  if [[ "$remove_scope" == 'user' ]]; then
    "$client" mcp remove --scope user relay >/dev/null 2>&1 || true
  else
    "$client" mcp remove relay >/dev/null 2>&1 || true
  fi
  if [[ "$add_scope" == 'user' ]]; then
    "$client" mcp add --scope user relay -- "$binary" || client_failures=1
  else
    "$client" mcp add relay -- "$binary" || client_failures=1
  fi
}

configure_pi_json() {
  local config="$1"
  command -v node >/dev/null 2>&1 || { printf 'Pi setup needs Node.js to merge %s\n' "$config" >&2; return 1; }
  node - "$config" "$binary" <<'NODE'
const fs = require('fs');
const path = require('path');
const [config, binary] = process.argv.slice(2);
let document = { mcpServers: {} };
if (fs.existsSync(config)) document = JSON.parse(fs.readFileSync(config, 'utf8'));
if (document === null || Array.isArray(document) || typeof document !== 'object') throw new Error('Pi configuration must be an object');
if (document.mcpServers === undefined) document.mcpServers = {};
if (document.mcpServers === null || Array.isArray(document.mcpServers) || typeof document.mcpServers !== 'object') throw new Error('mcpServers must be an object');
document.mcpServers.relay = { command: binary };
fs.mkdirSync(path.dirname(config), { recursive: true });
const temporary = path.join(path.dirname(config), `.${path.basename(config)}.${process.pid}.${Date.now()}`);
fs.writeFileSync(temporary, `${JSON.stringify(document, null, 2)}\n`, { mode: 0o600 });
fs.renameSync(temporary, config);
NODE
}

configure_pi() {
  local config="${RELAY_PI_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/mcp/mcp.json}"
  if ! command -v pi >/dev/null 2>&1; then
    printf 'Skipped Pi (not found). Run: pi install npm:pi-mcp-adapter\n'
    return
  fi
  pi install npm:pi-mcp-adapter || client_failures=1
  configure_pi_json "$config" || client_failures=1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -eq 2 ]] || { usage >&2; exit 2; }
      version="$2"
      shift 2
      ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; exit 2 ;;
  esac
done

trap cleanup EXIT
resolve_version
validate_version
platform

asset="relay_${version}_${os}_${arch}.tar.gz"
release_base_url="${RELAY_RELEASE_BASE_URL:-$DEFAULT_RELEASE_BASE_URL}"
workspace="$(mktemp -d "${TMPDIR:-/tmp}/relay-install.XXXXXX")" || die 'could not create a temporary workspace'
archive="$workspace/$asset"
manifest="$workspace/checksums.txt"

download "$release_base_url/download/$version/$asset" "$archive" || die "could not download $asset"
download "$release_base_url/download/$version/checksums.txt" "$manifest" || die 'could not download checksums.txt'
verify_checksum "$manifest" "$archive"

destination_dir="${GOBIN:-${HOME:?}/go/bin}"
mkdir -p "$destination_dir"
stage_dir="$(mktemp -d "$destination_dir/.relay-stage.XXXXXX")" || die 'could not stage the relay binary'
extract_binary "$archive"
binary="$destination_dir/relay"
mv -f "$stage_dir/relay" "$binary"
rm -rf "$stage_dir"
stage_dir=''
printf 'Installed relay to %s\n' "$binary"
if ! is_path_entry "$destination_dir"; then
  printf 'Add %s to PATH to run relay from a new shell.\n' "$destination_dir"
fi

configure_client claude user user "claude mcp add --scope user relay -- $binary"
configure_client codex none none "codex mcp add relay -- $binary"
configure_client opencode none none "opencode mcp add relay -- $binary"
configure_pi

(( client_failures == 0 )) || die 'one or more detected client setups failed; the verified relay binary remains installed'
