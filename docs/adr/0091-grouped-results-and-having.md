# ADR-0091: 그룹 결과와 집계 참조

- 상태: Accepted design — 그룹 기반 구현; 환경별 검증과 업무 연결은 TEST_EVIDENCE/활성 work가 소유
- 날짜: 2026-10-11
- 작업: [GDJ-0112](../../work/0112-grouped-aggregation-and-ticket-summary.md), [GDJ-0121](../../work/0121-numeric-aggregates-and-ticket-metrics.md), [GDJ-0122](../../work/0122-computed-results-and-priority-preview.md)

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
COUNT(DISTINCT field)·MIN/MAX·SUM/AVG와 aggregate별 WHERE 조건을 표현한다. 그룹의 WHERE는 원본 행을,
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
별도 계약이 필요하므로 아직 명시적으로 거부한다. 같은 행의 scalar tree는 계산된 키와 COUNT/MIN/MAX operand로,
int64/float64 산술·CASE는 SUM/AVG operand로 사용할 수 있다. 원본 source의 metadata와 결과 domain을 분리한다.
Typed 숫자 결과는 Optional이고 dynamic 결과는 NULL tag를 가진 query.Value다. 계산된 Decimal/Duration의 SUM/AVG,
일반 named annotation·집계 뒤 추가 stage·subquery/window는 후속 범위다.
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

Compiler는 그룹 완료 후 derived relation을 필터해 HAVING 의미를 구현한다. Group key는 SELECT ordinal로 참조하여
계산식 parameter가 GROUP BY에서 다른 값으로 재생성되지 않게 한다. Backend별 identifier·schema·물리 값·parameter
변환은 각 compiler가 소유한다. SQLite Float SUM/AVG의 NULL/NaN guard는 같은 operand/filter를 두 번 사용하므로
그 parameter도 SQL 순서대로 반복한다. HAVING/Page가 원본 표현식을 다시 평가하는 구조로 바꾸지 않는다.
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

## 숫자 합계와 평균

GDJ-0121은 정수·Float·Decimal·Duration의 root와 finite forward operand를 SUM/AVG로 연결한다.
Nullable 여부와 무관하게 결과는 Optional이다. SUM은 원본 scalar 종류를, AVG는 정수만 Float로 바꾸고
나머지는 같은 종류를 반환한다. `ResultValueKind`와 `ResultNullable`은 원본 FieldRef·nullable·Decimal precision과
분리된 결과 의미다. Typed sealed capability와 dynamic metadata 해석이 같은 AST를 만들고 HAVING도 이 결과 종류를 사용한다.
SUM/AVG의 DISTINCT와 개별 filter를 허용하며 empty·모두 NULL은 NULL이다. 접힌 empty는 기존 검증 뒤 I/O 없이 같은 값을 반환한다.

정수 합계의 공개 결과는 int64다. SQLite의 native integer sum은 중간 overflow도 오류이며 PostgreSQL의 더 넓은
중간 합계는 최종 bigint cast에서 범위를 검사한다. PostgreSQL의 정수 AVG는 double precision으로 명시적으로 반환한다.
어느 backend도 overflow를 float로 바꾸거나 wrap하지 않는다. 기존 root scalar aggregate의 slice/distinct source 경로를
SUM/AVG에도 유지한다. Filtered/related aggregate의 추가 source 조합 제한은 위 구현 경계와 같다.

Float는 [ADR-0068](0068-binary64-field-and-finite-json-boundaries.md)의 native binary64·Infinity/NaN 경계를 유지한다.
SQLite는 numeric storage class만 operand로 받고 native SUM/AVG를 사용한다. 결과 NULL과 비NULL operand count를 함께
검사해 NaN 산술 결과를 empty로 게시하지 않는다. PostgreSQL의 NaN/Infinity와 finite overflow는 native 의미를 유지한다.
서로 다른 합산 순서·보상 합산·overflow의 backend 차이를 같은 결과라고 주장하지 않는다.

