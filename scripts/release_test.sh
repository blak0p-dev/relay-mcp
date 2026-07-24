#!/usr/bin/env bash
# Release snapshot contract harness. Phase 3 supplies GoReleaser configuration.
set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly RELEASE_CONFIG="$ROOT/.goreleaser.yaml"
readonly RELEASE_WORKFLOW="$ROOT/.github/workflows/release.yml"
readonly CI_WORKFLOW="$ROOT/.github/workflows/ci.yml"
readonly VERSION="${RELAY_RELEASE_TEST_VERSION:-1.2.3}"

passed=0
failed=0

pass() {
  printf 'RED  %s\n' "$1"
  passed=$((passed + 1))
}

fail() {
  printf 'FAIL %s\n' "$1" >&2
  failed=$((failed + 1))
}

expected_assets() {
  printf 'relay_%s_linux_amd64.tar.gz\n' "$VERSION"
  printf 'relay_%s_linux_arm64.tar.gz\n' "$VERSION"
  printf 'relay_%s_darwin_amd64.tar.gz\n' "$VERSION"
  printf 'relay_%s_darwin_arm64.tar.gz\n' "$VERSION"
  printf 'relay_%s_windows_amd64.zip\n' "$VERSION"
  printf 'relay_%s_windows_arm64.zip\n' "$VERSION"
}

run_red() {
  if [[ -e "$RELEASE_CONFIG" ]]; then
    fail 'expected Phase 3 .goreleaser.yaml to be absent while release assertions are introduced'
    return
  fi

  local asset
  while IFS= read -r asset; do
    pass "snapshot requires exact asset $asset"
  done < <(expected_assets)
  pass 'snapshot requires checksums.txt to contain exactly one checksum for every exact asset'
}

verify_snapshot() {
  local dist="${RELAY_RELEASE_DIST:-$ROOT/dist}"
  [[ -f "$RELEASE_CONFIG" ]] || { printf 'missing %s\n' "$RELEASE_CONFIG" >&2; return 1; }
  [[ -f "$dist/checksums.txt" ]] || { printf 'missing %s/checksums.txt\n' "$dist" >&2; return 1; }

  local asset matches
  while IFS= read -r asset; do
    [[ -f "$dist/$asset" ]] || { printf 'missing asset %s\n' "$asset" >&2; return 1; }
    matches="$(grep -F -c "  $asset" "$dist/checksums.txt" || true)"
    [[ "$matches" == '1' ]] || { printf 'expected exactly one checksum for %s, found %s\n' "$asset" "$matches" >&2; return 1; }
  done < <(expected_assets)

  local archive_count
  archive_count="$(find "$dist" -maxdepth 1 -type f \( -name 'relay_*.tar.gz' -o -name 'relay_*.zip' \) | wc -l | tr -d ' ')"
  [[ "$archive_count" == '6' ]] || { printf 'expected six release archives, found %s\n' "$archive_count" >&2; return 1; }
  (cd "$dist" && sha256sum -c checksums.txt)
}

require_line() {
  local file="$1"
  local value="$2"
  grep -F -q -- "$value" "$file" || {
    printf 'missing %q in %s\n' "$value" "$file" >&2
    return 1
  }
}

job_block() {
  local job="$1"
  awk -v job="$job" '
    $0 == "  " job ":" { in_job = 1; next }
    in_job && $0 ~ /^  [A-Za-z0-9_-]+:$/ { exit }
    in_job { print }
  ' "$RELEASE_WORKFLOW"
}

require_job_line() {
  local job="$1"
  local value="$2"
  local block
  block="$(job_block "$job")"
  [[ -n "$block" ]] || {
    printf 'missing job %q in %s\n' "$job" "$RELEASE_WORKFLOW" >&2
    return 1
  }
  grep -F -q -- "$value" <<<"$block" || {
    printf 'missing %q in job %q in %s\n' "$value" "$job" "$RELEASE_WORKFLOW" >&2
    return 1
  }
}

