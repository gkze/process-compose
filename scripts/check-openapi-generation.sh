#!/usr/bin/env bash

set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
generator="${1:?usage: check-openapi-generation.sh GENERATOR EXPECTED_VERSION}"
expected_version="${2:?usage: check-openapi-generation.sh GENERATOR EXPECTED_VERSION}"

if [[ ! -x "$generator" ]]; then
  echo "OpenAPI generator is not executable: $generator" >&2
  exit 1
fi

actual_version="$({ go version -m "$generator" || true; } | awk '$1 == "mod" && $2 == "github.com/zxmfke/swagger2openapi3" { print $3 }')"
if [[ "$actual_version" != "$expected_version" ]]; then
  echo "OpenAPI generator module version is $actual_version, want $expected_version" >&2
  exit 1
fi

temporary_root="$(mktemp -d "${TMPDIR:-/tmp}/process-compose-openapi.XXXXXX")"
cleanup() {
  rm -rf -- "$temporary_root"
}
trap cleanup EXIT

generate() {
  local output_directory="$1"
  mkdir -p "$output_directory"
  (
    cd "$repository_root"
    "$generator" init \
      --dir src \
      --output "$output_directory" \
      -g api/pc_api.go \
      --openapiOutputDir "$output_directory" \
      --parseDependency \
      --parseInternal
    go run ./scripts/postprocess-openapi.go "$output_directory"
  )
}

compare() {
  local expected="$1"
  local actual="$2"
  if cmp -s "$expected" "$actual"; then
    return
  fi
  echo "Generated OpenAPI artifact differs: $expected versus $actual" >&2
  diff -u "$expected" "$actual" || true
  return 1
}

first="$temporary_root/first/docs"
second="$temporary_root/second/docs"
generate "$first"
generate "$second"

for artifact in docs.go swagger.json swagger.yaml; do
  compare "$first/$artifact" "$second/$artifact"
  compare "$repository_root/src/docs/$artifact" "$first/$artifact"
done

echo "OpenAPI generation is deterministic and tracked artifacts are current ($expected_version)."
