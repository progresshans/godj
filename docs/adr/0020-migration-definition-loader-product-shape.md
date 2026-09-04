# ADR-0020: Migration Definition Loader Product Shape

- 상태: Superseded by [ADR-0035](0035-pre-release-current-only-format-and-generated-publication.md)
- 날짜: 2026-08-09
- 관련 work/contract: [GDJ-0020](../../work/0020-migration-definition-loader-product-slice.md),
  MIG-057..MIG-064, Q-010, Q-012
- 선행 결정: [ADR-0019](0019-versioned-migration-definition-source.md)
- 대체하는 ADR: 없음

## 맥락

Accepted ADR-0019와 completed GDJ-0019는 caller-provided strict JSON document, exact tuple
`(definition format 1, loader ABI 1, operation codec 1, Schema IR 2)`, closed normalized IR codec,
atomic definition set와 deterministic error/digest/handoff를 contract-only로 잠갔습니다.
GDJ-0019 완료 시점의 `conformance/definitionload/**`는 feasibility를 보이는 test-only candidate일
뿐 importable product가 아니었으며, MIG-057..064도 `oracle_locked`였습니다.

제품화에는 public package/API, source/report/set ownership, graph와 lifecycle error의 소유권,
untrusted document의 CPU/memory 상한과 existing `Executor.Migrate` handoff를 결정해야 합니다.
Root `migrations` API나 backend/DB port를 넓히면 already-loaded lifecycle과 data source concern이
결합됩니다. 반대로 candidate를 그대로 옮기면 test helper의 mutable struct, `ir.FormatVersion`
alias와 unbounded parser behavior를 제품 ABI로 승격합니다.

이 ADR은 activation 시 Proposed였습니다. 아래 exact shape를 test/compile/conformance로 검증했고,
product commit `6172d843a4bb234592cafc176a8d1191933b141c`의 Draft PR #1 exact-head hosted
run `31309152526`까지 통과한 근거로 Accepted합니다. 이후 13-file completion-documentation commit
`a5422f2c1ba5db34986564fc065e4b8e28ef0115`도 별도 exact-head run `31310002784`의 Ubuntu/macOS
두 job에서 통과했습니다. 문서 존재만으로 FS discovery/CLI 등 의도적으로 결정하지 않은 범위까지
public support를 주장하지 않습니다.

## 결정 기준

- Caller가 I/O/source discovery를 소유하고 loader는 pure deterministic CPU boundary일 것
- Empty와 nonempty success, 모든 failure가 atomic immutable snapshot/report를 제공할 것
- Source/document/codec failure와 existing graph/lifecycle failure의 error ownership이 명확할 것
- Raw input, returned accessor와 concurrent caller mutation이 published set과 alias하지 않을 것
- Untrusted size/depth/fan-out가 allocation/semantic traversal 전에 bounded될 것
- Compatibility mismatch가 current codec semantic traversal보다 먼저 fail-closed할 것
- Existing Planner/Reconstructor/Executor public behavior와 package dependency를 재사용할 것
- Schema IR wire version drift가 자동 수용되지 않을 것
- MIG-057..064와 external consumer/race/false-green test로 검증 가능할 것
- FS/CLI/writer/upgrade와 backend expansion을 후속 work로 남길 수 있을 것

## 고려한 선택지

### `migrations` root에 `LoadDefinitions` 추가

Caller discover/source/codec 의미를 existing Planner/Executor root에 직접 노출할 수 있습니다.
하지만 root가 `schema/ir`, JSON codec과 source resource policy까지 소유하게 되고 lifecycle-only
consumer도 definition format dependency를 받습니다. Source error와 graph error도 한 taxonomy로
합치기 쉬워 기존 API 안정성이 나빠집니다.

### Stateful `Loader`와 filesystem/provider interface

