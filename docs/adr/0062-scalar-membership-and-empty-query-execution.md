# ADR-0062: Scalar IN과 빈 조회의 실행 경계

- 상태: Accepted
- 날짜: 2026-09-19
- 관련 작업: [GDJ-0075](../../work/0075-scalar-membership-and-empty-query-semantics.md)
- 보완: [공통 Boolean AST](0040-composable-typed-boolean-predicates-and-article-search.md), [projection·aggregate](0039-typed-projection-scalar-aggregate-and-stable-pagination.md)

## 직접 forward 경로의 추가 결정

2026-09-19 GDJ-0079는 직접 forward target-field IN도 같은 immutable 목록과 실행 경계로 확장한다.
`NewRelatedInCondition`은 path를 보존하며, 선언이 non-null인 Boolean 등도 nullable FK 뒤에서는 NULL operand가 될 수 있다.
`Condition.OperandNullable`을 사용해 부정의 NULL 보정과 empty-result 분석을 일치시킨다. Typed의 concrete 목록과
explicit nil을 허용하는 dynamic 목록의 표현 범위는 root scalar와 같다. 실제 scope·증거는
[ADR-0040 추가 결정](0040-composable-typed-boolean-predicates-and-article-search.md#직접-forward-대상의-scalar-lookup)과 TEST_EVIDENCE를 따른다.

## 공개 목록 조회

Auto/Integer·Char/Text·Boolean·DateTime field의 typed `.In(...)`과 root scalar dynamic `field__in`은 같은 immutable
`query.NewInCondition`을 만든다. Generated FieldSet은 공통 field runtime의 메서드를 사용하며 별도 generated SQL을 만들지 않는다.
Typed 목록은 field의 concrete Go 값을 받는다. Dynamic은 정수의 `[]int`·`[]int64`, 문자열의 `[]string`, `[]bool`, `[]time.Time` 또는
같은 scalar 값과 explicit nil을 담는 `[]any`를 받는다. 빈 typed slice도 field 종류를 검사하고 raw nil·pointer·문자열 coercion은 거부한다.
Allowlist policy가 값 parsing보다 먼저 실행되며 모든 입력과 accessor의 목록은 복사한다.

빈 목록과 explicit NULL member를 AST에 보존한다. 중복·입력 순서를 유지하고 SQL parameter에는 non-NULL 값만 같은 순서로 전달한다.
고정 Django가 SQL parameter를 deduplicate하는 내부 모양까지 복제하지 않는다. DateTime은 기존 UTC microsecond 정규화를 따른다.

## NULL과 Boolean 문맥

독립 Django 관찰에서 nullable field의 부정은 NULL member 유무에 따라 달라진다. 양의 `IN []`와 `IN [NULL]`은 빈 결과다.
`NOT IN []`은 모든 행을 포함하지만 nullable `NOT IN [NULL]`은 non-NULL 행만 포함한다. Nonempty 목록의 부정도 NULL member가
있으면 NULL 행을 제외하고, 없으면 NULL 행을 포함한다. NULL을 AST에서 먼저 제거하면 이 차이를 복원할 수 없다.

양 compiler는 odd negation에서 NULL member가 있으면 `IN(nonnull values) OR field IS NULL`, 없으면
`IN(nonnull values) AND field IS NOT NULL`로 leaf를 묶은 뒤 외부 NOT을 적용한다. Non-NULL 값이 없는 IN은 `0 = 1`로 compile한다.
공통 `Plan.EmptyResult`는 같은 negation parity로 알려진 true/false/unknown만 분석한다. Nullable NULL-only leaf의 음의 문맥은
row 값 없이 결정하지 않는다. 일반 expression optimizer나 SQL three-valued logic 전체를 추측하는 최적화는 하지 않는다.

## 검증 뒤에 실행을 생략한다

Backend와 session은 context·활성 상태·SQLite quarantine을 확인하고 전체 plan을 compile한 뒤에만 empty source를 처리한다.
GDJ-0080은 `LIMIT 0`도 같은 empty source 분석에 포함한다. Predicate가 없거나 Offset이 있어도 입력 행은 없으며,
Count는 0, model/projection은 빈 결과다. 이 경우에도 invalid metadata/capability와 취소·session 검증이 먼저 실행된다.
잘못된 field/order/result shape나 미지원 relation 표현은 빈 조건과 결합돼도 오류다. ORM의 조기 반환으로 backend 검증을 건너뛰지 않는다.
Model/projection은 zero rows, COUNT는 0, MIN/MAX는 NULL이다. 공통 synthetic rows는 이 좁은 aggregate의 scanner·cursor·취소 의미를
소유하며 실제 `database/sql`의 0/NULL scan과 대조한다. SQL 연결을 만들어 알려진 결과를 다시 읽지 않는다.

Session은 원래 transaction context와 callback 종료에 연결된 lifetime을 소유한다. 개별 Query가 다른 context를 받아도 transaction
취소를 무시하지 않으며 synthetic cursor도 transaction 종료 뒤 읽을 수 없다. SQL transaction 시작·종료와 coordination lock은
기존대로 수행한다. 빈 Query의 SELECT 생략을 전체 Atomic의 무 I/O로 주장하지 않는다. QuerySet cache·clone·terminal validation은 유지한다.

## 출처와 남은 범위

저장소 lock의 Django 6.1 `django/db/models/lookups.py`의 `In`과 `django/db/models/sql/query.py`의 nullable negation을
참조했다. 라이선스는 BSD-3-Clause다. [독립 runner](../../conformance/runners/django/membership_reference.py)와
[관찰 fixture](../../orm/testdata/in-django61.json)는 여섯 scalar field의 중복·NULL·empty·없는 값·AND/OR/NOT 결과와 SELECT 수를 보존한다.
GoDj에서 유래한 expected가 아니며 고정 reference는 USE_TZ=true·UTC·SQLite다. PostgreSQL 결과는 실제 별도 실행으로 검증한다.

Reverse/multi-hop relation IN·tuple/composite-key IN·subquery IN, 대규모 목록의 backend parameter 한도 초과 분할은 별도 미완료 범위다.
일반 bulk write나 새로운 field 종류를 포함하지 않는다. 실행 source·환경·실패 수정은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 기록한다.
