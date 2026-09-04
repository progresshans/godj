---
id: GDJ-0020
status: completed
updated: 2026-08-09
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "eecc75f7507414ad6043a090c97b84080ab0fb8b"
depends_on: ["GDJ-0019"]
contracts: ["MIG-057..MIG-064", "Q-010", "Q-012"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "migrations/definition/definition.go", "migrations/definition/load.go", "migrations/definition/error.go", "migrations/definition/limits.go", "migrations/definition/json.go", "migrations/definition/codec.go", "migrations/definition/ir.go", "migrations/definition/digest.go", "migrations/definition/definition_test.go", "migrations/definition/resource_limits_test.go", "migrations/definition/external_test.go", "internal/compiletest/compile_test.go", "internal/compiletest/testdata/migration_definition_external_consumer.go.txt", "conformance/contracts/migration-definition-source-manifest.json", "conformance/definitionload/product_equivalence_test.go", "conformance/runners/godj/migration_definition_source_scenarios.go", "conformance/runners/godj/runner.go", "conformance/runners/godj/runner_test.go", "conformance/cmd/godjcheck/main.go", "conformance/cmd/godjcheck/main_test.go", "conformance/internal/protocol/migration_definition_source_artifacts_test.go", "conformance/internal/protocol/migration_state_reconstruction_artifacts_test.go", "conformance/internal/protocol/migration_lifecycle_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "conformance/runners/django/tests/test_migration_definition_source_scenarios.py", "conformance/README.md", "docs/ARCHITECTURE.md", "docs/COMPATIBILITY.md", "docs/OPEN_QUESTIONS.md", "docs/ROADMAP.md", "docs/TESTING.md", "docs/adr/0020-migration-definition-loader-product-shape.md", "docs/adr/README.md", "docs/status/CURRENT.md", "docs/status/IMPLEMENTATION_MATRIX.md", "docs/status/TEST_EVIDENCE.md", "work/0020-migration-definition-loader-product-slice.md", "work/README.md"]
integration_owner: "one primary agent"
---

# Migration Definition Loader Product Slice

## 사용자에게 보이는 결과

Caller가 I/O를 끝낸 뒤 `migrations/definition.Source` 값으로 source ID와 JSON bytes를 명시적으로
넘기면, GoDj가 Accepted ADR-0019의 tuple `(1,1,1,2)`와 closed codec을 bounded하게 검증하고
immutable loader-owned `Set`을 원자적으로 반환합니다. 사용자는 canonical digest, migration
definition과 source inventory의 복사본을 읽고, 같은 set에서 기존
`Executor.Migrate` lifecycle로 정확히 한 번 handoff할 수 있습니다.

MIG-057..064의 열 번째 제품 adapter까지 검증한 현재 제품 분류는 정확히 10 adapter/105
contract의 `100 passing + 5 deviation`입니다. Product commit
`6172d843a4bb234592cafc176a8d1191933b141c`와 Draft PR #1 exact-head hosted run
`31309152526`, completion-documentation commit
`a5422f2c1ba5db34986564fc065e4b8e28ef0115`와 별도 exact-head run `31310002784`가 모두
completion gate를 통과했습니다. Activation 당시 9 adapter의
`92 passing + 5 deviation + 8 oracle_locked`였던 상태는 historical baseline으로 보존합니다.

## 목표

- 새 leaf package `github.com/progresshans/godj/migrations/definition`에 최소 공개 API 구현
- `Load(...Source) (Set, LoadReport, error)`의 caller-input snapshot과 atomic publish
- Zero-value `Set`을 canonical empty definition set으로 정의
- 성공과 오류 모두에서 값 snapshot인 `LoadReport`와 immutable `FailureContext` 제공
- Source/document/compatibility/operation/IR 실패를 9개 source `Error` code로 고정
- Graph failure는 raw `*migrations.PlanningError`, lifecycle failure는 기존 raw error를 보존
- `Set` accessor가 raw document를 보존하지 않고 매번 deep copy를 반환하도록 검증
- `Set.Migrate`가 fresh deep-copied definitions와 caller-provided immutable request value만 기존
  `Executor.Migrate`에 정확히 한 번 전달
- CPU/memory 증폭을 막는 10개 numeric resource limit과 overflow-safe 합산 구현
- Schema IR wire coordinate를 literal `2`로 잠그고 current `ir.FormatVersion` drift를 build에서 차단
- MIG-057..064를 `passing`으로 전환하는 실제 GoDj adapter와 false-green gate 추가
- 기존 9 product set, 5 accepted deviation과 locked oracle/static/SHA bytes 보존

## 비목표와 금지 경계

- Existing `migrations/*.go`, `migrations/backend/**`, `db/**`, `schema/**`, `ir/**` 제품 코드 변경
- 기존 `Executor.Migrate`, Planner, Reconstructor, operation 또는 backend port의 widening/breaking change
- Reflection/`unsafe`로 `LifecycleRequest` private kind/targets를 읽거나 복사하는 우회
- `conformance/definitionload/candidate_test.go` 등 test-only candidate를 제품 코드로 이동·복사·승격
- Django reference scenario/runner, checked-in oracle 또는 source artifact 내용 재생성
- `conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json`,
  `conformance/fixtures/godj-migration-definition-source-not-implemented.json`,
  `conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS` 수정
- File/directory/glob/module/package/embedded-FS/remote discovery와 `SourceID` path 해석
- `godj migrate`, `showmigrations`, `makemigrations`, project binary/build와 CLI exit code
- Writer, cache, upgrade/negotiation, codec v2+, generated Go registration과 `init()` 실행
- Python/Go executable callback, `RunPython`, `RunSQL`, raw SQL, custom operation/field/default
- `run_before`, `replaces`, squash/merge/fake/fake-initial, data migration과 historical app registry
- Existing database adoption/repair, crash reconciliation, schema drift, PostgreSQL/multi-DB
- Q-010의 global CLI/library/generator semver handshake 전체 해결
- 새 dependency, `go.mod`/`go.sum`, 새 CI job/Windows/DB matrix 또는 새 PR 생성. Existing Ubuntu
  job의 exact 32-bit focused step 외 workflow 변경은 금지

Python 수정의 유일한 예외는
`conformance/runners/django/tests/test_migration_definition_source_scenarios.py`에서 manifest status
assertion을 `oracle_locked`에서 `passing`으로 바꾸는 것입니다. Reference scenario, oracle generation,
decision provenance와 다른 assertion은 변경하지 않습니다.

## 선행 조건과 기준 상태

- Activation baseline은
  `codex/revision-fenced-migration-lifecycle@eecc75f7507414ad6043a090c97b84080ab0fb8b`
  (`docs: record hosted definition source validation`)이며 시작 checkout은 clean입니다.
- [GDJ-0019](0019-migration-definition-source-compatibility-contracts.md)은 MIG-057..064의 strict
  data document, tuple, closed codec, digest, deterministic failure와 handoff를 contract/reference/
  test-only proof로 완료했습니다.
- [Accepted ADR-0019](../docs/adr/0019-versioned-migration-definition-source.md)은 source 의미를
  채택했지만 public package/API와 numeric resource limit은 고정하지 않았습니다.
- [Accepted ADR-0020](../docs/adr/0020-migration-definition-loader-product-shape.md)은 이 work의
  product shape입니다. Activation 시 `Proposed`였고 external compile, bounded parser/codec,
  conformance와 exact-head hosted 결과를 근거로 completion에서 `Accepted`로 승격했습니다.
- Baseline product는 10 reference set/105 unique contract/90 ordered cross-binding을 검증하지만
  product adapter는 9개뿐입니다. MIG-057..064는 `oracle_locked`이고 product `godjcheck`는 exit 2로
  fail-closed합니다.
- 작업은 기존 Draft PR [#1](https://github.com/progresshans/godj/pull/1)에만 별도 commit을 쌓습니다.
  새 PR을 만들거나 oracle/static bytes를 갱신하지 않습니다.

## Contract와 product completion gate

| ID | 제품 gate |
|---|---|
| MIG-057 | Canonical two-source batch가 loader-owned 2-definition Set/source inventory/digest와 exact success counters를 publish |
| MIG-058 | Empty source가 zero `Set`, pinned empty digest, set publish 1과 zero I/O를 반환 |
| MIG-059 | JSON syntax/input order/source relabel은 semantic set/digest를 보존하고 operation order 변화는 digest를 바꿈 |
| MIG-060 | Tuple coordinate mismatch가 decode/publish/handoff 전에 coordinate order대로 fail-closed |
| MIG-061 | 한 malformed document가 canonical source/pointer/reason으로 batch 전체를 atomic 실패시키고 publish/handoff 0 유지 |
| MIG-062 | Duplicate migration identity가 last-wins 없이 `NewPlanner` 1회의 raw duplicate-node error로 실패 |
| MIG-063 | Closed codec이 executable/implicit unsupported operation을 semantic traversal 단계에서 fail-closed |
| MIG-064 | Loaded `Set.Migrate`가 raw document/digest 없이 fresh definitions와 immutable request value를 actual lifecycle에 정확히 한 번 전달 |

완료 시 manifest 8개 status만 `passing`으로 바뀌며 decision provenance와 oracle observation은
그대로입니다. Target은 정확히 `100 passing + 5 deviation`; 새 deviation이나 `oracle_locked`가
남으면 완료가 아닙니다. Static not-implemented fixture는 변경하지 않고 비교 시 MIG-057..064
ordered mismatch 8개를 계속 내야 합니다.

### Product adapter observation ownership과 false-green gate

열 번째 adapter는 checked-in expected/oracle/static fixture의 result/metrics constant를 복사하거나
그 값으로 actual을 채우면 안 됩니다. 각 observation field는 다음 actual product 경계에서만
만듭니다.

- `result.definition_set.definitions`, `result.sources`, digest와 canonicality 파생값:
  actual `Set.Definitions()`, `Set.Sources()`, `Set.Digest()`
- `documents_received`, `headers_validated`, `operations_decoded`, definitions/set publish와 `failure`:
  같은 actual `Load` 호출의 `LoadReport`/`Failure()`
- `handoff_calls`와 MIG-064 lifecycle observation: actual `Set.Migrate`가 호출한 instrumented
  Executor/backend 경로의 counter/state; hard-coded 1이나 oracle fixture 금지

False-green test는 valid fixture에서 source identity/inventory, compatibility header, ordered
operation payload와 graph identity/dependency를 각각 하나씩 mutate해야 합니다. 네 mutation 모두
actual observation을 바꿔야 합니다. Result-valued contract를 error-valued observation으로 바꾸는
incompatible compatibility mutation은 checked-in manifest/oracle에 대한 `protocol.Compare`의 shape
validation rejection을, 나머지는 non-empty diff를 만들어야 합니다. Adapter가 expected/oracle
`Value`를 반환하거나 mutation 뒤에도 comparison이 green이면 completion 실패입니다.

## 검증된 공개 API

Package import 경계는 `migrations/definition → migrations + schema/ir` 단방향입니다.
`migrations` root, backend와 database package는 `definition`을 import하지 않습니다.

```go
package definition

const (
    DefinitionFormatVersion int64 = 1
    LoaderABIVersion        int64 = 1
    OperationCodecVersion   int64 = 1
    SchemaIRVersion         int64 = 2
    EmptySetDigest                = "sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450"
    CategorySource                = "migration_definition_source_error"
)

const (
    MaxSources                    = 2_048
    MaxSourceIDBytes              = 1_024
    MaxDocumentBytes              = 1 << 20
    MaxBatchBytes                 = 16 << 20
    MaxJSONDepth                  = 64
    MaxDocumentJSONValues         = 65_536
    MaxJSONValues                 = 262_144
    MaxDependenciesPerMigration   = 2_047
    MaxOperationsPerMigration     = 2_048
    MaxFieldsPerCreateModel       = 2_048
)

type Source struct {
    SourceID string
    Document []byte
}

type Producer struct {
    Name    string
    Version string
}

type SourceInfo struct {
    SourceID  string
    Producer  Producer
    Migration migrations.MigrationKey
}

type GraphSource struct {
    Migration migrations.MigrationKey
    SourceID  string
}

type FailureContext struct {
    Stage          string
    SourceID       string
    JSONPointer    string
    App            string
    Name           string
    OperationIndex int
    Reason         string
    Limit          string
    Maximum        uint64
    Actual         uint64
    // graph source mapping is unexported
}

func (FailureContext) GraphSources() []GraphSource

type LoadReport struct {
    DocumentsReceived       int
    HeadersValidated        int
    OperationsDecoded       int
    PlannerConstruction     int
    DefinitionsPublished    int
    DefinitionSetsPublished int
    // failure state is unexported
}

func (LoadReport) Failure() (FailureContext, bool)

type ErrorCode string

const (
    CodeInvalidSource                 ErrorCode = "invalid_definition_source"
    CodeInvalidDocument               ErrorCode = "invalid_definition_document"
    CodeDefinitionFormatIncompatible  ErrorCode = "definition_format_incompatible"
    CodeLoaderABIIncompatible         ErrorCode = "loader_abi_incompatible"
    CodeOperationCodecIncompatible    ErrorCode = "operation_codec_incompatible"
    CodeSchemaIRIncompatible          ErrorCode = "schema_ir_incompatible"
    CodeUnsupportedOperation          ErrorCode = "unsupported_definition_operation"
    CodeInvalidOperation              ErrorCode = "invalid_definition_operation"
    CodeInvalidIR                     ErrorCode = "invalid_definition_ir"
)

type Error struct {
    Category string
    Code     ErrorCode
    // immutable failure context is unexported
}

func (*Error) Error() string
func (*Error) Context() FailureContext

type Set struct { /* unexported loader-owned snapshot */ }

func Load(sources ...Source) (Set, LoadReport, error)
func (Set) Digest() string
func (Set) Definitions() []migrations.Migration
func (Set) Sources() []SourceInfo
func (Set) Migrate(context.Context, migrations.Executor, migrations.LifecycleRequest) (migrations.ProjectState, error)
```

`FailureContext`와 `LoadReport`는 slice/pointer를 외부에 노출하지 않는 값 snapshot입니다.
`Failure()`/`Context()`/`GraphSources()`는 매번 독립 value/deep copy를 반환하므로 caller mutation이
report, error, `Set` 또는 concurrent reader에 영향을 주지 않습니다. `GraphSources()`는 raw
`PlanningError`의 non-zero Node/Related/Members에 해당하는 migration→SourceID를 중복 제거하고
app/name/SourceID byte order로 반환합니다. Duplicate-node처럼 identity 하나에 source가 둘이면 두
mapping을 모두 보존하고 exact pair만 중복 제거합니다. Raw `PlanningError` 자체의 role/order
의미는 바꾸지 않습니다. Graph failure의 scalar `SourceID`는 raw Node가 가리키는 canonical
definition 중 Planner input에서 마지막인 source입니다. 따라서 duplicate identity는 두 mapping을
모두 보존하면서 scalar context가 later duplicate source를 가리킵니다. Node가 zero이고 cycle
Members만 있으면 first canonical member의 first source를 사용합니다. `OperationIndex`가 적용되지
않는 context는 `-1`입니다.

Zero `Set`은 `Load()` 성공 결과와 같은 canonical empty set입니다. `Digest()`는
`sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450`, 두 accessor는 empty
fresh copy를 반환하고 concurrent read에 안전합니다. `Set`은 raw document bytes를 보존하지
않습니다. 성공 `LoadReport.Failure()`는 `false`; 실패는 zero `Set`, 동일 호출에서 수집된 report와
`true` failure를 반환합니다. 모든 실패에서 `DefinitionsPublished`와
`DefinitionSetsPublished`는 0입니다.

## Error ownership

Source-owned category는 exact string `migration_definition_source_error`입니다. `*definition.Error`는
다음 9개 code만 사용합니다.

1. `invalid_definition_source`
2. `invalid_definition_document`
3. `definition_format_incompatible`
4. `loader_abi_incompatible`
5. `operation_codec_incompatible`
6. `schema_ir_incompatible`
7. `unsupported_definition_operation`
8. `invalid_definition_operation`
9. `invalid_definition_ir`

Resource limit은 열 번째 code를 만들지 않습니다. `Reason="resource_limit_exceeded"`과 stable `Limit`,
`Maximum`, `Actual`을 source error와 report의 `FailureContext`에 함께 기록합니다.
이 reason을 Accepted semantic reason rank에 삽입하지 않습니다. 각 소유 stage가 child traversal이나
다음 stage에 들어가기 전에 실행하는 preflight guard입니다.

`migrations.NewPlanner`가 반환한 graph/history error는 definition error로 wrap하거나 code를
재분류하지 않고 raw `*migrations.PlanningError`를 그대로 `Load` error로 반환합니다. Report만
별도의 immutable graph-source mapping을 제공합니다. `Set.Migrate`는 set에서 fresh deep-copied
definitions를 만든 뒤 caller가 준 immutable `LifecycleRequest` value와 함께
`executor.Migrate(ctx, definitions, request)`를 정확히 한 번 호출합니다. Leaf package는 private
request kind/targets를 snapshot/validate하지 않습니다. 그 소유권은 existing `Executor.Migrate`
내부에 남으며 reflection/`unsafe`나 core API widening을 사용하지 않습니다. Context, request
validation, revision fence, transaction/recorder/cleanup을 포함한 lifecycle error는
wrap/reclassify/retry하지 않고 그대로 반환합니다. Digest, SourceInfo와 raw source bytes는 handoff
인자가 아닙니다.

### `FailureContext` scalar contract

`*definition.Error.Context()`와 같은 `Load` 호출의 `LoadReport.Failure()`는 exact same scalar/mapping
value를 반환합니다. 모든 failure는 특정 operation이 직접 소유할 때만 zero-based
`OperationIndex`를 사용하고, 그 밖에는 `-1`입니다. Non-resource failure는 `Limit=""`,
`Maximum=0`, `Actual=0`입니다. Resource failure는 `Reason="resource_limit_exceeded"`이고 다음 값을
사용합니다.

| Limit | Stage | SourceID | JSONPointer | OperationIndex |
|---|---|---|---|---:|
| `source_count` | `source` | `""` | `""` | -1 |
| `source_id_bytes` | `source` | `""` | `""` | -1 |
| `document_bytes` | `document` | selected bounded SourceID | `""` | -1 |
| `batch_bytes` | `document` | `""` | `""` | -1 |
| `json_depth` | `document` | owning SourceID | canonical over-depth container pointer | -1 |
| `document_json_values` | `document` | owning SourceID | `""` | -1 |
| `json_values` | `document` | canonical source where aggregate first exceeds | `""` | -1 |
| `dependencies_per_migration` | `semantic` | owning SourceID | `/migration/dependencies` | -1 |
| `operations_per_migration` | `semantic` | owning SourceID | `/migration/operations` | -1 |
| `fields_per_create_model` | `semantic` | owning SourceID | `/migration/operations/{i}/model/fields` | owning `{i}` |

Graph failure는 항상 `Stage="graph"`, `Reason=string(planningError.Code)`, `Limit=""`,
`Maximum=Actual=0`, `OperationIndex=-1`입니다.

- Invalid node는 Node source, pointer `/migration`, App/Name=Node를 사용합니다.
- Duplicate node는 canonical Planner input에서 같은 Node identity의 later source, pointer
  `/migration`, App/Name=Node를 사용합니다. MIG-062 scalar SourceID는 exact `z-duplicate`입니다.
- Invalid/duplicate/missing dependency edge는 child Node source, pointer
  `/migration/dependencies`, App/Name=Node를 사용합니다.
- Dependency cycle은 `Members()`의 first canonical member source, pointer
  `/migration/dependencies`, App/Name=그 member를 사용합니다.
- `GraphSources()`는 Node/Related/Members 중 실제 source가 있는 모든 `(MigrationKey, SourceID)` pair를
  app/name/SourceID 순으로 반환합니다. Duplicate pair 두 개를 모두 유지하고 exact 동일 pair만
  제거합니다. Missing Related에는 synthetic source를 만들지 않습니다.

## Numeric resource limits

Public constant와 diagnostic label은 다음 값으로 고정합니다. Maximum-1과 maximum은 그 resource
guard 자체를 통과하지만 뒤 syntax/compatibility/semantic/graph stage에서 실패할 수 있습니다.
Maximum+1은 해당 limit error입니다.

| Constant | Maximum | `FailureContext.Limit` | 측정/소유 code |
|---|---:|---|---|
| `MaxSources` | 2,048 | `source_count` | source count / `invalid_definition_source` |
| `MaxSourceIDBytes` | 1,024 bytes | `source_id_bytes` | 개별 raw ID bytes / `invalid_definition_source` |
| `MaxDocumentBytes` | 1 MiB | `document_bytes` | 개별 raw document bytes / `invalid_definition_document` |
| `MaxBatchBytes` | 16 MiB | `batch_bytes` | raw document bytes 합 / `invalid_definition_document` |
| `MaxJSONDepth` | 64 | `json_depth` | 개별 document open container depth / `invalid_definition_document` |
| `MaxDocumentJSONValues` | 65,536 | `document_json_values` | 개별 document JSON value 수 / `invalid_definition_document` |
| `MaxJSONValues` | 262,144 | `json_values` | batch 전체 JSON value 수 / `invalid_definition_document` |
| `MaxDependenciesPerMigration` | 2,047 | `dependencies_per_migration` | valid dependency array length / `invalid_definition_operation` |
| `MaxOperationsPerMigration` | 2,048 | `operations_per_migration` | valid operation array length / `invalid_definition_operation` |
| `MaxFieldsPerCreateModel` | 2,048 | `fields_per_create_model` | recognized CreateModel valid fields array length / `invalid_definition_ir` |

Dependencies가 2,047인 것은 child 하나와 가능한 모든 parent를 합쳐 `MaxSources=2,048` exact
boundary success를 만들기 위한 의도적 값입니다.

MiB는 `1<<20` bytes입니다. Depth는 동시에 열린 object/array container 수이며 top-level object가
1입니다. JSON value count는 object, array와 scalar 각각 1이고 object key는 별도 value로 세지
않습니다. Document와 batch count는 strict scanner가 canonical SourceID order로 세며 maximum+1을
관찰하면 즉시 멈춥니다. Streaming count의 `Actual`은 처음 초과를 관찰한 값(maximum+1), raw
length와 semantic container length의 `Actual`은 exact known length입니다. 모든 byte/value 합은
checked/saturating addition으로 overflow 전에 fail-closed하고 wraparound한 작은 `Actual`을 만들지
않습니다.

### Combined-fault precedence

다음 stage 순서를 public contract로 테스트합니다.

1. `MaxSources` (`source_count`)
2. `MaxSourceIDBytes` (`source_id_bytes`)
3. Bounded SourceID empty → invalid UTF-8 → duplicate validation
4. `MaxDocumentBytes` → overflow-safe `MaxBatchBytes`, 모두 document copy 전에 검사
5. 모든 허용 SourceID/document bytes의 loader-owned snapshot과 canonical SourceID order 확정
6. `MaxJSONDepth` → `MaxDocumentJSONValues` → overflow-safe `MaxJSONValues`
7. 기존 strict regular document framing/outer-envelope error
8. Tuple mismatch
   `definition_format → loader_abi → operation_codec → schema_ir`
9. `MaxDependenciesPerMigration` → `MaxOperationsPerMigration` →
   `MaxFieldsPerCreateModel` semantic container preflight
10. 기존 regular semantic candidate order/decode
11. Existing `migrations.NewPlanner` raw graph validation
12. Digest와 immutable Set/LoadReport publish

`source_id_bytes` guard는 `len(SourceID)`의 O(1) 정보만 읽습니다. Oversized content를 scan/hash/
compare/copy하지 않고 가장 작은 `Actual > MaxSourceIDBytes`를 고르며, 같은 길이는 empty SourceID와
동일 context라 동률입니다. 이 failure의 `SourceID`는 empty입니다. Cap을 통과한 ID에만 기존 empty →
invalid UTF-8 → duplicate와 canonical raw-byte ordering을 적용합니다.

그 밖의 같은 stage candidate는 canonical raw SourceID → RFC 6901 pointer → fixed label/reason rank를
사용하며 input document/token encounter order로 final failure를 고르지 않습니다. Document와 batch
byte limit이 함께 깨지면 document limit이
우선합니다. Depth/value scanner는 traversable JSON prefix에서 limit을 먼저 guard하고, JSON 구조가
깨져 limit을 판정할 수 없으면 추측하지 않고 regular document error를 냅니다. Value-count limit의
pointer는 root `""`; depth failure는 canonical smallest over-depth container pointer입니다. Limit
label rank는 depth → document values → batch values 순입니다.

Tuple mismatch는 **모든 semantic container limit보다 먼저**입니다. Tuple이 맞고
dependencies/operations/recognized CreateModel fields가 valid array로 판정되면 container length를
child shape/type/semantic traversal보다 먼저 검사합니다. 따라서 over-limit valid container 안의
malformed child와 결합하면 `resource_limit_exceeded`가 우선합니다. Container type 또는 operation
discriminator/ancestor가 잘못돼 어떤 cap인지 판정할 수 없으면 canonical `wrong_type`,
`unsupported_operation` 또는 기존 document/semantic error가 우선합니다. 어느 limit/error에서도
partial publish와 handoff는 0입니다.

10개 한도 각각 `maximum-1`, `maximum`, `maximum+1`을 검사합니다. 최소 한 개 이상의 test table은
각 limit과 다른 raw/compatibility/semantic/graph fault를 결합해 위 precedence와 canonical failure
context를 검증하고, semantic 세 한도는 valid-container malformed-child와 undecidable-container를
각각 포함합니다. Limit 숫자/label/precedence를 바꾸는 것은 loader ABI behavior change이므로 별도
work/ADR/contract 없이는 변경하지 않습니다.

### Strict scanner/numeric/canonical parity matrix

MIG-057..064의 여덟 observation만으로 parser/codec parity를 주장하지 않습니다. Product unit test와
`conformance/definitionload/product_equivalence_test.go`의 bounded table이 Accepted ADR-0019의 다음
negative/canonical matrix를 함께 검증해야 합니다.

- Raw invalid UTF-8, UTF-8 BOM과 trailing JSON value
- Decoded-equivalent key를 포함한 any-depth duplicate object member
- Escaped lone high surrogate, lone low surrogate rejection과 valid surrogate pair acceptance
- Outer/compatibility/producer/migration/operation/IR 각 known object의 unknown/missing field
- Decimal, exponent와 leading-zero number syntax rejection
- Tuple coordinate와 recognized `max_length`의 signed-int64 minimum/maximum/underflow/overflow
  classification 및 tuple-before-semantic precedence
- Canonical string의 literal `<`, `>`, `&`, U+2028/U+2029와 control-character escaping
- Returned definitions의 nested `Default` pointer, field/operation slice와 accessor result mutation 뒤
  original `Set`/digest/report가 바뀌지 않는 deep-copy gate

이 matrix도 raw byte/depth/value limit 안에서 생성하고 각 failure의 exact code/stage/pointer/reason과
publish 0을 검사합니다. Signed-int64 acceptance는 해당 coordinate/domain의 이후 compatibility/
semantic 성공을 뜻하지 않으며 lexical domain과 precedence를 검증한다는 의미입니다.

## Atomic snapshot과 schema drift gate

`Load` pipeline은 다음 단계를 정확히 소유합니다.

```text
MaxSources → MaxSourceIDBytes → bounded ID validation
→ document/batch bytes before copy
→ SourceID/document deep-copy + canonical raw SourceID order
→ bounded depth/value scan
→ regular strict JSON framing/outer envelope
→ all-source tuple handshake
→ semantic container limits
→ regular closed codec/fully-normalized IR decode
→ canonical []migrations.Migration
→ migrations.NewPlanner exactly once
→ canonical digest + immutable Set/LoadReport atomic publish
```

Failure 전까지 만든 내부 값은 외부에 publish하지 않습니다. 성공 set은 caller source/temporary
parser/returned accessor와 alias하지 않습니다. Empty load도 `NewPlanner()`를 정확히 한 번 거쳐
report에 `PlannerConstruction=1`, `DefinitionsPublished=0`, `DefinitionSetsPublished=1`을 남깁니다.
Graph failure는 construction count 1/publish 0입니다.

Wire constant는 current IR constant의 alias가 아니라 literal이어야 합니다.

```go
const SchemaIRVersion int64 = 2

var _ [SchemaIRVersion-int64(ir.FormatVersion)]struct{}
var _ [int64(ir.FormatVersion)-SchemaIRVersion]struct{}
```

두 방향 compile-time equality assertion과 unit test가 `SchemaIRVersion == 2` 및
`ir.FormatVersion == 2`를 각각 검사합니다. IR version이 drift하면 build를 깨뜨리고, ADR/codec/
loader ABI/contract를 명시적으로 갱신하기 전까지 새 IR 값을 wire에 자동 전파하지 않습니다.

## 구현 단계

1. `migrations/definition` external/unit tests로 public surface, zero set, alias/concurrency와 10 limits를
   먼저 잠급니다.
2. Bounded strict scanner와 source snapshot을 candidate와 독립된 새 product code로 구현합니다.
3. Tuple/closed codec/normalized IR/digest를 구현하고 literal Schema IR drift gate를 추가합니다.
   `AddField.model_name`은 local regex를 복사하지 않고 fixed-valid synthetic schema의
   `Model.Name`으로 넣어 `ir.Normalize`가 current identifier rule을 검증하게 합니다.
4. Private injected planner-validator의 call count, non-test product AST의 direct
   `migrations.NewPlanner` callsite exactly 1과 report counter를 독립적으로 검사하고 raw graph
   error/report mapping과 atomic publish를 검증합니다. Report 숫자만 exactly-once 증거로 쓰지 않습니다.
5. Non-test product AST의 direct `executor.Migrate` callsite exactly 1, actual handoff counter와 raw lifecycle
   error를 검증합니다. Request snapshot/validation은 existing Executor 내부 소유로 남깁니다.
6. 기존 `conformance/definitionload` candidate를 수정·import하지 않고 새
   `product_equivalence_test.go`에서 Load/Set/report/error/digest black-box parity만 검증합니다.
   Existing `scope_test.go`의 candidate direct `Migrate`/`migrations.NewPlanner` callsite 1/1 lock을
   보존하기 위해 이 새 file은 `.Migrate`나 `migrations.NewPlanner`를 직접 호출하지 않습니다.
   Lifecycle parity는 definition product test와 actual GoDj runner가 소유합니다.
7. MIG-057..064 GoDj runner/godjcheck를 연결하고 manifest status assertion만 승격합니다. Decision
   provenance set의 성공 출력은 Django 결과 호환이라고 과장하지 않고 locked reference oracle로
   구분합니다.
8. Full local/race/CGO-disabled/static gate와 같은 Draft PR #1의 Ubuntu/macOS hosted CI를 수집합니다.
9. ADR acceptance와 status/matrix/evidence를 실제 결과에 맞춰 완료합니다.

## 완료 조건

- [x] 공개 API가 external consumer에서 compile되고 zero `Set`이 canonical empty set으로 동작
- [x] Caller/source/accessor mutation, repeated/concurrent read와 race test에서 alias/race 없음
- [x] Source `Error`가 정확히 9 code만 사용하고 success/error report context가 immutable
- [x] Error.Context/LoadReport.Failure equality, non-limit zero fields와 limit/graph scalar mapping이
  exact contract와 일치
- [x] Raw `PlanningError`와 raw lifecycle error identity/`errors.As` 의미 보존
- [x] 10 limits 각각 maximum-1/equal/+1, overflow-safe sum, combined precedence 통과
- [x] Compatibility-before-semantic-cap, valid-container-before-child와 undecidable-type gate 통과
- [x] Schema IR literal 2, two-way compile drift assertion과 digest pins 통과
- [x] Load당 private injected planner-validator count 1, non-test product AST direct `migrations.NewPlanner`
  callsite exactly 1, report `PlannerConstruction=1`이 독립적으로 일치
- [x] Non-test product AST direct `executor.Migrate` callsite exactly 1과 actual handoff counter 1;
  request snapshot/validation은 existing Executor 내부 소유
- [x] MIG-057..064가 8 `passing`; exact 10 adapter/105 contract=`100 passing + 5 deviation`
- [x] Current adapter/aggregate gate만 10/`100+5`로 전환하고 historical 8-set `83+4`, 9-set
  `92+5` assertions와 이전 9-set artifact pins는 보존; definition-source manifest hash pin은
  status-only 새 bytes에 맞춰 갱신
- [x] Adapter result/report/handoff field가 actual Set/LoadReport/Set.Migrate에서만 생성되고 expected
  constant를 사용하지 않음
- [x] Source/header/operation/graph valid-fixture mutation 각각 observation 변화 + `protocol.Compare`
  diff 또는 success/error shape rejection 유발
- [x] Strict scanner/numeric/canonical escaping matrix와 nested Default/accessor mutation이 product
  unit + product-equivalence에서 통과
- [x] `conformance/definitionload/product_equivalence_test.go`는 direct `.Migrate`/`NewPlanner` call 0,
  기존 `scope_test.go` candidate count 1/1 보존
- [x] Oracle/static/SHA pins와 이전 9 product artifacts/deviation bytes 불변
- [x] Static not-implemented comparison이 ordered mismatch 8개 유지
- [x] `gofmt`, external compile, `go test`, `go vet`, race, CGO-disabled와 `make check` 통과
- [x] Portable/exact Python에서 status-only exception을 제외한 reference behavior 불변
- [x] Existing Ubuntu job에서
  `CGO_ENABLED=0 GOARCH=386 go test -count=1 ./migrations/definition` 통과; `max_length`의
  platform `int` conversion을 실제 32-bit runtime에서 검증
- [x] Draft PR #1의 Ubuntu 24.04/macOS 15 arm64 CI가 exact product completion head에서 PASS
- [x] 13-file completion-documentation commit도 별도 exact head Ubuntu 24.04/macOS 15 arm64
  CI에서 PASS
- [x] ADR/architecture/compatibility/testing/matrix/evidence/CURRENT가 같은 상태를 가리킴

## 진행 기록

- [x] AGENTS/CURRENT/GDJ-0019/ADR-0019/product migration API/definitionload proof 재검토
- [x] Baseline `eecc75f7507414ad6043a090c97b84080ab0fb8b`와 clean checkout 확인
- [x] Public shape, error ownership, bounded scanner/10 limits와 acceptance gate 설계
- [x] GDJ-0020 work/Proposed ADR activation 문서 작성
- [x] 제품 구현
- [x] local/conformance/race 검증
- [x] hosted CI와 completion 문서

## 수정 파일

Activation commit `5942a0bedd6cca7fe93e52d90219a01193c6f534`는 이 work, ADR-0020,
두 index, CURRENT, ROADMAP, OPEN_QUESTIONS의 정확히 7개 문서만 변경했습니다. 현재 product
commit과 completion 문서는 다음 exact allowed path로 제한합니다.

- 제품 loader: `migrations/definition/{definition,load,error,limits,json,codec,ir,digest}.go`
- 제품 loader 검증: `migrations/definition/{definition_test,resource_limits_test,external_test}.go`
- 외부 consumer compile gate: `internal/compiletest/compile_test.go`,
  `internal/compiletest/testdata/migration_definition_external_consumer.go.txt`
- 제품 adapter: `conformance/runners/godj/{migration_definition_source_scenarios,runner,runner_test}.go`
- independent product equivalence:
  `conformance/definitionload/product_equivalence_test.go`
- product aggregation/CLI gate: `Makefile`, `conformance/cmd/godjcheck/{main,main_test}.go`,
  `conformance/internal/protocol/{migration_definition_source_artifacts_test,migration_lifecycle_artifacts_test,migration_state_reconstruction_artifacts_test,write_migration_artifacts_test}.go`
- status-only artifact change:
  `conformance/contracts/migration-definition-source-manifest.json`,
  `conformance/runners/django/tests/test_migration_definition_source_scenarios.py`
- hosted 32-bit gate: `.github/workflows/ci.yml`
- completion/status 정본: 이 work, `docs/adr/0020-migration-definition-loader-product-shape.md`,
  `docs/status/{CURRENT,IMPLEMENTATION_MATRIX,TEST_EVIDENCE}.md`, `work/README.md`,
  `docs/adr/README.md`
- completion/general 문서: `conformance/README.md`, `docs/ARCHITECTURE.md`,
  `docs/COMPATIBILITY.md`, `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/TESTING.md`

Oracle/static/SHA, Python reference scenario, test-only candidate와 기존 migration/backend/DB 제품
코드는 변경하지 않았습니다. 범위 밖 변경은 사용자/다른 agent 소유로 보존합니다.

## 결정된 사항

- 2026-08-09: product loader는 existing `migrations` root를 widen하지 않는 leaf package
  `migrations/definition`으로 제한합니다 — [Accepted ADR-0020](../docs/adr/0020-migration-definition-loader-product-shape.md).
- 2026-08-09: explicit `Source`만 입력받는 pure bounded load를 채택하고 FS/CLI는 금지합니다.
- 2026-08-09: source-owned 9 code와 raw Planner/lifecycle error ownership을 분리합니다.
- 2026-08-09: 10 numeric limit, literal Schema IR 2와 drift gate를 completion 전 필수로 둡니다.
- 2026-08-09: Existing Ubuntu job에만 CGO-disabled GOARCH=386 definition package focused test를
  추가하고 새 job/Windows/DB matrix는 만들지 않습니다.
- 2026-08-09: 기존 Draft PR #1 하나만 사용하며 새 PR은 만들지 않습니다.
- 2026-08-09: Strict scanner의 RFC 6901 failure path는 lazy node/escaped-byte comparator로
  선택하고 최종 winner만 문자열화합니다. 1 MiB 이하 입력에서도 긴 ancestor와 다수 candidate가
  pointer 문자열을 곱셈 할당하지 않도록 adversarial fan-out과 8,281 ordered comparison을
  고정합니다.
- 2026-08-09: MIG-057..064는 Django result parity가 아닌 Accepted ADR-0019 decision set이므로
  `godjcheck` 성공 문구를 `locked reference oracle`로 구분합니다. Django-derived set의 기존
  `locked Django oracle` 문구는 보존합니다.

## 미결정/Blocker

외부 blocker는 없습니다. ADR-0020은 구현/compile/conformance와 exact product-head hosted 결과를
근거로 `Accepted`입니다. CLI/discovery/writer/upgrade는 이 work가 해결하지 않고 Q-010/Q-012와
후속 work에 남깁니다.

## 테스트 증거

- Local/product evidence:
  [EVID-20260809-021](../docs/status/TEST_EVIDENCE.md#evid-20260809-021--gdj-0020-bounded-migration-definition-loader-product-slice)
- Hosted evidence:
  [EVID-20260809-022](../docs/status/TEST_EVIDENCE.md#evid-20260809-022--gdj-0020-github-hosted-product-head-ci)
- Completion-documentation hosted evidence:
  [EVID-20260809-023](../docs/status/TEST_EVIDENCE.md#evid-20260809-023--gdj-0020-github-hosted-completion-documentation-head-ci)
- Final product commit:
  `codex/revision-fenced-migration-lifecycle@6172d843a4bb234592cafc176a8d1191933b141c`
- Completion-documentation/hosted-tested commit:
  `codex/revision-fenced-migration-lifecycle@a5422f2c1ba5db34986564fc065e4b8e28ef0115`
- `make check`: PASS. Full Go test/vet/race, portable Python, oracle/checksum/no-rewrite와 product
  conformance gate를 함께 통과했습니다.
- Focused normal/race/CGO-disabled/vet:
  `./migrations/definition`, `./conformance/definitionload`,
  `./conformance/runners/godj`, `./conformance/internal/protocol`,
  `./conformance/cmd/godjcheck` 모두 PASS.
- Repetition: definition/product-equivalence count 20 PASS.
- `go test ./migrations/definition -run '^$' -fuzz FuzzStrictScannerViaLoadNeverPanics -fuzztime=5s`:
  PASS, 150,235 executions. 독립 final review의 10초 run도 약 133,000 executions에서 PASS했습니다.
- `CGO_ENABLED=0 GOOS=linux GOARCH=386 go test -c ./migrations/definition`: local PASS, ELF 32-bit
  test binary 생성. Draft PR Ubuntu job은 `CGO_ENABLED=0 GOARCH=386 go test -count=1
  ./migrations/definition`을 실제 Linux/386 runtime에서 PASS했습니다.
- `make conformance-check`, `make godj-conformance`, `make python-test`,
  `make python-test-exact`, `make oracle-check`: PASS. Exact Python은 164/164입니다.
- 독립 `godjcheck` 두 process actual은 각각 29,631 bytes,
  `a3f40f9bbee06d4edc4af0a00f40a76da259207995ac20d030101aa2ec3aec87`이며 서로 byte-identical,
  locked oracle과 protocol difference 0입니다. Go actual JSON 자체와 Python oracle bytes의
  동일성은 계약이 아니며 주장하지 않습니다.
- Manifest는 status-only 5,147 bytes,
  `688556c4a338e4ad7f580bfcd4d6121ddda0e72c871d1bfba625c352d22c3488`로 바뀌었습니다.
  Oracle 29,851 bytes `efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`,
  static fixture 1,574 bytes `41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`,
  `SHA256SUMS`는 불변입니다.
- Independent final code review와 integration review는 P0/P1/P2/P3 finding 0으로 종료했습니다.
- Draft PR #1 [run 31309152526](https://github.com/progresshans/godj/actions/runs/31309152526)은
  exact product commit에서 Ubuntu 24.04와 macOS 15 arm64 두 job 모두 PASS했습니다. Ubuntu는
  `make ci`, 실제 Linux/386 focused test, checksum/no-rewrite를, macOS는 focused Go,
  exact Python 164/164, all-oracle/no-rewrite를 통과했습니다.
- Completion-documentation commit `a5422f2c1ba5db34986564fc065e4b8e28ef0115`의 별도 Draft PR #1
  [run 31310002784](https://github.com/progresshans/godj/actions/runs/31310002784)도 두 job 모두
  PASS했습니다. Ubuntu 24.04.4 job
  [93236227654](https://github.com/progresshans/godj/actions/runs/31310002784/job/93236227654)는
  `make ci`, portable Python 164 tests/15 skips, 실제 Linux/386 runtime과 checksum/no-rewrite를,
  macOS 15.7.7 arm64 job
  [93236227698](https://github.com/progresshans/godj/actions/runs/31310002784/job/93236227698)는
  focused CGO-disabled Go, exact Python 164/164, all-oracle/no-rewrite를 통과했습니다.
- Not run: Windows와 PostgreSQL/MySQL/non-SQLite DB matrix. 이 환경은 GDJ-0020 loader의
  backend-neutral pure CPU 경계와 기존 SQLite lifecycle handoff 범위 밖입니다.

## 위험과 rollback

- 가장 큰 위험은 test-only candidate를 그대로 제품으로 승격해 unbounded allocation과 private
  proof shape를 public ABI로 굳히는 것입니다. Candidate는 immutable comparative proof로 남깁니다.
- Mutable slice/pointer를 report/set에서 노출하면 atomic snapshot과 concurrency 계약이 깨집니다.
- Schema IR constant alias는 drift를 조용히 수용하므로 literal+two-way compile gate가 필수입니다.
- Semantic caps를 compatibility 전에 검사하면 old/unknown tuple을 current codec 의미로 탐색합니다.
- Rollback은 새 leaf package/adapter/status 변경만 되돌립니다. Existing migration/backend/DB와
  oracle/static/SHA는 애초에 수정하지 않습니다.

## 다음 정확한 작업

현재 active/ready work는 없습니다. 다음 통합 담당자는 먼저 EVID-023 append/status 교정의 지정
5-file patch를 검증·commit/push하고 그 exact evidence-patch head CI를 확인합니다. 그 뒤
filesystem/module source discovery와 public CLI/project-binary orchestration 중 다음 contract
slice의 목표·비목표·오류 소유권을 별도 work/ADR로 activation해야 합니다. GDJ-0020의 pure
`Source` loader에 path 의미나 I/O를 소급해 추가하지 않습니다.

Completion 문서는 exact product head `6172d843a4bb234592cafc176a8d1191933b141c`와 hosted run
`31309152526` 뒤에 13-file commit `a5422f2c1ba5db34986564fc065e4b8e28ef0115`로 작성됐고, 별도
hosted run `31310002784`에서 exact head로 재검증됐습니다. 현재 EVID-023 append/status 교정
5-file patch 자체는 아직 commit/push되지 않았으며 그 후속 head CI는 `not run/pending`입니다.
통합 담당자는 같은 Draft PR #1에만 evidence commit을 push하고 새 head CI를 별도로 확인합니다.

## 결과와 인수인계

GDJ-0020은 완료됐습니다. Product commit `6172d843a4bb234592cafc176a8d1191933b141c`의 새 bounded
leaf loader, exact 10 limits, atomic Set/LoadReport, raw graph/lifecycle handoff와 열 번째 actual-data
GoDj adapter가 local gate와 Draft PR #1 exact-head hosted gate에서 105 contract의
`100 passing + 5 deviation`을 만족합니다. ADR-0020은 이 근거로 Accepted입니다.

FS discovery/CLI/writer/upgrade/cache, executable/custom operations와 non-SQLite backend는
의도적으로 계속 비목표입니다. Completion-documentation head CI는 별도 run으로 통과했으며,
현재 EVID-023 evidence patch head의 `not run/pending` 상태와 구분합니다. 다음 work는 이 공개
loader를 변경하지 않고 discovery/CLI orchestration을 별도 contract로 activation해야 합니다.
