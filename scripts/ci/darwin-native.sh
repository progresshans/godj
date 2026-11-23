#!/usr/bin/env bash
set -euo pipefail

test "$(go env GOOS)" = darwin
flags=(-json -count=1 -timeout=15m)
case "$1" in
  race) flags+=(-race) ;;
  cgo0) export CGO_ENABLED=0 ;;
  *) exit 2 ;;
esac

evidence="${2:-$RUNNER_TEMP}"
required="$evidence/darwin-native-required.txt"
packages="$evidence/darwin-native-packages.txt"
log="$evidence/darwin-native-tests.json"
stderr_log="$evidence/darwin-native.stderr"
cp scripts/ci/darwin-native-required.txt "$required"
cut -d '|' -f 1 "$required" | sort -u > "$packages"
roots=$(cut -d '|' -f 2 "$required" | cut -d / -f 1 | sort -u | paste -sd '|' -)
test -n "$roots"
args=()
while IFS= read -r package; do args+=("$package"); done < "$packages"

if ! go test "${flags[@]}" -run "^($roots)$" "${args[@]}" > "$log" 2> "$stderr_log"; then
  python3 scripts/ci/go_test_events.py "$log" --diagnostics --stderr "$stderr_log" || true
  exit 1
fi
python3 scripts/ci/go_test_events.py "$log" --required "$required" --packages "$packages" --no-skips