Configurable limit/discovery/cache를 한 object에 모을 수 있습니다. 하지만 zero-value/config
의미, provider I/O/context, cache invalidation과 path/module ordering까지 이번 slice에 들어옵니다.
Accepted ADR-0019의 explicit caller-provided bytes와 좁은 product target보다 큽니다.

### Test-only candidate를 package로 이동

Contract parity를 가장 빨리 얻지만 private mutable types, unbounded parser와 current IR constant
alias를 그대로 public behavior로 굳힙니다. Feasibility proof와 independent product implementation
사이 false-green도 사라집니다. Candidate는 수정·import·복사하지 않는 comparative proof로 남겨야
합니다.

### Leaf package의 pure variadic load와 immutable set/report

`migrations/definition`이 Source→strict document→normalized definitions/digest까지만 소유하고,
graph는 existing `NewPlanner`, execution은 existing `Executor.Migrate`에 넘깁니다. Caller I/O와
product codec이 분리되고 zero value/atomicity/resource limit을 외부 compile test로 고정할 수
있습니다. 별도 package와 explicit accessors가 생기는 비용이 있습니다.

## 결정

### Package와 공개 표면

새 leaf package `github.com/progresshans/godj/migrations/definition`을 사용합니다. Import 방향은
`definition → migrations + schema/ir`뿐이며 root migrations/backend/DB는 역으로 import하지
않습니다. 최소 공개 표면은 다음입니다.

```go
type Source struct { SourceID string; Document []byte }
type Producer struct { Name, Version string }
type SourceInfo struct {
    SourceID string
    Producer Producer
    Migration migrations.MigrationKey
}
type GraphSource struct { Migration migrations.MigrationKey; SourceID string }

type FailureContext struct { /* scalar diagnostics + private graph-source snapshot */ }
func (FailureContext) GraphSources() []GraphSource

type LoadReport struct {
    DocumentsReceived, HeadersValidated, OperationsDecoded int
    PlannerConstruction, DefinitionsPublished, DefinitionSetsPublished int
    /* private failure snapshot */
}
func (LoadReport) Failure() (FailureContext, bool)

type ErrorCode string
type Error struct { Category string; Code ErrorCode /* private context */ }
func (*Error) Error() string
func (*Error) Context() FailureContext

type Set struct { /* private immutable snapshot */ }
func Load(...Source) (Set, LoadReport, error)
func (Set) Digest() string
func (Set) Definitions() []migrations.Migration
func (Set) Sources() []SourceInfo
func (Set) Migrate(context.Context, migrations.Executor, migrations.LifecycleRequest) (migrations.ProjectState, error)
```

`Load`는 I/O를 하지 않으므로 context를 받지 않습니다. `Source.Document`는 호출 시 caller-owned지만
bounded preflight 뒤 deep-copy합니다. `Set`, report와 context는 raw source를 보존하지 않고 모든
slice accessor가 fresh deep copy를 반환합니다. `LoadReport.Failure`는 success/error 모두에서 같은
immutable report value를 관찰하게 하며 nullable mutable pointer를 공개하지 않습니다.

Zero `Set`은 canonical empty set이고 `Load()` success와 observationally 같습니다. Digest는
`sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450`입니다. Empty load도 planner
construction 1/set publish 1로 기록합니다. Error return은 항상 zero `Set`, definition/set publish
counter 0과 immutable failure report를 동반합니다.

### Error ownership

Source category `migration_definition_source_error`는 exact 9 code
`invalid_definition_source`, `invalid_definition_document`,
`definition_format_incompatible`, `loader_abi_incompatible`,
`operation_codec_incompatible`, `schema_ir_incompatible`,
`unsupported_definition_operation`, `invalid_definition_operation`,
`invalid_definition_ir`만 사용합니다.

