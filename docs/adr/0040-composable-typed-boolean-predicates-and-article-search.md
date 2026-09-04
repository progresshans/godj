# ADR-0040: 하나의 typed Boolean predicate tree로 Article 검색을 확장한다

- 상태: Accepted
- 날짜: 2026-08-23
- 관련 work/contract: GDJ-0040, QRY-034..043, Q-011, M4/M5
- 대체하는 ADR: 없음

## 맥락

ADR-0003/0007은 typed field predicate와 ordered dynamic lookup이 같은 query AST로 수렴하도록 정했고,
ADR-0012는 immutable QuerySet plan과 evaluation cache의 수명을 분리했습니다. ADR-0039는 source/result shape,
projection, scalar aggregate와 stable offset pagination을 추가했습니다.

현재 where authority는 `[]query.Condition` 하나이며 여러 predicate와 연속 `Filter`는 compiler에서 평평한
`AND`로만 결합됩니다. 이 표현으로는 제목 또는 요약 검색, 명시적 괄호와 부정 조건을 나타낼 수 없습니다.
별도의 Q 전용 plan이나 backend별 fallback을 추가하면 typed/dynamic API, projection/aggregate와 두 compiler가
서로 다른 where 의미를 갖게 됩니다.

Relation compiler는 현재 평평한 conjunctive leaf를 먼저 훑어 JOIN을 정합니다. Nullable relation leaf를
`OR`/`NOT` 아래 즉시 허용하면 INNER/LEFT JOIN 선택 때문에 root row가 사라질 수 있습니다. 또한 SQL의
three-valued logic에서 nullable leaf에 단순 `NOT (...)`을 적용하면 Django의 negated lookup 결과와 다를 수
있습니다.

## 결정

### authoritative where tree는 하나만 둔다

`query.Plan`은 flat condition slice 대신 하나의 immutable Boolean expression tree를 소유합니다.

- leaf는 기존 scalar 또는 relation `Condition`입니다.
- connector는 ordered n-ary `AND`, ordered n-ary `OR`, unary `NOT`뿐입니다.
- 같은 connector의 중첩은 입력 순서를 보존한 채 canonical flatten합니다.
- `Filter(a, b)`와 `Filter(a).Filter(b)`는 같은 ordered root `AND` 의미로 수렴합니다.
- flat/tree 이중 저장, legacy compiler fallback과 empty Boolean constant는 두지 않습니다.
- accessor는 detached copy만 반환하고 caller가 child storage나 node pointer를 변경할 수 없습니다.

Plan/compiler validation은 최대 depth 64, 전체 node 1,024를 적용합니다. Empty/malformed connector,
zero/forged leaf, foreign source field, invalid lookup/value와 cap 초과는 backend I/O 전에 structured
`query_error/invalid_plan` 또는 명시적 unsupported error로 닫습니다.

### typed ORM composition

공개 typed 표면은 다음 세 top-level generic constructor를 사용합니다.

```go
orm.And(left, right, rest...)
orm.Or(left, right, rest...)
orm.Not(predicate)
```

각 함수는 `Predicate[M]`를 반환하므로 다른 model predicate 혼합은 compile time에 거부됩니다. `And`와 `Or`는
최소 두 operand를 함수 signature로 요구하고 `Not`은 정확히 하나만 받습니다. Invalid typed field나 nested
predicate error는 기존 Predicate configuration error 경로를 통해 terminal 전파됩니다.

Dynamic lookup은 기존 ordered leaf parser만 유지합니다. 문자열 Q parser, Django `Q` object ABI/deconstruction,
map iteration 기반 Boolean 입력은 만들지 않습니다.

### compiler와 NULL 의미

SQLite와 PostgreSQL은 동일한 DFS child order로 parenthesized tree를 compile합니다. PostgreSQL placeholder 번호와
argument order도 그 traversal 하나에서 결정합니다. Projection, direct/derived Count·Max와 full model query가
같은 where compiler를 재사용해야 하며 in-memory post-filter는 허용하지 않습니다.

Nullable leaf negation은 단순 SQL 문법을 추측하지 않고 pinned Django 6.1 QRY-038 truth table을 기준으로
compile합니다. 이번 범위의 `icontains`는 기존 exact ASCII/escaping profile만 유지하며 Unicode/collation parity를
주장하지 않습니다.

기존 conjunctive relation leaf는 보존합니다. Relation leaf가 `OR` 또는 `NOT` 아래 있으면 JOIN promotion을
암묵적으로 도입하지 않고 pre-I/O structured unsupported로 거부합니다. Related projection과 relation Boolean
composition은 별도 ADR/work가 소유합니다.

### Article 사용자 흐름

기존 Article request-local DTO 흐름을 다음 검색으로 넓힙니다.

```text
GET /articles/?q=go&published=true&exclude_title=draft&offset=0&limit=20
→ (title icontains "go" OR summary icontains "go")
→ AND published = true
→ AND NOT title icontains "draft"
→ stable ID order + offset/limit + typed projection
→ 같은 filtered source의 Count/Max report
```

`q`와 `exclude_title`은 각각 최대 256 bytes이며 malformed encoding, duplicate bounded parameter와 cap 초과는
DB I/O 전에 400입니다. Page projection과 report aggregate는 계속 request당 정확히 두 query입니다. SQLite와
PostgreSQL이 같은 rendered meaning을 내지만 두 query 사이 transaction snapshot은 주장하지 않습니다.

## 결과

- 검색 UI, 향후 Admin/API filter builder가 재사용할 typed Boolean 표현력을 얻습니다.
- source/result/cache 경계와 generated facade ABI v2를 바꾸지 않고 기존 `Filter`가 composite predicate를
  운반합니다.
- projection, aggregate와 두 backend가 하나의 where authority를 사용합니다.
- Relation OR/NOT의 JOIN cardinality 문제를 silent wrong-result 대신 explicit unsupported로 보존합니다.
- 새 persisted format/version이나 compatibility reader가 생기지 않습니다.

## 의도적으로 결정하지 않는 것

- F expression, field-to-field comparison과 arithmetic
- 새 lookup 종류, cursor lookup과 Unicode/collation 일반화
- relation predicate를 포함한 OR/NOT, related-column projection/aggregate
- annotation/grouping/having, subquery/window
- bulk update/delete, row locking, transaction-bound QuerySet와 request transaction
- Form/validation, CSRF/session/Auth/Admin/API, runserver와 dynamic routing
- MySQL과 추가 backend

## 검증

GDJ-0040은 QRY-034..043의 pinned Django result/side-effect/error 계약, oracle-blind SQLite product adapter,
SQLite/PostgreSQL compiler와 actual Article HTTP E2E, typed/dynamic convergence, cross-model compile rejection,
tree cap/immutability/race/cancellation/no-post-filter gate를 거칩니다. Phase A contract, Phase B core, 병렬 Phase C
backend/Article, final frozen milestone의 큰 checkpoint만 사용합니다.
