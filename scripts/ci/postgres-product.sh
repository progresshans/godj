#!/usr/bin/env bash
set -euo pipefail
mode="$MODE"
shard="$PART"
database=postgres
evidence="$RUNNER_TEMP"
if [ "$PRIVATE_DATABASE" = true ]; then
  # Each logical group starts on an empty, same-locale database on this
  # worker's ephemeral, pinned PostgreSQL instance.
  database="godj_ci_${shard//-/_}"
  docker exec "$POSTGRES_CONTAINER_ID" createdb -U postgres -T template0 "$database"
  evidence="$RUNNER_TEMP/postgres-parts/$shard"
  mkdir -p "$evidence"
fi
export GODJ_RELATED_EXPRESSION_REFERENCE_DIR="$RUNNER_TEMP/related-expression-reference"
export GODJ_COLLECTION_AGGREGATE_REFERENCE_DIR="$RUNNER_TEMP/collection-aggregate-reference"
export GODJ_DECIMAL_EXPRESSION_REFERENCE_DIR="$RUNNER_TEMP/decimal-expression-reference"
export GODJ_TEMPORAL_EXPRESSION_REFERENCE_DIR="$RUNNER_TEMP/temporal-expression-reference"
export GODJ_CALENDAR_EXTRACTION_REFERENCE_DIR="$RUNNER_TEMP/calendar-extraction-reference"
export GODJ_ANNOTATION_REFERENCE_DIR="$RUNNER_TEMP/annotation-reference"
export GODJ_ADMIN_CHOICE_REFERENCE_DIR="$RUNNER_TEMP/admin-choice-reference"
export GODJ_TEST_POSTGRES_URL="postgresql://postgres:godj-ci-pg-canary-8H2k7M4q9V6x3R@127.0.0.1:${POSTGRES_PORT}/${database}?sslmode=disable"
export GODJ_REQUIRE_POSTGRES="1"
export GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE="$RUNNER_TEMP/postgresql-17.10-two-process-v1.json"
export GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE="$RUNNER_TEMP/postgresql-17.10-sqlite-external-operator-v1.json"
export GODJ_NAMED_ANNOTATION_CAPTURE_DIR="$evidence/named-annotation-native"
export GODJ_ADMIN_CHOICE_CAPTURE_DIR="$evidence/admin-choice-native"
export GODJ_FILE_CONSUMER_CAPTURE="$evidence/file-consumer-native.jsonl"
export GODJ_GENERATED_CONSUMER_CAPTURE_DIR="$evidence/generated-consumer-diagnostics"
export GODJ_MODEL_RESOURCE_CLIENT_EVIDENCE="$evidence/model-resource-client"

operator_target_required_passes=(
  "github.com/progresshans/godj/conformance/projectmigratetargetproduct|TestGlobalTargetedMigratePostgresLifecycle"
  "github.com/progresshans/godj/conformance/projectmigratetargetproduct|TestTargetSchemaSQLRequiresExactSuccessfulPublicOutput"
  "github.com/progresshans/godj/conformance/projectmigratetargetproduct|TestGlobalTargetedMigratePostgresLifecycle/historical_reverse_sql_uses_project_schema_without_opening_database"
  "github.com/progresshans/godj/conformance/projectmigratetargetproduct|TestGlobalTargetedMigratePostgresLifecycle/public_delete_model_recreates_empty_storage"
  "github.com/progresshans/godj/conformance/projectmigratetargetproduct|TestGlobalTargetedMigratePostgresLifecycle/public_model_rename_preserves_rows"
  "github.com/progresshans/godj/conformance/projectoperatorproduct|TestOperatorPostgresSchemaSnapshotDetectsTriggerMutation"
  "github.com/progresshans/godj/conformance/projectoperatorproduct|TestGlobalCreatesuperuserExternalPostgresAndSQLiteProduct"
)
case "$shard" in
  operator-target)
    test_timeout=18m
    required_passes=("${operator_target_required_passes[@]}")
    packages=(
      ./conformance/projectmigratetargetproduct
      ./conformance/projectoperatorproduct
    )
    ;;
  *)
    partition="$RUNNER_TEMP/postgresql-plan-${mode}-${shard}"
    python3 scripts/ci/postgres_shards.py --mode "$mode" --shard "$shard" \
      --required scripts/ci/postgres-core-required.txt --output-dir "$partition"
    test_timeout="$(jq -er '.test_timeout' "$partition/plan.json")"
    required_passes=()
    while IFS= read -r sentinel; do required_passes+=("$sentinel"); done < "$partition/required.txt"
    packages=()
    while IFS= read -r package; do packages+=("$package"); done < "$partition/packages.txt"
    ;;
