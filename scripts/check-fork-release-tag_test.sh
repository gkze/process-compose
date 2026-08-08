#!/usr/bin/env bash

set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/process-compose-release-tag-test.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

mkdir -p "$test_root/scripts"
ln -s "$project_root/scripts/check-fork-release-tag.sh" "$test_root/scripts/check-fork-release-tag.sh"

git -C "$test_root" init --quiet --object-format=sha1
git -C "$test_root" config user.name "Process Compose Release Test"
git -C "$test_root" config user.email "release-test@example.invalid"

commit_index=0
commit_fixture() {
  local message="$1"
  local timestamp
  commit_index=$((commit_index + 1))
  printf -v timestamp '2000-01-01T00:00:%02dZ' "$commit_index"
  GIT_AUTHOR_DATE="$timestamp" GIT_COMMITTER_DATE="$timestamp" \
    git -C "$test_root" commit --quiet --no-gpg-sign --allow-empty --message "$message"
}

write_default_nix() {
  local version="$1"
  printf '{ }:\n{\n  version = "%s";\n}\n' "$version" >"$test_root/default.nix"
}

write_upstream_base() {
  local tag="$1"
  local commit="$2"
  printf 'tag=%s\ncommit=%s\n' "$tag" "$commit" >"$test_root/.upstream-base"
}

create_release_ref() {
  local ref="$1"
  local parent="$2"
  local version="$3"
  local recorded_tag="$4"
  local recorded_commit="$5"
  git -C "$test_root" checkout --quiet --detach "$parent"
  write_default_nix "$version"
  write_upstream_base "$recorded_tag" "$recorded_commit"
  git -C "$test_root" add default.nix .upstream-base
  commit_fixture "$ref"
  git -C "$test_root" update-ref "$ref" HEAD
}

expect_success() {
  local description="$1"
  shift
  if ! "$@" >"$test_root/case-output.txt" 2>&1; then
    echo "$description failed unexpectedly:" >&2
    cat "$test_root/case-output.txt" >&2
    exit 1
  fi
}

expect_failure() {
  local description="$1"
  local expected="$2"
  shift 2
  if "$@" >"$test_root/case-output.txt" 2>&1; then
    echo "$description was accepted unexpectedly" >&2
    exit 1
  fi
  if ! grep -F "$expected" "$test_root/case-output.txt" >/dev/null; then
    echo "$description failed for an unexpected reason:" >&2
    cat "$test_root/case-output.txt" >&2
    exit 1
  fi
}

policy="$test_root/scripts/check-fork-release-tag.sh"
valid_tag="v1.120.0+gkze1"
valid_version="1.120.0+gkze1"
upstream_tag_ref="refs/tags/v1.120.0"
upstream_main_ref="refs/remotes/upstream/main"

write_default_nix "1.120.0"
git -C "$test_root" add default.nix
commit_fixture "upstream release tag"
upstream_tag_commit="$(git -C "$test_root" rev-parse HEAD)"
GIT_COMMITTER_DATE="2000-01-01T00:00:10Z" \
  git -C "$test_root" tag --annotate --message "upstream release" v1.120.0

commit_fixture "recorded upstream base"
recorded_base_commit="$(git -C "$test_root" rev-parse HEAD)"
commit_fixture "upstream main after recorded base"
upstream_main_commit="$(git -C "$test_root" rev-parse HEAD)"
git -C "$test_root" update-ref "$upstream_main_ref" "$upstream_main_commit"

create_release_ref refs/heads/release-valid "$recorded_base_commit" \
  "$valid_version" v1.120.0 "$recorded_base_commit"

create_release_ref refs/heads/release-version-mismatch "$recorded_base_commit" \
  1.120.0+gkze2 v1.120.0 "$recorded_base_commit"
create_release_ref refs/heads/release-tag-mismatch "$recorded_base_commit" \
  "$valid_version" v1.119.0 "$recorded_base_commit"
create_release_ref refs/heads/release-malformed-commit "$recorded_base_commit" \
  "$valid_version" v1.120.0 not-a-commit
create_release_ref refs/heads/release-unresolved-commit "$recorded_base_commit" \
  "$valid_version" v1.120.0 ffffffffffffffffffffffffffffffffffffffff
create_release_ref refs/heads/release-wrong-ancestry "$upstream_tag_commit" \
  "$valid_version" v1.120.0 "$recorded_base_commit"
