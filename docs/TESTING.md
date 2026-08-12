# 테스트 전략

- 상태: Accepted
- 마지막 검토: 2026-08-12

GoDj에서 테스트는 구현 뒤에 붙이는 검사가 아니라 **Django에서 가져올 의미와 Go에서 새로 지킬 불변 조건을 먼저 고정하는 설계 도구**입니다.

테스트 우선은 Django 테스트 전체를 미리 번역하거나 범용 scenario 언어부터 만드는 뜻이 아닙니다. 소수의 외부 계약을 Django에서 고정하고, 그 계약을 끝까지 통과하는 얇은 수직 단면을 반복합니다.

## 세 계층

### 1. Differential contract

같은 시나리오를 고정된 Django와 GoDj에서 실행하고 정규화된 외부 결과를 비교합니다.

비교 대상:

- 조회 결과와 순서
- DB 최종 상태와 부작용
- 오류 범주와 발생 단계
- transaction/rollback/locking 의미
- model metadata와 migration schema
- form cleaned data와 오류 code
- HTTP response, Admin permission, realtime event, GIS 결과

SQL 문자열은 기본 비교 대상이 아닙니다.

### 2. Translated invariant

Django 테스트가 보장하려는 의미를 GoDj 내부 구조에 맞게 다시 표현합니다.

- QuerySet 지연 평가와 체이닝 불변성
- result cache 의미
- Schema IR normalization/versioning
- typed/dynamic API의 동일 Query AST
- migration project/historical state
- relation graph와 lookup registry
- backend capability와 compiler 의미
- app/Admin registry

Django의 Python class 이름이나 내부 객체 graph를 그대로 복제하지 않습니다.

### 3. Go-native safety

- unit/integration/compile test
- generated code golden/idempotency/atomicity test
- deterministic schema hash
- external consumer package compile test
- race detector와 goroutine leak test
- fuzz/property test
- context cancellation과 timeout
- connection pool과 concurrent transaction
- rollback/error path
- dependency boundary test
- benchmark/allocation/regression test

## Compatibility Lab 구조

M0에서는 다음 책임만 가진 작은 harness를 구현했습니다.

```text
contract manifest
    ├─ explicit Django scenario adapter
    └─ explicit GoDj scenario adapter
             ↓
       normalized observation
             ↓
          comparator
```

초기 scenario는 각 runner에 명시적으로 작성합니다. YAML/JSON으로 Django의 모든 operation을 표현하는 범용 언어는 만들지 않습니다. 반복 패턴이 실제로 확인된 뒤 fixture와 parameter만 공통 protocol로 추출합니다.

현재 directory는 다음 책임으로 구성됩니다.

```text
conformance/
  contracts/manifest.json
  contracts/write-migration-manifest.json
  contracts/save-lifecycle-manifest.json
  contracts/query-cache-manifest.json
  contracts/migration-planning-manifest.json
  contracts/migration-execution-manifest.json
  contracts/migration-restart-manifest.json
  contracts/migration-state-reconstruction-manifest.json
  contracts/migration-lifecycle-manifest.json
  contracts/migration-definition-source-manifest.json
  profiles/
  runners/django/
  runners/godj/
  internal/protocol/
  cmd/contractcheck/
  cmd/godjcheck/
  cmd/observationcmp/
  fixtures/godj-not-implemented.json
  fixtures/godj-write-migration-not-implemented.json
  fixtures/godj-save-lifecycle-not-implemented.json
  fixtures/godj-migration-lifecycle-not-implemented.json
  fixtures/godj-migration-lifecycle-deviation-expected.json
  fixtures/godj-query-cache-not-implemented.json
  fixtures/godj-migration-planning-not-implemented.json
  fixtures/godj-migration-execution-not-implemented.json
  fixtures/godj-migration-restart-not-implemented.json
  fixtures/godj-migration-state-reconstruction-not-implemented.json
  fixtures/godj-migration-definition-source-not-implemented.json
  oracles/django-6.1-sqlite-darwin-arm64/
  codegenbootstrap/
  definitionload/
```

## Reference 환경 잠금

oracle에는 다음을 기록합니다.

- Django exact version/tag/commit
- Python exact version
- DB product/library exact version
- timezone, locale, collation
- dependency lock/hash
- scenario version
- 생성 명령과 source checkout

GDJ-0001은 CPython 3.14.3, Django 6.1 wheel/hash, SQLite 3.50.4와
`sqlite_source_id`, darwin/arm64, UTC/C locale, uv/lock hash를 exact profile로
고정했습니다. 일반 Linux CI는 portable scenario와 artifact validation을 실행하지만
darwin oracle을 재생성했다고 주장하지 않습니다.

## 정규화 규칙

- 각 producer의 JSON bytes는 독립 재생성에서 결정적이어야 합니다. Cross-runtime 비교는
  object member ordering과 optional `null` 생략을 protocol decoder가 정규화한 typed
  payload 의미를 기준으로 합니다.
- datetime은 timezone과 precision을 명시합니다.
- decimal은 float로 바꾸지 않고 정규 문자열 또는 tuple로 표현합니다.
- UUID, bytes, auto PK, NULL을 타입이 드러나는 형태로 표현합니다.
- DB vendor가 포함된 raw exception text 대신 안정적인 error category/code를 비교합니다.
- 순서가 계약이면 절대 sort하지 않습니다.
- 비결정 값은 생성 원인을 통제하거나 명시적인 placeholder 규칙을 사용합니다.
- normalizer 자체에 unit/property test를 둡니다.

## M0 계약 후보와 검증

초기 8~12개 계약은 [COMPATIBILITY.md](COMPATIBILITY.md)의 후보에서 고릅니다. M0는 다음을 증명해야 합니다.

- Django oracle이 같은 환경에서 두 번 byte-identical하게 생성됨
- manifest/schema validation이 잘못된 contract를 거부함
- comparator가 의도적으로 바꾼 결과, 순서, error category를 탐지함
- 미구현 GoDj runner가 false pass를 만들지 않음
- upstream source와 license provenance가 추적됨

M0에서 11개 contract가 `oracle_locked` 상태가 됐고 normalizer/comparator의
mutation test가 false green을 차단합니다. M1은
`Schema DSL → IR → Codegen → Manager/QuerySet → AST → SQLite`의 한 모델 수직
단면으로 실제 differential contract를 통과해야 완료됩니다.

GDJ-0003은 같은 profile에 MOD 7개와 MIG 4개의 별도 set을 추가했습니다. 각 manifest는
8~12개 bound를 독립적으로 지키며, registry 전체를 선택 manifest 하나에 강제로 넣지
않습니다. Checked-in 두 manifest의 scenario 합집합은 registry와 정확히 일치해야 하고,
cross-set oracle, draft status, contract별 phase, result/error/DB state/metrics 변이는 모두
실패해야 합니다.
GDJ-0004는 두 번째 set도 실제 GoDj adapter로 실행해 11개 모두 `passing`으로
전환했습니다. Static GoDj fixture는 계속 11개 명시적 `not_implemented` mismatch를
내며, 미구현 상태를 녹색으로 만들 수 없는 false-green regression으로 보존합니다.
Contract별 phase binding을 추가한 GDJ-0003부터 protocol v2를 사용하며 v1 artifact는
v2 validator가 명시적으로 거부합니다.

GDJ-0005는 Save lifecycle 12개를 세 번째 set에 추가했습니다. `commit`은 persistent
write 성공, `evaluation`은 terminal no-op/validation 또는 explicit transaction 밖에서
끝난 실패, `rollback`은 명시적 transaction/savepoint 복원을 뜻합니다. 따라서 empty
`update_fields`, force-insert conflict와 0-row force-update는 statement count와 kind를
metrics로 보존하되 phase는 `evaluation`이고, 실제 `atomic()` 복원인 MOD-019만
`rollback`입니다. 세 set의 ID/scenario 전역 uniqueness, 모든 cross-pair, Save payload
mutation과 two-process oracle bytes를 gate로 둡니다.
GDJ-0006은 실제 GoDj Save adapter를 연결해 세 번째 set도 `passing`으로 전환했습니다.
Adapter metrics는 임의 contract/statement sequence가 recorder에서 직접 유도되는지
검증하고, SQLite primary-key 오류는 opaque wrapper 안의 structured extended code만
분류하는 회귀로 oracle-shaped 하드코딩과 문자열 비교 false green을 막습니다.

GDJ-0007은 QuerySet evaluation/cache 11개를 네 번째 set으로 먼저 추가했습니다. 모든
scenario는 setup DDL/DML과 terminal capture window를 분리하고, result/DB state와 함께
ordered step별 SELECT count를 비교합니다. 네 set의 ID/scenario 전역 uniqueness와 모든
12개 ordered cross-pair를 검증합니다. Query-count/result/error mutation뿐 아니라 fixture
sentinel과 capture token이 11개 scenario의 live 실행 결과까지 전파되는지를 검사해
checked-in oracle을 그대로 반환하는 하드코딩도 거부합니다. GDJ-0007 완료 당시에는 제품
adapter가 없어 `oracle_locked`였고, static fixture의 정확한 11 mismatch와 `godjcheck`
unknown-scenario fail-closed가 기대 결과였습니다.

GDJ-0008은 네 번째 set을 generated model, generic QuerySet과 SQLite 실제 제품 경로에
연결해 11개 모두 `passing`으로 전환했습니다. 당시 `make godj-conformance`는 M1 11개,
M2 write/migration 11개, Save 12개와 QuerySet cache 11개, 총 45개를 실행했습니다. 임의로
등록되지 않은 scenario는 계속 actual을 쓰지 않고 fail-closed합니다. QuerySet의 direct
copy/chain/fresh ownership, 같은 state `All` singleflight, owner cancellation 뒤 live
waiter 재시도와 waiter-only cancellation 격리, nullable pointer deep clone, cold/warm
`Count`/`Exists`/`At`/`First`, cache-bypass `Iterate`, context와 rows cleanup은 별도
unit/compile/race/SQLite gate로 검증합니다.

