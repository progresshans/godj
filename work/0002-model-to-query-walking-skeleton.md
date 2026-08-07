---
id: GDJ-0002
status: completed
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "8eac1dc21261ad467553217815133a01d04ad180"
depends_on: ["GDJ-0001", "ADR-0006"]
contracts: ["QRY-001..QRY-010", "SCH-001", "SCH-M1-001", "GEN-M1-001", "QRY-M1-001", "DB-SQLITE-001"]
allowed_paths: ["go.mod", "go.sum", "cmd/godj/**", "schema/**", "codegen/**", "orm/**", "query/**", "db/**", "internal/**", "conformance/**", "examples/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# 첫 Model-to-Query 수직 단면

## 사용자에게 보이는 결과

`Article` 모델을 선언·생성하고 typed/dynamic query로 SQLite에서 조회하면 Django 6.1 oracle과 같은 정규화 결과를 얻습니다.

## 목표 흐름

```text
minimal Schema DSL
→ normalized/versioned IR
→ deterministic generated Article/FieldSet/Descriptor
→ Manager[Article] / QuerySet[Article]
→ typed and dynamic predicates
→ same immutable Query AST
→ SQLite compiler/executor
→ All(ctx)
→ differential contract passing
```

## 제한 범위

- Fields: `AutoField`, `CharField`, `BooleanField`, contract에 필요한 최소 nullable field
- Lookups: exact, ASCII icontains, isnull
- Query operations: chained filter/AND, order, limit, All
- test-only schema provisioner 허용
- 한 앱, 한 모델, 관계 없음

## 비목표

- production migration engine
- save/delete/update lifecycle 전체
- ForeignKey/reverse relation
- annotation/aggregate/subquery
- PostgreSQL 또는 다른 backend
- Admin/Form/API

## 반드시 검증할 설계 가설

- Schema IR이 codegen과 runtime metadata의 동일 원본인지
- stale output 상황에서도 Q-001 결정이 generation을 가능하게 하는지
- 서로 다른 model의 predicate를 섞을 수 없는지
- typed/dynamic API가 같은 AST와 error taxonomy를 만드는지
- QuerySet chain이 원본 plan을 바꾸지 않는지
- context cancellation과 row/resource close가 전달되는지
- generated code가 repository 내부뿐 아니라 external consumer package 관점에서 compile되는지

## 시작 전 결정 spike

다음 항목은 문서 예시를 바로 공개 API로 만들지 않고 compile/runtime fixture로 먼저
비교합니다.

1. `ModelDescriptor[M]` interface와 generated concrete descriptor의 초기화/freeze
2. M1에 필요한 `NULL` 표현과 조회/`isnull` 의미 — partial update는 비목표
3. Dynamic lookup 오류를 반환하면서 chaining 가능한 최소 API
4. Package dependency direction과 external consumer compile fixture
5. SQLite driver 후보의 pure-Go/CGO, cancellation, supported Go version, license 영향

결과가 공개 사용법이나 장기 package 경계를 정하면 같은 변경에서 ADR을 추가합니다.

## 구현 순서

1. Q-005/Q-006/Q-008/Q-009와 SQLite driver 선택을 작은 spike로 좁힙니다.
2. ADR-0006에 따라 generated target을 import하지 않는 최소 Go Schema DSL package를
   만듭니다.
3. Versioned Schema IR normalization, canonical serialization/hash와 validation을
   구현합니다.
4. Package당 한 generated file로 `Article`, FieldSet, descriptor/codec binding을 만들고
   golden/idempotency/stale/compile test를 연결합니다.
5. Generic Manager/QuerySet과 불변 Query AST를 만들고 typed/dynamic predicate를 같은
   node로 수렴시킵니다.
6. SQLite compiler/executor와 test-only schema provisioner를 연결합니다.
7. GoDj observation adapter를 만들어 M0 oracle과 실제 comparator로 QRY contract를
   `passing`까지 올립니다.

## 완료 gate

- [x] M0 contract 중 M1 범위가 SQLite에서 `passing`
- [x] codegen golden/idempotency/stale/compile tests 통과
- [x] typed negative compile fixtures 통과
- [x] dynamic unknown/disallowed lookup이 실행 전 오류
- [x] context cancellation/error cleanup tests 통과
- [x] `go test`와 relevant race test evidence 기록
- [x] public API 결정을 ADR과 문서에 반영

GDJ-0001과 ADR-0006이 선행 조건을 닫았습니다. 이 문서의 타입/패키지 이름은 여전히
public API가 아니며, 시작 시 baseline commit과 첫 spike의 수정 경로를 기록합니다.

## 시작 기록

- 시작 시각 기준 checkout: `main@8eac1dc21261ad467553217815133a01d04ad180`
- 시작 시 worktree: clean
- 외부 blocker: 없음
- 첫 통합 범위: descriptor/nullable/dynamic lookup/package dependency/SQLite driver
  결정을 증거로 좁힌 뒤 Schema-to-Query 수직 단면을 구현

## 완료 결과

`SCH-M1-001`, `GEN-M1-001`, `QRY-M1-001`, `DB-SQLITE-001`은 두 번째 Django
manifest 계약이 아니라 M1 내부 capability/compile/runtime gate ID입니다. Differential
실행 정본은 기존 QRY-001..010과 SCH-001의 11개입니다.

- `schema` 선언에서 implicit AutoField를 포함한 versioned `schema/ir`을 만들고,
  canonical JSON/hash와 validation을 구현했습니다.
- `codegen`이 package당 한 파일로 `Article`, typed FieldSet, zero-state descriptor,
  generic Manager binding과 schema/generator hash를 byte-deterministic하게 생성합니다.
- M1 project runner가 Go overlay로 실제 target package를 compile-only 검증한 뒤에만
  generated 파일을 교체합니다. Init/TestMain은 실행하지 않으며 실패 시 last-good
  bytes를 보존하고 삭제된 target도 복구합니다.
- `Predicate[M]`, `Ordering[M]`, `Manager[M]`, `QuerySet[M]`과 copy-on-write Query Plan을
  구현했습니다. Typed/dynamic lookup은 동일 condition node로 수렴합니다.
- SQLite compiler/executor가 parameter binding, identifier quote, LIKE escape,
  exact/ASCII icontains/isnull/AND/order/limit를 실행합니다.
- GoDj adapter가 QRY-001..QRY-010과 SCH-001 총 11개를 독립 DB에서 관찰하며 locked
  Django oracle과 모두 일치합니다.

## 채택한 결정

- Descriptor, nullable read, dynamic parsing과 dependency 경계:
  [ADR-0007](../docs/adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md)
- M1 SQLite driver/execution 경계:
  [ADR-0008](../docs/adr/0008-m1-sqlite-driver-and-execution-boundary.md)
- M1 nullable CharField는 `*string`이지만 write patch의 omitted 의미는 Q-006으로
  남겼습니다.
- Dynamic input은 `ParseDynamic`에서 즉시 typed predicate 또는 error가 되고,
  QuerySet 안에 construction error를 숨기지 않습니다.
- M1 QuerySet은 result cache가 없습니다. Q-007의 최종 의미는 결정하지 않았습니다.
- 공개 `godj generate` CLI는 Q-010 전까지 만들지 않고 `internal/cmd/m1generate`만
  사용합니다.

## 변경 파일 묶음

- 선언/IR/codegen: `schema/**`, `codegen/**`, `internal/cmd/m1generate/**`,
  `examples/article/**`
- Generic query/runtime: `query/**`, `orm/**`, `db/**`
- 호환 adapter/gate: `conformance/runners/godj/**`,
  `conformance/cmd/godjcheck/**`, `internal/compiletest/**`
- dependency/license: `go.mod`, `go.sum`, `LICENSE.modernc-*`, `NOTICE.md`
- 운영/설계: `Makefile`, ADR-0007/0008, architecture/compatibility/status/work 문서

## 검증 증거

[EVID-20260808-001](../docs/status/TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton)에
전체 명령, checkout, backend fingerprint와 결과를 기록했습니다.

## 알려진 제한

- 한 app/한 model과 세 field kind만 지원합니다.
- Test-only schema provisioner이며 migration/write lifecycle은 없습니다.
- Relation, projection, aggregate, transaction API, 다른 backend는 없습니다.
- Django reference SQLite 3.50.4와 Go backend SQLite 3.53.3은 서로 다른 runtime입니다.
- CLI/library version, cache와 전체 goroutine safety는 계속 open입니다.

## 다음 정확한 작업

[GDJ-0003](0003-write-migration-compatibility-contracts.md)을 활성화하고 M1 완료 commit을
baseline으로 기록합니다. 기존 11개 manifest를 늘리지 말고, write/schema/transaction
동작 8~12개를 두 번째 contract set으로 먼저 Django runtime에서 조사·잠근 뒤 제품
write/migration 단면을 작성합니다.
