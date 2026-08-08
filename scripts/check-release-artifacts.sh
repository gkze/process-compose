#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -lt 1 || "$#" -gt 2 ]]; then
  echo "usage: check-release-artifacts.sh DIST_DIR [GITHUB_RELEASE_ID]" >&2
  exit 2
fi

dist_dir="$1"
github_release_id="${2:-}"
if [[ ! -d "$dist_dir" ]]; then
  echo "GoReleaser output directory does not exist: $dist_dir" >&2
  exit 1
fi

expected_manifest="$(mktemp "${TMPDIR:-/tmp}/process-compose-expected-artifacts.XXXXXX")"
actual_manifest="$(mktemp "${TMPDIR:-/tmp}/process-compose-actual-artifacts.XXXXXX")"
expected_checksum_manifest="$(mktemp "${TMPDIR:-/tmp}/process-compose-expected-checksums.XXXXXX")"
actual_checksum_manifest="$(mktemp "${TMPDIR:-/tmp}/process-compose-actual-checksums.XXXXXX")"
expected_remote_manifest="$(mktemp "${TMPDIR:-/tmp}/process-compose-expected-remote-artifacts.XXXXXX")"
actual_remote_manifest="$(mktemp "${TMPDIR:-/tmp}/process-compose-actual-remote-artifacts.XXXXXX")"
trap 'rm -f -- "$expected_manifest" "$actual_manifest" "$expected_checksum_manifest" "$actual_checksum_manifest" "$expected_remote_manifest" "$actual_remote_manifest"' EXIT

printf '%s\n' \
  process-compose_checksums.txt \
  process-compose_darwin_amd64.tar.gz \
  process-compose_darwin_arm64.tar.gz \
  process-compose_linux_386.tar.gz \
  process-compose_linux_amd64.tar.gz \
  process-compose_linux_arm.tar.gz \
  process-compose_linux_arm64.tar.gz \
  process-compose_windows_amd64.zip \
  process-compose_windows_arm64.zip \
  >"$expected_manifest"

find "$dist_dir" -mindepth 1 -maxdepth 1 -type f \
  ! -name artifacts.json \
  ! -name config.yaml \
  ! -name metadata.json \
  -exec basename {} \; \
  | LC_ALL=C sort >"$actual_manifest"

if ! diff -u "$expected_manifest" "$actual_manifest"; then
  echo "GoReleaser output does not contain exactly the approved release artifacts." >&2
  exit 1
fi

tail -n +2 "$expected_manifest" >"$expected_checksum_manifest"
checksum_file="$dist_dir/process-compose_checksums.txt"
if ! awk '
  NF != 2 || length($1) != 64 || $1 ~ /[^0-9a-f]/ { exit 1 }
  { print $2 }
' "$checksum_file" | LC_ALL=C sort >"$actual_checksum_manifest"; then
  echo "GoReleaser checksum manifest is malformed." >&2
  exit 1
fi
if ! diff -u "$expected_checksum_manifest" "$actual_checksum_manifest"; then
  echo "GoReleaser checksum manifest does not list exactly the eight approved archives." >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  hash_command=(sha256sum)
  checksum_command=(sha256sum --check)
elif command -v shasum >/dev/null 2>&1; then
  hash_command=(shasum -a 256)
  checksum_command=(shasum -a 256 --check)
else
  echo "Neither sha256sum nor shasum is available to verify release checksums." >&2
  exit 1
fi
if ! (cd "$dist_dir" && "${checksum_command[@]}" process-compose_checksums.txt); then
  echo "One or more release archives do not match the checksum manifest." >&2
  exit 1
fi

if [[ -n "$github_release_id" ]]; then
  : "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required when checking GitHub release assets}"
  gh_bin="${GH_BIN:-gh}"
  while IFS= read -r artifact; do
    digest="$("${hash_command[@]}" "$dist_dir/$artifact" | awk '{ print $1 }')"
    size="$(wc -c <"$dist_dir/$artifact" | tr -d '[:space:]')"
    printf '%s\tsha256:%s\t%s\n' "$artifact" "$digest" "$size"
  done <"$expected_manifest" | LC_ALL=C sort >"$expected_remote_manifest"

  "$gh_bin" api --paginate \
    "repos/$GITHUB_REPOSITORY/releases/$github_release_id/assets?per_page=100" \
    --jq '.[] | [.name, (.digest // ""), (.size | tostring)] | @tsv' \
    | LC_ALL=C sort >"$actual_remote_manifest"

  if ! diff -u "$expected_remote_manifest" "$actual_remote_manifest"; then
    echo "GitHub release assets do not exactly match the locally verified artifact digests and sizes." >&2
    exit 1
  fi
  echo "GitHub release assets exactly match the locally verified artifact digests and sizes."
fi

echo "GoReleaser output contains exactly the nine approved files and eight verified archive checksums."
