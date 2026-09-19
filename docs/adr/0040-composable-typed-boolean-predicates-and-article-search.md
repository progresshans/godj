# ADR-0040: 하나의 typed Boolean predicate tree로 Article 검색을 확장한다

> 결정 이유를 보존한 기록이다. 현재 API·지원 범위는 [현행 아키텍처](../ARCHITECTURE.md)와
> [구현 현황](../status/IMPLEMENTATION_MATRIX.md)를 따른다. 옛 내부 파일 구성·단계별 검증 절차는 현재 호환 요구가 아니다.
> 당시의 전체 기록과 실행 증거는 [고정 원문](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr/0040-composable-typed-boolean-predicates-and-article-search.md)에 있다.

- 상태: Accepted
- 날짜: 2026-08-23
- 관련 work/contract: GDJ-0040, QRY-034..043, Q-011, M4/M5
- 대체하는 ADR: 없음

## 직접 forward 대상의 scalar lookup

2026-09-19 GDJ-0079는 아래 GDJ-0078의 exact/non-null target 제한을 다음 범위에서 확장한다.

- Typed 대상은 Integer·Char/Text·DateTime의 nullable/non-null field와 현재 non-null Boolean이다. 비교 입력은 기존 root scalar와
  같은 Go 값 타입을 쓴다. Nullable 저장 pointer를 비교 입력에 요구하지 않으며, sealed `ReferenceField`가 target model과 값 타입을 검사한다.
- Exact·ordered comparison·icontains·isnull·IN을 기존 scalar kind별 capability에 맞춘다. Boolean의 ordered comparison은 지원하지 않는다.
  Typed `.In(...)`은 concrete scalar를 받으며 explicit NULL member는 dynamic `[]any`의 nil로 표현한다.
- Dynamic forward는 `relation__field` 또는 `relation__field__lookup`을 받는다. Lookup policy는 복사된 선언 field와 실제 lookup을
  값 parsing 전에 받으며, 실패한 batch는 부분 predicate를 반환하지 않는다. Relation-level source-key isnull은 기존 object parser가 소유한다.
- `Condition.OperandNullable`은 field 선언과 optional forward hop을 함께 본다. 모델 metadata 자체를 nullable로 바꾸지 않는다.
  Compiler의 부정 보정과 empty-result 분석은 같은 판단을 사용한다. Isnull은 그 Boolean 결과 자체가 NULL인 조건이 아니다.
- Target isnull false와 NOT isnull true는 대상 존재를 요구한다. Positive comparison·icontains·IN도 대상이 필요하며,
  nullable edge를 INNER/LEFT로 고르는 기존 Boolean 규칙에 참여한다. NULL member가 있는 IN의 홀수 부정은
  [ADR-0062](0062-scalar-membership-and-empty-query-execution.md)의 OR IS NULL 보정으로 absent target까지 구분한다.
- `NewRelatedInCondition`은 같은 list RHS를 소유하면서 직접 forward 경로를 보존한다. Source-key/reverse membership,
  relation F, nested traversal와 reverse non-exact/OR/NOT은 계속 명시적 미지원이다. Reverse 생성기는 forward의 새 field 폭을 상속하지 않는다.
- [Django 6.1 독립 관찰](../../conformance/runners/django/forward_lookup_reference.py)은 748개다. Typed로 표현하는 628개와
  explicit NULL member를 사용하는 dynamic 사례를 구분한다. 출처는 Django의 공개 QuerySet/Q/lookup 동작(BSD-3-Clause)이며
  private Python 구조를 복제하지 않는다. 환경별 실행 상태는 [GDJ-0079](../../work/0079-forward-scalar-lookups.md)와 TEST_EVIDENCE를 따른다.

## 직접 forward 관계의 Boolean 확장

2026-09-19 GDJ-0078에서 required/nullable direct forward 관계의 기존 지원 scalar exact predicate를 AND/OR/NOT에 허용한다.
Reverse one-to-many의 OR/NOT은 row multiplicity와 correlated existence 의미가 다르므로 기존 명시적 미지원 상태를 유지한다.

- Canonical relation path는 FK의 Nullable 값을 보존한다. Typed·dynamic·generated adapter는 같은 AST를 사용한다.
- 공통 `queryplan`은 Boolean 식이 참이려면 대상 행이 반드시 필요한 edge를 계산한다. Positive exact leaf는 자기 edge가 필요하고,
  AND는 합집합·OR는 교집합이다. NOT은 논리 connector를 뒤집으며 홀수 부정의 nullable leaf는 대상 행이 없어도 참일 수 있다.
- Nullable edge는 반드시 필요한 경우 INNER JOIN, 나머지는 LEFT OUTER JOIN이다. Eager projection도 같은 결정을 사용한다.
  SQL quoting·placeholder·physical schema는 각 backend가 계속 소유하며 alias와 argument 순서는 결정적이다.
- Nullable FK를 지난 대상 column은 선언 자체가 non-null이라도 JOIN 결과에서 NULL이 될 수 있다.
  홀수 부정에서는 비교와 함께 **joined 대상 column의 IS NOT NULL**을 괄호 안에 넣는다. Root FK 값만 검사하는 보정으로 대체하지 않는다.
- Root source-key isnull은 JOIN 없이 같은 Boolean 식에 참여한다. FK가 존재해야 하는 isnull 조건은 선언한 FK 무결성 아래에서
  같은 edge를 INNER JOIN으로 내릴 근거가 된다. Nullable 대상 scalar 자체, relation lookup 확장과
  다중/중첩 eager materialization은 포함하지 않는다. 다른 eager/filter edge의 All은 여전히 미지원이며 Count는 projection을 생략할 수 있다.
- 고정 Django 6.1/SQLite의 [독립 관찰](../../conformance/runners/django/nullable_forward_reference.py)은
  Char·Text·int64·DateTime·PK와 root 조건·isnull을 조합한 73개 case다.
  `QuerySet`, `Q`, `JoinPromoter`의 외부 행·Count·JOIN/NULL 결과를 참조했다(Django, BSD-3-Clause).
  Django의 target-PK JOIN 생략 최적화나 private join voting 구조를 복제하지 않는다.
- 이 결정의 제품·환경별 완료는 [GDJ-0078](../../work/0078-nullable-forward-relation-predicates.md)와
  [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)를 따른다. 설계 채택과 compile 성공만으로 runtime PASS를 기록하지 않는다.

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
