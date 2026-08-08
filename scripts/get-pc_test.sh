#!/usr/bin/env bash

set -euo pipefail

project_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d "${TMPDIR:-/tmp}/process-compose-installer-test.XXXXXX")"
trap 'rm -rf -- "$test_root"' EXIT

mkdir -p "$test_root/bin"
cat >"$test_root/bin/uname" <<'EOF'
#!/bin/sh
case "$1" in
  -s) printf '%s\n' "${INSTALLER_TEST_OS:-Linux}" ;;
  -m) printf '%s\n' "${INSTALLER_TEST_ARCH:-armv7l}" ;;
  *) exit 1 ;;
esac
EOF
cat >"$test_root/bin/curl" <<'EOF'
#!/bin/sh
output=
url=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|-H|-w)
      output_name=$1
      shift
      if [ "$output_name" = -o ]; then
        output=$1
      fi
      ;;
    -sL) ;;
    *) url=$1 ;;
  esac
  shift
done
case "$url" in
  */releases/v*)
    printf '%s\n' '{"tag_name":"v1.120.0+gkze1"}' >"$output"
    printf 200
    ;;
  */releases/download/*)
    printf '%s\n' "$url" >"$INSTALLER_CAPTURE"
    printf 404
    ;;
  *)
    printf 404
    ;;
esac
EOF
chmod +x "$test_root/bin/uname" "$test_root/bin/curl"

capture="$test_root/download-url.txt"
if PATH="$test_root/bin:$PATH" INSTALLER_CAPTURE="$capture" INSTALLER_TEST_ARCH=armv7l \
  "$project_root/scripts/get-pc.sh" v1.120.0+gkze1 >"$test_root/output.txt" 2>&1; then
  echo "installer unexpectedly completed with the intentionally failing download stub" >&2
  exit 1
fi

expected="https://github.com/gkze/process-compose/releases/download/v1.120.0+gkze1/process-compose_linux_arm.tar.gz"
if [[ ! -f "$capture" ]]; then
  echo "installer rejected Linux armv7 before selecting an approved archive:" >&2
  cat "$test_root/output.txt" >&2
  exit 1
fi
if [[ "$(<"$capture")" != "$expected" ]]; then
  echo "installer selected $(<"$capture"), want $expected" >&2
  exit 1
fi

armv6_capture="$test_root/armv6-download-url.txt"
if PATH="$test_root/bin:$PATH" INSTALLER_CAPTURE="$armv6_capture" INSTALLER_TEST_ARCH=armv6l \
  "$project_root/scripts/get-pc.sh" v1.120.0+gkze1 >"$test_root/armv6-output.txt" 2>&1; then
  echo "installer unexpectedly completed with the intentionally failing ARMv6 download stub" >&2
  exit 1
fi
if [[ ! -f "$armv6_capture" || "$(<"$armv6_capture")" != "$expected" ]]; then
  echo "installer did not route Linux ARMv6 to the GOARM=6 archive" >&2
  exit 1
fi

armv5_capture="$test_root/armv5-download-url.txt"
if PATH="$test_root/bin:$PATH" INSTALLER_CAPTURE="$armv5_capture" INSTALLER_TEST_ARCH=armv5l \
  "$project_root/scripts/get-pc.sh" v1.120.0+gkze1 >"$test_root/armv5-output.txt" 2>&1; then
  echo "installer unexpectedly accepted unsupported Linux ARMv5" >&2
  exit 1
fi
if [[ -e "$armv5_capture" ]]; then
  echo "installer selected a GOARM=6 archive for unsupported Linux ARMv5" >&2
  exit 1
fi
if ! grep -F "platform linux/armv5 is not supported" "$test_root/armv5-output.txt" >/dev/null; then
  echo "installer rejected Linux ARMv5 for an unexpected reason:" >&2
  cat "$test_root/armv5-output.txt" >&2
  exit 1
fi

echo "Linux ARMv6 and ARMv7 route to the GOARM=6 archive; ARMv5 is rejected."