두 독립 Go actual은 각각 56,283 bytes, SHA-256
`c7ccad635a13e3e071cba4d46b79d3110e24b2e9501a1ca95054ded520b0fa92`로 서로
byte-identical합니다. Django oracle은 56,426 bytes, SHA-256
`d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`이며 Go actual과
byte-identical하지 않습니다. 합격 근거는 protocol comparator의 계약 의미 0-diff입니다.
Query-cache static fixture의 ordered 11 mismatch는 구현 전 false-green 회귀 증거로 계속
유지합니다. 명령과 checkout별 결과는
[EVID-20260808-007](status/TEST_EVIDENCE.md#evid-20260808-007--gdj-0008-queryset-evaluation-and-cache-product-slice)에
기록합니다.

GDJ-0009은 MIG-005..016을 다섯 번째 reference set으로 추가했습니다. Linear/cross-app
forward/backward, applied pruning, prior/zero target, ordered multi-target/shared dependency와
target/history/graph/cycle error를 result/error, DB state와 I/O metrics로 함께 잠급니다.
Planning capture는 DDL/write/기타 non-SELECT statement가 0이고 recorder/schema state가
같은지 확인합니다. 모든 SELECT까지 0이라고 주장하지 않습니다.

다섯 set의 ID/scenario는 전역으로 유일하며 20개 ordered cross-pair가 모두 거부됩니다.
Two-process/random hash-seed와 graph insertion permutation이 reference bytes를 흔들지 않는지
검사하고, plan order/direction/target/applied state/dedup/retained branch/error facts/
zero-mutation payload를 각각 바꾸는 mutation gate를 둡니다. MIG-012는 caller target order와
dependency precedence만 잠그고 incomparable sibling의 Django private DFS tie-break는
계약하지 않습니다.

GDJ-0009 완료 당시 migration-planning manifest는 12개 `oracle_locked`이고 static fixture는
ordered 12 `not_implemented` mismatch를 냈습니다. GoDj product runner는 unknown scenario
exit 2와 actual 미생성으로 fail-closed했고, 당시 `make godj-conformance`는 제품 adapter가
있는 기존 45개만 실행했습니다. 상세 증거는
[EVID-20260808-008](status/TEST_EVIDENCE.md#evid-20260808-008--gdj-0009-migration-planning-compatibility-contracts)에
기록합니다.

GDJ-0010은 public immutable Planner와 다섯 번째 GoDj adapter를 연결해 MIG-005..016을
`passing`으로 전환했습니다. GDJ-0010 완료 당시 `make godj-conformance`는
11 + 11 + 12 + 11 + 12, 총 57개를 실행했습니다. Adapter의 plan/error는 실제 public API에서 얻고, logical
before/after applied state와 zero-I/O metrics는 backend를 호출하지 않는 공통 structural
capture에서 산출합니다. 실제 DB probe를 실행했다고 주장하지 않습니다.

Fixture target/applied/dependency 변이는 echo된 전체 observation이 아니라 `plan` 하위값을
직접 바꾸어야 하고, missing dependency를 self-cycle로 바꾸면 실제 error code가 바뀌어야
합니다. Adapter source의 `MIG-` literal/oracle/static path와 DB import를 금지하고, 두 독립
Go actual 결정성, static ordered 12 mismatch, unknown scenario fail-closed를 함께 유지합니다.
상세 증거는
[EVID-20260808-009](status/TEST_EVIDENCE.md#evid-20260808-009--gdj-0010-immutable-migration-planner-product-slice)에
기록합니다.

GDJ-0011은 MIG-017..026을 여섯 번째 reference set으로 추가했습니다. Success plan은
migration별 commit과 ordered schema/recorder outcome을, failure plan은 앞선 durable step,
실패 step rollback 또는 partial commit과 이후 `not_started`를 구분합니다. Mixed plan은 첫
domain step 전에 거부하고 empty plan은 recorder/backend mutation 없는 no-op입니다.

External metrics는 connection summary와 compact ordered steps만 비교합니다. Raw render/
operation/recorder/transaction event는 live scenario가 compact observation을 사실에서
유도하는지 확인하는 내부 assertion이며 protocol payload가 아닙니다. Historical
before/after는 MIG-019에만, recorder fault point는 MIG-023/024에
`before_record_write`로만 노출합니다. MIG-024는 schema A1, records A1/A2와 `commit` phase를
고정해 Django backward의 schema-then-record partial commit을 숨기지 않습니다.

여섯 set의 ID/scenario는 전역으로 유일하며 30개 ordered cross-pair가 모두 거부됩니다.
Two-process/random hash-seed exact bytes, step order/direction/status, transaction model,
schema/recorder outcome, historical transition, failure/not-started, fault point와
mixed/empty state mutation을 각각 검증합니다. Static fixture는 ordered 10 mismatch이며
product `godjcheck`는 exit 2/no actual output입니다.

GDJ-0011 완료 당시 `make godj-conformance`는 기존 다섯 adapter 57개만 0-diff로
실행했고 sixth set 10개는 `oracle_locked`였습니다. 당시 증거는
[EVID-20260808-010](status/TEST_EVIDENCE.md#evid-20260808-010--gdj-0011-migration-plan-execution-compatibility-contracts)에
기록합니다. GDJ-0012는 reference oracle/core comparator를 완화하지 않고 6 exact
`passing`과 4 atomic-reverse `deviation`을 별도
`godj-migration-execution-deviation-expected.json`으로 검증합니다. Reference manifest의
Django phase는 유지하고 effective product phase는 code-owned selector policy와
fail-closed harness 안에서만 적용합니다. Missing/extra/unregistered change, 잘못된
status/provenance 또는 fixture 누락은 actual을 쓰기 전에 exit 2로 실패해야 합니다.
GDJ-0012 완료 당시 `make godj-conformance`는 여섯 제품 set의 67개를 실행했고 결과를
`63 passing + 4 deviation`으로 구분해 보고했습니다. 상세 제품 증거는
[EVID-20260808-011](status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)에
기록합니다.

GDJ-0013은 MIG-027..036을 일곱 번째 reference set으로 추가했습니다. Recorder table
absent/empty, record/unrecord fresh read, database isolation, applied-prefix/fully-applied plan,
unknown/known history와 middle-failure restart를 비교합니다. Fresh는 setup object의 cache
재사용이 아니라 durable database에서 새 recorder/loader/executor를 구성하는 뜻입니다.

일곱 set의 ID/scenario는 전역으로 유일하며 42개 ordered cross-pair를 모두 거부합니다.
Two-process random-hashseed exact bytes, recorder presence/identity/setup transition, alias
partition, plan order/direction/empty, unknown partition, history error와 pre-plan timing,
durable prefix/tail, DDL/write/기타 non-SELECT 0과 before/after state를 각각 변형합니다.
Static fixture는 ordered 10 mismatch이고 product `godjcheck`는 exit 2/no actual output입니다.

GDJ-0013 완료 당시 새 set은 10 `oracle_locked`이며 `make godj-conformance`는 계속 제품
adapter가 있는 여섯 set만 실행해 `63 passing + 4 deviation`을 보고했습니다. 당시
reference 총 77개를 제품 통과로 세지 않습니다. 상세 증거는
[EVID-20260808-012](status/TEST_EVIDENCE.md#evid-20260808-012--gdj-0013-recorder-backed-restart-planning-compatibility-contracts)에
기록합니다.

GDJ-0014는 일곱 번째 live adapter를 별도 `AppliedMigrationReader`, core
`LoadAppliedState`/`Planner.CheckHistory`와 SQLite read-only recorder reader에
연결했습니다. File-backed database를 닫고 새 backend로 다시 열어 fresh boundary를
검증하며, absent table read의 no-create, exact missing-table normalization, raw invalid/
duplicate identity, unknown legacy preservation, pre-plan history timing, deterministic tail과
zero mutation을 unit/integration/race/source gate와 함께 확인합니다.

Manifest status는 10 `passing`으로 바뀌어 10,165 bytes, SHA-256
`79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290`입니다. Locked
Django oracle과 static fixture는 각각 33,888 bytes/
`90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`, 1,715 bytes/
`31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`로 유지됩니다. 두
독립 Go actual은 각각 33,795 bytes, SHA-256
`f9e4d3dc7078426f06a08374a36a670a36e1fa2ae08562fd08f80e91db1b31cb`이고 protocol
의미상 10개 0-diff입니다. GDJ-0014 완료 당시 `make godj-conformance`는 일곱 제품 set을 실행해
`73 passing + 4 deviation`을 구분해 보고하며, static ordered 10 mismatch와 42
cross-binding도 계속 보존합니다. 상세 증거는
[EVID-20260808-013](status/TEST_EVIDENCE.md#evid-20260808-013--gdj-0014-recorder-backed-restart-planning-product-slice)에
기록합니다.

GDJ-0015는 MIG-037..046을 여덟 번째 reference set으로 추가했습니다. Explicit empty,
first/middle before·after, cross-app dependency, multiple target/shared dependency,
omitted-target latest leaves, applied-prefix startup과 unrelated-known branch inclusion을
loaded migration definition의 state replay 결과로 비교합니다. Unknown legacy identity는
applied observation에 남기되 schema state로 만들지 않고, deliberately divergent live
database는 capture 전후 불변이어야 합니다.

여덟 set의 ID/scenario는 전역으로 유일하며 56개 ordered cross-pair를 모두 거부합니다.
State의 app/model/field 포함, table/column, field kind/primary-key/null/max-length/default,
request mode/target/position, applied membership, graph dependency와 DB/metrics를 각각
변형하는 semantic gate를 둡니다. 두 random-hashseed exact process와 checked-in oracle은
byte-identical합니다. Static fixture는 MIG-037..046 ordered 10 mismatch이고 제품
`godjcheck`는 exit 2/no actual output으로 fail-closed합니다.

Manifest는 9,257 bytes, SHA-256
`04b7e92a5bbf9ff50f0247be7708dfb18a5534e40bac86a518a6b744fc0ef728`, Django oracle은
89,997 bytes, SHA-256
`bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`, static fixture는
1,715 bytes, SHA-256
`9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1`입니다. 새 10개는
`oracle_locked`이고 GDJ-0015 완료 당시 `make godj-conformance`는 일곱 product set만
실행했으므로 분류는 `73 passing + 4 deviation + 10 oracle_locked`, reference 총계는
87개였습니다. 상세
증거는
[EVID-20260808-014](status/TEST_EVIDENCE.md#evid-20260808-014--gdj-0015-historical-projectstate-reconstruction-compatibility-contracts)에
기록합니다.

완료된 [GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)은
Accepted [ADR-0016](adr/0016-historical-project-state-reconstruction.md)의 immutable
reconstructor와 explicit empty/latest/before/after/applied request API, read-only
recorder-backed live adapter를 구현했습니다. Passing manifest는 9,197 bytes, SHA-256
`85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c`입니다. Locked
oracle/static/SHA256SUMS는 불변이고, 두 actual은 각각 89,867 bytes, SHA-256
`a307d185e5a3c67a679f62bfa4575f6f43ef8ad41e55c78fdf34d5acb5866e44`로
byte-identical하며 oracle과 protocol 의미상 10개 0-diff입니다. `make godj-conformance`는
8 product set을 실행해 `83 passing + 4 deviation`을 보고하며 87 unique contract와 56
cross-binding, static ordered 10 mismatch를 유지합니다. 상세 증거는
[EVID-20260808-015](status/TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
기록합니다.

GDJ-0017은 MIG-047..056을 아홉 번째 reference set으로 추가했습니다. Public Django
orchestration의 fresh/prefix/target/reverse/zero/unknown/preflight/failure/restart 의미를
compact state/schema/recorder/step payload로 비교하고 SQL 문자열, SELECT count, timestamp,
path와 reverse private transaction topology는 제외합니다. 아홉 set 97 ID/scenario와 72
ordered cross-binding, contract-ID independence, definition/target/fault/seed/legacy mutation,
two-process oracle byte identity, static ordered 10 mismatch와 product exit 2/no actual을
검증합니다.

`conformance/lifecyclefence/**`는 제품 package를 바꾸지 않는 test-only gate입니다. Current
unfenced stale acceptance를 먼저 재현한 뒤 persistent epoch+revision CAS와 fingerprint 보조
검증으로 stale-before-write, step 사이 conflict, two-connection/process single winner,
uninitialized bootstrap, all-stage BUSY/LOCKED, DDL/recorder 이후 rollback, exact no-retry와
legacy capability fail-closed를 검증합니다. 이 gate의 성공을 public lifecycle API나 crash-safe
제품 구현으로 분류하지 않습니다. 상세 증거는
[EVID-20260808-016](status/TEST_EVIDENCE.md#evid-20260808-016--gdj-0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike)에
기록합니다.

GDJ-0018은 public `Executor.Migrate`와 revision-fenced SQLite backend를 직접 실행하는 ninth
adapter를 추가했습니다. Lifecycle 9개는 `passing`, MIG-052만
`result.plan[0..2]`/`metrics.steps[0..2]` 여섯 ordered path의 DEV-0002 `deviation`입니다.
기존 DEV-0001 네 계약은 변경하지 않았고, GDJ-0018 완료 당시
`make godj-conformance` 분류는 `92 passing + 5 deviation`이었습니다. 97 unique contract와
모든 72 ordered cross-binding,
live target/definition/seed/legacy/fault propagation과 adapter source hardcode 금지를 함께
검증합니다.

현재 lifecycle manifest는 13,735 bytes/SHA-256
`5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0`, locked oracle은
98,436 bytes/`7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`, static fixture는
1,681 bytes/`b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`, DEV-0002
expectation은 6,769 bytes/`58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b`입니다. 두
independent Go actual은 각각 98,304 bytes/SHA-256
`a32e768323dae33a312267d5f8041818570d55f1fd887b29580cf8d4c5b3064b`로 byte-identical하며
reviewed expectation과 10-contract match입니다.

제품 safety gate는 session snapshot이 exact-one인지, snapshot 뒤 connection을 pin하지 않는지,
각 SQLite step이 `BEGIN IMMEDIATE` 안에서 epoch/revision/fingerprint를 첫 mutation 전에
검사하는지 확인합니다. Unsupported backend fallback과 existing recorder 자동 adoption은
허용하지 않습니다. `CommitRolledBack`은 confirmed state/token을 advance하지 않고 SQLite
session을 poison하며 semantic retry를 하지 않습니다. Default-bearing `AddField`는 empty table의
logical default와 physical no-default를 함께 확인하고 nonempty table은 거부합니다.

GDJ-0019는 MIG-057..064를 열 번째 contract-only reference set으로 추가했습니다. Explicit
caller bytes의 strict JSON v1 framing, exact tuple `(1,1,1,2)`, closed
`CreateModel`/non-PK `char`·`boolean` `AddField` codec, canonical normalized definitions/digest,
loader-owned deep-copy snapshot과 all-or-nothing publish를 검증합니다. Failure gate는 source/document,
compatibility, semantic payload/normalized IR, existing graph, digest/publish, lifecycle 순서와 각
stage의 canonical candidate selection을 고정합니다. MIG-064는 public Django graph/executor의
reference-only success outcome입니다.

GDJ-0019 completion manifest는 5,195 bytes/SHA-256
`8a5f914a05eaa6382d1f43589743e4e8ba466b747e6fa80eb1cabef61bb924e6`, locked oracle은
29,851 bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static
not-implemented fixture는 1,574 bytes/
`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`, `SHA256SUMS`는
959 bytes/`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`입니다. Exact
Python suite는 164 passed, portable suite는 149 passed/15 skipped이고 두 explicit hashseed
process와 checked-in oracle bytes가 일치합니다. 10 reference set의 105 unique contract와 90
ordered cross-binding도 검증합니다.

`conformance/definitionload/**`는 GDJ-0019에서 `*_test.go`만으로 actual
`migrations.NewPlanner`와 public `Executor.Migrate` handoff feasibility를 검증했습니다. 당시에는
importable product loader와 열 번째 GoDj adapter가 없었으므로 새 8개는 `oracle_locked`, 제품
분류는 9 adapter의 `92 passing + 5 deviation`이었습니다.

GDJ-0020은 public `migrations/definition` loader를 별도 leaf package로 구현하고
`conformance/definitionload/product_equivalence_test.go`에서 test-only candidate와 독립적인
black-box parity를 검증합니다. Loader의 exact 10 cap은 다음과 같습니다.

| Resource | Maximum |
|---|---:|
| sources | 2,048 |
| SourceID | 1,024 bytes |
| document | 1 MiB |
| batch | 16 MiB |
| JSON depth | 64 |
| document JSON values | 65,536 |
| batch JSON values | 262,144 |
| dependencies per migration | 2,047 |
| operations per migration | 2,048 |
| `CreateModel` fields | 2,048 |

각 cap의 maximum-1/equal/+1, checked aggregate와 combined-fault precedence를 고정합니다. Strict
scanner gate는 invalid UTF-8/BOM/trailing value, any-depth duplicate member, surrogate pair,
decimal/exponent/leading-zero와 signed-int64 boundary, bounded depth/value counting, canonical
escaping과 RFC 6901 failure order를 검증합니다. 긴 ancestor와 다수 candidate에서도 최종 winner
외 pointer를 문자열화하지 않는 lazy comparator를 adversarial fan-out으로 확인합니다.

Ownership gate는 caller source mutation, nested Default/operation/IR accessor mutation,
repeated/concurrent read와 race detector가 immutable `Set`/report/digest를 바꾸지 않는지 확인합니다.
Source-owned failure는 정확히 9개 code, resource breach는 stable limit/maximum/actual context만
사용합니다. Graph failure의 raw `*migrations.PlanningError`와 `Set.Migrate` lifecycle error는
identity와 `errors.As` 의미를 보존하고 wrap/reclassify/retry하지 않습니다. Private injected
planner count, non-test AST의 direct `migrations.NewPlanner`/`executor.Migrate` callsite와 actual
handoff counter로 exactly-once를 각각 독립 검증합니다.

Actual adapter false-green gate는 valid source identity/header/operation/graph를 각각 mutate해
non-empty protocol diff 또는 success/error shape rejection을 요구합니다. MIG-057..064는 Django
parity가 아닌 decision-reference 8 `passing`이고 제품 성공 출력은 `locked reference oracle`로
구분합니다. GDJ-0020 완료 당시 `make godj-conformance` 분류는 10 adapter/105 contract의
`100 passing + 5 deviation`; 90 ordered cross-binding이었습니다.

Status-only manifest는 5,147 bytes/SHA-256
`688556c4a338e4ad7f580bfcd4d6121ddda0e72c871d1bfba625c352d22c3488`입니다. Locked oracle
29,851 bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static fixture
1,574 bytes/`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`와
`SHA256SUMS` 959 bytes/`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`는
불변입니다. Static comparison은 MIG-057..064 ordered mismatch 8개를 계속 냅니다. Product
commit은 `6172d843a4bb234592cafc176a8d1191933b141c`입니다. File discovery/CLI, writer/upgrade,
custom/executable/data/raw-SQL operation과 non-SQLite backend는 이 green 결과의 지원 범위가
아닙니다.

완료된 GDJ-0021은 MIG-065..074의 열한 번째 independent decision-reference set을 추가했습니다. Exact
`godj.toml` selection/descriptor, project-linked build와 closed runner protocol, flat no-follow source
discovery, `definition.Load` exactly once, zero DB/lifecycle call과 public exit `0/1/2/3/130`을
`conformance/projectcheck/**` test-only proof로 검증했습니다. Manifest의 새 10 contract는 모두
`oracle_locked`이고 product adapter나 production CLI는 추가하지 않습니다.

Artifact gate는 manifest 4,580 bytes/
`0cd8d77b03820af75c8bda8434620f40acd1a3cb6319cf4fb732db4b38d44218`, oracle 19,971 bytes/
`49f50b97bfa1973cef6fe464296a7c973b87e4ad1f9aaefecee24ab64f04d4d2`, static fixture 1,729 bytes/
`86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680`을 pin합니다. 기존 checksum
10-line prefix 뒤 11번째 line만 append한 `SHA256SUMS`는 1,061 bytes/
`74b5b253b2026b98ff4cf5a6abce4c0aa4881488df6c874c9012050495b0b59f`입니다. Protocol gate는
11 set/115 unique contract/110 ordered cross-binding을 요구합니다. Static comparison은 exit 1/
ordered mismatch 10을 유지합니다. GDJ-0021 완료 당시 제품 `godjcheck`는 exit 2/no actual output으로
fail-closed했고 `make godj-conformance`는 10 adapter/105 contract의
`100 passing + 5 deviation`이었습니다.

Completed GDJ-0022는 test-only proof와 독립인 global/linked/protocol product kernel,
public `project.Config`/`project.Run`, actual process E2E와 열한 번째 adapter를 추가했습니다. Product
adapter는 actual report만 사용하고 oracle/static/candidate를 읽지 않으며 mutation gate에서 observation/
diff가 바뀌어야 합니다. MIG-065..074는 10 `passing`이고 GDJ-0022 완료 당시 제품은 11 adapter/115
contract의 `110 passing + 5 deviation`이었습니다. Completed GDJ-0025의 REL-001 metadata와 REL-004
required predicate actual까지 포함한 GDJ-0025 완료 시점 제품은 12 adapter/127 contract의
`112 passing + 5 deviation + 10 oracle_locked`입니다.
Local normal/race/CGO-disabled/vet/count-20, Linux/386 compile-only,
`make ci`, exact oracle와 independent audits는 EVID-027, initial exact 18/18 hosted acceptance는 EVID-028,
completion-documentation failure와 final process stabilization exact 18/18은 EVID-029에 기록했습니다.
Status-only manifest는 4,520 bytes/
`0bbf254e80fea17b52070d0589da5ddcd401ff67440062a89b4fcd3e8309c048`이고 oracle/static/SHA256SUMS는
위 GDJ-0021 pins에서 byte-identical입니다.

## 기능별 기본 테스트 요구

모든 테스트 종류를 모든 작은 변경에 억지로 추가하지는 않습니다. 위험에 맞게 선택하되, 다음 변경은 기본 gate를 가집니다.

| 변경 | 최소 검증 |
|---|---|
| Schema/IR | validation, normalization, round-trip, deterministic hash, fuzz |
| Codegen | golden, idempotency, compile, stale output, multi-file failure atomicity |
| Typed query API | compile-positive/negative, AST invariant, differential result |
| Dynamic lookup | validation/coercion, allowlist, injection/error, typed AST equivalence |
| Query execution | integration, cancellation, resource close, backend contract |
| QuerySet cache/terminal | state ownership, singleflight, cancellation isolation, clone alias, cold/warm I/O, differential |
| Migration | state diff, graph construction, applied pruning, forward/backward, recorder absent/fresh read, raw history validation, zero-mutation planning, structured graph/history/execution error, full-plan preflight, migration별 commit, failure/rollback, cancellation, concurrent lock; definition loader는 strict scanner, exact resource caps, snapshot/deep-copy와 raw graph/lifecycle error ownership을 별도 gate로 검증; historical reconstruction은 별도 contract와 제품 replay/round-trip/determinism gate 뒤에만 지원으로 분류 |
| Concurrency | `go test -race`, cancellation, goroutine/connection leak |
| Backend | capability matrix, conformance, explicit unsupported errors |
| Security boundary | regression test, adversarial input, no silent fallback |

## 테스트 증거

“테스트 통과”라는 문장만 남기지 않습니다. [status/TEST_EVIDENCE.md](status/TEST_EVIDENCE.md)에 다음을 기록합니다.

- 날짜와 checkout/commit
- 환경과 backend
- 실행한 정확한 명령
- pass/fail/skip 수와 exit status
- 실패 또는 실행하지 못한 항목
- 관련 contract/work ID

checkout이 바뀌면 이전 결과는 역사적 증거이며 현재 통과를 뜻하지 않습니다.

## CI gate 순서

구현이 생기면 빠른 gate부터 실행합니다.

1. format/static checks와 manifest validation
2. unit/compile/golden tests
3. SQLite integration과 differential subset
4. race/fuzz의 제한된 CI profile
5. backend matrix와 긴 conformance suite
6. release 전 security/performance/migration matrix

현재 GitHub Actions는 기존 `ubuntu-24.04` x64 full `conformance-validation`과 `macos-15` arm64
exact `exact-darwin-validation`을 보존합니다. 별도 `project-check-matrix`, actual product
`product-project-check-matrix`와 actual-backend `sqlite-matrix`는 각각 exact
`ubuntu-22.04` linux/amd64, `ubuntu-24.04-arm` linux/arm64, `macos-15-intel` darwin/amd64,
`macos-26` darwin/arm64의 네 leg를 가집니다. 각 leg는 Go 1.26.5 coordinate assertion,
normal/race/CGO-disabled/vet, 20분 timeout, `fail-fast: false`, no `continue-on-error`와 final clean
worktree를 요구합니다. Ubuntu `python-compatibility-matrix`는 exact
3.12.13/3.13.15/3.14.3/3.14.7 네 leg에서 Django 6.1/asgiref 3.12.1/sqlparse 0.5.5, portable
193/17 expected skips와 127-scenario payload를 검증합니다. Existing topology 18은 full/exact 2 +
project-check proof 4 + SQLite 4 + product 4 + Python 4입니다. Routine
Ubuntu/compatibility는 uv 0.12.3, embedded profile을 재현하는 exact darwin job만 uv 0.10.12입니다.
GDJ-0023은 이 exact 18을 유지하면서 test-only
`conformance/relationbinding` package를 같은 Linux/macOS x64/arm64 네 좌표에서 normal/race/
CGO-disabled/vet/clean으로 독립 실행하는 `relation-binding-matrix`를 추가해 exact 22 required
executions로 확장했습니다. 이 proof는 relation 제품 adapter가 아니며 PostgreSQL/Windows 지원 claim도
아닙니다. Local routine Python은 CPython 3.14.3 + uv 0.12.3 하나만 실행하고, 3.12.13/3.13.15/
3.14.3/3.14.7 exact compatibility와 갱신된 127-scenario digest는 hosted matrix가 담당합니다.
Completed GDJ-0024는 existing exact 22를 대체하지 않고 같은 four-coordinate의 actual
`relation-product-matrix` 4 legs를 별도로 추가한 exact 26을 implementation acceptance로 운영합니다.
각 새 leg는 mixed v2 target/v3 source companion/bridge compile, app-to-app import edge 0, atomic binder,
REL-001 observed + REL-002..012 ordered payload-free not-implemented, exact required-observed allowlist,
normal/race/CGO-disabled/vet, candidate validation committed-output no-write, artifact no-rewrite와 clean tree를 검증해야 합니다.
Checked-in relationproduct authors/blog main+companion/bridge fixture는 declaration regenerate bytes와 같고 actual observer가
bridge `Bind()` metadata를 adapter에 전달해야 합니다. Django runner/oracle은 그대로 두고 Python의 manifest
status assertion만 `passing` 1 + `oracle_locked` 11로 동기화하며 test count/digest는 불변입니다.
Ubuntu는 metadata-only package의 Linux/386 범위도 검증합니다. Implementation head run `31348285559`에서
exact 26/26 jobs와 326/326 recorded steps가 성공했고 각 relation-product leg는 exact
394 run/394 pass/0 skip, 40,630-byte inventory SHA-256 `2eb1fe8c...20ce`, normal/race/CGO-disabled/vet,
no-rewrite와 clean tree를 통과했습니다. 상세 증거는 EVID-036에 기록합니다. Completion-documentation head
`e9498a67f74bfe05f6ec7d7bcd14f817929bdbef`도 별도 run `31349791188`의 exact 26/26 jobs와
326/326 recorded steps, 각 relation-product leg의 같은 394/394/0·40,630-byte inventory SHA-256을
통과했고 EVID-037에 기록합니다. Final evidence/status head `5bf143575e9b703117a328c1fc5b7eb5823fbfd6`도
run `31351169780`의 exact 26/26·326/326을 통과해 EVID-038에 기록했습니다. GDJ-0025 activation head
`cf8cb589...`도 run `31354040515`의 exact 26/26·326/326을 통과했습니다. Completed GDJ-0025
implementation head `98db55a30ff71a2f2f70722cb569a046208a5403`의
[run 31357283530](https://github.com/progresshans/godj/actions/runs/31357283530)은 same exact 26/26 jobs와
326/326 recorded steps를 성공했습니다. Four relation-product legs는 query/ORM/codegen/SQLite와 actual
relation-query fixture를 포함해 각각 exact 492 run/492 pass/0 skip, 49,902-byte inventory SHA-256
`05064a7f...82eb`, normal/race/CGO-disabled/vet/no-rewrite/clean을 재현했습니다. Full Ubuntu는 actual
Linux/386 relation-query path와 exact relation stdout `2 required contracts; 10 remain not implemented`를,
four Python legs는 uv 0.12.3과 portable 193/17을 통과했습니다. 상세 증거는 EVID-040에 기록합니다.
Completion-documentation head `7b5cebda7410ae8c096a8c30bd60daad1295bbf2`도 별도
[run 31358640776](https://github.com/progresshans/godj/actions/runs/31358640776)의 exact 26/26 jobs와
326/326 recorded steps를 성공했고, four relation-product legs의 같은 492/492/0·49,902-byte inventory
SHA-256, actual Ubuntu Linux/386, exact darwin 193/193과 four Python exact legs를 유지했습니다.
상세 증거는 EVID-041에 기록합니다. Final evidence/status head
`bffc52844de87a2791959ea1e8f99c60dd13d1aa`도 별도
[run 31359958949](https://github.com/progresshans/godj/actions/runs/31359958949)의 exact 26/26 jobs와
326/326 recorded steps, four relation-product 492/492/0 inventory, actual Ubuntu Linux/386와 four exact Python
legs를 성공해 EVID-042에 기록했습니다. 이 clean tested head가 GDJ-0026 baseline이며 activation diff 자체는
별도 activation run `31364944816`의 exact 26/26·326/326을 통과했습니다.
GDJ-0026 gate는 exact-26 topology를 늘리지 않고 relation-product four-coordinate package inventory만
확장했습니다. Sealed descriptor/storage snapshot, pointer self-sentinel, target-PK Limit(2) cache의 0/1/2-row
warm 분류, clone/singleflight/cancellation/failure retry/session lifetime, typed/dynamic source-key Plan.Equal,
SQLite reviewer `IS NULL` result `[11]`/SELECT 1/JOIN 0과 old generated/query SQL byte locks를 필수로 합니다.
Implementation head `5be46141...`의 run `31370313755`은 exact 26/26 jobs·326/326 recorded steps를
성공했습니다. Four relation-product coordinates는 각각 exact 533 run/533 pass/0 skip, 54,076-byte
inventory와 SHA-256 `6d2958b6...7aee`, normal/race/CGO-disabled/vet/no-rewrite/clean gates를 통과했습니다.
Full Ubuntu는 actual `GOARCH=386 CGO_ENABLED=0` exact relation package set을 실행했고 four Python legs는
uv 0.12.3에서 portable 193/17, exact Darwin은 historical uv 0.10.12에서 193/193 skip 0을 통과했습니다.
Completion-documentation head `7f92fcf0...`의 별도 run `31372360481`도 같은 exact
26/26·326/326, four-coordinate 533/533/0 inventory, actual Linux/386 exact package set과 Python gates를
통과했습니다. Final-status head `9ba1d0ee...`도 별도 run `31374150640`의 exact 26/26·326/326과 같은
533/533/0 inventory를 성공해 EVID-046에 기록했습니다.

GDJ-0027은 exact-26 topology를 유지하고 REL-005 reverse runtime/codegen/SQLite/actual packages를 기존
relation-product four-coordinate inventory에 추가했습니다. Local sub-lane은 focused normal test, root는 한 번의
full normal/format/generate/conformance integration을 소유했고 heavy race/CGO0/vet와 Linux/macOS x64/arm64,
bounded Linux/386, exact Darwin/Python은 implementation-head Actions가 소유했습니다. 이는 gate 삭제가 아니라
실행 위치 분리입니다. Typed/dynamic reverse Plan.Equal, query/object capability split, owner wrapper/RelatedSet
cache/cancel/retry, project-only generator exact bytes/import-edge-0, reverse INNER JOIN과 accessor
`[10,11]`/lookup `[1]` actual, manifest REL-005-only transition 및 `115 + 5 + 7` 전환을 모두 검증했습니다.
Implementation head `7db68415...`의 run `31419940399`는 exact 26/26 jobs·326/326 recorded steps를 성공했고
four relation-product coordinates는 각각 569/569/0·57,738 bytes·SHA-256 `739bb6fc...c2d7`을 재현했습니다.
Exact 15-file completion-documentation head `7998a835...`의 별도
[run 31422614250](https://github.com/progresshans/godj/actions/runs/31422614250)도 exact 26/26·326/326,
four-coordinate 569/569/0 inventory, actual Linux/386 exact package set, exact Darwin과 four Python gates를
통과해 EVID-049에 기록했습니다. EVID-049를 포함한 terminal 7-file evidence/status 기록은
documentation-only이며 implementation/completion run을 그 later patch의 proof로 재사용하지 않고, 기록
자체를 증명하기 위한 재귀 evidence를 만들지 않습니다.
Actual adapter가 없는 PostgreSQL/MySQL service-only job은 두지
않습니다. PostgreSQL/MySQL 첫 backend job은 digest-pinned service image, health check, UTC timezone과
C locale 또는 명시적으로 승인된 collation, actual query/write/transaction/schema/migration/
recorder/revision-lifecycle 및 durable restart/persistence contract를 모두 실행해야 합니다. Expected
contract 수와 executed 수가 같고 `skipped=0`, `continue-on-error` 없음, final clean worktree도
필수입니다.

GDJ-0021 implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`은 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)에서 위 exact
`2 + 4 + 4 = 10` hosted execution을 모두 통과했습니다. 이 결과는 reference-only/test-only
project-check와 actual SQLite 범위의 증거이며 PostgreSQL/MySQL 지원 증거가 아닙니다. Ubuntu full
job은 portable Python 174 tests/16 exact-only skips, 실제 Linux/386 loader runtime, 11개 checksum과
no-rewrite를 통과했고, macOS 15 arm64 exact job은 Python 174/174, 11 oracle과 no-rewrite를
통과했습니다. Project-check와 SQLite 각 네 좌표는 모두 normal/race/CGO-disabled/vet/clean
worktree를 통과했으며 PR checkout synthetic merge tree도 exact implementation head tree와
일치했습니다. 상세 job/step 증거는
[EVID-20260810-025](status/TEST_EVIDENCE.md#evid-20260810-025--gdj-0021-github-hosted-10-job-implementation-head-ci)에
기록합니다.

GDJ-0021 exact 16-file completion-documentation head
`34ae58fc2490deb8f884a0b5591520b11bae8669`도 같은 Draft PR #1의
[run 31322122760](https://github.com/progresshans/godj/actions/runs/31322122760)에서 별도로 exact
10-job 재검증을 통과했습니다. Ubuntu 24.04.4 full job
[93266624027](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624027)은
portable Python 174/16 expected skips, focused project-check, 실제 Linux/386 loader, 11 checksum과
no-rewrite를, macOS 15.7.7 arm64 exact job
[93266624013](https://github.com/progresshans/godj/actions/runs/31322122760/job/93266624013)은 exact Python
174/174, 11 oracle와 no-rewrite를 통과했습니다. Project-check/SQLite 각 네 좌표도 모두
normal/race/CGO-disabled/vet/clean을 다시 통과했습니다. 상세 증거는
[EVID-20260810-026](status/TEST_EVIDENCE.md#evid-20260810-026--gdj-0021-github-hosted-completion-documentation-head-10-job-ci)에
기록합니다. EVID-026 append/status commit `f7fbbd50465a610ed9492227909eece524455f15`은 별도
run `31322959993`의 같은 exact 10 job을 통과했고, GDJ-0022 activation commit
`e4de64645bd93cf5e55c746bb6a109c53916cca8`도 run `31324469403`에서 exact 10 job을 통과했습니다.

GDJ-0022 local implementation은
[EVID-20260810-027](status/TEST_EVIDENCE.md#evid-20260810-027--gdj-0022-project-linked-migration-check-product-slice)의
`make ci`, focused normal/race/CGO-disabled/vet/count-20, Linux/386 compile-only, historical exact oracle와
independent audits를 통과했습니다. Initial implementation head
`06858dd6aafeb20449bc4fbfa9aeac78c7a794ce`의 run `31329231255`는 네 Python leg가 테스트 전 brittle
uv exact-string assertion에서 실패해 취소했습니다. Metadata suffix를 허용한 fix head
`3dfeff2a881a3313883729943519896798d92afc`의
[run 31329294154](https://github.com/progresshans/godj/actions/runs/31329294154)는 exact 18/18 성공했고,
job/step/checkout과 four-version 174/16-skip·115-scenario digest는
[EVID-20260810-028](status/TEST_EVIDENCE.md#evid-20260810-028--gdj-0022-github-hosted-exact-18-job-completion-ci)에
기록했습니다. EVID-028/status commit `68b408add3b050d0938ccebc6c83200499f57b2a`의
[run 31330601427](https://github.com/progresshans/godj/actions/runs/31330601427)은 exact 18 중
16 success/2 macOS product normal failure였습니다. `macos-26`은 non-atomic helper readiness에서 empty
payload를 읽었고, `macos-15-intel` actual SIGINT E2E는 cold private build가 fixed 20-second readiness를
넘었습니다. Atomic publication, cold-build-aware bounded harness와 race audit가 찾은 production
reaped-before-Wait-publication reconciliation을 포함한 final fix
`385382efffd1872ae7fb427192bab27b95dc57e2`는 focused repetition/`make ci`/P0-P3=0 audit와
[run 31332208055](https://github.com/progresshans/godj/actions/runs/31332208055)의 exact 18/18을
통과했습니다. 상세 명령/job/log/checkout은
[EVID-20260810-029](status/TEST_EVIDENCE.md#evid-20260810-029--gdj-0022-final-github-hosted-process-stabilization-ci)에
기록합니다. EVID-029/status commit `1f161f311daa775e6a386ec0df568ff85d681f15`의 별도
[run 31333420261](https://github.com/progresshans/godj/actions/runs/31333420261) exact 18/18은
[EVID-20260810-030](status/TEST_EVIDENCE.md#evid-20260810-030--gdj-0022-final-evidence-documentation-exact-head-ci-and-gdj-0023-activation-baseline)에
기록했습니다. GDJ-0023 implementation head `b56ccf52d71a09e2f4db42ce30fb5eaf58ffba99`의
[run 31338151743](https://github.com/progresshans/godj/actions/runs/31338151743)은 exact 22/22와
273/273 successful steps를 통과했습니다. Four Python legs는 193/17과 127-scenario digest를, four
relation-binding legs는 normal/race/CGO-disabled/vet/no-rewrite/clean을 검증했고 상세 결과는
[EVID-20260810-032](status/TEST_EVIDENCE.md#evid-20260810-032--gdj-0023-github-hosted-exact-22-job-implementation-head-ci)에
기록했습니다. Completion-documentation head `31784ae1e8261ad0698921b93803aa35e9b63f93`도 별도
[run 31339409336](https://github.com/progresshans/godj/actions/runs/31339409336)의 exact 22/22와
273/273 successful steps를 통과했고 상세 결과는
[EVID-20260810-033](status/TEST_EVIDENCE.md#evid-20260810-033--gdj-0023-github-hosted-completion-documentation-head-exact-22-job-ci)에
기록했습니다. Final evidence/status head `50578ddc4756452b2a9a0d2afd75711a35b76d8a`도
[run 31340170361](https://github.com/progresshans/godj/actions/runs/31340170361)의 exact 22/22와
273/273 successful steps를 통과해
[EVID-20260810-034](status/TEST_EVIDENCE.md#evid-20260810-034--gdj-0023-final-evidence-documentation-exact-head-ci-and-gdj-0024-activation-baseline)에
기록했습니다. GDJ-0024 activation commit `758cd093...`의 run `31344980929`는 exact 22/22·273/273을,
implementation commit `05e6e218db16e17ce13f7b504a01c603041e4a2a`의
[run 31348285559](https://github.com/progresshans/godj/actions/runs/31348285559)은 exact 26/26·326/326을
성공했고 후자는
[EVID-20260810-036](status/TEST_EVIDENCE.md#evid-20260810-036--gdj-0024-github-hosted-exact-26-job-implementation-head-ci)에
기록했습니다. EVID-036을 포함한 completion-documentation commit
`e9498a67f74bfe05f6ec7d7bcd14f817929bdbef`도 별도
[run 31349791188](https://github.com/progresshans/godj/actions/runs/31349791188)의 exact 26/26·326/326을
성공해
[EVID-20260810-037](status/TEST_EVIDENCE.md#evid-20260810-037--gdj-0024-github-hosted-completion-documentation-head-exact-26-job-ci)에
기록했습니다. 이어 final evidence/status head `5bf143575e9b703117a328c1fc5b7eb5823fbfd6`의
[run 31351169780](https://github.com/progresshans/godj/actions/runs/31351169780)도 exact 26/26·326/326을
성공해 EVID-038에 기록했습니다. GDJ-0025 activation commit `cf8cb589...`은 별도 run `31354040515`의
exact 26/26을 통과했고, implementation commit `98db55a30ff71a2f2f70722cb569a046208a5403`은
[run 31357283530](https://github.com/progresshans/godj/actions/runs/31357283530)의 exact 26/26·326/326을
성공해 EVID-040에 기록했습니다. Completion-documentation commit
`7b5cebda7410ae8c096a8c30bd60daad1295bbf2`도 별도
[run 31358640776](https://github.com/progresshans/godj/actions/runs/31358640776)의 exact 26/26·326/326을
성공해
[EVID-20260810-041](status/TEST_EVIDENCE.md#evid-20260810-041--gdj-0025-github-hosted-completion-documentation-head-exact-26-job-ci)에
기록했습니다. 이 EVID-041/final-status exact 6-file patch의 자체 exact-head CI는 run `31358640776`으로
재귀 증명하지 않습니다. 그 final-status commit `bffc52844de87a2791959ea1e8f99c60dd13d1aa`은 별도
[run 31359958949](https://github.com/progresshans/godj/actions/runs/31359958949)의 exact 26/26·326/326을
성공해 EVID-042로 recursive pending을 닫았고, 이후 GDJ-0026 activation diff에는 재사용하지 않습니다.
GDJ-0026 activation commit `aad4f7ff0d77a1abe16ebddd01782e78c335395f`은 별도 run `31364944816`의
exact 26/26·326/326을 통과했고, implementation commit
`5be46141d943800a3c621975e3e5070f6d01eaf9`도 별도
[run 31370313755](https://github.com/progresshans/godj/actions/runs/31370313755)의 exact 26/26·326/326을
성공해 EVID-044에 기록했습니다. Completion-documentation commit
`7f92fcf036d03a5004953d9857a10291f4603efb`도 별도
[run 31372360481](https://github.com/progresshans/godj/actions/runs/31372360481)의 exact 26/26·326/326을
성공해 EVID-045에 기록했습니다. Final-status commit `9ba1d0ee4cb96c265269000700beb5889fef2206`도 별도
[run 31374150640](https://github.com/progresshans/godj/actions/runs/31374150640)의 exact 26/26·326/326을
성공해 EVID-046에 기록했습니다. 이 run은 GDJ-0027 clean baseline일 뿐 later activation/implementation
proof로 재사용하지 않습니다. GDJ-0027 activation commit `9dbc2fd2...`은 별도 run `31414060387`의 exact
26/26·326/326을 성공했고, implementation commit `7db684159ecfebbcbe1dc0673928e899ab8b0835`도 별도
[run 31419940399](https://github.com/progresshans/godj/actions/runs/31419940399)의 exact 26/26·326/326을
성공해 EVID-048에 기록했습니다. Four relation-product coordinates는 각각 569/569/0 inventory를 재현했고
actual Ubuntu Linux/386, exact Darwin과 four Python compatibility legs도 통과했습니다.
Completion-documentation commit `7998a8351c7668d53b9263bc9a381a815c6c9eb6`도 별도
[run 31422614250](https://github.com/progresshans/godj/actions/runs/31422614250)의 exact 26/26·326/326을
성공해 EVID-049에 기록했습니다. 이 terminal evidence/status append는 completion run의 범위를 later
documentation-only patch로 넓히지 않았고, completion run `31422614250`을 재사용해 EVID-050을 만들지
않았습니다.

그 terminal documentation head `e9dc361f983f1c02af1f63737a1f282998d5a533`은 이후 별도
[run 31424055711](https://github.com/progresshans/godj/actions/runs/31424055711)의 exact 26/26 jobs·326/326
recorded steps를 성공해
[EVID-20260811-050](status/TEST_EVIDENCE.md#evid-20260811-050--gdj-0027-terminal-exact-head-ci-and-gdj-0028-activation-baseline)에
기록했습니다. Four relation-product coordinates는 각각 existing 569/569/0 inventory·57,738 bytes·SHA-256
`739bb6fc...c2d7`, actual Ubuntu Linux/386 exact package set, exact Darwin과 four Python legs를 재현했습니다.
이 run은 GDJ-0028 clean baseline일 뿐 later activation/API 또는 REL-012 implementation evidence로 재사용하지
않습니다. GDJ-0028 activation `3ae4a2ce...`은 별도 run `31429245980`의 exact 26/26·326/326을 통과했지만
implementation proof로 재사용하지 않았습니다. Local EVID-051의 focused lanes, root `make ci`, exact
594/594/0·60,237-byte inventory와 independent audits 뒤 implementation head
`4858ab88b82647793cd463e9f348e43d3f5e4bb7`의
[run 31432551159](https://github.com/progresshans/godj/actions/runs/31432551159)이 exact 26/26 jobs·326/326
recorded steps를 통과했습니다. Four hosted coordinates, race/CGO0/vet, actual Ubuntu Linux/386 bounded set,
exact Darwin/Python, clean-worktree/no-rewrite와 hosted audit P0/P1/P2/P3=0은 EVID-052에 기록합니다.
Result/DB state/primary-vs-batch metrics, IN args/order, atomic warm-cache failure gates, deterministic exact
ten-file union과 REL-012-only manifest transition을 모두 검증했습니다. Exact 15-file completion-documentation
head `9dc4eb1312791ae74b384afbbfdbfef89aaf55bb`의 별도
[run 31435136950](https://github.com/progresshans/godj/actions/runs/31435136950)도 exact 26/26·326/326,
four-coordinate 594/594/0 inventory, actual Linux/386 exact package set, exact Darwin과 four Python gates를
통과해 EVID-053에 기록했습니다. EVID-053을 포함한 terminal 7-file evidence/status 기록은 documentation-only이며
implementation/completion run을 그 later patch의 proof로 재사용하지 않고 자기 자신을 재귀 증명하지 않습니다.

그 terminal evidence/status head `5c0efef12560203d720e4c2dd7bda50c0324a228`은 별도 Draft PR
[run 31436881856](https://github.com/progresshans/godj/actions/runs/31436881856)의 exact 26/26 jobs·326/326
recorded steps를 통과해 EVID-054에 기록했습니다. Four relation-product coordinates는 각각 current
594/594/0 inventory·60,237 bytes·SHA-256 `98a0a37b...8c47e`를 재현했고 exact Darwin, four pinned Python,
bounded Ubuntu Linux/386와 independent hosted audit P0/P1/P2/P3=0도 통과했습니다. 이는 GDJ-0028 terminal
head와 GDJ-0029 clean baseline만 증명합니다. GDJ-0029 activation docs, Proposed projection API,
REL-009/010/011 implementation 또는 target `119 + 5 + 3`의 증거로 재사용하지 않습니다. Activation exact-head,
local implementation과 implementation exact-head hosted gates는 각각 별도 evidence가 필요합니다.

GDJ-0029 activation `0a1da373a443527e48a154ca6ccc7284e5e80dc0`은 별도
[run 31465198903](https://github.com/progresshans/godj/actions/runs/31465198903)의 exact
26/26·326/326을 통과했지만 activation-only evidence입니다. EVID-055의 pre-commit implementation은 root
`make ci`, exact 630/630/0·63,928 bytes·SHA-256 `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`,
REL-009/010/011 oracle-blind actual과 runtime/codegen/integration audits를 통과했습니다. Independent review가
발견한 same-edge source-key/projection provenance P1은 source/target identity, target table과 target PK mutation으로
재현한 뒤 pre-I/O full-hop equality를 강제하는 최소 수정으로 닫았고 remediation audits는 P0/P1/P2/P3=0입니다.

Exact implementation head `c02aab672db5175d7a0886688efb5cc684c67744`의
[run 31470292759](https://github.com/progresshans/godj/actions/runs/31470292759)은 exact 26/26 jobs·326/326
recorded steps를 통과했습니다. Four relation-product coordinates는 각각 630/630/0·63,928 bytes·SHA-256
`4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`를 재현했고 full Ubuntu의
`make ci`/9 required·3 NI/actual bounded Linux-386, exact Darwin 193/193, four Python 193/17과 127-scenario
digest, no-rewrite/clean-worktree 및 hosted audit P0/P1/P2/P3=0을 통과해 EVID-056에 기록했습니다. 이 run으로
bounded REL-009/010/011 product는 `119 + 5 + 3`, relation 9/12가 됐습니다. Canonical facade, multiple/nested/
reverse eager와 broader backend는 검증 범위가 아닙니다. Exact 15-file completion-documentation head
`fb9985e20c92f71eaca7bac81bc61466369e0ebd`의 별도
[run 31482242288](https://github.com/progresshans/godj/actions/runs/31482242288)도 exact 26/26·326/326,
four-coordinate 630/630/0 inventory, actual Linux/386 exact package set, exact Darwin과 four Python gates를
통과해 EVID-057에 기록했습니다. EVID-057을 포함한 terminal exact seven-file 기록 자체의 hosted CI는
그 시점에는 `not run/pending`이었고 implementation/completion run을 그 later patch의 proof로 재사용하지
않았습니다.

GDJ-0029 terminal evidence/status head `d0396c76d016c0f0335b484fbad56c70b80cf6d4`은 별도 Draft PR
[run 31484369693](https://github.com/progresshans/godj/actions/runs/31484369693)의 exact 26/26 jobs·326/326
recorded steps를 통과해 EVID-058에 기록했습니다. Four relation-product coordinates는 각각
630/630/0·63,928 bytes·SHA-256 `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`를
재현했고 exact Darwin, four Python, bounded Ubuntu Linux/386, no-rewrite/clean-worktree와 source diff 0을
확인했습니다. 이는 GDJ-0029 terminal과 GDJ-0030 clean activation baseline만 증명하며 이 activation diff나
REL-007/008 implementation proof로 재사용하지 않습니다.

GDJ-0030 activation head `83e6ea05e5c224a39f1d1d43aa17a3e58cf81c98`의 첫 hosted run은
relation-product `go test -json | tee`가 macOS Intel Actions log backpressure를 받아 모든 ORM test가 PASS한 뒤
Go 1.26.5의 one-minute output `WaitDelay`를 소진하는 false-negative를 드러냈습니다. Relation-product normal gate는
verbose JSON을 `$RUNNER_TEMP` regular file에 직접 기록하고, test process가 끝난 뒤 canonical 630 top-level run
inventory와 count/bytes/SHA summary만 stdout에 게시합니다. Nonzero exit에서는 failed test/package output과 fail
events를 합쳐 최대 64 KiB만 사후 게시하고 formatter 실패와 무관하게 원래 status를 반환합니다. Protocol test는 live `tee` 부재, direct-file capture, failure
propagation과 compact evidence publication을 고정합니다. 이 instrumentation 변경은 contract count/status나 product
API를 바꾸지 않습니다.

Corrected stabilization head `48472a1cba1ec706939f362ebdb1c4bea7f825eb`은 별도 Draft PR
[run 31503631942](https://github.com/progresshans/godj/actions/runs/31503631942)에서 26/26 jobs·326/326 steps를
통과했습니다. Four relation-product coordinates는 각 631-line compact output에서 630/630/0·63,928 bytes·
SHA-256 `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`를 게시했습니다. Compact run records로
630 unique sorted runs와 payload bytes/SHA를 독립 재구성했고, passes=630/skips=0은 raw JSON parser가 검증해
summary로 게시했습니다.
`WaitDelay`/`Test I/O incomplete`는 0건이었습니다. EVID-060은 이 activation gate만 증명하며 이후 local
REL-007/008 implementation bytes의 증거로 재사용하지 않습니다.

GDJ-0030 implementation head `c3803acba1929921f23e4751679dc21d4bba9c0f`의 EVID-061/
[run 31510689383](https://github.com/progresshans/godj/actions/runs/31510689383)은 exact 26/26 jobs와
326/326 recorded steps를 통과했습니다. Full Ubuntu `make ci`, `godjcheck` exact 11 required/1 not implemented,
actual Linux/386 bounded package gate, exact Darwin, four Python compatibility legs와 four relation-product
coordinates 각각 687/687/0·69,597 bytes·SHA-256
`363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`가 성공했습니다. Current manifest는
10,776 bytes/SHA-256 `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`, exact thirteen-file generated
union은 SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`이며 product는 exact
`121 passing + 5 deviation + 1 oracle_locked`, relation 11/12입니다. Local normal/race/CGO0/vet/386 compile,
Django relation 11/11, inventory twice와 independent audits P0/P1/P2/P3=0도 통과했습니다. 이 exact 15-file
completion-documentation head `635e9c38a4464b98987d56c1b7d796aa42734661`은 별도 EVID-062/
[run 31514159835](https://github.com/progresshans/godj/actions/runs/31514159835)의 exact 26/26 jobs·326/326
recorded steps, unchanged four-coordinate 687/687/0 inventory, full Ubuntu `make ci`, exact Darwin/four Python,
actual Linux/386와 independent hosted audit P0/P1/P2/P3=0을 통과했습니다. EVID-062와 상태를 추가한 exact
seven-file terminal head `ceff9e5...`도 별도 EVID-063/run `31516174741`의 exact 26/26·326/326과
independent audit P0/P1/P2/P3=0을 통과했습니다. EVID-063은 later GDJ-0031 activation/implementation
tree의 proof로 재사용하지 않습니다.

Completed GDJ-0030 testing은 이전 9 required/3 NI baseline에서 REL-007/008 두 contract만 actual로
전환했습니다. PROTECT는 public constructor rows<=0 rejection, external ORM construction, errors.Is/errors.As,
`ProtectedSourceRows`, mutation 0과 unchanged DB/caller를 검증합니다. 같은 row/two-edge는 global count 1,
different source model/same numeric PK는 count 2이고 Linux/386 compile도 required입니다. SET_NULL은 fixture
`NO ACTION`/`RESTRICT`, framework UPDATE→DELETE, affected 2→1, transaction 1을 검증하되 Delete 성공 `(1,nil)`은
target rows만 뜻하고 adapter만 두 oracle delete count에 매핑합니다. 각 PROTECT edge의 nil/typed-nil rows+nil은
method-call 0과 `backend_error/invalid_plan`, nil/typed-nil rows+error는 primary 보존/method-call 0, genuinely non-nil
rows+error는 close-once와 primary+close error 보존을 검증합니다. `Next`/`Scan`/`Rows.Err`/`Close`/context failure는
mutation 전 abort와 acquired genuine rows exactly-once close를 증명해야 하며 exact selected/scanned source PK+FK,
non-NULL FK target equality와 all-row drain을 고정합니다.
Terminal rows error를 protected-zero로 오판하는 false green을 금지합니다. SET_NULL count 0은 valid, negative는
backend contract violation/rollback이고 fixture는 exact 2입니다.

SQLite relation session은 relation connection과 모든 owned/competing writer의 FK-on을 각각 확인합니다. Real
file/two-connection gate는 no-wait BUSY/no retry와 wait-through-COMMIT FK rejection/no orphan을 분리하고, 모든
declared incoming edge의 metadata-matching physical `NO ACTION`/`RESTRICT` FK를 fixture `PRAGMA foreign_key_list`로
증명합니다. Missing/mismatched constraints와 FK-off writer는 unsupported입니다. Raw BEGIN error/BUSY는 callback/
retry 0, forced discard와 primary+discard error를 검증합니다. Confirmed discard는 clean reborrow를, unconfirmed
discard는 poisoned physical handle의 비재사용/비상속과 다른 connection이 Backend close 전 BUSY일 수 있음을
검증합니다. Pre-COMMIT
fault/cancellation/count failure는 callback context와 독립된 bounded cleanup context의 raw ROLLBACK 또는 forced
physical connection discard로 pool reuse를 막고, primary+cleanup error 보존, confirmed cleanup일 때 unchanged DB,
confirmed cleanup의 clean reborrow를 검증합니다. Relation cleanup은 explicit termination-confirmed bool을 쓰고
`driver.ErrBadConn`/`sql.ErrConnDone`만 discard/done confirmation으로 인정합니다. Raw nil fake는 unconfirmed,
Close 0/retained/no-reuse이고 mutation-possible이면 transaction marker, raw-BEGIN/callback-0이면 unknown marker 0임을
증명합니다. Relation session은 매 `Insert`/`Update`/`Delete`/`RelationSetNull` 호출
직전에 mutation-possible을 표시하며, 이 bounded deleter의 첫 mutation entry는 SET_NULL 또는 target DELETE입니다.
그 뒤 rollback/discard confirmation이 모두 실패한 경우만 outermost
`backend_error/transaction_outcome_unknown`, unchanged pointer/DB outcome unknown과 reconciliation-required이고
joined Cause에서 primary+rollback+discard cause가 모두 reachable이어야 합니다. Primary 자체가 `*query.Error`인
fixture에서도 `errors.As`가 outer marker를 먼저 반환해야 합니다. Raw BEGIN/callback-0, pre-mutation read/PROTECT/
resource failure, confirmed rollback/discard와 literal COMMIT error는 이 code를 쓰지 않습니다. Literal COMMIT-call error만 outermost
`backend_error/commit_outcome_unknown`이 errors.Is/errors.As로 도달하고, joined COMMIT/cleanup causes를 보존하며 cleanup 결과와 무관하게 durability unknown
`(0,error)`, unchanged pointer, 한 `Delete` invocation의 backend attempt exact 1과 internal automatic retry 0을
검증합니다. 두 marker 모두 caller는 reconciliation 전 명시적으로 재호출해서는 안 되지만 이 packet은 poison token/fence/registry를
제공하지 않으므로 두 번째 caller invocation이 runtime에서 거부된다고 assert하지 않습니다. Canceled-context
rollback, forced-discard, successful COMMIT 뒤 context/close error가 `(1,nil)`/key clear를 downgrade하지
않는 gate가 필수입니다. Panic path는 detached cleanup 뒤 confirmed discard 또는 poisoned-handle retention으로
transaction inheritance를 막고 exact original panic value identity를 보존하며
cleanup error/marker 반환을 약속하지 않습니다. Recover한 caller는 cleanup 결과를 구분할 수 없으므로 retry 전
external reconciliation이 필요하다는 계약만 검증합니다. Caller key preflight→clone→clone key exact equality도 I/O 전에 검증하며,
second clone clear probe는 `query.Integer(0)`/present=false와 canonical non-PK `WriteFieldValue` before/after equality를
검증합니다. Clone key drift, Clear no-op/key residue/non-PK mutation descriptor는 `query_error/invalid_plan`,
`(0,error)`/I/O 0입니다. Descriptor method determinism/purity는 extension precondition입니다. FK-off/out-of-band
writer는 unsupported입니다.

SQLite Backend retention gate는 `Open`이 private per-Backend state를 초기화하고 unconfirmed poisoned handle을
operation 중 강하게 보유하는지 검증합니다. `Backend.Close`는 `sql.DB.Close`를 먼저 호출한 뒤, 그 호출이 error를
반환해도 retained set을 seal/take/drain하고 DB-close와 `database/sql`이 실제 반환한 handle-close error를
보존해야 합니다. Ordering fake는 pool seal 전
retained `Conn.Close` 호출 0, seal 뒤 terminal `Conn.Close` attempt exact 1/no pool return을 증명합니다. Custom
driver는 close invocation/order를 기록하되 `database/sql`이 숨기는 underlying driver-close error까지 public
결과로 관찰된다고 주장하지 않습니다. Pre-seal retain, post-seal
retain, retain-vs-close race와 idempotent second close를 각각 실행하며 retained set residue 0을 확인합니다. 다른
connection은 explicit Backend close 전 retained lock 때문에 BUSY/block될 수 있으므로 lock-free reborrow를
acceptance로 사용하지 않습니다. External compile gate는 private state가 pointer field여서 기존 `Backend` value의
comparability와 public method set이 유지됨을 고정합니다. Second-close idempotence는 순차 gate이며 concurrent losing
Close가 winner 완료를 기다린다는 새 계약은 만들지 않습니다.

`AtomicRelation` callback은 precondition/begin failure 0회, 그 밖에는 synchronous exact 1회입니다. ORM은
`AtomicRelation` 반환 직후 active/single guard를 원자적으로 seal하고 completed callback snapshot으로 outer result와
key clear를 결정합니다. Fake backend의 nil-without-callback과 seal 전에 등록된 double/concurrent callback은
`backend_error/invalid_plan`, `(0,error)`, unchanged caller와 rejected-entry mutation 0을 증명합니다. 별도 late gate는
seal 이후에 동기화한 invocation 자체가 `backend_error/invalid_plan`으로 거부되고 mutation 0임만 증명하며, 이미
결정된 `Delete` 결과나 caller key가 소급 변경된다고 assert하지 않습니다. Backend-return과 seal 사이를 경합해
first callback이 seal 전에 완료되는 악성 port violation은 synchronous callback과 구분할 수 없으므로 detection이나
outer result를 assert하지 않고 race-safety만 검증합니다. Callback error를 swallow/commit하는 backend는 explicit
port violation이며 DB outcome을 보장하지 않습니다.

Direct binder와 generator는 target에 supported incoming many-to-one edge가 최소 하나 있어야 합니다. Generator는
canonical `Bind()`와 같은 authoritative declared universe input, reordered identical bytes/digest,
v3-target pre-byte rejection, static WriteDescriptor assertion, exact `zz_godj_relation_delete.go`의 zero/nonzero
`RelationDeleters`/`BindRelationDeleters`, namespace/external compile, separate exact thirteen-file
`relationdeleteproduct` union과 prior twelve-file byte lock/last-good를 검증합니다. Generated field는
`<ExportedPackageAlias><ModelGoName>`이고 alias≠app-label adversarial determinism/namespace/compile이 필수입니다.
Aggregate binder는 full `Bind()`의 canonical unique incoming-target identity set과 generated emitted-target set을
I/O 전에 exact 비교하고, added/removed target field 또는 per-target fingerprint drift를 cold `invalid_plan`으로
거부해야 합니다. Binding/delete companions가 함께 undeclared source보다 stale한 경우를 runtime에서 탐지한다고
assert하지 않고 authoritative generation/check precondition으로 둡니다.
Manifest target 10,776 bytes/SHA-256
`3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`는 REL-007/008 status-only exact
transition/revert가 입증된 뒤에만 current가 됩니다. Activation, implementation, completion and terminal heads는
각각 별도 local/hosted evidence를 사용합니다.

### GDJ-0031 relation facade compile-usability 결과

GDJ-0031 activation baseline `ceff9e534e541edb0bd19cd6a1a61682b5435454`은 EVID-063/
[run 31516174741](https://github.com/progresshans/godj/actions/runs/31516174741)의 exact 26/26 jobs·326/326
recorded steps를 통과했습니다. Activation documentation head `624347e...`는 EVID-064/
[run 31520396606](https://github.com/progresshans/godj/actions/runs/31520396606), compile implementation head
`0653902...`는 EVID-065/[run 31528039746](https://github.com/progresshans/godj/actions/runs/31528039746)의 서로
다른 exact 26/26 jobs·326/326 recorded steps를 통과했습니다. Product는 unchanged exact `121 + 5 + 1`, relation
11/12이며 REL-002만 locked입니다. 앞선 run을 later head의 proof로 재사용하지 않습니다.

Compile fixture의 authoritative physical baseline은 `conformance/relationdeleteproduct/**` exact 16 files입니다.
Generated 13 + `fixture/schema.go` + `observer.go` + `product_test.go`의 fixture-relative sorted
`path + NUL + decimal size + NUL + content`는 62,538 bytes/SHA-256
`992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`입니다. Generated subset 13은
26,140 bytes/SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`입니다.
One-file overlay를 더한 logical exact 17은 65,970 bytes/SHA-256
`29d37c4cc1446ce320bcd5476afafb77989cd980a1dd3f96cb0732803835737f`로 고정됐습니다.

`internal/compiletest`는 이 physical bytes를 수정하지 않고 nonexistent project `.go` target 하나만 Go overlay로
주입해 logical exact 17 compile view를 만들었습니다. Overlay가 있을 때 외부 consumer는 exact
`post, found, err := models.BlogPosts.OrderBy(blog.PostFields.ID.Asc()).First(ctx)`, explicit `Model()` unwrap,
lazy/eager 동일 source pointer와 `Author(ctx)` 후보를 컴파일했습니다. 같은 consumer는 overlay 없이는 exact 두
`undefined: project.Using` 진단으로 실패합니다. Root relation-delete product도 overlay view로 compile-only 검증했고
physical virtual target은 실행 전후 모두 absent입니다. `db.RelationSession`은 callback 안에서 query-only
`db.Queryer` candidate에 전달되는 structural assignability만 확인하며 runtime pinning/lifetime을 주장하지 않습니다.

새 top-level `Test*`와 `t.Run`을 추가하지 않고 existing external-consumer/typed-misuse tests에 결합해 exact 4/1을
유지했습니다. AST/source
whitelist는 allowed public project/authors/blog/core imports와 forward read-only symbols만 허용하고 other relation
fixtures, oracle/static/not-implemented/runner/protocol source read, reflection/unsafe/process/file/network I/O와
reverse/REL-002/write/delete/cache/JSON/custom-method symbols를 거부합니다. Exact 16에 reverse aggregate가 없으므로
`author.Posts`나 다른 fixture를 합친 chaining은 false green입니다. Wrong-model predicate/ordering/selector,
declaration/signature/receiver drift, generated delete/reviewer/version symbol, import/binder shadow, goroutine/defer/
nested callback escape, extra physical entry와 symlink/path escape mutation은 모두 negative gate에서 거부됩니다.

Local에서는 focused/full normal, race, CGO-disabled와 `go vet ./internal/compiletest`, unchanged product inventory
687/687/0·69,597 bytes·SHA-256 `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`,
diff/scope와 independent P0/P1/P2/P3=`0/0/0/0`을 확인했습니다. Hosted full Ubuntu `make ci`, exact Darwin,
four Python과 four relation-product coordinates도 통과했습니다. Compile success는 candidate feasibility일 뿐
production generator, public API acceptance, runtime/cache/query-count parity가 아닙니다. 모든 candidate 이름은
noncanonical이고 Q-017은 P1/open입니다. Exact 11-path completion-documentation head
`e9b2c0e4812e7619d0b5ffd3862731714b00273d`은 별도 EVID-066/
[run 31531470440](https://github.com/progresshans/godj/actions/runs/31531470440)의 exact 26/26 jobs·326/326
recorded steps를 통과했습니다. EVID-066과 상태를 추가한 exact seven-file terminal documentation head
`3d661251...`도 별도 EVID-067/
[run 31533890720](https://github.com/progresshans/godj/actions/runs/31533890720)의 exact 26/26 jobs·326/326
recorded steps와 independent audit P0/P1/P2/P3=0을 통과했습니다. Completion run을 terminal proof로 재사용하지
않았고, EVID-067은 later GDJ-0032 activation tree의 proof로 재사용하지 않습니다.

### GDJ-0032 production forward facade implementation gate

GDJ-0032는 기존 test-only overlay를 제품 증거로 승격하지 않고 production companion을 별도로 생성·게시했습니다.
Terminal baseline EVID-067, activation documentation EVID-068/run `31537726792`, implementation head
`ba2fa0fa30f32abf3d70598c7a3a4e4334a43020`의 EVID-069는 서로 다른 exact head를 증명합니다. Product
classification은 unchanged exact `121 passing + 5 deviation + 1 oracle_locked`, relation 11/12, REL-002 locked입니다.
Exact eleven-file completion-documentation head `6089e214ee7a0b564f6636e65e6d6f96c167e2c6`은 별도
EVID-070/[run 31544273477](https://github.com/progresshans/godj/actions/runs/31544273477)의 exact
26/26 jobs·326/326 recorded steps를 통과했습니다. EVID-070을 추가한 exact seven-file terminal head
`8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed`도 별도 EVID-071/
[run 31563615648](https://github.com/progresshans/godj/actions/runs/31563615648)의 exact 26/26·326/326을
통과했고 completion run을 재사용하지 않았습니다. EVID-071은 later GDJ-0033 activation proof로 재사용하지 않습니다.

Implementation test는 다음 경계를 동시에 잠갔습니다.

- Existing generated exact 13, 26,140 bytes/SHA-256
  `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`의 before/after byte identity
- 새 project companion 한 파일의 deterministic generation, golden, missing-target first publish와 replacement
  last-good preservation; generated exact 14는 39,243 bytes/SHA-256
  `2a141f1962887a9c610dd2d0005f401ecd8759e4d0bf0ce5cde1c3f210d1ba5f`, physical exact 17은 94,439
  bytes/SHA-256 `3fc7aba625cf231bc3521f3ad19270a05405e9bfea3d4799b36ca3dd907752fd`
- Every declared model의 project-owned pointer wrapper/query root와 low-level `Object` namespace disjointness
- Target query root와 required Author/nullable Reviewer accessor의 같은 target wrapper static type; raw app model이나
  low-level Object 반환은 negative
- Required `(*TargetWrapper, error)`와 nullable `(*TargetWrapper, bool, error)` shape, NULL=`nil,false,nil`
- Queryer+Mutator-only minimal fake/`db.Session` positive가 RelationAtomic/RelationMutator 없이 성립하고,
  statically typed `db.Queryer`는 negative
- Binder failure가 nil/typed-nil backend보다 original cause 그대로 우선; valid binding의 nil-like backend는
  `backend_error/invalid_plan`; 모두 I/O 0
- Author/Reviewer common selector와 selected concrete eager query/evaluation state 보존; Gate 0 selector
  representation/name은 이 bounded facade에서 canonical
- Author/Reviewer `SelectRelated`를 Filter/OrderBy/Limit 전후에 적용한 required present, nullable present/NULL
- Lazy 첫/둘째 접근 query count와 eager 추가-query 0; copied/repeated eager는 한 evaluation state를 공유하고 derived
  chain은 독립 evaluation
- Source relation cache는 source wrapper-scoped이고 separately materialized source/target wrapper와 target pointer/
  downstream cache identity는 비계약
- Nil/typed-nil/zero/cross-model selector, zero Models/root/eager와 nil/zero/dereference-copy wrapper는
  `query_error/invalid_plan`, I/O 0; category/code는 stable, detail message는 noncontractual
- Explicit raw-model unwrap/clone; AST/source gate가 Save/Delete/reverse와 low-level Object re-exposure를 거부
- Session-origin callback 내부 사용만 positive; callback 이후 항상 성공/실패한다는 assertion은 금지
- Reverse manager, stable target wrapper pointer identity와 downstream target cache symbol/claim 부재
- Test-only overlay 제거 뒤 production no-overlay external consumer, typed misuse와 exact product package compile
- Generator fixture는 current two-model 외 unrelated model, multi-app/multi-model permutation,
  target-also-source와 self-edge construction을 포함하되 multi-hop runtime support를 주장하지 않음

`WriteFile(Check:true)`는 byte comparison만 증명하며 Verify 실행을 대신하지 않습니다. Actual write는 same-directory
temp/sync, whole candidate package Verify, single rename 순서를 검증합니다. 여러 generated file의 coordinated upgrade,
directory fsync, rename/deprecation/repair나 CLI를 이 gate에서 주장하지 않습니다.

Binder/backend precedence와 invalid category/code는 위와 같이 고정했습니다. Gate 0의 `Backend`, `Using`, `Models`,
singular roots/wrappers, `BlogPostRelationSelector(s)`, `BlogPostEagerQuery`, `Unwrap`은 이 bounded facade의 canonical
surface입니다. Implicit English pluralization을 사용하지 않고 existing/new project declaration namespace 전체의
deterministic collision과 exact candidate-union compile을 검증했습니다. 이는 reverse/write/general generated
upgrade 이름이나 동작을 확정하지 않습니다. EVID-069는 implementation head만 증명하며 later completion-documentation
tree proof로 재사용하지 않습니다.

### GDJ-0033 REL-002 assignment/save/cache implementation gate

GDJ-0033 activation head `a4a627a...`는 EVID-072/run `31566524953`, decision-documentation head `9d728610...`은
EVID-074/run `31574653183`의 서로 다른 exact 26/26 jobs·326/326 steps를 통과했습니다. Exact 23-path bounded
product는 EVID-075의 local final gates에서 exact `122 passing + 5 deviation + 0 oracle_locked`, relation 12/12,
REL-002 passing으로 전환됐습니다. Exact implementation head `be6f3d4e...`는 별도 EVID-076/run `31586910749`의
26/26 jobs·326/326 steps, four-coordinate 715/715/0 inventory와 audit P0..P3=0을 통과했습니다. ADR-0033은
Accepted, code는 Implemented, 이 명시된 hosted 환경에서는 Verified입니다. 앞선 activation/decision run을
implementation proof로 재사용하지 않았습니다.

#### Phase A — Django observable semantics

- Exact Django object `fe0a859f...`를 `git show <commit>:<path>`로 읽고 moving checkout을 evidence로 쓰지 않습니다.
- Assignment/cache, same/different scalar, PK-zero presence, no-PK/manual-PK, pending-only reconcile, key-present-later-
  change invalidation, nullable clear와 rollback memory non-rewind를 separate rows로 고정했습니다.
- Locked REL-002 payload/phase/category/code/metrics/DB state와 reference artifacts는 byte-frozen입니다.
- Typed generated select-related cause loss는 독립 P2로 기록했고 GDJ-0033 fixed claim을 금지합니다.

#### Phase B — no-product feasibility

- Frozen patch `8329bb0a...`의 public API, private descriptor와 project package compile을 증명했습니다.
- Scalar presence/cache/pending을 분리해 raw-zero unset, explicit-zero present, loaded-zero present와 target-object PK zero를
  모두 구분했습니다.
- Corrected preflight Phase 1은 모든 relation-cache tuple을 canonical normalized source-model identity + relation field
  `Name` 순서로 검증·snapshot하고, Phase 2는 모든 assigned target origin을 검증하면서 target PK를 edge별 정확히 한 번
  snapshot하며, Phase 3은 같은 순서의 첫 no-PK target을 반환합니다. 그 뒤에만 required-unset을 검사합니다.
- Save가 전체 relation state를 read-only validate하고 candidate raw/write/object/cache를 만든 뒤에만 publish합니다.
- Reversed declaration + both invalid, 앞선 Author no-PK와 뒤 Reviewer corrupt cache/self/origin의 양방향 masking,
  candidate rebuild failure와 corrupt cache tuple은 I/O 0/no partial publication입니다.
- Pending+source-empty-only reconciliation, caller-changed-scalar precedence, key-present-later-change invalidation,
  full `(presence,value)` tuple의 same/different scalar와 nullable clear를 검증했습니다.
- Per-edge COW는 changed edge만 교체하고 unrelated ready/absent를 independent cell로 보존하며 cold/flight를 공유하지
  않습니다. Actual eager Reviewer hydration 뒤 Author derivation도 Reviewer query 0입니다.
- Manual PK missing row는 exact backend mutation +1, non-REL002, cause-preserved/DB unchanged입니다.
- Nil/zero/copy/cross-origin/context/session boundaries와 caller-synchronized race path를 검증했습니다.
- Normal/race/CGO0/vet/full codegen+orm+query/Linux386가 PASS했습니다. Full `./...` sole failure는 unpublished product
  companion deterministic drift이고 그 외 package는 PASS했습니다.

Product external compile consumer는 두 exact flows를 모두 가져야 합니다.

1. new source + new no-PK target → assignment → source Save의 REL-002 failure shape
2. new source + new target → assignment → target Save → same derived source Save의 later-key reconciliation shape

추가로 required unset, ID zero, nullable clear, same/different scalar와 invalid wrapper signature를 compile합니다.

#### Phase C — freeze와 bounded implementation

ADR-0033은 exact query-root `New`, wrapper `Save`, `WithAuthor`/`WithReviewer`, ID helpers와 `ClearReviewer`, fresh source
derivation, project-private descriptor, pending-only reconciliation, corrected canonical three-phase preflight와 per-edge
COW를 Accepted했고 work frontmatter의 exact 23 source/product path에 local 구현했습니다.
REL-002 manifest transition은 Go protocol/godjcheck
hard locks와 `conformance/runners/django/tests/test_relation_scenarios.py`의 product-manifest status assertion만 measured
update하고, `conformance/README.md`, relation/migration-project/write-migration artifact locks와 workflow measured
inventory를 함께 갱신해야 합니다. Django scenario execution/oracle/checksum/static fixture는 byte-frozen으로 유지합니다.

Local gate는 focused normal/race/CGO-disabled/vet, product/external compile, generator golden/determinism/last-good,
unchanged app-generated exact 13, bounded Linux/386, full `go test ./...`, measured 715-test workflow roster와 independent
P0-P3 audit까지 EVID-075에서 통과했습니다. EVID-076은 exact Darwin 193/193, four Python exact-profile suites와
four relation-product coordinate each 715/715/0을 포함한 exact implementation head를 검증했습니다. EVID-076을 포함하는
exact 15-document completion head `81f4aacb...`는 EVID-077/run `31590911735`의 별도 exact 26/26 jobs·326/326
steps와 audit P0..P3=0을 통과했습니다. EVID-077을 포함하는 exact seven-document terminal head `db5c11f6...`도
EVID-078/run `31593500615`의 별도 exact 26/26·326/326과 audit P0..P3=0을 통과했습니다. Completion run을 terminal
proof로 재사용하지 않았습니다. Q-013은 `Partial`, Q-017은 P1/open이고 relation-capable migration,
reverse/general facade와 non-SQLite backend는 이 gate의 claim이 아닙니다.

#### GDJ-0034 — typed generated `select_related` cause preservation

GDJ-0034는 새 Django contract나 SQL/result 의미를 추가하지 않습니다. Stale/mismatched generated 조합에서 typed
builder가 `ResolveForwardSelectPath` 또는 required/nullable bind의 structured error를 잃는 진단 P2만 다음
Go-native safety gate로 고칩니다.

- Required resolve failure, required bind failure와 nullable bind failure를 각각 구성하고 terminal `All(ctx)`의
  category/code/detail 및 unwrap/cause chain 보존과 backend query/mutation 0을 검증합니다.
- Stored configuration failure와 nil context, typed-nil context, cancelled context를 각각 조합해 ADR-0029의
  context precedence가 먼저이고 original configuration cause는 backend validation/I/O보다 먼저임을 검증합니다.
- `ParseDynamic`의 기존 exact-cause control과 실제 zero/corrupt query의 generic invalid-plan control을 함께 둡니다.
- 정상 required/nullable eager result, query count와 warm cache publication이 바뀌지 않음을 재검증합니다.
- Generator v2 golden/determinism/last-good, 두 checked-in companion no-rewrite, physical no-overlay external compile와
  final measured digest/inventory lock을 검증합니다.
- Focused normal/race/CGO-disabled/vet, bounded Linux/386, full `go test ./...`, exact hosted matrix와 independent
  P0..P3 audit 순서를 유지합니다.

EVID-078은 GDJ-0033 terminal baseline만 증명하며 이 activation documentation이나 이후 구현 증거로 재사용하지
않습니다. 현재 activation tree는 `not run/pending`입니다. Implementation, completion documentation과 terminal tree는
각각 고유 exact-head CI를 사용합니다. REL-009/010/011 및 aggregate product classification은 활성화 시점에 그대로이고,
locked Django relation oracle/manifest/checksum을 바꾸지 않습니다.

이전 두 job은 PR #1의
[run 31295886061](https://github.com/progresshans/godj/actions/runs/31295886061)에서 처음 함께
통과했으며 상세 환경과 결과는
[EVID-20260809-018](status/TEST_EVIDENCE.md#evid-20260809-018--gdj-0018-github-hosted-ubuntu와-darwinarm64-ci)에
기록합니다.

GDJ-0019 completion head `4d9a64a0c42406bda931820f7eb38a0f737d117c`에서도 같은 두 job을
[run 31302983804](https://github.com/progresshans/godj/actions/runs/31302983804)로 다시 실행했습니다.
Ubuntu portable 164 tests/15 skips, macOS exact 164/164, migration-definition-source를 포함한
10개 oracle checksum/`--check`와 no-rewrite가 통과했으며 상세 결과는
[EVID-20260809-020](status/TEST_EVIDENCE.md#evid-20260809-020--gdj-0019-github-hosted-ubuntu와-darwinarm64-ci)에
기록합니다.

GDJ-0020 product head `6172d843a4bb234592cafc176a8d1191933b141c`은 같은 Draft PR #1의
[run 31309152526](https://github.com/progresshans/godj/actions/runs/31309152526)에서 두 job이 모두
통과했습니다. Ubuntu 24.04 portable job은 `CGO_ENABLED=0 GOARCH=386 go test -count=1
./migrations/definition`을 실제 Linux/386 runtime으로 실행했고, macOS 15 arm64 exact job은
locked profile/oracle/no-rewrite gate를 유지했습니다. 세부 local/product-head 증거는
[EVID-20260809-021](status/TEST_EVIDENCE.md#evid-20260809-021--gdj-0020-bounded-migration-definition-loader-product-slice)과
[EVID-20260809-022](status/TEST_EVIDENCE.md#evid-20260809-022--gdj-0020-github-hosted-product-head-ci)에
기록합니다.

GDJ-0020 completion-documentation head
`a5422f2c1ba5db34986564fc065e4b8e28ef0115`도 같은 Draft PR #1의
[run 31310002784](https://github.com/progresshans/godj/actions/runs/31310002784)에서 별도로
재검증했습니다. Ubuntu 24.04.4 job
[93236227654](https://github.com/progresshans/godj/actions/runs/31310002784/job/93236227654)는
Go 1.26.5/uv 0.10.12/Python 3.14.3/Django 6.1/SQLite 3.50.4 profile에서 `make ci`, portable
Python 164 tests/15 skips, actual `CGO_ENABLED=0 GOARCH=386` definition runtime,
checksum/no-rewrite를 통과했습니다. macOS 15.7.7 arm64 job
[93236227698](https://github.com/progresshans/godj/actions/runs/31310002784/job/93236227698)는 같은 pinned
tool profile에서 focused CGO-disabled Go, exact Python 164/164, all-oracle/no-rewrite를
통과했습니다. 상세 증거는
[EVID-20260809-023](status/TEST_EVIDENCE.md#evid-20260809-023--gdj-0020-github-hosted-completion-documentation-head-ci)에
기록합니다. EVID-023 append/status 교정 commit
`53729103651bfc34acc5fe07fb4376d5dd78c204` 자체도 별도 Draft PR #1
[run 31310606332](https://github.com/progresshans/godj/actions/runs/31310606332)의 Ubuntu/macOS 두
job을 통과했으므로 run 31310002784를 그 patch의 PASS로 재사용하지 않습니다.
