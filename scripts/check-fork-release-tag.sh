#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

tag="${1:?usage: check-fork-release-tag.sh TAG [RELEASE_REF [UPSTREAM_MAIN_REF [UPSTREAM_TAG_REF]]]}"
release_ref="${2:-HEAD}"
upstream_main_ref="${3:-upstream/main}"

if [[ ! "$tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)[+]gkze([1-9][0-9]*)$ ]]; then
  echo "Fork release tag must match vMAJOR.MINOR.PATCH+gkzeN with N greater than zero: $tag" >&2
  exit 1
fi

base_version="${BASH_REMATCH[1]}"
fork_version="${tag#v}"
if ! release_commit="$(git -C "$repository_root" rev-parse --verify "$release_ref^{commit}" 2>/dev/null)"; then
  echo "Could not resolve release ref $release_ref" >&2
  exit 1
fi

if ! default_nix="$(git -C "$repository_root" show "$release_commit:default.nix" 2>/dev/null)"; then
  echo "Could not read default.nix from release ref $release_ref" >&2
  exit 1
fi
nix_version="$(printf '%s\n' "$default_nix" | sed -nE 's/^[[:space:]]*version = "([^"]+)";$/\1/p')"
if [[ -z "$nix_version" || "$nix_version" == *$'\n'* ]]; then
  echo "Could not read exactly one package version from default.nix at release ref $release_ref" >&2
  exit 1
fi
if [[ "$fork_version" != "$nix_version" ]]; then
  echo "Fork tag version $fork_version does not match default.nix version $nix_version" >&2
  exit 1
fi

if ! upstream_base_metadata="$(git -C "$repository_root" show "$release_commit:.upstream-base" 2>/dev/null)"; then
  echo "Could not read .upstream-base from release ref $release_ref" >&2
  exit 1
fi

metadata_lines=()
while IFS= read -r metadata_line; do
  metadata_lines+=("$metadata_line")
done <<<"$upstream_base_metadata"
if [[ "${#metadata_lines[@]}" -ne 2 || "${metadata_lines[0]}" != tag=* || "${metadata_lines[1]}" != commit=* ]]; then
  echo ".upstream-base at release ref $release_ref must contain exactly tag=... and commit=..." >&2
  exit 1
fi
recorded_tag="${metadata_lines[0]#tag=}"
recorded_commit="${metadata_lines[1]#commit=}"
if [[ -z "$recorded_tag" || -z "$recorded_commit" ]]; then
  echo ".upstream-base at release ref $release_ref must contain non-empty tag and commit values" >&2
  exit 1
fi

base_tag="v$base_version"
if [[ "$recorded_tag" != "$base_tag" ]]; then
  echo "Recorded upstream tag $recorded_tag does not match fork tag base $base_tag" >&2
  exit 1
fi
if [[ ! "$recorded_commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Recorded upstream commit must be a full 40-character lowercase hexadecimal commit: $recorded_commit" >&2
  exit 1
fi
if ! resolved_recorded_commit="$(git -C "$repository_root" rev-parse --verify "$recorded_commit^{commit}" 2>/dev/null)"; then
  echo "Could not resolve recorded upstream commit $recorded_commit" >&2
  exit 1
fi
if [[ "$resolved_recorded_commit" != "$recorded_commit" ]]; then
  echo "Recorded upstream commit $recorded_commit resolved unexpectedly to $resolved_recorded_commit" >&2
  exit 1
fi
if ! upstream_main_commit="$(git -C "$repository_root" rev-parse --verify "$upstream_main_ref^{commit}" 2>/dev/null)"; then
  echo "Could not resolve upstream main ref $upstream_main_ref" >&2
  exit 1
fi
upstream_tag_ref="${4:-$recorded_tag}"
if ! upstream_tag_commit="$(git -C "$repository_root" rev-parse --verify "$upstream_tag_ref^{commit}" 2>/dev/null)"; then
  echo "Could not resolve recorded upstream tag $recorded_tag from $upstream_tag_ref" >&2
  exit 1
fi
if ! git -C "$repository_root" merge-base --is-ancestor "$upstream_tag_commit" "$recorded_commit"; then
  echo "Recorded upstream tag $recorded_tag ($upstream_tag_commit) is not an ancestor of recorded upstream commit $recorded_commit" >&2
  exit 1
fi
if ! git -C "$repository_root" merge-base --is-ancestor "$recorded_commit" "$upstream_main_commit"; then
  echo "Recorded upstream commit $recorded_commit is not on upstream main $upstream_main_ref ($upstream_main_commit)" >&2
  exit 1
fi
if ! git -C "$repository_root" merge-base --is-ancestor "$recorded_commit" "$release_commit"; then
  echo "Release ref $release_ref is not based on recorded upstream commit $recorded_commit" >&2
  exit 1
fi
fork_commit_count="$(git -C "$repository_root" rev-list --count "$upstream_main_commit..$release_commit")"
if [[ "$fork_commit_count" -eq 0 ]]; then
  echo "Release ref $release_ref contains no commits outside upstream main $upstream_main_ref" >&2
  exit 1
fi
merge_base="$(git -C "$repository_root" merge-base "$release_commit" "$upstream_main_commit")"
if [[ "$merge_base" != "$recorded_commit" ]]; then
  echo "Release ref $release_ref diverges from upstream main at $merge_base, want recorded upstream commit $recorded_commit" >&2
  exit 1
fi

echo "Fork release tag $tag matches default.nix version $nix_version and recorded upstream base $recorded_tag ($recorded_commit)."
