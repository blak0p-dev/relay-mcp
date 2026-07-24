#!/usr/bin/env bash
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly INSTALLER="$ROOT/scripts/install.sh"

workspace=""
passed=0

cleanup() { [[ -z "$workspace" ]] || rm -rf "$workspace"; }
pass() { printf 'PASS %s\n' "$1"; passed=$((passed + 1)); }
fail() { printf 'FAIL %s\n' "$1" >&2; exit 1; }
expect_failure() { "$@" >/dev/null 2>&1 && fail "expected failure: $*" || true; }
expect_file_equals() { cmp -s "$1" "$2" || fail "files differ: $1 $2"; }

sha256() { sha256sum "$1" | awk '{print $1}'; }

write_manifest() {
  local manifest="$1" asset="$2" hash
  hash="$(sha256 "$asset")"
  printf '%s  %s\n' "$hash" "$(basename "$asset")" >"$manifest"
}

make_archive() {
  local kind="$1" archive="$release/$asset"
  rm -rf "$stage" && mkdir -p "$stage"
  printf '#!/bin/sh\nprintf relay\n' >"$stage/relay"
  chmod +x "$stage/relay"
  case "$kind" in
    valid) tar -C "$stage" -czf "$archive" relay ;;
    malformed) printf 'not a tar archive' >"$archive" ;;
    traversal) tar -C "$stage" --transform='s,^relay,../relay,' -czf "$archive" relay ;;
    duplicate)
      mkdir -p "$stage/first" "$stage/second"
      cp "$stage/relay" "$stage/first/relay"
      cp "$stage/relay" "$stage/second/relay"
      tar -C "$stage" -czf "$archive" first/relay second/relay
      ;;
  esac
  write_manifest "$release/checksums.txt" "$archive"
}

run_installer() {
  env PATH="$fake_bin:$system_path" HOME="$home" GOBIN="$gobin" \
    RELAY_RELEASE_BASE_URL="file://$release_root" RELAY_PI_CONFIG="${RELAY_PI_CONFIG:-$pi_config}" \
    RELAY_CLIENT_LOG="$client_log" RELAY_REGISTRY="$registry" \
    "$INSTALLER" --version v1.2.3 "$@"
}

assert_prior_binary() { grep -Fx 'prior relay binary' "$gobin/relay" >/dev/null || fail 'existing relay changed'; }
assert_installed_binary() { "$gobin/relay" | grep -Fx relay >/dev/null || fail 'relay was not installed'; }

make_fake_commands() {
  cat >"$fake_bin/uname" <<'EOF'
#!/usr/bin/env bash
case "${1:-}" in -s) printf '%s\n' "${RELAY_TEST_OS:-Linux}" ;; -m) printf '%s\n' "${RELAY_TEST_ARCH:-x86_64}" ;; esac
EOF
  cat >"$fake_bin/client" <<'EOF'
#!/usr/bin/env bash
name="$(basename "$0")"
printf '%s\0' "$name" "$@" >>"${RELAY_CLIENT_LOG:?}"
if [[ "${1:-}" == mcp && "${2:-}" == remove ]]; then rm -f "${RELAY_REGISTRY:?}/$name"; exit 0; fi
if [[ "${1:-}" == mcp && "${2:-}" == add ]]; then printf 'relay\n' >"${RELAY_REGISTRY:?}/$name"; fi
[[ "${RELAY_FAIL_CLIENT:-}" != "$name" ]]
EOF
  cat >"$fake_bin/pi" <<'EOF'
#!/usr/bin/env bash
printf '%s\0' pi "$@" >>"${RELAY_CLIENT_LOG:?}"
[[ "${RELAY_FAIL_CLIENT:-}" != pi ]]
EOF
  chmod +x "$fake_bin/uname" "$fake_bin/client" "$fake_bin/pi"
  for client in claude codex opencode; do ln -s client "$fake_bin/$client"; done
}

setup() {
  cleanup
  workspace="$(mktemp -d)"
  release_root="$workspace/releases"
  release="$release_root/download/v1.2.3"
  home="$workspace/home"
  gobin="$workspace/go/bin"
  stage="$workspace/stage"
  fake_bin="$workspace/fake-bin"
  registry="$workspace/registry"
  client_log="$workspace/client.log"
  pi_config="$workspace/pi/mcp.json"
  asset='relay_1.2.3_linux_amd64.tar.gz'
  system_path="$PATH"
  mkdir -p "$release" "$home" "$gobin" "$fake_bin" "$registry" "$(dirname "$pi_config")"
  printf 'prior relay binary\n' >"$gobin/relay"
  chmod +x "$gobin/relay"
  printf '{"mcpServers":{"unrelated":{"command":"keep-me"}}}\n' >"$pi_config"
  make_fake_commands
  make_archive valid
}

test_requires_installer() {
  [[ -x "$INSTALLER" ]] || fail 'install.sh is missing: this is the expected RED state before Task 2.1'
}

test_install_and_path_guidance() {
  local output
  output="$(run_installer)"
  assert_installed_binary
  grep -F "Add $gobin to PATH" <<<"$output" >/dev/null || fail 'missing PATH guidance'
  pass 'valid archive installs through a unique temporary workspace with PATH guidance'
}

