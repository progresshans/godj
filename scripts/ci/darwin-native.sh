#!/usr/bin/env bash
set -euo pipefail

test "$(go env GOOS)" = darwin
# Each test still creates its own threads/processes. Limit simultaneous Go
# build tools on the small native runners, and reuse trimpath dependency builds.
flags=(-p=1 -trimpath -json -count=1 -timeout=15m)
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
timings="$evidence/darwin-native-timings.txt"
: > "$log"
: > "$stderr_log"
: > "$timings"
sqlite=github.com/progresshans/godj/db/sqlite
args=()
while IFS= read -r package; do
  if [ "$package" != "$sqlite" ]; then args+=("$package"); fi
done < "$packages"
grep -Fxq "$sqlite" "$packages"
args+=("$sqlite")

status=0
for package in "${args[@]}"; do
  roots=$(awk -F '|' -v package="$package" '$1 == package { split($2, parts, "/"); print parts[1] }' "$required" | sort -u | paste -sd '|' -)
  test -n "$roots"
  printf '%s start %s\n' "$(date -u +%FT%TZ)" "$package" | tee -a "$timings"
  if go test "${flags[@]}" -run "^($roots)$" "$package" >> "$log" 2>> "$stderr_log"; then
    outcome=pass
  else
    outcome=fail
    status=1
  fi
  printf '%s %s %s\n' "$(date -u +%FT%TZ)" "$outcome" "$package" | tee -a "$timings"
done

if [ "$status" != 0 ]; then
  python3 scripts/ci/go_test_events.py "$log" --diagnostics --stderr "$stderr_log" || true
  exit 1
fi
python3 scripts/ci/go_test_events.py "$log" --required "$required" --packages "$packages" --no-skips