Resource breach는 새 code가 아니라 `Reason=resource_limit_exceeded`, stable limit label과
`Maximum`/`Actual` context입니다. Existing `NewPlanner`가 반환한 `*migrations.PlanningError`는
wrap/reclassify하지 않고 raw 반환하며 report의 `GraphSources`만 source mapping을 보완합니다.
Mapping은 Node/Related/Members의 모든 migration→source pair를 app/name/SourceID 순으로 반환합니다.
Duplicate-node scalar `SourceID`는 canonical Planner input에서 Node identity의 later source를 사용하고
두 source 모두 mapping에 남겨 MIG-062의 `z-duplicate` context와 ambiguity를 함께 해결합니다.
`Set.Migrate`는 fresh deep-copied definitions와 caller-provided immutable `LifecycleRequest` value로
`Executor.Migrate`를 exactly once 호출하고 lifecycle error를 wrap/retry/reclassify하지 않습니다.
Private request kind/targets의 snapshot/validation은 existing Executor 내부 소유입니다. Leaf package는
reflection/`unsafe`를 쓰거나 core API를 widen하지 않습니다. Digest/inventory/raw bytes는
handoff하지 않습니다.

`Error.Context()`와 `LoadReport.Failure()`는 같은 immutable value를 반환합니다. 특정 operation이
직접 소유하지 않으면 `OperationIndex=-1`이고 non-limit context는 empty Limit와 zero
Maximum/Actual입니다. Source count/ID limit은 source/root, document/batch/value limit은
document/root(깊이는 canonical offending pointer), dependencies/operations cap은 semantic container
pointer/index -1, CreateModel fields cap은 semantic fields pointer/owning operation index를 사용합니다.

Graph context는 Stage `graph`, Reason=raw planning code, empty limit/zero maximum/actual입니다.
Duplicate node는 canonical later source와 `/migration`, edge error는 child Node source와
`/migration/dependencies`, cycle은 first canonical Member source와 dependencies pointer를 사용합니다.
`GraphSources()`는 Node/Related/Members의 실제 `(key,source)` pair를 app/name/SourceID 순으로
반환하고 duplicate-node의 두 source pair를 모두 보존합니다.

### Bounded atomic pipeline

```text
MaxSources → MaxSourceIDBytes → bounded ID validation
→ document/batch bytes before copy
→ complete source/document snapshot + canonical source order
→ bounded depth/value scan → regular strict JSON framing/outer extraction
→ all-source tuple compatibility
→ semantic container caps → regular closed codec/normalized IR
→ canonical []Migration → NewPlanner exactly once
→ canonical digest + Set/LoadReport atomic publish
```

한도와 label은 다음 10개입니다.

| Limit | Maximum | Label | Error code |
|---|---:|---|---|
| sources | 2,048 | `source_count` | `invalid_definition_source` |
| source ID bytes | 1,024 | `source_id_bytes` | `invalid_definition_source` |
| document bytes | 1 MiB | `document_bytes` | `invalid_definition_document` |
| batch bytes | 16 MiB | `batch_bytes` | `invalid_definition_document` |
| JSON depth | 64 | `json_depth` | `invalid_definition_document` |
| document JSON values | 65,536 | `document_json_values` | `invalid_definition_document` |
| aggregate JSON values | 262,144 | `json_values` | `invalid_definition_document` |
| dependencies/migration | 2,047 | `dependencies_per_migration` | `invalid_definition_operation` |
| operations/migration | 2,048 | `operations_per_migration` | `invalid_definition_operation` |
| fields/CreateModel | 2,048 | `fields_per_create_model` | `invalid_definition_ir` |

Byte/value 합은 overflow-safe checked/saturating addition입니다. Exact preflight order는
`MaxSources → MaxSourceIDBytes → bounded ID empty/invalid UTF-8/duplicate → document/batch bytes before
copy → snapshot → depth/document-value/batch-value → regular document → tuple →
dependencies/operations/fields caps → regular semantic → Planner → digest/publish`입니다.
Resource reason은 semantic reason rank에 넣지 않고 각 stage의 preflight guard로만 사용합니다.
`MaxSourceIDBytes` guard는 O(1) string length만 읽고 oversized content를 scan/hash/copy하지 않습니다.
여러 실패는 minimum `Actual`로 고르며 same-length는 empty SourceID/동일 context입니다. UTF-8,
duplicate와 raw-byte ordering은 cap을 통과한 ID에만 적용합니다.

