# 테스트 전략

- 상태: Accepted
- 마지막 검토: 2026-08-08

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
  fixtures/godj-query-cache-not-implemented.json
  fixtures/godj-migration-planning-not-implemented.json
  oracles/django-6.1-sqlite-darwin-arm64/
  codegenbootstrap/
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
연결해 11개 모두 `passing`으로 전환했습니다. `make godj-conformance`는 이제 M1 11개,
M2 write/migration 11개, Save 12개와 QuerySet cache 11개, 총 45개를 실행합니다. 임의로
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

현재 migration-planning manifest는 12개 `oracle_locked`이고 static fixture는 ordered 12
`not_implemented` mismatch를 냅니다. GoDj product runner는 unknown scenario exit 2와
actual 미생성으로 fail-closed합니다. 따라서 `make godj-conformance`는 계속 실제 제품
adapter가 있는 기존 45개만 실행합니다. 상세 증거는
[EVID-20260808-008](status/TEST_EVIDENCE.md#evid-20260808-008--gdj-0009-migration-planning-compatibility-contracts)에
기록합니다.

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
| Migration | state diff, graph construction, applied pruning, forward/backward, zero-mutation planning, structured graph/history error, failure/rollback, concurrent lock |
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

실제 command는 toolchain과 파일이 생긴 작업에서 확정합니다. 존재하지 않는 명령을 현재 표준처럼 문서화하지 않습니다.
