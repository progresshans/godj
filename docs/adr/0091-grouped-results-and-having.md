# ADR-0091: 그룹 결과와 집계 참조

- 상태: Accepted design — 그룹 기반 구현; 환경별 검증과 업무 연결은 TEST_EVIDENCE/활성 work가 소유
- 날짜: 2026-10-15
- 작업: [GDJ-0112](../../work/0112-grouped-aggregation-and-ticket-summary.md), [GDJ-0121](../../work/0121-numeric-aggregates-and-ticket-metrics.md), [GDJ-0122](../../work/0122-computed-results-and-priority-preview.md), [GDJ-0123](../../work/0123-derived-results-and-ticket-ratios.md), [GDJ-0124](../../work/0124-related-expressions-and-report-status.md), [GDJ-0125](../../work/0125-collection-aggregates-and-label-usage.md)

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

그룹 key/COUNT(field)의 finite single-valued 값과 원본 relation filter를 허용한다. Forward FK/OneToOne과
reverse OneToOne의 혼합 경로를 같은 값 AST로 표현한다. 원본 collection join이 행을
늘리는 multiplicity와 negated collection의 EXISTS 의미는 기존 query compiler를 유지한다. 조건부 aggregate의
single-valued 경로는 전체 그룹을 제거하는 WHERE가 아니므로 그 자체로 optional join을 inner join으로 승격하지 않는다.
GDJ-0125에서 collection field aggregate와 조건을, GDJ-0127에서 집계 안의 collection scalar/CASE operand를
아래 계약으로 확장한다. Collection group key와 일반 행 projection의 범위는 넓히지 않는다.
이 설계 채택과 구현·검증 상태는 구분한다.

일반 scalar codec의 동등/정렬·scan 의미를 재사용한다. JSON key/path의 동등 관계와 aggregate operand는
별도 계약이 필요하므로 아직 명시적으로 거부한다. 같은 source와 single-valued 관계의 scalar tree는 계산된 키와 COUNT/MIN/MAX operand로,
int64/float64 산술·CASE와 GDJ-0126의 Decimal 계산식은 SUM/AVG operand로 사용할 수 있다. 원본 source의 metadata와 결과 domain을 분리한다.
Typed 숫자 결과는 Optional이고 dynamic 결과는 NULL tag를 가진 query.Value다. Decimal AVG는 정확한 Decimal이다.
GDJ-0128의 computed Duration SUM/AVG는 아래 native 정밀도 계약을 따른다.
일반 named annotation·subquery/window는 후속 범위다. 집계 뒤 명시적 결과 stage는 아래 경계를 따른다.
새 filtered/related aggregate를 기존 terminal `AggregateInto`에 사용할 때 source slice/distinct/eager 조합도
아직 지원하지 않는다. 기존 unfiltered root aggregate의 지원 범위를 축소하지 않는다.
GDJ-0124에서 relation-filtered root MIN/MAX도 SUM/AVG와 같은 grouping compiler로 연결한다.
GDJ-0124의 related 숫자 MIN/MAX는 `Numeric(related)`, dynamic ordered 값은 `DynamicScalar.Min/Max`를 사용한다.
GDJ-0125는 직접 related field의 MIN/MAX와 dynamic aggregate field 경로도 같은 집계 AST에 연결한다.
관계 filter의 multiplicity를 숨기지 않으며 단일 COUNT(*)의 기존 slice/distinct 경로는 별도로 유지한다.

## 컬렉션 필드 집계

라벨을 원본으로 연결 티켓의 지표를 읽을 때 빈 관계도 하나의 라벨 그룹으로 남긴다. 집계 전용 constructor와
sealed typed capability가 finite reverse FK·ManyToMany를 포함한 필드 경로를 받는다. 행을 선택하는 scalar와
group key의 cardinality 검증을 집계에 재사용하지 않는다. Collection field를 일반 projection·그룹 키·행 정렬·
computed WHERE·UPDATE 값으로 실행하는 것은 허용하지 않는다. Root/target metadata·경로 연결·선택 결과 scope는 유지한다.

COUNT(field)는 NULL 제외·empty 0, MIN/MAX/SUM/AVG는 empty/all-NULL에서 NULL이다. 기존 ordered scalar와
숫자 kind·physical operand 검증·Decimal precision·Duration 반올림·typed Optional을 그대로 사용한다.
Decimal MIN/MAX도 각 기여 입력의 physical kind·canonical 값·선언 precision을 검사한다. 최종 극값만 검사해
다른 잘못된 저장값을 숨기지 않는다. Aggregate FILTER에서 제외한 행은 기여 입력이 아니며 검사하지 않는다.
여러 collection JOIN은 SQL의 행 곱을 만든다. COUNT/SUM/AVG의 DISTINCT는 operand 값에 대한 명시적 선택이며,
다른 행이 같은 값을 가졌다는 이유로 자동 dedup하지 않는다. 중복 through 행도 실제 입력 행이다.