Tuple mismatch는 세 semantic container cap보다 우선합니다. Tuple이 맞고 container가 valid
array임을 알 수 있으면 cap을 child semantic traversal보다 먼저 적용합니다. Container/
discriminator/ancestor type이 잘못돼 cap을 판정할 수 없으면 resource error를 추측하지 않고
canonical type/unsupported error를 반환합니다. `MaxDependenciesPerMigration=2,047`은 child migration
하나와 가능한 모든 parent가 함께 `MaxSources=2,048` exact boundary에서 성공할 수 있게 합니다.

`AddField.model_name`은 product-local identifier regex로 검증하지 않습니다. Decoded value를
fixed-valid synthetic Schema IR의 `Model.Name`에 넣고 fixed valid GoName/DBTable/PK/candidate field와
함께 `ir.Normalize`하여 current IR identifier rule을 한 곳에서 검증합니다. Literal Schema IR 2
compile drift gate는 그대로 적용합니다.

각 한도는 maximum-1/equal/+1, combined fault와 semantic undecidable-type을 검증합니다.
Maximum-1/equal은 해당 resource guard만 통과하며 later semantic/graph 성공을 뜻하지 않습니다.
어느 failure든 planner/publish/handoff의 허용 stage 이전 counter는 0입니다. Limit 변경은 loader
ABI behavior change로 취급합니다.

### Schema IR drift fence

Wire coordinate는 alias가 아닌 `const SchemaIRVersion int64 = 2` literal입니다. 두 방향 zero-length
array compile assertion으로 `ir.FormatVersion`과 equality를 요구하고 두 literal을 unit test합니다.
IR이 바뀌면 build가 실패해야 하며 ADR/codec/loader ABI/contract 검토 없이 새 값을 자동 수용하지
않습니다.

### Product conformance와 immutable artifacts

MIG-057..064 status를 정확히 8 `passing`으로 바꾸고 열 번째 GoDj adapter를 `godj-conformance`에
연결합니다. 완료 목표는 105 contract의 `100 passing + 5 deviation`입니다. Django Python 수정은
manifest status assertion 한 줄의 기대값 변경만 허용합니다.

Reference oracle, static not-implemented fixture, `SHA256SUMS`, scenario generator와 decision
provenance는 byte-for-byte 보존합니다. Static fixture comparison의 ordered mismatch 8개도
유지합니다. 기존 Draft PR #1만 사용하고 새 PR은 만들지 않습니다.

Product adapter observation은 actual product에서만 파생합니다. Definition/source/digest는 actual
`Set` accessor, document/header/operation/publish/failure counters는 actual `LoadReport`,
`handoff_calls`는 instrumented actual `Set.Migrate` path가 유일한 source입니다. Checked-in
expected/oracle/static fixture value를 actual constant로 복사하지 않습니다. Source/operation/graph
input mutation은 각각 observation을 바꾸고 `protocol.Compare` non-empty diff를 만듭니다.
Compatibility header mutation은 valid success를 typed error로 바꾸며 result shape가 없으므로
`protocol.Compare`가 success/error shape mismatch를 reject해야 합니다.

## 결과

- Source I/O/discovery와 pure loader, loader와 lifecycle error ownership이 분리됩니다.
- Caller는 zero-value-safe set, canonical digest와 explicit immutable diagnostics를 얻습니다.
- Untrusted JSON과 semantic fan-out가 product allocation/traversal 전에 bounded됩니다.
- Existing Planner/Executor를 바꾸지 않고 contract handoff를 제품화할 수 있습니다.
- 새 package/API와 fixed numeric limits가 pre-1.0 public compatibility 부담이 됩니다.
- `Set.Migrate` convenience는 definition package가 migrations lifecycle에 의존하게 하지만 역방향
  import는 만들지 않습니다.