test_platform_and_tag_rejection() {
  if RELAY_TEST_OS=Plan9 run_installer >/dev/null 2>&1; then fail 'unsupported OS unexpectedly installed relay'; fi
  assert_prior_binary
  if RELAY_TEST_ARCH=mips run_installer >/dev/null 2>&1; then fail 'unsupported architecture unexpectedly installed relay'; fi
  assert_prior_binary
  expect_failure "$INSTALLER" --version 'v1.2.3;touch unsafe'
  [[ ! -e "$workspace/unsafe" ]] || fail 'tag metacharacter executed'
  pass 'unsupported platform and unsafe tag reject before replacement'
}

test_integrity_failures_preserve_destination() {
  local prior="$workspace/prior"
  cp "$gobin/relay" "$prior"
  printf '0000000000000000000000000000000000000000000000000000000000000000  %s\n' "$asset" >"$release/checksums.txt"
  expect_failure run_installer; expect_file_equals "$prior" "$gobin/relay"
  : >"$release/checksums.txt"
  expect_failure run_installer; expect_file_equals "$prior" "$gobin/relay"
  write_manifest "$release/checksums.txt" "$release/$asset"
  cp "$release/checksums.txt" "$workspace/duplicate-checksum-entry"
  cat "$workspace/duplicate-checksum-entry" >>"$release/checksums.txt"
  expect_failure run_installer; expect_file_equals "$prior" "$gobin/relay"
  pass 'bad, missing, and duplicate checksums preserve the existing binary'
}

test_archive_failures_preserve_destination() {
  local kind prior="$workspace/prior"
  cp "$gobin/relay" "$prior"
  for kind in malformed traversal duplicate; do
    make_archive "$kind"
    expect_failure run_installer
    expect_file_equals "$prior" "$gobin/relay"
  done
  rm -f "$release/$asset"
  expect_failure run_installer
  expect_file_equals "$prior" "$gobin/relay"
  pass 'malformed, traversal, duplicate, and interrupted downloads preserve the existing binary'
}

test_clients_are_fixed_argv_and_idempotent() {
  make_archive valid
  run_installer >/dev/null
  run_installer >/dev/null
  local client
  for client in claude codex opencode; do
    [[ "$(wc -l <"$registry/$client")" -eq 1 ]] || fail "$client registration is not idempotent"
  done
  tr '\0' '\n' <"$client_log" | grep -Fx "$gobin/relay" >/dev/null || fail 'binary path missing from client argv'
  rm "$fake_bin/codex"
  local output
  output="$(run_installer)"
  grep -F "codex mcp add relay -- $gobin/relay" <<<"$output" >/dev/null || fail 'missing Codex remediation'
  RELAY_FAIL_CLIENT=claude expect_failure run_installer
  tr '\0' '\n' <"$client_log" | grep -Fx opencode >/dev/null || fail 'later clients were not attempted after a failure'
  pass 'clients use fixed argv, skip safely, aggregate failures, and remain idempotent'
}

test_metacharacter_binary_is_data() {
  local sentinel="$workspace/command-must-not-run"
  local unsafe_gobin="$workspace/bin;touch $sentinel"
  mkdir -p "$unsafe_gobin"
  printf 'prior relay binary\n' >"$unsafe_gobin/relay"
  env GOBIN="$unsafe_gobin" PATH="$fake_bin:$system_path" HOME="$home" \
    RELAY_RELEASE_BASE_URL="file://$release_root" RELAY_PI_CONFIG="$pi_config" \
    RELAY_CLIENT_LOG="$client_log" RELAY_REGISTRY="$registry" "$INSTALLER" --version v1.2.3 >/dev/null
  [[ ! -e "$sentinel" ]] || fail 'binary path metacharacter executed'
  pass 'binary paths containing metacharacters remain data'
}

test_pi_merge_is_atomic_and_preserves_entries() {
  make_archive valid
  run_installer >/dev/null
  grep -F '"unrelated"' "$pi_config" >/dev/null || fail 'Pi unrelated entry was removed'
  node -e 'const x=JSON.parse(require("fs").readFileSync(process.argv[1])); if (x.mcpServers.relay.command !== process.argv[2]) process.exit(1)' "$pi_config" "$gobin/relay" || fail 'Pi relay entry missing'
  local before
  before="$(cat "$pi_config")"
  run_installer >/dev/null
  [[ "$before" == "$(cat "$pi_config")" ]] || fail 'Pi repeat changed equivalent configuration'
  local malformed="$workspace/pi/malformed.json"
  printf '{"mcpServers":' >"$malformed"
  local malformed_before
  malformed_before="$(cat "$malformed")"
  RELAY_PI_CONFIG="$malformed" expect_failure run_installer
  [[ "$malformed_before" == "$(cat "$malformed")" ]] || fail 'malformed Pi JSON changed'
  pass 'Pi configuration merges atomically, preserves entries, and leaves malformed JSON unchanged'
}

main() {
  setup
  trap cleanup EXIT
  test_requires_installer
  test_install_and_path_guidance
  setup; test_platform_and_tag_rejection
  setup; test_integrity_failures_preserve_destination
  setup; test_archive_failures_preserve_destination
  setup; test_clients_are_fixed_argv_and_idempotent
  setup; test_metacharacter_binary_is_data
  setup; test_pi_merge_is_atomic_and_preserves_entries
  printf 'Installer harness: %d scenarios passed.\n' "$passed"
}

main "$@"
