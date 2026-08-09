---
id: GDJ-0019
status: completed
updated: 2026-08-09
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "3269d662a8b403b5d73096c04abf9fa630b22974"
depends_on: ["GDJ-0018"]
contracts: ["MIG-057..MIG-064", "Q-010", "Q-012"]
allowed_paths: ["Makefile", "NOTICE.md", "conformance/README.md", "conformance/contracts/migration-definition-source-manifest.json", "conformance/fixtures/godj-migration-definition-source-not-implemented.json", "conformance/runners/django/runner.py", "conformance/runners/django/migration_definition_source_scenarios.py", "conformance/runners/django/tests/test_migration_definition_source_scenarios.py", "conformance/runners/django/tests/test_runner_safety.py", "conformance/runners/django/tests/test_scenarios.py", "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json", "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS", "conformance/internal/protocol/migration_definition_source_artifacts_test.go", "conformance/internal/protocol/migration_lifecycle_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "conformance/cmd/godjcheck/main_test.go", "conformance/definitionload/**", "docs/ARCHITECTURE.md", "docs/COMPATIBILITY.md", "docs/LICENSING.md", "docs/OPEN_QUESTIONS.md", "docs/ROADMAP.md", "docs/SOURCES.md", "docs/TESTING.md", "docs/adr/0019-versioned-migration-definition-source.md", "docs/adr/README.md", "docs/status/CURRENT.md", "docs/status/IMPLEMENTATION_MATRIX.md", "docs/status/TEST_EVIDENCE.md", "work/0019-migration-definition-source-compatibility-contracts.md", "work/README.md"]
integration_owner: "one primary agent"
---

# Migration Definition Source Compatibility Contracts

## 사용자에게 보이는 결과

Caller가 명시적으로 제공한 migration definition document 집합을 GoDj가 어떤 버전과 데이터
경계에서 같은 loader-owned deep-copied definition snapshot으로 해석해야 하는지 MIG-057..064로
잠급니다. Go의 `[]Migration` 자체가 immutable하다고 주장하지 않고 caller/source와 alias하지
않는 value ownership을 계약합니다. 성공한 load는 문서 순서나 JSON 공백/객체 key 순서와
무관한 canonical definition set과 digest를 만들고, 실패한 load는 부분 definition이나 lifecycle
side effect를 노출하지 않습니다.

이번 GDJ-0019는 **contract-only** 작업입니다. 제품 loader, public Go API, migration 파일 탐색,
project binary, `godj migrate` CLI를 제공하지 않습니다. 완료 분류는 기존 9 product set의
`92 passing + 5 deviation`과 새 8개의 `oracle_locked`를 분리한
`92 passing + 5 deviation + 8 oracle_locked`입니다. 이 계약을 제품에 연결하는 작업은 별도
[GDJ-0020](#gdj-0019와-gdj-0020-경계)이며, GDJ-0019 완료만으로 source load 지원을 주장하지
않습니다.

## 목표

- MIG-057..064의 tenth manifest/oracle/not-implemented fixture와 exact provenance 구축
- Caller-provided document만 받는 source boundary와 zero-I/O empty source 의미 고정
- Strict data-only JSON definition format v1과 exact compatibility tuple `(1, 1, 1, 2)` 고정
- Fully normalized Schema IR v2와 closed operation codec v1의 lossless decode 경계 고정
- Codec v1을 `CreateModel`과 non-PK `char`/`boolean` `AddField`로 제한하고
  executable/custom payload를 fail-closed
- Batch 전체의 atomic all-or-nothing publish와 deterministic error precedence 고정
- Semantic canonical order와 SHA-256 definition-set digest 고정
- Valid definition set을 exactly once existing public `Executor.Migrate`에 넘기는 handoff 고정
- 10 reference set, 105 contract의 global uniqueness와 90 ordered cross-binding 거부
- 기존 `92 passing + 5 deviation`, 9 product adapter와 이전 9개 locked artifact 보존
- [Accepted ADR-0019](../docs/adr/0019-versioned-migration-definition-source.md)의 결정을
  contract/reference/test-only proof로 검증하되 제품 공개 API와 구분

## 비목표와 금지 경계

- `migrations/**`, `migrations/backend/**`, `db/**` 제품 source 변경 또는 public loader 구현
- `conformance/runners/godj/**` GoDj product runner/adapter 추가
- 현재 9개 product manifest의 status, 기존 `92 passing + 5 deviation` 분류 변경
- 기존 9개 manifest/oracle/static/deviation payload의 수정 또는 재생성
- 기존 `SHA256SUMS` entry의 교체; 새 oracle checksum 한 줄 추가 시 기존 줄은 byte-for-byte 보존
- Directory scan, filename convention, module/package import, embedded FS, glob, watch와 source discovery
- Go source registration, `init()` side effect, plugin/reflection, generated Go runner 또는 user code 실행
- `run_before`, `replaces`, squash/merge, `initial`, `atomic`, fake/fake-initial과 optimizer
- `RunPython`, `RunSQL`, callback/function symbol, raw SQL, arbitrary custom operation/field/validator
- Data migration callback/plugin ABI, historical app registry와 executable default
- Public `godj migrate`, `showmigrations`, `makemigrations`, project build/discovery와 CLI exit code
- Global CLI/project library/generator handshake 전체 Q-010 해결
- Existing database adoption/repair, crash reconciliation, schema drift와 non-SQLite backend
- GDJ-0020의 product package/API 이름, resource limit 숫자와 upgrade command 확정

## 선행 조건과 기준 상태

- 활성 baseline은
  `codex/revision-fenced-migration-lifecycle@3269d662a8b403b5d73096c04abf9fa630b22974`
  (`docs: record hosted lifecycle validation`)입니다.
- [GDJ-0018](0018-revision-fenced-migration-lifecycle-product-slice.md)은 already-loaded,
  version-compatible `[]Migration`과 existing public
  `Executor.Migrate(ctx, definitions, request)`를 제품화했고 MIG-047..056을 Verified했습니다.
- GitHub-hosted 기준은 PR #1 run 31295886061의 Ubuntu 24.04와 macOS 15 arm64 PASS이며
  [EVID-20260809-018](../docs/status/TEST_EVIDENCE.md#evid-20260809-018--gdj-0018-github-hosted-ubuntu와-darwinarm64-ci)에
  기록돼 있습니다.
- 현재 protocol v2는 9 reference/product adapter set, 97 contract이며 제품 분류는
  `92 passing + 5 deviation`입니다. 9 set의 72 ordered cross-binding은 거부됩니다.
- Exact reference profile은 Django 6.1 tag/commit
  `fe0a859f537d4238cf49fca39073513206f83122`, CPython 3.14.3, SQLite 3.50.4,
  UTC/C locale, macOS arm64입니다.
- Activation 시 baseline checkout은 clean이었습니다. 범위 밖에서 이후 생긴 사용자/다른
  agent 변경은 보존하고 한 명의 integration owner만 stable 문서와 artifact를 통합합니다.

## Django Reference / Provenance

Pinned Django 6.1 source에서 다음 symbol을 exact commit으로 다시 확인했습니다.

- `django/db/migrations/loader.py::MigrationLoader.load_disk`: installed app의 Python migration
  package를 import하고 module을 열거한 뒤 `Migration` class를 실행 가능한 object로 load
- `django/db/migrations/loader.py::MigrationLoader.build_graph`: disk migration instance와
  dependency를 graph에 결합
- `django/db/migrations/migration.py::Migration.__init__`: class-level operations,
  dependencies, `run_before`, `replaces`를 instance list로 복사
- `django/db/migrations/writer.py::OperationWriter.serialize`: operation `deconstruct()`와 Python
  import/constructor source를 serialize
- `django/db/migrations/writer.py::MigrationWriter.as_string`: executable Python migration module 생성
- `django/db/migrations/serializer.py::serializer_factory`: Python value/callable/class serializer 선택
- `tests/migrations/test_loader.py::LoaderTests.test_load`,
  `test_load_import_error`, `test_ignore_files`, `test_loading_namespace_package`
- `tests/migrations/test_writer.py::OperationWriterTests`,
  `WriterTests.test_simple_migration`, `test_custom_operation`, `test_sorted_dependencies`,
  `test_register_serializer`

Django가 Python module/file, import, arbitrary deconstructible/custom operation과 serializer를
지원한다는 사실은 provenance입니다. GoDj는 그 source 형식이나 Python object model과 호환하지
않습니다. MIG-057..064의 JSON envelope, tuple, digest와 source error order는 **GoDj ADR
결정**이지 Django exact file ABI가 아닙니다. Pinned Django에서 보존하는 외부 의미는 ordered
operation과 dependency를 가진 named migration definition이 loader graph/lifecycle 입력이
된다는 점입니다. Go에서는 이를 명시적이고 versioned한 data document로 재설계합니다.

## Contract set

| ID | Phase | Comparison | 잠글 외부 동작 |
|---|---|---|---|
| MIG-057 | `construction` | `result`, `metrics` | 순서가 뒤섞인 explicit `CreateModel`/non-PK `char`·`boolean` `AddField` document를 load하면 identity로 정렬된 loader-owned deep-copied lossless definition set, canonical digest와 source inventory가 만들어짐 |
| MIG-058 | `construction` | `result`, `metrics` | Empty document source는 유효한 empty set/digest이며 source I/O, decode publish와 lifecycle handoff가 0 |
| MIG-059 | `construction` | `result`, `metrics` | Document input 순서와 JSON whitespace/object-key order가 달라도 같은 set/digest이고, operation 순서를 바꾸면 다른 digest가 됨 |
| MIG-060 | `environment` | `error`, `metrics` | Source/consumer의 pre-construction tuple 네 좌표 중 하나라도 `(1,1,1,2)`와 다르면 어느 operation도 decode/publish하거나 lifecycle에 handoff하기 전에 structured incompatibility로 실패 |
| MIG-061 | `construction` | `error`, `metrics` | Canonical batch의 `b-invalid` document가 `/migration/name` duplicate key이면 전체 load가 `invalid_definition_document`로 원자 실패하고 publish/handoff 0 |
| MIG-062 | `construction` | `error`, `metrics` | 둘 이상의 document가 같은 `(app,name)` identity를 선언하면 last-wins 없이 deterministic duplicate identity error, publish/handoff 0 |
| MIG-063 | `construction` | `error`, `metrics` | Codec v1 closed union 밖 unknown/executable/custom operation을 code execution 없이 `unsupported_definition_operation`으로 거부 |
| MIG-064 | `commit` | `result`, `db_state`, `metrics` | Test-only proof가 valid loader-owned snapshot의 definitions와 request만 existing `Executor.Migrate`에 exactly once 전달해 expected successful lifecycle state에 도달; digest/source audit은 call 밖 observation에 유지 |

MIG-057..063은 product DB mutation을 하지 않습니다. MIG-064는 이미 Verified된 lifecycle
boundary로의 **oracle-locked reference handoff shape**만 관찰하며 Go handoff 지원을 뜻하지
않습니다. Loader construction은 decoded definitions에 existing `NewPlanner` graph validation을
그대로 적용해 `invalid_node → duplicate_node → invalid/duplicate edge → missing → cycle` 의미와
canonical diagnostics를 재정의하지 않습니다. Operation state transition, request target,
applied-history/fence와 execution 오류는 publish/handoff 뒤 existing
`Executor.Migrate`의 Planner/Reconstructor/Executor 경계가 계속 소유합니다.

각 ID의 top-level outcome은 하나입니다. MIG-057/058/059/064는 success `result`,
MIG-060/061/062/063은 failure `error`이며 result/error를 동시에 두지 않습니다. MIG-060 canonical
document는 `definition_format=2`, MIG-061은 위 duplicate key, MIG-062는 exact duplicate identity,
MIG-063은 operation `kind=run_python`을 사용합니다. 다른 tuple 좌표, malformed kind, trailing/
unknown/missing field, implicit IR와 lifecycle sentinel failure는 base observation에 섞지 않고
mutation/unit gate로 검증합니다. MIG-059만 하나의 successful result-valued case matrix 안에 base,
syntax/permutation-equivalent와 operation-reordered digest를 함께 기록합니다.

## Accepted document/source contract

이 절은 MIG-057..064, locked artifact와 test-only feasibility proof로 검증해 ADR-0019에서
Accepted한 exact contract입니다. Accepted인 것은 data boundary와 compatibility 의미이며 제품
loader API 또는 source load 지원은 아닙니다.

Caller는 각 항목에 non-empty unique valid-UTF-8 `source_id`와 caller-owned UTF-8 JSON bytes를
제공합니다. Synchronous snapshot이 끝날 때까지 caller가 input을 concurrent하게 mutate하면 안
됩니다. Loader는 entry와 bytes를 load 시작에 deep-copy해 snapshot 반환 뒤 caller mutation과
alias하지 않습니다. `source_id`는 파일 경로나 module name이 아니라 caller가 선택한 opaque diagnostic
label입니다. Exact UTF-8 byte identity/order를 사용하고 Unicode normalization이나 path semantics를
적용하지 않습니다. Invalid UTF-8 label은 `invalid_definition_source`입니다. Loader는 path를
열거나 `source_id`를 해석하지 않습니다. 한 document는 정확히 한 migration을 담습니다.

```json
{
  "compatibility": {
    "definition_format": 1,
    "loader_abi": 1,
    "operation_codec": 1,
    "schema_ir": 2
  },
  "producer": {
    "name": "godj-example-generator",
    "version": "0.1.0"
  },
  "migration": {
    "app": "alpha",
    "name": "0001_initial",
    "dependencies": [],
    "operations": [
      {
        "kind": "create_model",
        "app_label": "alpha",
        "model": {
          "name": "widget",
          "go_name": "Widget",
          "db_table": "alpha_widget",
          "fields": [
            {
              "name": "id",
              "go_name": "ID",
              "column": "id",
              "kind": "auto",
              "primary_key": true,
              "nullable": false,
              "max_length": 0,
              "default": null
            }
          ]
        }
      }
    ]
  }
}
```

Top-level과 nested object는 알려진 field만 허용하고 required field 생략, duplicate object key,
trailing JSON value, invalid UTF-8, escaped lone surrogate와 non-integer number를 거부합니다. Version
좌표와 recognized `create_model.model.fields[]`/`add_field.field` arm의 `max_length` lexical JSON
integer domain은 signed int64입니다. Closed union 밖 operation payload 내부는 known field arm으로
해석하지 않습니다. Signed-int64 밖 integer는 handshake 전에
`invalid_definition_document`/`out_of_range`, domain 안이지만 exact `1/1/1/2`와 다른
version 값은 coordinate-specific `*_incompatible`입니다. `max_length`는 추가로 cross-platform
`0..2147483647` 뒤 field-kind validation을 적용합니다. Decimal/exponent와 범위 밖 integer를
거부하므로 성공한 v1 digest의 모든 number는 RFC 8785/I-JSON safe-integer domain 안에 있습니다.
이는 document byte/depth/count 같은 product resource limit가 아니라 wire type compatibility입니다.
`producer.name/version`은 non-empty
Unicode-scalar string인 필수 audit provenance지만 compatibility equality gate나 semantic digest에는
들어가지 않습니다. Generator version이 달라도 tuple과 의미가 같으면 load할 수 있으며, producer
version만 같다고 tuple mismatch를 우회할 수 없습니다.

Compatibility handshake는 range negotiation이나 best-effort upgrade가 아닌 네 좌표의 exact
equality입니다.

```text
(definition format, loader ABI, operation codec, Schema IR) = (1, 1, 1, 2)
```

- Definition format v1은 document envelope/identity/dependency/order 의미를 소유합니다.
- Loader ABI v1은 atomic batch, ordering, digest와 error/handoff observation 의미를 소유합니다.
- Operation codec v1은 `create_model`과 `add_field` wire union만 소유합니다.
- Schema IR v2는 current `schema/ir.FormatVersion == 2`의 normalized model/field 의미를 소유합니다.

`CreateModel`은 explicit `app_label`과 fully normalized `ir.Model`, `AddField`는 explicit
`app_label`, `model_name`과 fully normalized `ir.Field`를 담습니다. Operation `app_label`은
migration `app`과 같아야 합니다. `AddField.model_name`은 current database identifier rule을
독립적으로 통과해야 하며 empty/invalid migration identity와 dependency는 canonical
`NewPlanner` validation이 판정합니다. Model의 `go_name`/`db_table`, field의
`go_name`/`column`, boolean flags, `max_length`와 absent/typed scalar default까지 명시합니다.
`CreateModel`은 wire model을
`ir.Schema{FormatVersion: 2, AppLabel: app_label, Models: []ir.Model{model}}`로 감싸
`ir.Normalize`한 뒤 normalized schema 전체가 wire-derived wrapper와 deep-equal해야 합니다.
`AddField` candidate는 `char` 또는 `boolean`, `primary_key=false`만 허용하며 `auto`/PK candidate는
`invalid_definition_ir`입니다. Validation-only wrapper는
`ir.Schema{FormatVersion: 2, AppLabel: app_label, Models: []ir.Model{syntheticModel}}`이고,
synthetic model은 Name/DBTable `_godj_loader_validation`, GoName `GodjLoaderValidation`입니다.
Fields index 0은 Name/Column `_godj_loader_pk`, GoName `GodjLoaderPK`, Kind `auto`,
PrimaryKey `true`, Nullable `false`, MaxLength `0`, Default `nil`인 synthetic field입니다. Candidate의
Name/Column/GoName 중 하나와 충돌하는 동안 synthetic Name/Column에는 `_`, GoName에는 `X`를
lockstep append한 뒤 candidate를 Fields index 1에 둡니다. Wrapper 전체를 `ir.Normalize`하고
normalized wrapper가 pre-normalization wrapper와 deep-equal하며 normalized
`Models[0].Fields[1]`이 candidate와 deep-equal할 때만 candidate를 반환합니다. Synthetic model/field는
output과 digest에 들어가지 않습니다. Loader가 이름, column, table, default를 채워 historical
artifact를 현재 규칙으로 재해석하지 않습니다.

Diagnostic pointer는 AddField의 `auto`/unsupported kind와 PK restriction을 각각 candidate
`/kind`, `/primary_key` member에 두고, CreateModel의 no-auto/no-PK와 duplicate field
name/go_name/column 같은 model aggregate invariant는 model `/fields` list에 둡니다. 이 후보는
같은 object의 unknown/missing/type fault가 있어도 독립 수집한 뒤 canonical pointer order로
선택합니다. Field kind와 typed default의 cross-invariant는 candidate `/default`에 두며, trusted
default discriminator/payload가 있으면 extra default member와 별개로 수집합니다.

Field kind는 current IR v2의 `auto`, `char`, `boolean` closed set입니다. Codec v1 `default`는
JSON `null`, 정확히 `{"kind":"string","string":...}` 또는
`{"kind":"boolean","boolean":...}` 중 하나입니다. AutoField default는 항상 `null`, CharField는
string, BooleanField는 boolean만 허용합니다. Current IR type에 존재하지만 현 field kind에서
성공할 수 없는 `ScalarInteger`/`{"kind":"integer",...}` arm은 v1 wire union에서 제외하고
`invalid_definition_ir`로 거부합니다. Kind에 맞지 않는 scalar/extra member도 거부하며 Go
struct의 `omitempty`나 zero value에 wire 의미를 맡기지 않습니다.

Codec v1은 operation discriminator와 executable-bearing key가 closed union입니다. `run_python`,
`run_sql`, raw SQL/callback/import path key, custom operation/field/validator와 arbitrary map
extension은 허용하지 않습니다. Allowed CharField string default는 `"print(...)"`, Go/Python source
처럼 보이는 내용도 실행하지 않고 inert scalar로 lossless 보존합니다. String 내용을 keyword로
scan/reject하지 않습니다. 이후 operation은 codec version을 올리거나 명시적
backward-compatible rule을 새 ADR/contract로 잠근 뒤 추가합니다.

## Atomic load, canonical order와 digest

Loader는 caller bytes를 먼저 snapshot하고 다음 stage를 batch 전체에 적용합니다.

```text
snapshot source_id/bytes + validate unique non-empty SourceID set
→ canonical SourceID order에서 모든 document full JSON framing/duplicate-key/scalar validity scan + strict outer envelope extraction
→ parse 가능한 모든 document의 compatibility tuple exact check
→ identity/dependency/closed operation/fully-normalized IR semantic decode
→ loader construction당 canonical []Migration으로 existing migrations.NewPlanner를 exactly once 호출
→ canonical loader-owned deep-copied definition set digest compute + atomic publish
→ MIG-064 test-only proof에서 definitions/request만 existing Executor.Migrate에 handoff
```

어느 stage든 실패하면 published definition count와 handoff count는 0입니다. 앞 document가
decode됐더라도 partial set을 반환하거나 cache하지 않습니다. Empty source는 valid empty set이며
digest를 갖지만 MIG-064 handoff scenario와 섞지 않습니다.

Deterministic success order는 `(app UTF-8 bytes, name UTF-8 bytes)` ascending입니다. 각 migration의
dependency는 같은 identity order로 sort하고 중복을 거부합니다. Operation과 model field order는
semantic order라 보존합니다. Input document order, JSON object key order, whitespace와
`source_id`는 semantic digest에 영향을 주지 않습니다.

Full-document framing scan은 어느 nesting level의 duplicate key, invalid UTF-8와 escaped lone
surrogate도 tuple handshake 전에 거부합니다. 이 단계에서는 outer
`compatibility`/`producer`/`migration` envelope의 unknown/missing field만 판정하고 operation/IR
nested unknown/missing field는 tuple 뒤 semantic stage가 판정합니다. 따라서 parse할 수 없는
syntax/duplicate/lone-surrogate fault는 version보다 먼저이고, parse 가능한 version+unsupported-kind
조합은 version이 먼저입니다.

Digest는 lowercase `sha256:<64 hex>`입니다. Hash 입력은 아래 **하나의 canonical digest
document**를 RFC 8785 UTF-8로 serialize한 bytes이며 BOM과 trailing newline은 없습니다.

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

RFC 8785 object-key order로 empty set의 exact hash input은
`{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"definitions":[],"domain":"godj:migration-definition-set:v1"}`이고 digest는
`sha256:53f20df43573a361318abbff8c9e6bebad203a7f13f86c1f55c2df2cf4a43450`입니다.
Nonempty one-`CreateModel` golden의 exact 470-byte canonical input은 다음과 같습니다.

```text
{"compatibility":{"definition_format":1,"loader_abi":1,"operation_codec":1,"schema_ir":2},"definitions":[{"app":"alpha","dependencies":[],"name":"0001_initial","operations":[{"app_label":"alpha","kind":"create_model","model":{"db_table":"alpha_widget","fields":[{"column":"id","default":null,"go_name":"ID","kind":"auto","max_length":0,"name":"id","nullable":false,"primary_key":true}],"go_name":"Widget","name":"widget"}}]}],"domain":"godj:migration-definition-set:v1"}
```

그 SHA-256은
`sha256:07e61f8d956002cff0d7fe2db10c16ea4a30829e9f0ced09c69c40ff2c2399bc`입니다.
Canonical `definitions[]` item은 exact `{app,dependencies,name,operations}` 의미이고 dependency는
`{app,name}`, `create_model`은 `{app_label,kind,model}`, `add_field`는
`{app_label,field,kind,model_name}`입니다. Model/field/default는 위 strict explicit wire shape를
사용하고 absent default도 `null`로 남깁니다. Producer, `source_id`, raw document bytes는 digest
document에 없습니다. Dependencies는 canonical order, operations/fields는 선언 order를
사용합니다. 따라서 equivalent syntax/permutation/relabel은 같은 digest이고 operation/field 또는
semantic scalar 변경은 다른 digest입니다. 이 digest는 externally trusted expected digest와 비교할
때만 artifact corruption을 검출하는 semantic fingerprint이며, 스스로 trust를 부여하지 않습니다.
Recorder revision fence, applied-history drift, schema drift나 ABA detection token도 아닙니다.

## Deterministic error contract

Exact message와 host path는 contract가 아닙니다. Existing protocol v2 `ObservedError`의 stable
`category`/`code`만 error에 넣고 failure detail은 typed protocol Value
`metrics.failure={stage,source_id,json_pointer,app,name,operation_index,reason}`로 비교합니다.
모든 field는 항상 존재합니다. 적용되지 않는 string은 `""`, operation index는 `-1`입니다.
Invalid-UTF-8 SourceID만 `source_id="hex:<lowercase raw bytes>"`로 표시합니다. Success contract의
`metrics.failure`은 explicit `null`입니다. Protocol top-level/error field를 늘리지 않습니다.
확정한 precedence는 다음과 같습니다.

1. Request snapshot/framing: empty/duplicate `source_id`, invalid aggregate input; caller input order를
   버리고 canonical `source_id` order를 확정
2. 모든 document의 full framing + strict outer envelope: UTF-8, syntax, any-depth duplicate key,
   escaped lone surrogate, outer unknown/missing field와 trailing value
3. 모든 envelope가 parse된 뒤 compatibility tuple mismatch: tuple coordinate order
   `definition_format → loader_abi → operation_codec → schema_ir`, 그 안에서는 `source_id` byte order
4. Identity/dependency/operation/IR strict semantic decode error와 nested unknown/missing field
5. Canonical definitions로 loader construction당 existing `NewPlanner`를 한 번 호출해 current
   `invalid_node → duplicate_node → invalid_dependency → duplicate_dependency →
   dependency_not_found → dependency_cycle` order를 그대로 사용
6. Canonical digest compute와 atomic publish
7. 성공 publish 뒤에만 MIG-064 existing lifecycle error

Contract-owned structured taxonomy는 다음과 같습니다. GDJ-0020은 Go error type/wrapping을
결정할 수 있지만 category/code 의미를 바꾸지 않습니다.

| Category | Code | 의미 |
|---|---|---|
| `migration_definition_source_error` | `invalid_definition_source` | empty/duplicate SourceID 또는 invalid batch envelope |
| `migration_definition_source_error` | `invalid_definition_document` | JSON framing/shape/key/type/range 위반 |
| `migration_definition_source_error` | `definition_format_incompatible` | definition format 좌표 mismatch |
| `migration_definition_source_error` | `loader_abi_incompatible` | loader ABI 좌표 mismatch |
| `migration_definition_source_error` | `operation_codec_incompatible` | operation codec 좌표 mismatch |
| `migration_definition_source_error` | `schema_ir_incompatible` | Schema IR 좌표 mismatch |
| `migration_definition_source_error` | `unsupported_definition_operation` | v1 closed union 밖 operation kind |
| `migration_definition_source_error` | `invalid_definition_operation` | known operation의 shape/app/argument 위반 |
| `migration_definition_source_error` | `invalid_definition_ir` | explicit IR value가 v2 normalization/field invariant 위반 |
| existing `migration_graph_error` | existing `invalid_node`..`dependency_cycle` | `NewPlanner` taxonomy/order 그대로 |

한 stage에서 여러 오류가 있어도 first canonical error 하나만 반환합니다. Syntax error와 tuple
mismatch가 함께 있으면 trustworthy header가 없으므로 syntax가 먼저입니다. 두 envelope가 모두
parse 가능할 때는 tuple mismatch가 unsupported operation보다 먼저입니다. Error scenario는
`result=null`, `definitions_published=0`, `definition_sets_published=0`, `handoff_calls=0`을 함께
검증합니다. Tuple mismatch는
operation decode count가 0이어야 하며 unsupported operation payload에 함께 있어도 version
error가 우선합니다. Exact Go error type, exported fields와 wrapping은 GDJ-0020에서 결정합니다.

Stage별 candidate key를 섞지 않습니다. Source stage는 `(reason rank, raw source_id bytes)`이며
duplicate는 반복된 exact label bytes를 사용합니다. Document와 semantic stage는
`(source_id raw UTF-8 bytes, RFC 6901 JSON Pointer UTF-8 bytes, reason rank)`, compatibility stage는
`(coordinate rank, source_id raw UTF-8 bytes)`입니다. Graph stage는 canonical `[]Migration`에 existing
`NewPlanner`를 loader construction당 한 번 호출해 그 함수의 ordering을 전부 위임합니다. Existing
PlanningError의 category/code, primary Node와 selection order만 이 set에서 관찰합니다.
Related/Members는 이미 잠긴 Planner 의미이며 `metrics.failure`에 중복하지 않습니다. Root pointer는 `""`이고 `~`/`/`는
RFC 6901의 `~0`/`~1`로 escape합니다. Syntax/invalid UTF-8/trailing value는 root pointer, duplicate
key는 duplicate member pointer, lone surrogate와 shape fault는 offending value/member pointer를
사용합니다. Fixed reason rank는 다음과 같습니다.

```text
source: empty_source_id < invalid_source_id_utf8 < duplicate_source_id
document: invalid_utf8 < syntax < duplicate_key < lone_surrogate < unknown_field
          < missing_field < wrong_type < out_of_range < trailing_value
compatibility: definition_format < loader_abi < operation_codec < schema_ir
semantic: unsupported_operation < invalid_operation < invalid_ir < wrong_type < out_of_range
graph: invalid_node < duplicate_node < invalid_dependency < duplicate_dependency
       < dependency_not_found < dependency_cycle
```

Semantic `wrong_type`/`out_of_range`는 tuple handshake 뒤 fully normalized field를 검증할 때의
`max_length` type과 cross-platform `0..2147483647` 경계에만 사용합니다. Signed-int64 lexical
overflow는 recognized arm에서 semantic stage에 도달하지 않고 앞선 document `out_of_range`로
끝납니다.

## Protocol observation payload

Existing protocol v2의 `Observation` envelope와 result/error exclusivity를 유지합니다. 새 private
Python/fixture schema를 protocol package의 새 public top-level type으로 만들지 않습니다.
`result`의 canonical object는 다음 의미를 담습니다.

```text
result.compatibility = {definition_format, loader_abi, operation_codec, schema_ir}
result.definition_set = {digest, definitions[]}
result.sources[] = {source_id, producer:{name,version}, app, name} # SourceID byte order
result.handoff = {attempted, calls, observed_digest}               # MIG-064만 calls=1
```

`definition_set.definitions[]`만 canonical definition을 소유하며 중복 `result.definitions` field는
없습니다. `operations[]`는 v1 wire value를 lossless canonical object로 관찰합니다. Success observation은
`error=null`; failure는 `result=null`과
`error={category,code,message_is_contract:false}`입니다. `ObservedError` schema는 확장하지
않습니다. MIG-057/058/059의 handoff는 exact `{attempted:false,calls:0,observed_digest:null}`이고
MIG-064만 `{attempted:true,calls:1,observed_digest:definition_set.digest}`입니다. Failure contract
MIG-060..063의 comparison은 정확히 `error`, `metrics`입니다.

위 네 field는 모든 success result의 공통 envelope입니다. Scenario별 추가 result field는 정확히
다음 둘만 허용합니다. MIG-059는 한 success observation 안에서 syntax/permutation/relabel과
operation-order 의미를 비교하기 위해
`canonicality={equivalent_definition_set,equivalent_digest,operation_order_changed_digest,operation_order_is_semantic,source_relabel_preserved_digest}`를
추가합니다. MIG-064는 public reference handoff 결과를 비교하기 위해
`lifecycle={targets,plan,returned_state}`를 추가합니다. MIG-057/058은 공통 envelope 외 result
field가 없으며, 어느 extension도 canonical definitions를 중복 소유하지 않습니다.

`metrics`는 최소한 다음 count/sentinel을 포함합니다.

```text
documents_received, headers_validated, operations_decoded,
definitions_published, definition_sets_published, source_reads_after_snapshot, handoff_calls,
failure
```

Empty/nonempty success 모두 `definition_sets_published=1`, failure는 `0`입니다. 따라서 empty success의
`definitions_published=0`과 atomic failure를 혼동하지 않습니다.

MIG-064의 Python oracle은 explicit-entry Django public loader/executor route로 reference final state를
관찰합니다. 이와 별개인 `conformance/definitionload/*_test.go` Go feasibility proof coordinator가
digest/source audit을 snapshot 옆에 보유하고 actual Go
`Executor.Migrate(ctx, deepCopiedDefinitions, request)`를 exactly once 호출합니다. Digest는 actual
call argument가 아니며, Go Executor 내부 `NewStateReconstructor`/Planner 재검증은 loader-construction
`NewPlanner` 1회 count 밖입니다. Existing lifecycle result/DB state/metrics와 coordinator-owned
observed digest를 함께 기록하고 `source_reads_after_snapshot=0`을 검증합니다. 두 증거 모두 product
loader support가 아닙니다.
MIG-057..063은 `db_state`를 만들지 않으며 source/filesystem I/O도 하지 않습니다.

## False-green과 mutation gate

- **Precomputed definition/digest**: identity, dependency, operation order, normalized field/default를
  각각 바꾸면 canonical output이나 digest/error가 예상대로 달라져야 합니다.
- **Permissive JSON**: unknown/missing field, duplicate key, trailing value, invalid UTF-8와 number
  shape mutation을 각각 거부합니다. `max_length`의 `0..2147483647` 경계 밖과 decimal/exponent,
  excluded integer-default arm, escaped lone surrogate mutation도 Python/Go에서 같은 structured
  error여야 합니다.
- **Version check 우회**: tuple 각 좌표를 낮게/높게 바꾸고 unsupported operation을 함께 넣어도
  operation decode/handoff 0과 version error precedence를 유지합니다.
- **Implicit normalization**: empty `go_name`, `db_table`, `column`, omitted default/boolean semantic
  field가 현재 normalization으로 보충되면 false green입니다.
- **Executable escape**: executable-bearing discriminator/key, import/callback/function/custom kind와
  unknown extension field는 거부하고 runner가 이를 import/exec하지 않습니다. 반대로 allowed
  CharField default의 inert string `"print(...)"`는 lossless round-trip하며 절대 실행되지 않아야
  합니다.
- **Partial publish/last-wins**: valid+invalid batch permutation과 duplicate identity document
  permutation 모두 publish/handoff 0이어야 합니다.
- **Source coupling**: 같은 semantic documents의 `source_id`, input order, whitespace/key order와
  producer version을 바꿔도 semantic digest는 같아야 합니다. SourceID relabel은 diagnostic/error
  handle만 바꾸며 migration identity, dependency/recorder key나 digest를 바꾸지 않습니다.
- **Combined-fault precedence**: syntax+version, version+unsupported operation,
  semantic+duplicate/graph fault를 caller input permutation과 함께 실행해 stage-major + canonical
  SourceID tie-break가 흔들리지 않는지 검증합니다.
- **Order erasure**: dependency order permutation은 canonicalized되지만 operation/field order
  mutation은 digest mismatch를 만들어야 합니다.
- **Handoff shortcut**: MIG-064 oracle은 loader-owned deep-copied definitions를 existing public
  `Executor.Migrate` boundary에 exactly once 넘기는 reference shape와 canonical successful lifecycle
  result를 잠급니다. Source re-read와 second handoff는 허용하지 않지만 GDJ-0019에는 Go handoff
  implementation/product support가 없습니다.
- **False product support**: GoDj product runner는 MIG-057..064를 exit 2/no actual로 유지하고
  `godj-conformance` product target은 9 adapter만 실행합니다.
- **Cross-set 결속 누락**: 10 set의 ID/scenario 전역 uniqueness와 45 unordered set pair의
  양방향 90 ordered cross-binding을 모두 거부합니다.
- **Prior artifact drift**: 기존 9 manifest/oracle/static/deviation bytes와 기존 checksum entry를
  independently pin하고 새 파일만 추가합니다.

## GDJ-0019와 GDJ-0020 경계

GDJ-0019는 reference contract, locked machine artifact, not-implemented product fixture와
`conformance/definitionload/**`의 test-only strict-decoder/digest proof만 소유합니다. 이 subtree의
모든 Go source는 `*_test.go`여야 하고 importable non-test package/product source를 만들 수 없으며,
existing product package가 이를 import해서도 안 됩니다. Fixture는 허용된 Python test/runner에
embedded합니다. 별도 `testdata/**`가 필요하면 만들기 전에 activation `allowed_paths`를 amend해야
합니다. Test-only proof는 product import graph/public API가 아니며 완료 뒤에도 지원 상태는
`oracle_locked`입니다.

별도 GDJ-0020은 Accepted ADR-0019를 전제로 product loader API/package, resource limits,
deep-copy/value ownership, structured Go error와 existing `Executor.Migrate` wiring을 구현하고 새 8개를
`passing` 또는 reviewed deviation으로 전환합니다. Exact하게 구현되면 목표 분류는 10 product
set, 105 contract, `100 passing + 5 deviation`입니다. 그 전에는 product manifest/status,
GoDj runner 또는 제품 source를 변경하지 않습니다.

CLI보다 definition/source compatibility를 먼저 잠그는 이유는 CLI가 directory discovery,
project build와 process exit semantics를 동시에 가져오면 malformed source, version mismatch와
lifecycle failure의 소유자가 섞이기 때문입니다. GDJ-0019/0020이 pure explicit-document load와
existing lifecycle handoff를 고정한 뒤 Q-010의 CLI/project handshake가 이를 orchestration할 수
있습니다. CLI가 source format을 암묵적으로 정의하거나 global tool version으로 document tuple을
대체해서는 안 됩니다.

## 구현 단계

1. Pinned Django loader/migration/writer/serializer와 upstream tests를 MIG-057..064 provenance에
   연결하고 Go redesign/non-goal을 review합니다.
2. Contract title/scenario/phase/status/comparison을 가진 tenth manifest를 추가합니다.
3. Python reference registry에서 strict data document, canonical output/digest/error와 MIG-064
   handoff observation을 독립 fixture로 생성합니다.
4. Exact oracle 두 process byte identity를 확인하고 new checksum entry만 append합니다.
5. Product not-implemented fixture와 unknown-scenario exit 2/no output을 잠그고 GoDj runner는
   추가하지 않습니다.
6. `conformance/definitionload/**`의 `*_test.go`에서 strict duplicate-key/unknown-field/version-first,
   normalized IR, atomic publish/digest와 actual public `Executor.Migrate` handoff를 test-only로
   증명합니다. Importable non-test package나 product wiring은 만들지 않습니다.
7. 10-set 105 contract uniqueness와 90 ordered cross-binding, protocol mutation gate를 추가합니다.
8. Existing 9 locked artifact byte pins, `92 passing + 5 deviation`, full Go/Python/checksum/docs gate를
   회귀 검증합니다.
9. 독립 exact/scope/false-green 감사를 거쳐 ADR-0019를 Accepted/Rejected로 결정하고 GDJ-0020
   activation 조건을 기록합니다.

## 완료 조건

- [x] MIG-057..064 title/scenario/phase/comparison과 pinned provenance review
- [x] Strict data-only JSON v1, tuple `(1,1,1,2)`과 normalized IR v2 oracle lock
- [x] `CreateModel`/non-PK `char`·`boolean` `AddField` lossless codec와 executable/custom/unknown rejection
- [x] Empty source, permutation/canonical digest와 operation-order significance 검증
- [x] Atomic malformed/duplicate failure, deterministic error precedence와 partial publish 0
- [x] MIG-064 source re-read 0, existing `Executor.Migrate` exactly-once handoff
- [x] Oracle two-process byte identity, static 8 ordered status mismatch와 checksum
- [x] GoDj product runner 없음/exit 2/no actual, product target 9 adapter 유지
- [x] 10 set/105 contract uniqueness와 90 ordered cross-binding 거부
- [x] MIG-057..064 manifest provenance 각각 `kind=decision`, `reference=ADR-0019`, `derived=false`
- [x] `COMPATIBILITY.md`가 이 set의 `oracle_locked`를 Django result가 아닌 synthetic GoDj decision
      oracle로 명시
- [x] Payload/digest/error/source/version/IR/operation/handoff mutation gate
- [x] 기존 9 locked artifact/checksum entry byte 불변
- [x] 기존 `92 passing + 5 deviation`과 full Go/Python/checksum/docs 회귀
- [x] 완료 분류가 `92 passing + 5 deviation + 8 oracle_locked`이고 제품 지원 claim 없음
- [x] ADR/work/CURRENT/matrix/evidence가 같은 checkout과 상태를 가리킴

## 진행 기록

- [x] GDJ-0018 제품/hosted CI 완료 기준 `3269d662` 확인
- [x] Pinned Django 6.1 loader/migration/writer/serializer symbol과 upstream tests 재확인
- [x] Contract-only activation, GDJ-0020/CLI/non-goal과 one-owner 경계 작성
- [x] Activation front matter, Markdown, local links/headings, whitespace와 exact 7-path diff scope 검증
- [x] Tenth manifest/reference oracle/not-implemented artifact
- [x] Test-only feasibility와 false-green/cross-binding gate
- [x] Full verification, ADR 결정과 handoff

## 수정 파일

GDJ-0019의 activation baseline `3269d662a8b403b5d73096c04abf9fa630b22974`부터 final code
commit `58c66fdc751867a3c2f1541a8594c6615c9fbb59`까지 delivery commit 세 개가 변경한 exact
24개 경로는 다음과 같습니다.

1. `Makefile`
2. `conformance/cmd/godjcheck/main_test.go`
3. `conformance/contracts/migration-definition-source-manifest.json`
4. `conformance/definitionload/candidate_test.go`
5. `conformance/definitionload/contract_test.go`
6. `conformance/definitionload/lifecycle_test.go`
7. `conformance/definitionload/scope_test.go`
8. `conformance/fixtures/godj-migration-definition-source-not-implemented.json`
9. `conformance/internal/protocol/migration_definition_source_artifacts_test.go`
10. `conformance/internal/protocol/write_migration_artifacts_test.go`
11. `conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS`
12. `conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json`
13. `conformance/runners/django/migration_definition_source_scenarios.py`
14. `conformance/runners/django/runner.py`
15. `conformance/runners/django/tests/test_migration_definition_source_scenarios.py`
16. `conformance/runners/django/tests/test_runner_safety.py`
17. `conformance/runners/django/tests/test_scenarios.py`
18. `docs/OPEN_QUESTIONS.md`
19. `docs/ROADMAP.md`
20. `docs/adr/0019-versioned-migration-definition-source.md`
21. `docs/adr/README.md`
22. `docs/status/CURRENT.md`
23. `work/0019-migration-definition-source-compatibility-contracts.md`
24. `work/README.md`

Delivery commits는 activation `058bc0aba66c78e344f2d8bc87afa2995b2b585a`, machine artifact
`4c7b8390c34ce4f9c4bd9524f22779208cff0df0`, feasibility/final code
`58c66fdc751867a3c2f1541a8594c6615c9fbb59`입니다. 위 delivery-path diff 뒤의 completion
documentation diff는 다음 16개 문서를 동기화합니다.

- `NOTICE.md`, `conformance/README.md`
- `docs/ARCHITECTURE.md`, `docs/COMPATIBILITY.md`, `docs/LICENSING.md`,
  `docs/OPEN_QUESTIONS.md`, `docs/ROADMAP.md`, `docs/SOURCES.md`, `docs/TESTING.md`
- `docs/adr/0019-versioned-migration-definition-source.md`, `docs/adr/README.md`
- `docs/status/CURRENT.md`, `docs/status/IMPLEMENTATION_MATRIX.md`,
  `docs/status/TEST_EVIDENCE.md`
- `work/0019-migration-definition-source-compatibility-contracts.md`, `work/README.md`

## 결정된 사항

- 2026-08-09: GDJ-0019를 `3269d662a8b403b5d73096c04abf9fa630b22974`에서 active로 시작하고
  통합 소유자는 한 명으로 제한했습니다.
- 2026-08-09: 기존 `Executor.Migrate`는 already-loaded lifecycle boundary로 보존하며 새 source
  contract가 lifecycle safety/transaction 의미를 다시 소유하지 않게 했습니다.
- 2026-08-09: Python source compatibility가 아닌 caller-provided strict data-only JSON v1과
  exact tuple `(1,1,1,2)`을 [ADR-0019](../docs/adr/0019-versioned-migration-definition-source.md)의
  GoDj-owned contract로 Accepted했습니다.
- 2026-08-09: SourceID는 validation/diagnostic precedence handle일 뿐 identity나 digest input이
  아니며, definition-set digest는 semantic observation이고 revision/trust fence가 아니라고
  고정했습니다.
- 2026-08-09: Full-document framing, all-source tuple, semantic IR, existing graph validation,
  digest/publish, lifecycle 순서와 SourceID → RFC 6901 pointer → reason canonical selection을
  MIG-057..064 및 59/59 Go/Python error parity로 고정했습니다.
- 2026-08-09: `conformance/definitionload/**`는 실제 `migrations.NewPlanner` construction과 public
  `Executor.Migrate` handoff를 증명하는 test-only feasibility gate입니다. Importable product loader,
  GoDj runner와 제품 지원 상태는 만들거나 바꾸지 않았습니다.

## 미결정/Blocker

외부 blocker는 없습니다. ADR-0019의 source contract는 Accepted됐고 GDJ-0019는 완료됐습니다.
다음 제품 결정은 별도 GDJ-0020 activation 전까지 의도적으로 미결정입니다.

- Product loader public type/function/package 이름과 exact resource limit
- Public Go error taxonomy가 existing migration error에 합쳐질지 별도 source error인지
- Definition writer/generator와 upgrade tool, persistent cache 여부
- Filesystem/project discovery와 global CLI/project binary handshake
- Codec v2 operation/data callback 확장 정책

Hosted CI는 final code commit `58c66fdc751867a3c2f1541a8594c6615c9fbb59`에서 아직 실행하지
않았습니다. 이는 수집하지 않은 원격 증거이며 local completion blocker나 PASS claim이 아닙니다.

## 테스트 증거

- Evidence ID: [EVID-20260809-019](../docs/status/TEST_EVIDENCE.md#evid-20260809-019--gdj-0019-migration-definition-source-compatibility-contracts)
- Checkout: activation `058bc0aba66c78e344f2d8bc87afa2995b2b585a`, machine artifact
  `4c7b8390c34ce4f9c4bd9524f22779208cff0df0`, final code
  `58c66fdc751867a3c2f1541a8594c6615c9fbb59`
- Result: root `make check`, full `CGO_ENABLED=0 go test -count=1 ./...`, definitionload normal/race/
  CGO-disabled/vet/count-20 PASS; portable reference 164 cases with 15 skips, exact reference 164/164,
  Go/Python canonical error parity 59/59 PASS
- Contract shape: 10 reference sets, 105 unique contracts, 90 ordered cross-bindings; existing product
  state remains 9 adapters, 97 contracts, `92 passing + 5 deviation`, plus 8 synthetic
  `oracle_locked` reference contracts
- False-green: static comparator exit 1 with 8 ordered mismatches; `godjcheck` exit 2, empty stdout and no
  actual artifact for unsupported product scenarios
- Artifact pins: manifest 5,195 bytes
  `8a5f914a05eaa6382d1f43589743e4e8ba466b747e6fa80eb1cabef61bb924e6`; oracle 29,851 bytes
  `efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`; static fixture 1,574 bytes
  `41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`; `SHA256SUMS` 959 bytes
  `c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`
- Source pins: scenario source 102,128 bytes
  `53c52e3dbcd8af13e0307e62738383a01d6f307464332942c5c8ad97b71aad77`; scenario test 68,504 bytes
  `b30b5ed338da16388fc354ecc3cdceef7d8ca8948bc41b46e4f840a0e845605a`
- Not run: hosted CI on final code commit; status is pending/uncollected, not PASS

## 위험과 rollback

- Strict format을 너무 일찍 public API로 노출하면 이후 operation/field 확장 비용이 큽니다.
  GDJ-0019는 protocol fixture/test-only spike로만 검증합니다.
- Producer version을 compatibility gate로 오인하면 동일 semantic artifact의 재현성이 깨집니다.
  Tuple과 provenance를 분리하고 mutation gate로 고정합니다.
- Implicit normalization은 오래된 migration의 의미를 현재 generator 규칙으로 바꿀 수 있습니다.
  Fully normalized IR equality를 fail-closed합니다.
- Error order가 input order/map iteration을 따르면 cross-platform oracle이 흔들립니다. Canonical
  source/identity/path order와 two-process byte gate를 둡니다.
- Rollback은 위 세 delivery commit의 contract/reference/test-only artifact를 순서대로 되돌리는
  문서·fixture·test rollback입니다. Product loader/DB schema/data migration은 추가하지 않았고 existing
  9 product adapter와 prior locked artifact pins는 보존됐습니다.

## 다음 정확한 작업

현재 active/ready work는 없습니다. 다음 통합 담당자는 product loader public API, resource limits,
structured error/value ownership과 existing `Executor.Migrate` wiring을 다루는 **별도 GDJ-0020**을
새 work item/allowed paths로 먼저 activation해야 합니다. 그 전에는 product source, GoDj runner,
filesystem/module discovery, writer나 CLI handshake를 구현하지 않습니다.

## 결과와 인수인계

GDJ-0019는 완료됐고 ADR-0019는 Accepted입니다. MIG-057..064의 explicit source/version/codec/IR,
digest/error precedence와 lifecycle handoff가 10-set/105-contract synthetic GoDj reference 및
test-only Go proof로 잠겼습니다. 새 8개는 `oracle_locked`이며 product runner가 없으므로 제품 상태는
9 adapters/97 contracts와 `92 passing + 5 deviation` 그대로입니다.

알려진 제한은 codec v1의 `CreateModel`과 non-PK `char`/`boolean` `AddField`, caller-provided bytes,
test-only loader/coordinator에 국한된다는 점입니다. Product loader/API, resource limits, writer,
filesystem/module discovery, CLI handshake와 hosted CI final-head 증거는 후속 범위입니다. 다음 작업은
GDJ-0020을 별도로 activation하는 것이며, 이 완료 문서 자체가 product support claim은 아닙니다.
