# ADR-0019: Migration definition source는 explicit versioned data document로 제한한다

- 상태: Accepted
- 날짜: 2026-08-09
- 관련 work/contract: GDJ-0019, MIG-057..MIG-064, Q-010, Q-012
- 대체하는 ADR: 없음

## 맥락

[Accepted ADR-0018](0018-revision-fenced-migration-lifecycle-product-shape.md)은
already-loaded, version-compatible `[]Migration`과 explicit lifecycle request를 existing public
`Executor.Migrate`에 전달하는 안전한 제품 경계를 채택했습니다. Source file, loader/version
handshake와 operation codec는 의도적으로 caller 앞단에 남겼습니다. 이 경계를 정의하지 않은
채 CLI나 file discovery를 붙이면 source read, decode, graph/lifecycle failure와 project build
오류가 한 orchestration에 섞입니다.

Pinned Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`에서 다음을 확인했습니다.

- `MigrationLoader.load_disk()`는 installed app의 migration Python package/module을 import하고
  `Migration` class를 instance로 만듭니다.
- `Migration.__init__()`는 class-level ordered operations/dependencies와 `run_before`/`replaces`를
  instance list로 복사합니다.
- `OperationWriter.serialize()`와 `MigrationWriter.as_string()`은 operation `deconstruct()` 결과,
  imports와 Python constructor를 executable migration source로 만듭니다.
- `serializer_factory()`와 writer tests는 callable, class, custom serializer/custom operation까지
  Python object/source로 표현할 수 있음을 보여 줍니다.
- Loader tests는 module override, import failure, filename/package discovery를 검증합니다.

GoDj는 Django Python 구현/third-party migration module/source compatibility를 목표로 하지
않습니다. JSON v1, tuple, digest와 error order는 GoDj가 새로 정하는 ABI이지 Django exact file
ABI가 아닙니다. 다만 named migration, dependency와 ordered operation이라는 외부 의미는 안정적인
historical artifact로 보존해야 합니다. Go source registration은 compile/link/init과 definition
load를 결속하고 arbitrary user code를 실행합니다. Unversioned JSON은 오래된 artifact를 현재 IR
normalization/codec으로 조용히 재해석할 수 있습니다.

GDJ-0019는 이 결정을 contract/reference/test-only proof로 검증했습니다. Accepted 상태는 아래
GoDj-owned source contract를 채택했다는 뜻이며 product loader API나 런타임 지원 약속은 아닙니다.
제품 구현은 별도 GDJ-0020 activation과 검증 뒤에만 상태를 바꿉니다.

## 결정 기준

- Caller가 source ownership과 I/O를 명시적으로 소유함
- Historical definition을 실행하지 않고 data로만 inspect/hash/validate할 수 있음
- Format/loader/operation/IR version mismatch가 operation decode와 DB mutation 전에 fail-closed
- Same semantic input이 document order, whitespace/key order와 producer version에 무관하게 동일
- Partial load, duplicate last-wins와 map/input-order error nondeterminism이 없음
- Current fully normalized Schema IR v2를 lossless하게 보존하고 implicit default 재해석이 없음
- Current product operation 중 `CreateModel`과 non-PK `char`/`boolean` `AddField`만 좁게 표현함
- Existing Planner/Reconstructor/`Executor.Migrate` 경계와 error ownership을 재사용함
- Python/Go executable callback/custom operation이 data codec으로 우회하지 못함
- Product loader, CLI/project build/discovery와 future codec 확장을 서로 독립적으로 검증 가능
- 기존 product runner, 9 locked artifact와 `92 passing + 5 deviation`을 보존함

## 고려한 선택지

### Go source의 generated registration과 `init()` 수집

Go type checking과 built-in operation constructor를 직접 쓸 수 있습니다. 그러나 모든 definition
load에 project compile/link와 executable initialization이 필요하고, load가 user side effect를
실행할 수 있습니다. Old artifact가 current Go API와 함께 compile되지 않으면 historical state를
읽을 수도 없습니다. CLI/project binary handshake까지 먼저 결정하게 되므로 GDJ-0019의 좁은
compatibility boundary에 맞지 않습니다.

### Unversioned JSON/YAML document

Data-only inspectability는 얻지만 loader가 unknown field, current defaults와 operation union을
암묵적으로 선택합니다. IR/codec이 바뀔 때 old artifact 의미가 조용히 바뀔 수 있습니다. YAML의
tag/alias/type coercion은 strict deterministic subset을 별도로 정의해야 해 최소 format보다
복잡합니다.

### 하나의 project-wide definition bundle

한 파일 digest와 atomicity는 단순합니다. 하지만 migration identity 단위 review/merge/generation,
incremental replacement과 source diagnostics가 불필요하게 결속됩니다. Bundle 파일명/위치는
filesystem/project discovery 결정까지 요구합니다.

### Django Python migration source와 module discovery 호환

Django ecosystem source를 직접 읽을 수 있지만 Python runtime, import system, arbitrary code,
custom serializer/operation과 Django internal object compatibility가 필요합니다. 프로젝트 정의와
Go redesign 원칙에 정면으로 어긋납니다.

### Caller-provided strict versioned data document

Caller가 이미 얻은 bytes와 opaque source label만 넘기고 loader는 path/module을 해석하지
않습니다. Strict envelope, closed codec, normalized IR와 deterministic batch/digest를 각각
version으로 잠글 수 있습니다. Filesystem/CLI를 후속 orchestration으로 분리하고 executable
escape를 fail-closed할 수 있습니다. Format writer/upgrade와 resource limits를 추후 결정해야 하는
비용이 있습니다.

## 결정

Migration definition source contract는 **caller-provided explicit versioned data document**를
사용합니다. MIG-057..064가 아래 source/version/codec/digest/error/handoff 경계를 검증했으므로
이를 Accepted합니다. 이 결정은 Django Python source ABI, file/module discovery 또는 product
loader 구현을 뜻하지 않습니다.

### Source와 document ownership

- Caller는 non-empty unique valid-UTF-8 opaque `source_id`와 caller-owned UTF-8 JSON bytes batch를
  제공합니다. Caller는 synchronous snapshot이 끝날 때까지 input을 concurrent하게 mutate하지
  않아야 합니다. Loader는 load 시작에 entry/bytes를 deep-copy하므로 snapshot 반환 뒤 caller
  mutation과 alias하지 않습니다. SourceID는 exact UTF-8 bytes로 비교/정렬하고 Unicode normalization이나 path semantics를
  적용하지 않습니다. Invalid UTF-8 SourceID는 `invalid_definition_source`입니다.
- Loader는 path, filename, module/package, embedded FS와 environment를 읽거나 `source_id`를
  해석하지 않습니다. Empty batch는 valid empty definition set입니다.
- 한 document는 정확히 한 migration `(app,name)`, dependencies와 ordered operations를 담습니다.
- Top-level/nested object는 known required field만 허용합니다. Unknown/missing field, any-depth
  duplicate key, trailing JSON value, invalid UTF-8, escaped lone surrogate와 non-integer number는
  거부합니다. Version 좌표와 recognized `create_model.model.fields[]`/`add_field.field` arm의
  `max_length` lexical JSON integer domain은 signed int64입니다. Closed union 밖 operation payload
  내부는 known field arm으로 해석하지 않습니다. Signed-int64 밖 integer는 handshake 전
  `invalid_definition_document`/`out_of_range`, domain 안의
  version 값이 exact `1/1/1/2`와 다르면 coordinate-specific `*_incompatible`입니다. `max_length`는
  추가로 `0..2147483647`이어야 합니다. 따라서 성공한 v1 digest의 모든 number는 RFC 8785/I-JSON
  safe-integer domain에 있습니다. 이는 byte/depth/count 같은 product resource-limit 결정과
  구분합니다.
- `producer={name,version}`은 non-empty Unicode-scalar string인 필수 provenance지만 compatibility
  equality/digest input은 아닙니다.

### Version handshake

V1 loader는 다음 tuple의 exact equality만 받습니다.

```text
(definition_format, loader_abi, operation_codec, schema_ir) = (1, 1, 1, 2)
```

Definition format은 envelope/identity, loader ABI는 atomic/order/digest/handoff, operation codec은
wire operation union, Schema IR은 normalized model/field 의미의 version입니다. Lower/higher/unknown
어느 mismatch도 best-effort decode, range negotiation, producer-version fallback 없이 operation
decode/publish/handoff 전에 실패합니다. MIG-060은 source/consumer 사이의 pre-construction
environment handshake이지 Q-010의 global CLI/library/generator semver resolution이 아닙니다.
Generator/producer version은 audit 정보이지 tuple의 대체물이 아닙니다.

### Fully normalized IR와 operation codec

- Codec v1은 `create_model`과 `add_field` 두 discriminator만 허용합니다.
- `CreateModel`은 explicit app label과 fully normalized Schema IR v2 `Model`을, `AddField`는
  app label/model name과 fully normalized IR v2 `Field`를 담습니다.
- Operation app은 enclosing migration app과 같아야 합니다. `AddField.model_name`은 current database
  identifier rule을 독립적으로 통과해야 하며 migration/dependency key validation은 existing
  `NewPlanner`에 위임합니다.
- Derived `go_name`, `db_table`, `column`, boolean/max-length와 absent/typed scalar default까지
  explicit semantic value로 decode합니다.
- Field kind는 current IR v2의 `auto`/`char`/`boolean` closed set입니다. Default는 `null`, exact
  `{"kind":"string","string":...}` 또는 `{"kind":"boolean","boolean":...}`뿐입니다.
  AutoField default는 `null`, CharField는 string, BooleanField는 boolean만 허용합니다. Current IR에
  있지만 이 field set에서 성공할 수 없는 `ScalarInteger`/integer wire arm은 v1에서 제외합니다.
  Kind가 다른 scalar/extra member를 거부하고 `omitempty`나 host-language zero value를 wire 의미로
  사용하지 않습니다.
- `CreateModel`은 wire model을
  `ir.Schema{FormatVersion:2, AppLabel:app_label, Models:[]ir.Model{model}}`로 감싸 Normalize한 전체
  schema가 wire-derived wrapper와 deep-equal할 때만 받습니다.
- `AddField`는 `char`/`boolean`, `primary_key=false` candidate만 허용하고 `auto`/PK candidate는
  `invalid_definition_ir`입니다. Exact validation wrapper의 schema app은 operation app, model은
  Name/DBTable `_godj_loader_validation`, GoName `GodjLoaderValidation`입니다. Fields index 0은
  Name/Column `_godj_loader_pk`, GoName `GodjLoaderPK`, Kind `auto`, PrimaryKey `true`, Nullable
  `false`, MaxLength `0`, Default `nil`이고 candidate는 index 1입니다. Candidate와 synthetic
  Name/Column/GoName이 충돌하는 동안 synthetic Name/Column에 `_`, GoName에 `X`를 lockstep
  append합니다. Normalize한 wrapper 전체와 `Models[0].Fields[1]`이 각각 원래 wrapper/candidate와
  deep-equal해야 하며 synthetic value는 output/digest에서 버립니다.
- Diagnostic pointer는 AddField kind/PK restriction을 candidate `/kind`/`/primary_key` member에,
  CreateModel의 no-auto/no-PK와 duplicate field identity aggregate를 model `/fields` list에 둡니다.
  Field-kind/typed-default cross-invariant는 candidate `/default`에 둡니다. Trusted default
  discriminator/payload가 있으면 extra default member와 별개로 이 후보를 수집하고, 모든
  shape/type fault와 함께 canonical pointer order로 하나를 선택합니다.
- Loader가 old artifact의 name/column/table/default를 current rule로 보충하지 않습니다.
- Operation과 field order는 보존합니다. Dependency는 unique identity set으로 canonical sort합니다.
- Executable-bearing discriminator/key, callback/function/import path, raw SQL, custom
  operation/field/validator, `run_before`, `replaces`, squash/merge와 data migration은 codec v1에
  없습니다. 반대로 allowed CharField string default `"print(...)"` 같은 source-looking text는 inert
  scalar로 lossless 보존하고 keyword scan이나 실행을 하지 않습니다.

### Atomicity, deterministic order와 digest

Loader는 batch 전체에 다음 stage를 적용합니다.

```text
snapshot source_id/bytes + validate SourceID set
→ all-document full JSON framing/duplicate-key/scalar validity + strict outer envelope extraction
→ all parseable documents의 tuple exact handshake
→ identity/dependency/closed operation/fully-normalized IR semantic decode
→ loader construction당 canonical []Migration으로 existing migrations.NewPlanner exactly once
→ canonical digest compute + loader-owned deep-copied snapshot atomic publish
→ MIG-064 test-only proof에서 definitions/request만 Executor.Migrate handoff
```

어느 stage라도 실패하면 partial result/cache, published definition set과 lifecycle handoff는 0입니다.
Full-document framing은 any-depth duplicate key, invalid UTF-8와 escaped lone surrogate를 tuple 전에
거부합니다. 이 단계의 unknown/missing 판정은 outer `compatibility`/`producer`/`migration` envelope,
nested operation/IR unknown/missing 판정은 tuple 뒤 semantic stage가 소유합니다. Canonical
`[]Migration`을 loader construction당 existing `NewPlanner`에 한 번 전달해 duplicate identity/dependency와 current
`invalid_node → duplicate_node → invalid_dependency → duplicate_dependency → dependency_not_found →
dependency_cycle` order를 전부 위임하며 loader가 이를 재구현하지 않습니다.

Success definition order는 app/name UTF-8 byte ascending, dependency order도 identity ascending이며
operation/field order는 보존합니다. Semantic digest는 lowercase `sha256:<hex>`로 표시합니다.
Hash input은 RFC 8785 UTF-8, no BOM/newline인 정확히 다음 logical document입니다.

```json
{
  "domain": "godj:migration-definition-set:v1",
  "compatibility": {
    "definition_format": 1,
    "loader_abi": 1,
    "operation_codec": 1,
    "schema_ir": 2
  },
  "definitions": []
}
```

Empty set exact canonical bytes는
`{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"definitions":[],"domain":"godj:migration-definition-set:v1"}`이고 digest는
`sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450`입니다. Exact
one-CreateModel 470-byte nonempty canonical input은 다음과 같습니다.

```text
{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"definitions":[{"app":"alpha","dependencies":[],"name":"0001_initial","operations":[{"app_label":"alpha","kind":"create_model","model":{"db_table":"alpha_widget","fields":[{"column":"id","default":null,"go_name":"ID","kind":"auto","max_length":0,"name":"id","nullable":false,"primary_key":true}],"go_name":"Widget","name":"widget"}}]}],"domain":"godj:migration-definition-set:v1"}
```

그 digest는
`sha256:07e61f8d956002cff0d7fe2db10c16ea4a30829e9f0ced09c69c40ff2c2399bc`입니다. Canonical
definitions item/wrapper/null shape는 GDJ-0019 exact contract를 따릅니다. Input order,
whitespace/object key order, producer, SourceID와 raw bytes는 제외하고 tuple과 semantic
identity/dependency/operation/normalized IR은 포함합니다. SourceID는 non-empty unique diagnostic와
precedence handle일 뿐 migration identity/dependency/recorder key가 아니며 relabel은 digest를 바꾸지
않습니다. Digest는 externally trusted expected digest와 비교할 때만 corruption을 검출하는 semantic
fingerprint이며 스스로 trust를 부여하지 않습니다. Recorder revision fence, history/schema drift나
ABA binding도 아닙니다.

### Deterministic errors와 protocol observation

Stage precedence는 source snapshot → full-document framing/outer envelope → all-source tuple → semantic
decode → existing graph validation → digest/publish → optional lifecycle입니다. Syntax/duplicate/lone
surrogate로 trustworthy header가 없으면 document error가 version보다 먼저이고, parse 가능한
version+unsupported-operation fault는 version이 먼저입니다. Candidate key는 stage마다 고정합니다.

- Source: `(reason rank, raw source_id bytes)`
- Document/semantic: `(source_id raw UTF-8 bytes, RFC 6901 pointer UTF-8 bytes, reason rank)`
- Compatibility: `(definition_format < loader_abi < operation_codec < schema_ir, source_id bytes)`
- Graph: canonical `[]Migration`에 대한 existing `NewPlanner` category/code, primary Node와 order;
  Related/Members는 already-locked Planner 의미로 남기고 `metrics.failure`에 중복하지 않음

RFC 6901 root pointer는 `""`이고 `~`/`/`는 `~0`/`~1`입니다. Syntax/invalid UTF-8/trailing value는
root, duplicate key는 duplicate member, lone surrogate와 shape error는 offending value/member
pointer를 사용합니다. Source code는 `invalid_definition_source`, document는
`invalid_definition_document`, tuple은 `definition_format_incompatible`/
`loader_abi_incompatible`/`operation_codec_incompatible`/`schema_ir_incompatible`, semantic은
`unsupported_definition_operation`/`invalid_definition_operation`/`invalid_definition_ir`입니다.
Graph error는 existing `migration_graph_error` code를 그대로 전달합니다.

Semantic reason rank는 `unsupported_operation < invalid_operation < invalid_ir < wrong_type <
out_of_range`입니다. 뒤의 두 reason은 tuple handshake 뒤 nested field `max_length`의 type과
cross-platform `0..2147483647` 경계에만 사용합니다. Signed-int64 lexical overflow는 앞선
document-stage `out_of_range`이며 recognized operation arm에만 적용합니다.

Existing protocol v2 `ObservedError`는 stable category/code와 non-contract message만 유지합니다.
Failure context는 top-level error field가 아니라 typed Value
`metrics.failure={stage,source_id,json_pointer,app,name,operation_index,reason}`이며 모든 field가
항상 존재합니다. 적용되지 않는 string은 `""`, operation index는 `-1`, invalid-UTF-8 SourceID만
`hex:<lowercase raw bytes>`이고 success에서는 explicit `null`입니다. Success result의 공통 envelope는
`compatibility`, `definition_set={digest,definitions[]}`, SourceID order의
`sources[]={source_id,producer:{name,version},app,name}`, `handoff={attempted,calls,observed_digest}`를
소유하며 definitions를 다른 field에 중복하지 않습니다. MIG-057/058/059 handoff는 exact
`{attempted:false,calls:0,observed_digest:null}`, MIG-064는
`{attempted:true,calls:1,observed_digest:definition_set.digest}`입니다. Metrics는
`definitions_published`와 `definition_sets_published`를 모두 두어 empty/nonempty success는 후자가
`1`, failure는 `0`임을 구분합니다. Failure comparison은 `error`, `metrics`이고 protocol v3나
ObservedError 확장은 없습니다. Exact message와 public Go error type/wrapping은 GDJ-0020 범위입니다.

이 네 field는 공통 success envelope입니다. MIG-059만 result에
`canonicality={equivalent_definition_set,equivalent_digest,operation_order_changed_digest,operation_order_is_semantic,source_relabel_preserved_digest}`를
추가해 equivalent syntax/permutation/relabel과 operation-order semantic matrix를 한 success
observation으로 잠급니다. MIG-064만
`lifecycle={targets,plan,returned_state}`를 추가해 public reference handoff 결과를 잠급니다.
MIG-057/058은 공통 envelope 외 result field가 없고, 두 extension 모두 canonical definitions를
중복하지 않습니다.

### Existing lifecycle handoff

MIG-064 Python oracle은 explicit-entry Django public loader/executor route의 reference final state를
관찰합니다. 별도 `conformance/definitionload/*_test.go` Go feasibility proof는 loader-owned
deep-copied definitions/request를 source re-read 없이 actual public
`Executor.Migrate(ctx, definitions, request)`에 exactly once 전달합니다. Digest/source audit은
coordinator가 snapshot 옆에 보유하고 actual call argument에는 넣지 않습니다. Go Executor 내부
`NewStateReconstructor`/Planner 재검증은 loader-construction `NewPlanner` 1회 count 밖입니다. 어느
증거도 product loader support claim이 아닙니다. Historical reconstruction, target/history/revision
fence, operation state와 handoff 뒤 error/last durable state는 Accepted ADR-0018 lifecycle이 계속
소유합니다.

## 결과와 비용

- Historical migration definition을 arbitrary code 실행 없이 inspect/validate/hash할 수 있습니다.
- Source I/O와 loader가 분리되어 in-memory, embedded, remote source도 같은 byte contract를 쓸 수
  있지만 discovery/support는 각각 별도 결정입니다.
- Tuple mismatch와 unsupported operation이 DB/lifecycle 전에 fail-closed합니다.
- Canonical digest와 order로 cross-platform/repeated generation drift를 검출할 수 있습니다.
- Fully normalized artifact는 현재 generator default 변화로 old history가 재해석되는 일을 막습니다.
- V1은 `CreateModel`과 non-PK `char`/`boolean` `AddField`만 표현하므로 현재 제품 범위 밖
  migration을 load할 수 없습니다.
- Strict duplicate-key decoder, canonical JSON/digest와 batch allocation/resource-limit 구현 비용이
  생깁니다.
- Producer provenance와 semantic digest를 별도로 보존해야 합니다.
- Format/codec/IR evolution은 tuple compatibility matrix, writer/upgrade와 새 contract가 필요합니다.
- Caller-provided bytes만으로는 사용자에게 편한 file/module discovery가 없습니다. 이는 의도한
  layering이며 CLI 이후 범위입니다.

## GDJ-0019와 GDJ-0020, CLI 순서

GDJ-0019는 contract/reference/test-only feasibility만 수행했고 제품 source, GoDj runner와 current
product classification을 바꾸지 않았습니다. 완료 결과는 10 reference set, 105 contract,
90 ordered cross-binding과 `92 passing + 5 deviation + 8 oracle_locked`입니다.

별도 GDJ-0020은 이 Accepted ADR을 전제로 product loader API, deep-copy/value ownership,
resource limits, structured Go error와 `Executor.Migrate` wiring을 구현합니다. Exact product 목표는
`100 passing + 5 deviation`이지만 GDJ-0019에서는 그 상태를 주장하지 않습니다.

GDJ-0019의 test-only proof source는 `conformance/definitionload/**` 안의 `*_test.go`로만 제한하며
importable non-test package/product source를 만들거나 product가 이를 import할 수 없습니다. Fixture는
허용된 runner/test source에 embedded하고 별도 `testdata/**`가 필요하면 먼저 work의 allowed paths를
amend합니다. MIG-057..064 manifest의 모든 contract는 GoDj 결정 provenance
`kind=decision, reference=ADR-0019, derived=false`를 가져야 합니다. Django provenance는 named
identity/dependency/ordered-operation 의미에만 추가할 수 있습니다.

CLI/project binary는 그 뒤에 explicit loader를 호출하는 orchestration으로 설계합니다. 이 순서가
source/version error와 project discovery/build/exit-code error를 분리하고, CLI version이 document
tuple을 암묵적으로 대신하지 못하게 합니다. Q-010의 global CLI/library/generator handshake는
ADR-0019 tuple과 관련되지만 이 ADR 하나로 해결되지 않습니다.

## 의도적으로 결정하지 않은 것

- Public Go type/function/package name, sync/stream API와 exact numeric resource limit
- Definition writer/generator, cache, file extension/directory layout와 atomic file replacement
- Global CLI/project binary/generator compatibility와 build cache/exit code
- Go source registration, plugin과 embedded/remote/filesystem source adapter 지원
- Codec v2+, data migration callback, raw SQL/custom operation과 security sandbox
- `run_before`, replacement/squash/merge/optimizer/fake/fake-initial
- Graph conflict resolution은 하지 않고 missing/cycle은 existing `NewPlanner` validation을 재사용;
  applied-history/target 오류는 existing lifecycle 소유
- Migration artifact signing, trust, encryption과 supply-chain distribution
- Database adoption/repair, crash reconciliation, multi-DB/non-SQLite execution

## 검증 근거

GDJ-0019는 다음 acceptance gate를 모두 검증했습니다.

- MIG-057..064 exact manifest/provenance/oracle/static fixture와 two-process byte identity
- Tuple 네 좌표의 lower/higher mismatch가 operation decode/handoff 전에 거부됨
- Strict JSON unknown/missing/any-depth duplicate/trailing/UTF-8/lone-surrogate/number mutation rejection
- `max_length 0..2147483647` boundary와 overflow/decimal/exponent, excluded integer-default arm rejection
- Exact wrapper equality와 CreateModel/non-PK char·boolean AddField lossless round-trip; AddField
  auto/PK rejection
- Executable-bearing key/custom/unknown discriminator가 code execution 없이 fail-closed하고 allowed
  source-looking CharField string은 inert/lossless
- Input/source/syntax permutation canonical digest와 operation/field semantic mutation detection
- Malformed/duplicate batch permutation의 partial publish/cache/handoff 0, deterministic first error
- Syntax+version, version+unsupported operation, semantic+graph combined fault의 caller-order
  permutation이 stage-major + canonical SourceID precedence를 유지
- Existing `Executor.Migrate` exactly-once/deep-copied-definition handoff, digest argument 없음과 source
  re-read 0; MIG-064 canonical success outcome
- 10 set/105 contract uniqueness와 90 ordered cross-binding rejection
- 모든 MIG-057..064 decision provenance와 synthetic GoDj `oracle_locked` 의미를
  `COMPATIBILITY.md`에 명시
- Existing 9 product adapter, `92 passing + 5 deviation`과 prior locked artifacts/checksum lines 불변
- GoDj runner가 MIG-057..064 exit 2/no actual이고 product support claim이 없음
- Independent exact-contract, scope/non-goal과 false-green audit

상세 실행 증거는
[EVID-20260809-019](../status/TEST_EVIDENCE.md#evid-20260809-019--gdj-0019-migration-definition-source-compatibility-contracts)에
기록했습니다. Activation `058bc0aba66c78e344f2d8bc87afa2995b2b585a`, machine artifact
`4c7b8390c34ce4f9c4bd9524f22779208cff0df0`, feasibility/final code
`58c66fdc751867a3c2f1541a8594c6615c9fbb59`에서 10 reference set/105 unique contract/90 ordered
cross-binding, exact reference 164/164와 Go/Python canonical error 59/59를 확인했습니다. Product는
9 adapters/97 contracts와 `92 passing + 5 deviation` 그대로이고 새 8개는 synthetic
`oracle_locked`입니다. Completion head `4d9a64a0c42406bda931820f7eb38a0f737d117c`는 Draft PR #1
[run 31302983804](https://github.com/progresshans/godj/actions/runs/31302983804)에서 Ubuntu 24.04와
macOS 15 arm64 gate가 모두 통과했으며, checkout-scoped 결과는
[EVID-20260809-020](../status/TEST_EVIDENCE.md#evid-20260809-020--gdj-0019-github-hosted-ubuntu와-darwinarm64-ci)에
기록했습니다. 이 hosted 결과도 product loader 지원을 뜻하지 않습니다.

상세 payload, allowed paths와 완료 조건은
[GDJ-0019](../../work/0019-migration-definition-source-compatibility-contracts.md)에 기록합니다.