모델 query의 `Distinct()` 뒤에 그룹화해도 집계 전 JOIN 행을 먼저 줄이지 않는다. 고정 Django의
`distinct().values(...).annotate(...)`처럼 결과 행의 중복 제거이며, 모든 그룹 키를 선택한 결과는 이미 고유하다.
원본 JOIN의 행 곱과 집계 값은 유지한다. 먼저 선택·중복 제거한 결과를 집계하려면 명시적 결과 단계를 사용한다.
이 의미는 아직 거부하는 filtered/related 전역 aggregate의 distinct source와 구분한다.

고정 Django의 [JOIN 재사용](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/sql/query.py#L1060-L1085)과
[표현식 조건 해석](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/query_utils.py#L92-L106)을 기준으로,
집계는 원본 Filter가 이미 materialize한 같은 collection 경로의 가장 최근 JOIN을 재사용한다.
재사용 가능한 JOIN이 없으면 관계를 outer JOIN으로 추가한다. 독립 Filter가 만든 여러 JOIN의 배수는 보존한다.
공통 compiler의 집계용 경로 해석은 원본 WHERE와 구분하며 immutable 결과 표현식의 identity를 다시 쓰지 않는다.
집계 FILTER의 NOT는 해당 joined row와 NULL 보호를 사용하고, 원본 collection 배제의 correlated EXISTS로 바꾸지 않는다.
같은 aggregate를 HAVING·order·명시적 다음 결과 stage에서 참조해도 선택 identity와 decoder가 유지돼야 한다.

Typed `Count/Sum/Avg/Min/Max(relatedField)`와 project-bound dynamic group aggregate가 같은 의미를 사용한다.
관계 이름은 sealed project binding에서 해석한다. 일반 field·scalar의 Count capability도 동일 결과를 반환한다.
원본 모델·관련 leaf·selected-result 열의 provenance와 context/cache/rows 정리는 기존 결과 query가 소유한다.
양 DB의 독립 관찰과 generated consumer에서 빈 관계·nullable leaf·중복·조건·연속 Filter·복합 경로를 대조한 뒤
검증 범위를 기록한다. 설계 문서만으로 현재 지원이나 Django parity를 주장하지 않는다.

## 컬렉션 계산식 집계

[GDJ-0127](../../work/0127-collection-computed-aggregates.md)은 이미 지원한 숫자 산술·CASE의 operand를 finite collection으로 확장한다.
표현식 구성과 사용 문맥을 나눈다. `RelatedExpression`은 불변 경로를 보존하고 `ScalarAggregateResult`가 집계를 완성한다.
`ScalarResult`·row source 검증은 collection을 거부한다. CASE의 조건에만 숨은 collection도 같은 경계를 적용한다.
원본 행의 필드·관계와 명시적 결과 stage의 cell은 별도로 bind하며, 결과 stage 없이 중첩 집계를 만들지 않는다.
별도 계산 AST나 source metadata를 발명하지 않으며 resource·kind·precision 검증도 공통 경로를 사용한다.

값·CASE 조건·aggregate FILTER의 모든 collection 경로는 위의 최신 원본 JOIN occurrence 해석을 함께 사용한다.
동일 경로의 두 leaf는 같은 관련 행을 참조하고, 다른 collection의 JOIN 행 곱은 유지한다.
CASE나 FILTER의 참 여부로 optional JOIN을 승격하지 않는다. 집계 표현식에 다른 Filter가 미리 묶은 occurrence를
넣을 수 없다. Compiler는 행 WHERE를 만드는 우회 없이 실제 plan authority에서 집계 조건을 검사하고 렌더링한다.
NOT의 field NULL 보정과 computed comparison의 SQL 3값 의미는 기존 차이를 유지한다.

SUM은 각 joined row의 값을 먼저 계산하고 집계한다. DISTINCT는 계산된 값에 적용한다. 빈 collection의 LEFT JOIN
행에서 계산 결과가 NULL이면 제외하고, CASE가 명시적 fallback 값을 만들면 그 값은 실제 기여 값이다.
FILTER에서 제외된 행과 선택되지 않은 CASE branch는 먼저 계산하지 않는다. 기여한 Decimal leaf의 손상은
0 곱셈이나 후속 집계로 숨기지 않는다. Backend의 division·remainder·반올림 차이는 기존 산술 계약을 따른다.

Typed `Numeric`·`Decimal`과 `Count`는 같은 collection scalar를 집계한다. 행용 dynamic `BindExpression`의 경계는
유지하고 `BindAggregateExpression[In]`과 생성 Query의 `AggregateExpression`이 완료된 집계를 반환한다.
`DynamicAggregateInput.Expression`도 이 경로를 사용한다. `Predicate`는 집계 문맥의 computed FILTER이고
기존 `Filter` field lookup들과 AND로 결합한다. Dynamic 관계 이름은 sealed project binding을 요구한다.
이 API는 생성물과 함께 관리한다. 설계 채택은 실행 PASS가 아니며 현재 구현·검증은 활성 work와 TEST_EVIDENCE로 구분한다.

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
원본 field와 exact operand의 SUM/MIN/MAX scanner가 float를 받도록 넓히지 않는다. 최대 int64 microseconds의 단일 평균이 native
binary64 반올림으로 `2^63`이 되어도 유효한 Duration으로 반환한다. PostgreSQL은 native INTERVAL 집계를 사용한다.
HAVING은 각 DB의 native 결과를 비교한다. SQLite의 평균 `1.5µs`는 반환 시 `2µs`지만 `>=2µs` HAVING에는
포함되지 않고 PostgreSQL의 native INTERVAL 평균은 포함된다. 이 실제 Django 차이를 SQL 안의 추가 반올림으로 지우지 않는다.
SQLite의 Duration literal/storage int64 제한과 전역 모델/result day 범위를 구분한다.

GDJ-0128은 computed Duration의 SUM/AVG·MIN/MAX에도 operand의 평균 출처를 보존한다. SQLite AVG에서 유래한
fractional microseconds는 산술·CASE·부호 반전·재집계에서 유지하고 최종 Duration scanner에서 반올림한다.
CASE가 exact branch를 선택하면 원 int64 연산의 정확성·overflow 검사를 유지한다. DateTime 결과로 바뀌는 경계의
half-even과 UTC 이동은 [ADR-0090](0090-query-update-and-scalar-expressions.md)이 소유한다.
기여 temporal operand와 완료된 집계의 모델 범위를 statement 안에서 검사하며 NULL·상쇄·후속 HAVING으로 숨기지 않는다.
FILTER 제외 행은 검사 대상에 넣지 않는다. PostgreSQL의 guard가 literal 집계를 다른 SELECT의 행에 귀속시키지 않도록
집계의 원 query level을 유지한다. 이 확장의 실행 완료 여부는 GDJ-0128과 TEST_EVIDENCE를 따른다.
PostgreSQL은 각 operand의 month 부재와 정규화된 모델 day 범위를 확인한다. 서로 취소되는 외부 month 값도
정상 합계로 숨기지 않고 native statement에서 거부한다.

Decimal의 exact 합계·평균 precision과 source/result 범위는 [ADR-0069](0069-exact-decimal-values-and-storage.md)가 소유한다.
위 설계 채택은 typed/dynamic·생성물·업무 소비자나 환경별 검증 완료를 뜻하지 않는다.

## Helpdesk 업무 요약

`TicketSummary`의 HTML과 API는 ViewTicket 인가 뒤 하나의 읽기 service를 사용한다. 현재 Category는 application이
소유하며 query로 입력받지 않는다. Category의 존재/이름과 우선순위 그룹을 같은 `ReadSnapshot` handle로 읽고
행·건수는 집계 뒤 결과 단계의 `ResultQuery.Page` 한 statement에서 가져온다. callback의 누락·반복·동시 호출·오류 누락과 cleanup/취소
실패에는 결과를 게시하지 않는다. 별도의 페이지 요청 사이에 snapshot을 유지한다고 주장하지 않는다.

Priority의 NULL·기존 int64 값도 그룹이다. IR choices는 label만 제공하고 결과를 enum으로 좁히거나 데이터를 수정하지
않는다. 전체 건수는 COUNT(*), 열린 건수는 closed=false인 ID의 조건부 COUNT다. 최소 열린 건수 HAVING과 최소 열린 비율 후속 필터 뒤 열린 건수
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

## 결과 열과 후속 조회 단계

후속 조회는 모델 FieldRef와 구분된 결과 scope를 사용한다. Scope는 선택한 scalar 결과의 순서·kind·NULL을
소유하며, 결과 참조는 해당 scope의 열 위치를 가리킨다. 정수 AVG의 Float 결과와 SUM의 NULL을 원 필드의
타입·nullable·Decimal precision으로 바꾸지 않는다. 결과를 모델 metadata에 임시 필드로 추가하지 않는다.

Scope 복사본은 같은 불변 identity를 공유한다. 같은 shape로 따로 만든 scope는 별개의 소유자이며 이름·타입·
선택 순서가 우연히 같아도 참조를 재결합하지 않는다. 이 identity는 SQL alias나 전역 번호가 아니다.
SQL compiler는 실제 조회 단계 안에서 열 위치를 결정적으로 배치한다. 참조가 가리키는 값은 현재 입력 단계의
실행 결과이며, 이전에 평가한 QuerySet cache나 다른 backend/session의 결과를 가져오는 handle이 아니다.

원 행 필드와 후속 결과 참조는 각각 자기 source에서 검증한다. 선택된 값을 UPDATE나 원 행 WHERE에 넣거나,
선택 없이 원 행 필드를 후속 단계로 가져오는 것을 거부한다. CASE의 값뿐 아니라 조건에 숨은 참조도 같은 규칙을
적용한다. Scope chain에는 scalar tree 깊이와 별개인 32단계 상한을 둔다. 산술·부호 반전·CASE 조건·집계 filter도
선행 단계 깊이를 보존하므로 표현식의 모양을 바꾸어 한도를 우회할 수 없다.

새 조회 단계는 입력 plan의 필터·집계·DISTINCT·정렬·slice를 불변 snapshot으로 보존한다. 후속 조건과 계산은
해당 입력의 결과 열만 참조한다. 상수 출력이나 COUNT(*)로 열 참조가 사라져도 물리 단계 깊이를 줄이지 않는다.
전체 입력 chain에는 65,536개 node 한도를 추가한다. 행 잠금은 결과 단계로 암묵 전달하지 않고 명시적으로 거부한다.
정렬·slice·DISTINCT가 있는 결과를 다시 집계할 때는 새로운 입력 단계를 만들어 그 순서를 보존한다.

Compiler는 CTE의 열 목록으로 실제 결과 위치를 명명한다. 입력 SQL을 앞에 배치해 SQLite의 positional parameter와
PostgreSQL의 numbered parameter가 같은 논리적 순서를 갖도록 한다. CTE/그룹 페이지 이름은 입력 SQL에서 이미
사용한 이름을 피하고 case-insensitive table 이름도 고려한다. 원 모델 table 이름을 바꿔 충돌을 숨기지 않는다.
오류 분류에 필요한 물리 source는 보존된 입력 plan에서 찾는다.

`SelectQuery`/`AggregateQuery`는 projection과 한 행 aggregate를 지연 조회로 보존한다. 생성 facade도
`Select<Model>Query`/`Aggregate<Model>Query`를 제공한다. `Derive`는 이 query 또는 `GroupedQuery`의 plan을
새 입력 scope로 고정한다. `SelectedField`/`SelectedAggregate`는 입력에서 실제 선택한 표현식을 찾아 같은 Go
cell decoder와 결과 domain을 유지한다. 결과의 모델 타입 표지는 `ResultOf[M]`이며 다음 단계마다 중첩된다.
같은 타입의 별도 scope를 혼동하는 경우도 runtime preflight에서 거부한다. Dynamic `DynamicColumn(index)`와
결과 source의 `Expression`/`Predicate`/`Project`는 같은 AST를 만들며 원 모델 이름을 이 단계에 다시 바인딩하지 않는다.

`SelectResults`/`AggregateResults`/`GroupResults`는 해당 scope를 읽는다. `OptionalOperand`/`NullableOperand`는
Go 입력 wrapper를 벗겨 산술·CASE에 연결하되 SQL NULL을 변환하거나 채우지 않는다. 원 projection의 nullable pointer,
aggregate의 Optional과 computed Optional은 선택 단계 후에도 같은 decoder를 유지한다. 결과 열은 물리 행 UPDATE,
DELETE나 잠금 API를 제공하지 않는다. Filter/OrderBy/Distinct를 기존 slice 뒤로 옮기려면 명시적으로 다시 Derive한다.
Aggregate의 한 행을 필터·slice할 때도 같은 경계를 요구한다.

`CombineProjections`/`CombineAggregates`는 선택 목록과 순수 builder를 합친다. 고정 arity를 늘리지 않고 넓은 DTO를
조립하며 공통 selection 예산과 중복/타입 검증을 유지한다. Lazy result와 group은 완전히 읽고 닫은 private cell만
cache한다. DTO는 게시할 때 다시 만들고 nullable 값의 메모리를 caller별로 소유한다. 원 QuerySet cache나 앞 단계의
DTO builder를 새 stage 입력으로 재사용하지 않는다. Context/session·scan/iteration/close 실패에는 부분 결과를 게시하지 않는다.

`ResultQuery.Page`는 입력을 한 번 materialize한 CTE에서 전체 수와 페이지를 만든다. Presence envelope와
`ResultPage[R]`를 group page와 공유한다. 원 selected 순서·multiplicity·DISTINCT를 유지하며 NULL과 빈 페이지를 구분한다.
명시 정렬 다음의 tie는 선택 cell로 채운다. Non-distinct projection의 미선택 정렬 값은 private 입력 열로만 추가한다.
DISTINCT/group 결과에 미선택 정렬을 몰래 추가하거나 정렬 열을 공개 DTO에 끼워 넣지 않는다. 이미 slice된 결과는
새 Derive 단계 후 Page할 수 있다. Limit 0도 전체 수를 읽고 Page는 All cache를 채우지 않는다.

정적으로 빈 projection/group은 다음 단계의 빈 원본이다. 빈 원본의 aggregate는 COUNT 0/나머지 NULL 한 행이므로
다음 단계에서도 그 한 행을 보존한다. Backend는 전체 원 plan을 검증한 뒤 빈 입력을 같은 scalar 종류의 합성 SQL로
바꾸며, 접힌 빈 원본의 쓰지 않는 산술이나 table I/O를 되살리지 않는다. 실제 I/O가 필요한 빈 결과는 DB가 실행한다.
Duration AVG의 SQLite fractional microseconds는 결과 참조·CASE·후속 비교와 GDJ-0128의 산술·재집계에서도 유지하고
최종 Duration scanner에서 half-even 반올림한다. 일반 Duration field와 exact SUM의 int64 storage 검증을 넓히지 않는다.

GDJ-0123 Helpdesk는 전체·열린 건수에서 닫힌 건수와 `open_basis_points = (10000 * open) / total`을 DB에서 계산한다.
총수가 양수인 CASE branch에서 계산하며 정수 산술의 중간 overflow 오류를 유지한다. API는 required int64
`closed`/`open_basis_points`와 적용한 `min_open_basis_points`를 반환하고 HTML은 100 basis points를 1%로 표시한다.
필터는 0..10000의 canonical 정수이며 같은 snapshot 안에서 비율 조건 후 Page한다. Application은 반환된 NULL/건수/
범위와 비율 정책을 확인하고 오류 시 전체 응답을 거부한다. 별도 Go 재계산을 DB 실패의 fallback으로 사용하지 않는다.
원 Ticket·link·audit는 변경하지 않는다.

단계별 compiler·공개 ORM·소비자의 구현과 검증 상태는 [CURRENT](../status/CURRENT.md)와
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)를 따른다.

## Helpdesk 라벨 사용 현황

GDJ-0125는 Label을 root로 묶어 빈 관계를 보존한다. Count·조건부 open Count·비용 Sum·노력 Avg·시간 Sum에 각각
현재 Category의 Ticket 조건을 적용한다. 원본 Label의 Category 조건만으로 외부 연결의 존재나 수치를 공개하지 않는다.
GDJ-0127은 기존 비용 합계와 함께 각 present 비용에 `factor`(6/4, 기본 1)·`delta`(14/2, 기본 0)를 적용한
조정 합계를 제공한다. 이 집계에도 같은 Category FILTER를 적용한다. 비용이 없는 티켓에는 가감액을 적용하지 않고,
배율이 0이어도 NULL은 0이 되지 않는다. 음수·0 배율을 허용하며 각 행의 정확한 Decimal 결과를 합산한다.
HTML과 API는 같은 query codec과 snapshot을 사용하고 페이지 링크는 배율·가감액·usage를 보존한다.
원래 비용·조정 비용의 NULL 여부가 다르면 결과를 게시하지 않는다. 저장 값이나 audit를 바꾸지 않는다.
사용/미사용은 scoped Count의 HAVING이며 페이지 총수도 같은 필터 이후의 라벨 수다.
두 view 권한·query 검증은 snapshot 시작 전에, Category와 group Page는 한 snapshot에서 처리한다.
TicketSummary·ServiceReportStatus·LabelUsage는 같은 callback/cleanup 소유권 검사를 사용하며,
누락·반복·동시·늦거나 미완료인 callback과 오류 삼킴·취소·cursor/출력 실패 때 결과를 게시하지 않는다.
