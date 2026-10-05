# ADR-0091: 그룹 결과와 집계 참조

- 상태: Accepted design — 그룹 기반 구현; 환경별 검증과 업무 연결은 TEST_EVIDENCE/활성 work가 소유
- 날짜: 2026-10-06
- 작업: [GDJ-0112](../../work/0112-grouped-aggregation-and-ticket-summary.md)

## 문제와 기준

Helpdesk의 현재 Category에서 우선순위별 전체·열린 티켓 수를 보고 열린 건수로 그룹을 고른다.
기존 projection은 원본 행을 선택하고 기존 aggregate는 한 행을 반환하므로 그룹별 결과·HAVING·페이지를
모델 행이나 일반 WHERE로 위장하지 않는다. Schema IR의 scalar kind·nullable·관계 provenance를 유지한다.

고정 Django 6.1의 `values(...).annotate(...)`, 조건부 Count/Min/Max, alias 필터·순서·slice를 실제
SQLite/PostgreSQL에서 독립 관찰했다. [고정 aggregate source](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/aggregates.py)와
[upstream 설명](https://docs.djangoproject.com/en/dev/topics/db/aggregation/)은 외부 동작의 기준이며 Python API나
내부 query 객체를 복제할 의무는 아니다. 실행 버전·module hash·원 byte와 차이는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 둔다.
Django 출처와 BSD-3-Clause는 [SOURCES](../SOURCES.md)를 따른다.

## 선택한 경계

`ResultGrouped`는 1..32개 scalar 키와 1..64개 aggregate를 명시한다. COUNT(*)·COUNT(field)·
COUNT(DISTINCT field)·MIN/MAX와 aggregate별 WHERE 조건을 표현한다. 그룹의 WHERE는 원본 행을,
HAVING은 선택한 키/aggregate만 참조한다. HAVING 참조는 같은 표현식의 구조적 identity로 검증하며
같은 source field라도 aggregate 함수·distinct·조건이 다르면 다른 참조다. SQL alias를 public metadata로 만들지 않는다.

Count는 int64, MIN/MAX는 명시적 Optional을 반환한다. NULL 키는 한 그룹으로 유지하며 Count(field)는
NULL을 제외한다. 조건부 COUNT는 필드를 명시한다. 빈 원본의 그룹 결과는 0행이며 그룹 Count는 0이다.
키와 집계 값의 비교는 같은 scalar kind만 허용한다. Boolean 산술·암묵 수치 변환을 추가하지 않는다.

Typed `GroupBy(source, Projection, Aggregate, builder)`와 dynamic `GroupValues`는 같은 AST·scanner를 사용한다.
생성 facade는 `Group<Model>By`와 Query의 `GroupValues`를 제공한다. Dynamic 입력의 이름은 source의 보존된
metadata에서 해석하며, 결과의 동적 HAVING/정렬은 선택한 cell 위치를 같은 AST 참조로 바꾼다.
관계 이름은 generic `GroupValuesIn`에 전달된 명시적 BoundModel로 해석한다. 생성 facade가 그 binding을
소유하며 원본 descriptor·metadata와의 일치를 먼저 검증한다. Eager materialization을 binding의 대용으로 쓰지 않는다.
Field/aggregate·다른 모델의 조합은 typed API 또는 metadata preflight에서 거부한다. Raw SQL 입력은 받지 않는다.

그룹 key/COUNT(field)의 finite forward 값과 원본 relation filter를 허용한다. 원본 collection join이 행을
늘리는 multiplicity와 negated collection의 EXISTS 의미는 기존 query compiler를 유지한다. 조건부 aggregate의
forward 경로는 전체 그룹을 제거하는 WHERE가 아니므로 그 자체로 optional join을 inner join으로 승격하지 않는다.
Collection을 key·aggregate operand/조건으로 사용하는 추가 범위와 related MIN/MAX의 public typed 표면은 후속 범위다.

일반 scalar codec의 동등/정렬·scan 의미를 재사용한다. JSON key/path의 동등 관계와 aggregate operand는
별도 계약이 필요하므로 아직 명시적으로 거부한다. SUM/AVG·임의 annotation·계산식·subquery/window는 후속 범위다.
새 filtered/related aggregate를 기존 terminal `AggregateInto`에 사용할 때 source slice/distinct/eager 조합도
아직 지원하지 않는다. 기존 unfiltered root aggregate의 지원 범위를 축소하지 않는다.

## 복사·실행과 페이지

Builder는 순수 함수다. 그룹 query의 복사본은 성공한 내부 scalar cell cache를 공유하며 파생 query/Fresh는
별도 cache를 갖는다. 사용자 DTO는 cache에 보관하지 않고 매번 새 nullable 입력과 builder로 만들어 반환한다.
원본 모델 QuerySet의 warm cache를 집계 원본으로 재사용하지 않는다. Context와 session을 읽기 전후·
cache/DTO 게시 전에 검사하고 실패한 rows/scan/close 결과나 부분 목록을 성공 cache로 게시하지 않는다.

일반 Count는 현재 그룹 slice 안의 그룹 수다. `Page(ctx, limit, offset)`는 HAVING 이후 전체 그룹 수와
페이지 행을 한 SQL 문장에서 읽는다. 그룹 CTE와 total의 outer join을 사용해 빈 페이지에도 정확한 total을
보존하며, NULL 키와 빈 페이지를 구분하는 presence cell은 backend/ORM 내부에만 둔다. 페이지 행은
전체-query cache를 채우지 않는다. 명시된 정렬 뒤 빠진 그룹 키를 NULLS LAST tie breaker로 붙인다.

Compiler는 그룹 완료 후 derived relation을 필터해 HAVING 의미를 구현한다. Aggregate filter의 SQL·
parameter를 반복하지 않으며 backend별 identifier·schema·물리 값·parameter 변환은 각 compiler가 소유한다.
SQLite의 root/관계 table은 main schema로 한정해 내부 CTE가 같은 이름의 실제 table을 가리지 않는다.
Predicate별 깊이/노드, 전체 group predicate 4096 nodes, backend parameter와 statement 한도를 사전 검증한다.

## 관찰한 차이와 명시적 거부

Django의 비키 source ordering은 추가 GROUP BY 항목이 되어 같은 표시 키를 여러 행으로 나눌 수 있다.
GoDj는 비키 순서를 먼저 명시적으로 비우도록 요구한다. 키 순서는 그룹 순서로 옮긴다. 이미 sliced된 원본,
eager/prefetch/row-lock source는 거부한다. Group slice 뒤 HAVING/OrderBy도 거부한다.
일반 ordering의 NULL 위치는 backend native 값을 유지하고 명시적 NullsFirst/NullsLast를 제공한다.
PostgreSQL은 transaction 안에서도 grouped FOR UPDATE를 거부하고 SQLite는 이를 무시했으므로 GoDj는
양 backend에서 그룹과 행 잠금의 결합을 사전에 거부한다.

HAVING의 aggregate 비교·NOT는 SQL의 3값 논리를 유지한다. NULL aggregate를 비교한 NOT는 NULL 그룹을
자동 포함하지 않는다. Nullable key의 부정은 기존 field predicate와 같은 NULL 보호를 사용한다.
전체 catalog의 annotation/group/having 영역이나 Django의 모든 aggregate 조합을 이 수직 단면으로 완료 처리하지 않는다.
