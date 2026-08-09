# 테스트 전략

- 상태: Accepted
- 마지막 검토: 2026-08-10

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
diff가 바뀌어야 합니다. MIG-065..074는 10 `passing`, 현재 제품은 11 adapter/115 contract의
`110 passing + 5 deviation`입니다. Local normal/race/CGO-disabled/vet/count-20, Linux/386 compile-only,
`make ci`, exact oracle와 independent audits는 EVID-027에 기록했고 fix head exact 18/18 hosted
acceptance는 EVID-028에 기록했습니다.
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
174/16 expected skips와 115-scenario payload를 검증합니다. Expanded required topology는 existing 2 +
test-only proof 4 + SQLite 4 + product 4 + Python 4의 exact 18 hosted executions입니다. Routine
Ubuntu/compatibility는 uv 0.12.3, embedded profile을 재현하는 exact darwin job만 uv 0.10.12입니다.
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
기록했습니다. EVID-028/status patch 자체의 final exact-head CI는 commit/push 전이라 `not run/pending`이며
run 31329294154를 그 후속 patch의 success로 재사용하지 않습니다.

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
