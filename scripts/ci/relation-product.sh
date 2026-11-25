#!/usr/bin/env bash
set -euo pipefail

if [ "$SCOPE" = darwin-native ]; then
  case "$1" in
    prepare) exit 0 ;;
    run) exec bash scripts/ci/darwin-native.sh "$MODE" ;;
    *) exit 2 ;;
  esac
fi
test "$SCOPE" = relation
mode="$MODE"
consumer_package=github.com/progresshans/godj/codegen/consumertest
work="$RUNNER_TEMP/relation"
discovery="$work/relation-consumer-discovery.json"
test_timeout="$TEST_TIMEOUT"
test_flags=(-json -count=1)
case "$mode" in
  normal) ;;
  race) test_flags+=(-race) ;;
  cgo0) export CGO_ENABLED=0 ;;
  *) exit 1 ;;
esac

case "$1" in
  prepare)
    mkdir -p "$work"
    selected_packages="$(go list ./... | python3 scripts/ci/packages.py relation)"
    make project-command-dependencies
    packages=()
    printf '%s\n' "$selected_packages" > "$work/selected-packages.txt"
    while IFS= read -r package; do packages+=("$package"); done < "$work/selected-packages.txt"
    expected=("${packages[@]}")
    if [ "$PORTABLE_COVERED" != true ]; then
      expected+=(./conformance/cmd/godjcheck)
    fi
    expected_packages="$work/relation-packages.txt"
    go list "${expected[@]}" > "$expected_packages"
    required="$work/relation-required.txt"
    # Project-check owns the complete runner on every coordinate.
    awk -F'|' -v portable="$PORTABLE_COVERED" '$1 != "github.com/progresshans/godj/conformance/runners/godj" && (portable != "true" || $1 != "github.com/progresshans/godj/conformance/cmd/godjcheck")' scripts/ci/relation-required.txt > "$required"
    if [ "$mode" != race ]; then
      cat scripts/ci/compile-required.txt >> "$required"
    fi
    go test -timeout="$test_timeout" "${test_flags[@]}" -list . "$consumer_package" > "$discovery"
    exit 0
    ;;
  run) ;;
  *) exit 2 ;;
esac

shard="$PART"
shards="$SHARDS"
partition_args=(--shard "$shard" --shards "$shards" --layout "$CONSUMER_LAYOUT")
part="$work/part-$shard"
mkdir "$part"
shard_dir="$part/relation-shard"
log="$part/relation-product-tests.json"
required="$work/relation-required.txt"
expected_packages="$work/relation-packages.txt"
export GODJ_GENERATED_CONSUMER_CAPTURE_DIR="$part/generated-consumer-diagnostics"
packages=()
while IFS= read -r package; do packages+=("$package"); done < "$work/selected-packages.txt"
relation_godjcheck='^(TestRunMatchesTwelveContractRelationProductBeforePublishingActualOutput|TestRunLeavesMigrationRelationReferenceNotImplementedWithoutProductHandlers|TestRunRejectsMigrationRelationFalseGreenBeforePublishingActualOutput|TestRunDoesNotPublishRelationActualBeforePayloadComparison|TestRunRejectsRelationRegistryStatusFalseGreensBeforeActualOutput|TestRunRejectsUnknownRelationStatusBeforeActualOutput)$'
python3 scripts/ci/relation_shards.py plan --discovery "$discovery" --required "$required" --packages "$expected_packages" --output-dir "$shard_dir" "${partition_args[@]}"
required="$shard_dir/required.txt"
expected_packages="$shard_dir/packages.txt"
if grep -Fqx "$consumer_package|TestGeneratedModelResources" "$required"; then
  make api-client-dependencies
fi
stderr_log="$part/relation-product.stderr"
status=0
: > "$log"
: > "$stderr_log"
run_tests() {
  local package_timeout="$1"
  shift
  go test -timeout="$package_timeout" "${test_flags[@]}" "$@" >> "$log" 2>> "$stderr_log" || status=$?
}
if [ "$shards" -eq 1 ]; then
  run_tests "$test_timeout" "${packages[@]}"
elif [ "$shard" -eq 0 ]; then
  sqlite_timeout="$SQLITE_TIMEOUT"
  sqlite_package=github.com/progresshans/godj/db/sqlite
  if [ -n "$sqlite_timeout" ]; then grep -Fxq "$sqlite_package" "$expected_packages"; fi
  runtime_packages=()
  for package in "${packages[@]}"; do
    if [ "$package" = "$consumer_package" ]; then continue; fi
    if [ -n "$sqlite_timeout" ] && [ "$package" = "$sqlite_package" ]; then
      echo "Running $package with Go package timeout $sqlite_timeout"
      run_tests "$sqlite_timeout" "$package"
    else
      runtime_packages+=("$package")
    fi
  done
  if [ "${#runtime_packages[@]}" -gt 0 ]; then
    # Large generated packages can each use several GiB during compilation.
    # Limit simultaneous Go build actions on the small native runner.
    runtime_args=("$test_timeout")
    if [ "$SERIAL_RUNTIME_PACKAGES" = true ]; then runtime_args+=(-p=1); fi
    run_tests "${runtime_args[@]}" "${runtime_packages[@]}"
  else
    test -n "$sqlite_timeout"
  fi
else
  run_tests "$test_timeout" -run "$(cat "$shard_dir/run.txt")" "$consumer_package"
fi
runtime_owner=false
if [ "$shards" -eq 1 ] || [ "$shard" -eq 0 ]; then runtime_owner=true; fi
if [ "$status" -eq 0 ] && [ "$runtime_owner" = true ] && [ "$PORTABLE_COVERED" != true ]; then
  run_tests "$test_timeout" -p=1 -run "$relation_godjcheck" ./conformance/cmd/godjcheck
fi
if [ "$status" -ne 0 ]; then
  python3 scripts/ci/go_test_events.py "$log" --diagnostics --stderr "$stderr_log" || true
  exit "$status"
fi
python3 scripts/ci/go_test_events.py "$log" --required "$required" --packages "$expected_packages" --no-skips
python3 scripts/ci/relation_shards.py verify --discovery "$discovery" --packages "$expected_packages" --log "$log" "${partition_args[@]}"
if [ "$mode" = normal ] && [ "$runtime_owner" = true ] && [ "$PORTABLE_COVERED" != true ]; then
  go vet ./migrations ./db/sqlite
fi