create_release_ref refs/heads/release-advanced-merge-base "$upstream_main_commit" \
  "$valid_version" v1.120.0 "$recorded_base_commit"

git -C "$test_root" checkout --quiet --detach "$upstream_tag_commit"
commit_fixture "alternate recorded base"
alternate_base_commit="$(git -C "$test_root" rev-parse HEAD)"
create_release_ref refs/heads/release-base-off-main "$alternate_base_commit" \
  "$valid_version" v1.120.0 "$alternate_base_commit"

empty_tree="$(git -C "$test_root" mktree </dev/null)"
orphan_base_commit="$(
  printf 'orphan recorded base\n' | \
    GIT_AUTHOR_DATE="2000-01-01T00:01:00Z" \
    GIT_COMMITTER_DATE="2000-01-01T00:01:00Z" \
    git -C "$test_root" commit-tree "$empty_tree"
)"
orphan_main_commit="$(
  printf 'orphan upstream main\n' | \
    GIT_AUTHOR_DATE="2000-01-01T00:01:01Z" \
    GIT_COMMITTER_DATE="2000-01-01T00:01:01Z" \
    git -C "$test_root" commit-tree "$empty_tree" -p "$orphan_base_commit"
)"
git -C "$test_root" update-ref refs/remotes/upstream/orphan-main "$orphan_main_commit"
create_release_ref refs/heads/release-tag-not-ancestor "$orphan_base_commit" \
  "$valid_version" v1.120.0 "$orphan_base_commit"

create_release_ref refs/remotes/upstream/no-divergence-main "$recorded_base_commit" \
  "$valid_version" v1.120.0 "$recorded_base_commit"
no_divergence_commit="$(git -C "$test_root" rev-parse refs/remotes/upstream/no-divergence-main)"

expect_success "valid fork release" \
  "$policy" "$valid_tag" refs/heads/release-valid "$upstream_main_ref" "$upstream_tag_ref"

write_default_nix "9.9.9"
write_upstream_base v9.9.9 ffffffffffffffffffffffffffffffffffffffff
expect_success "release-ref metadata independent of mutable worktree" \
  "$policy" "$valid_tag" refs/heads/release-valid "$upstream_main_ref" "$upstream_tag_ref"

expect_failure "malformed fork tag" \
  "must match vMAJOR.MINOR.PATCH+gkzeN" \
  "$policy" v1.120.0-gkze.1 refs/heads/release-valid "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "zero fork revision" \
  "must match vMAJOR.MINOR.PATCH+gkzeN" \
  "$policy" v1.120.0+gkze0 refs/heads/release-valid "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "default.nix version mismatch" \
  "does not match default.nix version 1.120.0+gkze2" \
  "$policy" "$valid_tag" refs/heads/release-version-mismatch "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "recorded upstream tag mismatch" \
  "Recorded upstream tag v1.119.0 does not match fork tag base v1.120.0" \
  "$policy" "$valid_tag" refs/heads/release-tag-mismatch "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "malformed recorded upstream commit" \
  "must be a full 40-character lowercase hexadecimal commit" \
  "$policy" "$valid_tag" refs/heads/release-malformed-commit "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "unresolved recorded upstream commit" \
  "Could not resolve recorded upstream commit" \
  "$policy" "$valid_tag" refs/heads/release-unresolved-commit "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "release with wrong ancestry" \
  "is not based on recorded upstream commit" \
  "$policy" "$valid_tag" refs/heads/release-wrong-ancestry "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "recorded base outside upstream main" \
  "is not on upstream main" \
  "$policy" "$valid_tag" refs/heads/release-base-off-main "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "upstream tag outside recorded base ancestry" \
  "is not an ancestor of recorded upstream commit" \
  "$policy" "$valid_tag" refs/heads/release-tag-not-ancestor refs/remotes/upstream/orphan-main "$upstream_tag_ref"
expect_failure "advanced merge base" \
  "diverges from upstream main at $upstream_main_commit, want recorded upstream commit $recorded_base_commit" \
  "$policy" "$valid_tag" refs/heads/release-advanced-merge-base "$upstream_main_ref" "$upstream_tag_ref"
expect_failure "release without fork divergence" \
  "contains no commits outside upstream main" \
  "$policy" "$valid_tag" "$no_divergence_commit" refs/remotes/upstream/no-divergence-main "$upstream_tag_ref"

echo "Fork release provenance policy accepted the valid topology and rejected every invalid topology."
