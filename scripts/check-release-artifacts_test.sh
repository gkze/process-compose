#!/usr/bin/env bash

set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
checker="$project_root/scripts/check-release-artifacts.sh"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/process-compose-release-artifacts-test.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

expected_artifacts=(
  process-compose_checksums.txt
  process-compose_darwin_amd64.tar.gz
  process-compose_darwin_arm64.tar.gz
  process-compose_linux_386.tar.gz
  process-compose_linux_amd64.tar.gz
  process-compose_linux_arm.tar.gz
  process-compose_linux_arm64.tar.gz
  process-compose_windows_amd64.zip
  process-compose_windows_arm64.zip
)

write_checksum() {
  local filename="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$filename"
  else
    shasum -a 256 "$filename"
  fi
}

create_snapshot() {
  local destination="$1"

  mkdir -p "$destination/process-compose_linux_amd64_v1"
  touch \
    "$destination/artifacts.json" \
    "$destination/config.yaml" \
    "$destination/metadata.json" \
    "$destination/process-compose_linux_amd64_v1/process-compose"
  for artifact in "${expected_artifacts[@]:1}"; do
    printf 'fixture for %s\n' "$artifact" >"$destination/$artifact"
  done
  (
    cd "$destination"
    for artifact in "${expected_artifacts[@]:1}"; do
      write_checksum "$artifact"
    done
  ) >"$destination/process-compose_checksums.txt"
}

exact_dist="$test_root/exact"
create_snapshot "$exact_dist"
"$checker" "$exact_dist"

missing_dist="$test_root/missing"
create_snapshot "$missing_dist"
rm "$missing_dist/process-compose_linux_arm.tar.gz"
if "$checker" "$missing_dist" >"$test_root/missing-output.txt" 2>&1; then
  echo "Snapshot missing an expected archive was accepted" >&2
  exit 1
fi
if ! grep -F "process-compose_linux_arm.tar.gz" "$test_root/missing-output.txt" >/dev/null; then
  echo "Missing archive failure did not identify the missing file" >&2
  exit 1
fi

unexpected_dist="$test_root/unexpected"
create_snapshot "$unexpected_dist"
touch "$unexpected_dist/process-compose_linux_amd64.deb"
if "$checker" "$unexpected_dist" >"$test_root/unexpected-output.txt" 2>&1; then
  echo "Snapshot containing an unexpected artifact was accepted" >&2
  exit 1
fi
if ! grep -F "process-compose_linux_amd64.deb" "$test_root/unexpected-output.txt" >/dev/null; then
  echo "Unexpected artifact failure did not identify the extra file" >&2
  exit 1
fi

missing_checksum_dist="$test_root/missing-checksum"
create_snapshot "$missing_checksum_dist"
grep -v 'process-compose_linux_arm.tar.gz$' \
  "$missing_checksum_dist/process-compose_checksums.txt" \
  >"$missing_checksum_dist/process-compose_checksums.tmp"
mv "$missing_checksum_dist/process-compose_checksums.tmp" "$missing_checksum_dist/process-compose_checksums.txt"
if "$checker" "$missing_checksum_dist" >"$test_root/missing-checksum-output.txt" 2>&1; then
  echo "Checksum manifest missing an expected archive was accepted" >&2
  exit 1
fi
if ! grep -F "process-compose_linux_arm.tar.gz" "$test_root/missing-checksum-output.txt" >/dev/null; then
  echo "Missing checksum failure did not identify the omitted archive" >&2
  exit 1
fi

corrupt_dist="$test_root/corrupt"
create_snapshot "$corrupt_dist"
printf 'corrupt\n' >>"$corrupt_dist/process-compose_linux_amd64.tar.gz"
if "$checker" "$corrupt_dist" >"$test_root/corrupt-output.txt" 2>&1; then
  echo "Archive that does not match its recorded checksum was accepted" >&2
  exit 1
fi

mock_gh="$test_root/gh"
cat >"$mock_gh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == api ]]
cat "${MOCK_GH_RESPONSE:?}"
EOF
chmod +x "$mock_gh"

remote_manifest="$test_root/remote-manifest.txt"
for artifact in "${expected_artifacts[@]}"; do
  digest="$(write_checksum "$exact_dist/$artifact" | awk '{ print $1 }')"
  size="$(wc -c <"$exact_dist/$artifact" | tr -d '[:space:]')"
  printf '%s\tsha256:%s\t%s\n' "$artifact" "$digest" "$size"
done | LC_ALL=C sort >"$remote_manifest"

GITHUB_REPOSITORY=gkze/process-compose \
  GH_BIN="$mock_gh" \
  MOCK_GH_RESPONSE="$remote_manifest" \
  "$checker" "$exact_dist" 12345

corrupt_remote_manifest="$test_root/corrupt-remote-manifest.txt"
awk 'BEGIN { OFS = "\t" } NR == 1 { $2 = "sha256:0000000000000000000000000000000000000000000000000000000000000000" } { print }' \
  "$remote_manifest" >"$corrupt_remote_manifest"
if GITHUB_REPOSITORY=gkze/process-compose \
  GH_BIN="$mock_gh" \
  MOCK_GH_RESPONSE="$corrupt_remote_manifest" \
  "$checker" "$exact_dist" 12345 >"$test_root/corrupt-remote-output.txt" 2>&1; then
  echo "GitHub release asset with the wrong digest was accepted" >&2
  exit 1
fi
if ! grep -F "do not exactly match" "$test_root/corrupt-remote-output.txt" >/dev/null; then
  echo "Remote digest failure did not explain the artifact mismatch" >&2
  exit 1
fi

echo "Exact local and GitHub release artifacts were accepted; missing, unexpected, and corrupt artifacts were rejected."