Duration은 microsecond 결과와 half-even 평균을 사용한다. SQLite는 signed int64 operand와 native SUM/AVG를 사용하고,
AVG의 binary64 결과만 전용 result scanner에서 half-even으로 반올림해 전체 모델 day 범위를 검사한다.
원본 field와 SUM/MIN/MAX scanner가 float를 받도록 넓히지 않는다. 최대 int64 microseconds의 단일 평균이 native
binary64 반올림으로 `2^63`이 되어도 유효한 Duration으로 반환한다. PostgreSQL은 native INTERVAL 집계를 사용한다.
HAVING은 각 DB의 native 결과를 비교한다. SQLite의 평균 `1.5µs`는 반환 시 `2µs`지만 `>=2µs` HAVING에는
포함되지 않고 PostgreSQL의 native INTERVAL 평균은 포함된다. 이 실제 Django 차이를 SQL 안의 추가 반올림으로 지우지 않는다.
SQLite의 Duration literal/storage int64 제한과 전역 모델/result day 범위를 구분한다.
PostgreSQL은 각 operand의 month 부재와 정규화된 모델 day 범위를 확인한다. 서로 취소되는 외부 month 값도
정상 합계로 숨기지 않고 native statement에서 거부한다.

Decimal의 exact 합계·평균 precision과 source/result 범위는 [ADR-0069](0069-exact-decimal-values-and-storage.md)가 소유한다.
위 설계 채택은 typed/dynamic·생성물·업무 소비자나 환경별 검증 완료를 뜻하지 않는다.

## Helpdesk 업무 요약

`TicketSummary`의 HTML과 API는 ViewTicket 인가 뒤 하나의 읽기 service를 사용한다. 현재 Category는 application이
소유하며 query로 입력받지 않는다. Category의 존재/이름과 우선순위 그룹을 같은 `ReadSnapshot` handle로 읽고
행·건수는 `GroupedQuery.Page` 한 statement에서 가져온다. callback의 누락·반복·동시 호출·오류 누락과 cleanup/취소
실패에는 결과를 게시하지 않는다. 별도의 페이지 요청 사이에 snapshot을 유지한다고 주장하지 않는다.

Priority의 NULL·기존 int64 값도 그룹이다. IR choices는 label만 제공하고 결과를 enum으로 좁히거나 데이터를 수정하지
않는다. 전체 건수는 COUNT(*), 열린 건수는 closed=false인 ID의 조건부 COUNT다. 최소 열린 건수 HAVING 뒤 열린 건수
내림차순·priority 내림차순(NULL 마지막)으로 20개씩 조회한다. 끝을 지난 페이지도 필터 뒤 전체 그룹 수를 유지한다.
비용 합계·노력 평균·경과 시간 합계는 열린/닫힌 행을 모두 포함한 같은 그룹의 비NULL 값으로 계산한다.
모두 NULL이면 API는 명시적 null, HTML은 Not set으로 표시하며 실제 0과 구분한다. Decimal 합계는 원본
fixed scale/precision으로 좁히지 않는 exact canonical 문자열이다. Duration은 정규 모델 문자열, 노력 평균은
finite JSON/HTML 숫자다. 산술·scan·출력 오류와 NaN/Infinity에는 전체 응답을 거부하며 부분 지표를 게시하지 않는다.
Query 한도/정규 decimal 입력과 API schema는 실제 handler와 독립 generated client로 대조한다. 조회는 ticket 본문·
라벨·digest를 로드하거나 고치지 않고 audit를 기록하지 않는다. HTML은 별도의 쓰기/audit 구성을 요구하지 않는다.

GDJ-0122는 원 priority와 CASE로 계산한 한 번 상승 뒤 priority를 같은 group key로 읽는다. NULL→Normal,
Low→Normal, Normal→Urgent이며 Urgent와 legacy int64 값은 보존한다. 원 priority가 키에 남으므로 서로 다른 현재
그룹이 같은 예정값으로 합쳐지지 않는다. SQL 결과가 현재 업무 정책과 일치하고 non-null인지 확인한 뒤
`raise_to_priority`·`raise_to_label`·`would_change`를 API와 HTML의 After raise 열에 게시한다.
이 값은 해당 읽기 snapshot의 미리보기다. 나중의 실제 명령은 자기 transaction의 현재 행으로 정책을 다시 적용한다.
