---
id: GDJ-0002
status: proposed
updated: 2026-08-07
baseline_branch: ""
baseline_commit: ""
depends_on: ["GDJ-0001", "Q-001 decision"]
contracts: ["SCH-001+", "GEN-001+", "QRY-001..QRY-009", "DB-SQLITE-001+"]
allowed_paths: ["cmd/godj/**", "schema/**", "codegen/**", "orm/**", "query/**", "db/**", "conformance/**", "examples/**", "docs/**", "work/**"]
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

## 완료 gate

- [ ] M0 contract 중 M1 범위가 SQLite에서 `passing`
- [ ] codegen golden/idempotency/stale/compile tests 통과
- [ ] typed negative compile fixtures 통과
- [ ] dynamic unknown/disallowed lookup이 실행 전 오류
- [ ] context cancellation/error cleanup tests 통과
- [ ] `go test`와 relevant race test evidence 기록
- [ ] public API 결정을 ADR과 문서에 반영

세부 구현 계획은 GDJ-0001 결과와 Q-001 결정 후 확정합니다. 이 문서의 타입/패키지 이름은 아직 public API가 아닙니다.