## 의도적으로 결정하지 않은 것

- Filesystem/module/embed/remote source provider와 discovery order
- CLI/project binary, generator/library semver negotiation과 exit code
- Writer, cache, upgrade/downgrade와 codec/format v2+
- Executable/custom/data operation ABI와 historical app registry
- Expected digest signature/trust, schema/history revision binding
- Adoption/repair/crash reconciliation, multi-DB/non-SQLite backend
- Resource limit runtime configuration이나 per-project override

## 검증

- External consumer compile test로 exact public surface와 import graph 검증
- Unit/property/race test로 zero set, deep-copy accessor, caller mutation과 concurrent read 검증
- 10 limits 각각 maximum-1/equal/+1, overflow-safe aggregate, combined/undecidable precedence 검증
- Error/report test로 exact 9 code, immutable context, raw PlanningError/lifecycle error 보존 검증
- Private injected planner-validator count, non-test product AST direct `migrations.NewPlanner` callsite 1과
  report counter를 독립 검증; report만 exactly-once 증거로 사용하지 않음
- Non-test product AST direct `executor.Migrate` callsite 1과 actual handoff counter를 독립 검증하고 request
  snapshot/validation이 existing Executor 안에 남는지 확인
- Schema IR literal/two-way compile drift gate와 empty/nonempty digest pin 검증
- Existing candidate와 black-box product parity를 비교하되 candidate code를 수정/승격하지 않음
- Product-equivalence는 Load/Set/report/error/digest에 한정하고 direct `.Migrate`/
  `migrations.NewPlanner` call 0을 유지해 existing definitionload scope-test count 1/1 보존;
  lifecycle은 product package test와 actual runner가 검증
- Invalid raw UTF-8/BOM/trailing, any-depth decoded duplicate, lone high/low surrogate와 valid pair,
  unknown/missing field, decimal/exponent/leading-zero, tuple/max_length signed-int64 boundary/overflow,
  canonical `<>&`/U+2028/U+2029/control escaping의 bounded unit+equivalence matrix 검증
- Nested Default pointer, field/operation/accessor mutation이 original Set/digest/report와 alias하지
  않는지 검증
- MIG-057..064 actual adapter, static false-green, 10-set 105-contract/90-cross-binding 검증
- `make check`, full/race/CGO-disabled/vet와 Draft PR #1 Ubuntu/macOS exact-head CI 검증
- Existing Ubuntu job의
  `CGO_ENABLED=0 GOARCH=386 go test -count=1 ./migrations/definition`로 `max_length` host-int
  conversion을 실제 32-bit에서 검증; 새 job/Windows/DB matrix는 추가하지 않음
- Oracle/static/SHA와 이전 product artifact hashes가 불변인지 확인

Acceptance evidence는
[EVID-20260809-021](../status/TEST_EVIDENCE.md#evid-20260809-021--gdj-0020-bounded-migration-definition-loader-product-slice)과
[EVID-20260809-022](../status/TEST_EVIDENCE.md#evid-20260809-022--gdj-0020-github-hosted-product-head-ci),
completion-documentation exact-head evidence는
[EVID-20260809-023](../status/TEST_EVIDENCE.md#evid-20260809-023--gdj-0020-github-hosted-completion-documentation-head-ci)에
기록했습니다. Local `make check`, focused normal/race/CGO-disabled/vet/count-20, 5초 fuzz와 exact
Python 164/164가 통과했고, exact product 및 completion-documentation head의 Ubuntu 24.04 job은
실제 `CGO_ENABLED=0 GOARCH=386` runtime을, macOS 15 arm64 job은 focused Go와 exact
oracle/no-rewrite를 통과했습니다. 현재 EVID-023 append/status 교정 patch 자체의 hosted CI는 아직
`not run/pending`이며 completion-documentation run을 재귀 사용하지 않습니다.
