---
id: GDJ-0002
status: ready
updated: 2026-08-07
baseline_branch: ""
baseline_commit: ""
depends_on: ["GDJ-0001", "ADR-0006"]
contracts: ["QRY-001..QRY-010", "SCH-001 reference", "M1 SCH/GEN/DB IDs TBD"]
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

- [ ] M0 contract 중 M1 범위가 SQLite에서 `passing`
- [ ] codegen golden/idempotency/stale/compile tests 통과
- [ ] typed negative compile fixtures 통과
- [ ] dynamic unknown/disallowed lookup이 실행 전 오류
- [ ] context cancellation/error cleanup tests 통과
- [ ] `go test`와 relevant race test evidence 기록
- [ ] public API 결정을 ADR과 문서에 반영

GDJ-0001과 ADR-0006이 선행 조건을 닫았습니다. 이 문서의 타입/패키지 이름은 여전히
public API가 아니며, 시작 시 baseline commit과 첫 spike의 수정 경로를 기록합니다.
