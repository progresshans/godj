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

- key ordering과 JSON encoding을 canonical하게 고정합니다.
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

## 기능별 기본 테스트 요구

모든 테스트 종류를 모든 작은 변경에 억지로 추가하지는 않습니다. 위험에 맞게 선택하되, 다음 변경은 기본 gate를 가집니다.

| 변경 | 최소 검증 |
|---|---|
| Schema/IR | validation, normalization, round-trip, deterministic hash, fuzz |
| Codegen | golden, idempotency, compile, stale output, multi-file failure atomicity |
| Typed query API | compile-positive/negative, AST invariant, differential result |
| Dynamic lookup | validation/coercion, allowlist, injection/error, typed AST equivalence |
| Query execution | integration, cancellation, resource close, backend contract |
| Migration | state diff, forward/backward, failure/rollback, concurrent lock |
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