esac
required_tests=()
for sentinel in "${required_passes[@]}"; do
  test_name="${sentinel#*|}"
  test_root="${test_name%%/*}"
  # Select the enclosing test even when only a child is a sentinel.
  # Keep complete child names in the inventory checked after Go exits.
  case " ${required_tests[*]-} " in
    *" $test_root "*) ;;
    *) required_tests+=("$test_root") ;;
  esac
done
required_regex="^($(IFS='|'; printf '%s' "${required_tests[*]}"))$"
test_flags=(-p=1 -timeout="$test_timeout" -json -count=1 -run "$required_regex")
case "$mode" in
  normal)
    ;;
  race)
    test_flags+=(-race)
    ;;
  cgo0)
    export CGO_ENABLED=0
    ;;
  *)
    echo "unsupported PostgreSQL product mode: $mode" >&2
    exit 1
    ;;
esac
log="$RUNNER_TEMP/postgresql-product-${mode}-${shard}.json"
stderr_log="$RUNNER_TEMP/postgresql-product-${mode}-${shard}.stderr"
required="$RUNNER_TEMP/postgresql-${mode}-${shard}-required-tests.txt"
printf '%s\n' "${required_passes[@]}" > "$required"
case " ${required_tests[*]} " in
  *" TestGeneratedModelResources "*) make api-client-dependencies ;;
esac
status=0
if [ "$shard" = "core" ] || [ "$shard" = "core-consumers" ] || [ "$shard" = "core-consumers-2" ]; then
  python3 scripts/ci/s3_service.py run --directory "$RUNNER_TEMP/s3-reference" -- \
    go test "${test_flags[@]}" "${packages[@]}" > "$log" 2> "$stderr_log" || status=$?
else
  go test "${test_flags[@]}" "${packages[@]}" > "$log" 2> "$stderr_log" || status=$?
fi
if [ "$status" -ne 0 ]; then
  python3 scripts/ci/go_test_events.py "$log" --diagnostics --stderr "$stderr_log" || true
  exit "$status"
fi
python3 scripts/ci/go_test_events.py "$log" --required "$required" --no-skips
if [ "$mode" != "normal" ]; then
  exit 0
fi
case "$shard" in
  core-consumers) go vet "${packages[@]}"; exit 0 ;;
  core|operator-target) ;;
  *) exit 0 ;;
esac
if [ "$shard" = "core" ]; then
  test -f "$GODJ_SYSTEM_STATE_POSTGRES_ATTESTATION_CAPTURE"
  export GODJ_TEST_POSTGRES_SCHEMA="godj_postgresproduct_ci${GITHUB_RUN_ID}${GITHUB_RUN_ATTEMPT}"
  project_runner="$RUNNER_TEMP/postgres-projectrunner"
  go build -o "$project_runner" ./conformance/postgresproduct/cmd/projectrunner
  "$project_runner" prepare
  docker restart "$POSTGRES_CONTAINER_ID" >/dev/null
  # GitHub services publish an ephemeral host port that Docker may
  # reallocate when the container stops and starts.
  postgres_host_port="$(
    docker inspect --format '{{with (index .NetworkSettings.Ports "5432/tcp")}}{{(index . 0).HostPort}}{{end}}' \
      "$POSTGRES_CONTAINER_ID"
  )"
  case "$postgres_host_port" in
    ""|*[!0-9]*)
      echo "restarted PostgreSQL service has no numeric published port" >&2
      exit 1
      ;;
  esac
  if [ "$postgres_host_port" -lt 1 ] || [ "$postgres_host_port" -gt 65535 ]; then
    echo "restarted PostgreSQL service published port is outside the valid range" >&2
    exit 1
  fi
  export GODJ_TEST_POSTGRES_URL="postgresql://postgres:godj-ci-pg-canary-8H2k7M4q9V6x3R@127.0.0.1:${postgres_host_port}/postgres?sslmode=disable"
  ready=0
  for attempt in $(seq 1 30); do
    if timeout 3s "$project_runner" probe >/dev/null 2>&1; then
      ready=1
      break
    fi
    sleep 1
  done
  test "$ready" -eq 1
  "$project_runner" resume
  "$project_runner" verify
  "$project_runner" cleanup
  go vet "${packages[@]}"
  exit 0
fi
test "$shard" = "operator-target"
test -f "$GODJ_PROJECT_OPERATOR_POSTGRES_ATTESTATION_CAPTURE"
go vet "${packages[@]}"
