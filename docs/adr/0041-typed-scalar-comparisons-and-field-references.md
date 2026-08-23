# ADR-0041: typed scalar comparison과 same-model field reference를 하나의 condition RHS로 표현한다

- 상태: Accepted
- 날짜: 2026-08-23
- 관련 work/contract: GDJ-0041, QRY-044..053, Q-011, M4
- 대체하는 ADR: 없음

## 맥락

ADR-0040은 literal leaf를 운반하는 하나의 immutable Boolean tree를 구현했지만 `exact`, `icontains`, `isnull`,
`in`만 지원합니다. 사용자는 ID 범위나 같은 row의 두 field를 비교하려면 raw SQL 또는 메모리 후처리 없이 typed
QuerySet source 안에서 이를 표현할 수 있어야 합니다.

Field reference는 literal과 다릅니다. SQL bind parameter가 아니라 검증·quote된 source column이어야 하고, model과
value kind가 일치해야 하며, nullable RHS를 부정할 때 SQL three-valued logic가 Django observable complement와 달라질
수 있습니다. 반면 지금 arithmetic/functions/annotation까지 포괄하는 공개 expression interface를 열면 아직 검증하지
않은 큰 ABI를 잠그게 됩니다.

## 결정 기준

- 기존 generated typed field를 그대로 사용하는 짧은 Go 호출
- cross-model/kind 오류의 compile-time 차단과 forged AST의 pre-I/O 차단
- literal과 field RHS의 명시적 one-of, 불변성, 결정적 SQL traversal
- SQLite/PostgreSQL 결과와 nullable negation parity
- 이후 arithmetic expression을 추가하더라도 이번 API를 compatibility shim으로 남기지 않을 수 있는 범위

## 고려한 선택지

### 선택지 A — broad public Expression interface

Literal, field, arithmetic, function을 모두 하나의 공개 expression interface로 만들 수 있지만, 현재 범위 밖의 type
promotion, NULL propagation, backend capability와 annotation ownership까지 미리 결정해야 합니다.

### 선택지 B — field를 comparison method에 직접 전달

호출은 짧지만 nullable/non-null field concrete type 조합마다 method가 늘고, literal overload가 없는 Go에서 method
이름과 의미가 불균일해집니다. Django의 `F`에 해당하는 값이라는 의도도 호출부에서 약합니다.

### 선택지 C — sealed typed `orm.F` reference와 bounded RHS union

Generated field를 `orm.F`로 감싸 model/value type을 보존하고, condition은 literal/list/field reference 중 하나만
소유합니다. Arithmetic/function capability를 열지 않은 채 exact/range lookup만 field RHS를 받을 수 있습니다.

## 결정

선택지 C를 채택합니다. Phase A의 pinned Django 6.1 reference와 외부 모듈 compile proof에서 아래 공개
경계와 nullable complement가 확인됐습니다.

- `orm.F(typedField)`는 immutable sealed `FieldReference[M, V]`를 만듭니다.
- Integer/String literal에는 `GreaterThan`, `GreaterThanOrEqual`, `LessThan`, `LessThanOrEqual`을 추가합니다.
- Same-model Integer/String reference는 `ExactField`, `GreaterThanField`, `GreaterThanOrEqualField`,
  `LessThanField`, `LessThanOrEqualField`로 받고 `exact`/`gt`/`gte`/`lt`/`lte`만 허용합니다. Boolean field
  reference는 이번 public/AST 범위에 넣지 않습니다.
- Dynamic lookup은 typed scalar literal `gt`/`gte`/`lt`/`lte`만 허용하며 dynamic field-reference parser는 만들지 않습니다.
- Query condition은 literal scalar, list, field reference가 동시에 존재하지 못하는 private discriminated union으로
  검증합니다. Field RHS kind는 LHS와 같고 source field inventory에 포함되어야 합니다.
- Relation path condition과 field RHS 조합, `Plan.SourceFields`에 없는 RHS와 unknown lookup은 terminal 전에 structured
  error입니다. `FieldRef`는 model/table provenance를 보유하지 않으므로 structurally identical reference를 별도
  source라고 식별하는 runtime claim은 하지 않습니다. Typed `orm.F`의 model parameter가 정상 public 호출의
  cross-model 혼합을 compile time에 거부합니다.
- SQLite/PostgreSQL은 field RHS를 quote된 identifier로 출력하고 argument/placeholder를 소비하지 않습니다.
- Nullable negation은 홀수 `NOT`에서 nullable LHS와 nullable field RHS에 각각 `IS NOT NULL` guard를 넣고,
  같은 field가 양쪽 operand면 guard를 한 번만 냅니다. 짝수 `NOT`에는 guard를 넣지 않습니다. 이 규칙은
  QRY-050/051의 Django observable complement를 보존합니다.

병렬 legacy alias나 reflection 기반 general expression fallback은 만들지 않습니다.

## 결과

- Range와 same-row comparison이 기존 Boolean/projection/aggregate source에 합류합니다.
- Generated output과 persisted format을 바꾸지 않고 generic ORM layer의 기능으로 추가할 수 있습니다.
- Arithmetic/function/annotation은 아직 표현할 수 없으며, 후속 ADR이 별도 operand kind/capability를 결정합니다.
- Field RHS source binding과 nullable complement 검증 비용이 두 backend에 추가됩니다.

## 의도적으로 결정하지 않은 것

- Arithmetic, database functions, transforms, annotation/grouping/having
- Relation field reference, cross-model reference와 join promotion
- Dynamic F parser와 arbitrary string field path
- Non-Integer/String ordering semantics, Unicode/collation 일반화
- Mutation expression, subquery/window와 locking

## 검증

- External module에서 same-model/same-kind 호출은 compile되고 cross-model/kind 호출은 compile-fail합니다.
- QRY-044..053 independent Django oracle과 oracle-blind GoDj/SQLite actual이 result/DB-state/metrics에서 일치합니다.
- SQLite/PostgreSQL unit/integration은 literal/reference mixed DFS, placeholder count, nullable odd/even NOT과 malformed
  union/source/relation rejection을 검증합니다.
- Article advanced filter는 invalid input DB I/O 0, success request projection+aggregate 정확히 2 query를 검증합니다.
- Phase A proof는 exact Django 전체 239/239, QRY-034..043 observation-prefix 동일성, same-model/same-kind external
  compile과 cross-model/kind/Boolean/relation compile-fail을 통과했습니다. 제품·actual·hosted 검증은 active work의
  후속 phase에서 별도로 기록합니다.