forbid_job_line() {
  local job="$1"
  local value="$2"
  local block
  block="$(job_block "$job")"
  [[ -n "$block" ]] || {
    printf 'missing job %q in %s\n' "$job" "$RELEASE_WORKFLOW" >&2
    return 1
  }
  if grep -F -q -- "$value" <<<"$block"; then
    printf 'unexpected %q in job %q in %s\n' "$value" "$job" "$RELEASE_WORKFLOW" >&2
    return 1
  fi
}

verify_release_prerequisites() {
  require_job_line 'windows-installer' 'runs-on: windows-latest' || return 1
  require_job_line 'windows-installer' './scripts/install_test.ps1' || return 1
  require_job_line 'release' 'needs: windows-installer' || return 1
  forbid_job_line 'release' './scripts/install_test.ps1'
}

verify_release_config() {
  [[ -f "$RELEASE_CONFIG" ]] || { printf 'missing %s\n' "$RELEASE_CONFIG" >&2; return 1; }
  [[ -f "$RELEASE_WORKFLOW" ]] || { printf 'missing %s\n' "$RELEASE_WORKFLOW" >&2; return 1; }

  local config_required=(
    'version: 2'
    'project_name: relay'
    'main: ./cmd/relay-mcp'
    'binary: relay'
    'CGO_ENABLED=0'
    '- -trimpath'
    '- -s -w -X github.com/blak0p/relay-mcp/internal/server/description.ServerVersion={{ .Version }}'
    '- linux'
    '- darwin'
    '- windows'
    '- amd64'
    '- arm64'
    'name_template: "relay_{{ .Version }}_{{ .Os }}_{{ .Arch }}"'
    'name_template: "checksums.txt"'
  )
  local value
  for value in "${config_required[@]}"; do
    require_line "$RELEASE_CONFIG" "$value" || return 1
  done

  local workflow_required=(
    'tags:'
    "- 'v*'"
    'contents: write'
    'fetch-depth: 0'
    'go vet ./...'
    'go test ./...'
    'go test -race ./...'
    'bash scripts/release_test.sh'
    'goreleaser/goreleaser-action@v6'
    "version: '~> v2'"
    'args: release --clean'
  )
  for value in "${workflow_required[@]}"; do
    require_line "$RELEASE_WORKFLOW" "$value" || return 1
  done

  verify_release_prerequisites
}

verify_ci_config() {
  [[ -f "$CI_WORKFLOW" ]] || { printf 'missing %s\n' "$CI_WORKFLOW" >&2; return 1; }

  local required=(
    'pull_request:'
    'push:'
    'contents: read'
    'actions/checkout@v6'
    'actions/setup-go@v5'
    'go vet ./...'
    'go test -race -shuffle=on -count=1 ./...'
    'bash scripts/install_test.sh'
    'windows-installer:'
    'runs-on: windows-latest'
    'Test Windows installer runtime'
    'shell: pwsh'
    './scripts/install_test.ps1'
    'bash scripts/release_test.sh --release-config'
  )
  local value
  for value in "${required[@]}"; do
    require_line "$CI_WORKFLOW" "$value" || return 1
  done
}

main() {
  case "${1:-}" in
    --red)
      [[ "$#" -eq 1 ]] || { printf 'usage: %s [--red]\n' "${0##*/}" >&2; return 2; }
      run_red
      printf 'RED harness: %d expected failing release contracts captured; %d harness failures.\n' "$passed" "$failed"
      (( failed == 0 ))
      ;;
    '')
      verify_snapshot
      ;;
    --release-config)
      [[ "$#" -eq 1 ]] || { printf 'usage: %s [--red|--release-config]\n' "${0##*/}" >&2; return 2; }
      verify_release_config
      ;;
    --ci-config)
      [[ "$#" -eq 1 ]] || { printf 'usage: %s [--red|--release-config|--ci-config]\n' "${0##*/}" >&2; return 2; }
      verify_ci_config
      ;;
    *)
      printf 'usage: %s [--red|--release-config|--ci-config]\n' "${0##*/}" >&2
      return 2
      ;;
  esac
}

main "$@"
