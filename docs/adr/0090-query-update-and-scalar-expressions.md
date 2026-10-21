# ADR-0090: QuerySet 갱신과 scalar 표현식

- 상태: Accepted design — 공통 기반 구현; 환경별 검증과 업무 연결은 TEST_EVIDENCE/활성 work가 소유
- 날짜: 2026-10-13
- 작업: [GDJ-0111](../../work/0111-query-update-and-writable-expressions.md), [GDJ-0122](../../work/0122-computed-results-and-priority-preview.md), [GDJ-0123](../../work/0123-derived-results-and-ticket-ratios.md), [GDJ-0124](../../work/0124-related-expressions-and-report-status.md), [GDJ-0128](../../work/0128-temporal-expressions-and-deadline-preview.md), [GDJ-0129](../../work/0129-calendar-extraction-and-deadline-groups.md)

## 입력과 공개 API

`QuerySet.Update(ctx, assignments...)`는 원 query의 조건에 맞는 행에 같은 assignment 집합을 한 native UPDATE로
적용하고 matched-row count를 반환한다. 이미 같은 값을 가진 행도 센다. 없는 행을 생성하거나 행별 Save/Clean을
호출하지 않으며, choices·Form 검증·application audit는 consumer가 소유한다. Primary key도 명시적으로 할당할 수
있지만 실제 고유성/FK 제약을 통과해야 한다. 실패 결과에는 부분 count를 게시하지 않는다.

`Assign(field, value)`, `AssignExpression(field, F(field))`, `AssignNull(nullableField)`가 typed 입력이다.
`Value`, `NullValue`, `Add/Subtract/Multiply/Divide`와 두 operand를 받는 `...Expressions`, 정수 `Remainder`,
`Negate`로 현재 행의 값을 조합한다. Model과 값 타입은 sealed generic capability로 연결한다. FK의
`ForeignKeyField`/`NullableForeignKeyField`도 원 모델의 concrete 정수 key 열을 나타내며 관계 join이나 관련 객체가 아니다.
생성 `ModelFields.<FK GoName>`은 같은 metadata의 읽기·field reference·명시적 write mask를 제공한다.

`UpdateDynamic(ctx, DynamicUpdateInput{Field: "amount", Value: DynamicF("amount").Add(int64(1))})`도 같은
Schema IR metadata와 scalar AST로 준비한다. 동적 이름은 schema의 concrete field name이다. Go 필드 이름이나
관계 traversal을 UPDATE 값이나 SQL 문자열로 해석하지 않는다. 읽기의 관계 이름 binding은 아래 별도 계약을 따른다.
`DynamicValue`는 닫힌 scalar 입력을 즉시 소유하며 임의 객체,
aggregate, mutable raw container와 알 수 없는 이름은 I/O 전에 거부한다.

Manager와 생성 root/eager/prefetch facade는 같은 연산을 제공한다. 기존의 한 모델·PatchInput 저장 메서드는
`Manager.Patch`로 이름을 분리한다. 미배포 내부 API이므로 이전 Update signature를 유지하는 overload/호환 layer를
만들지 않는다. 모델 Save/update_fields와 BulkUpdate에 expression을 넣는 별도 표면은 아직 제공하지 않는다.

## 불변 AST와 수치 의미

`ScalarExpression`은 literal, source field, finite related field, selected result, binary arithmetic, negate, CASE의 private 불변 tree다. 관계의 행/집계 cardinality는 사용 문맥에서 검증한다. Boolean 조건 AST와
별도 값 표현식이지만 typed/dynamic 경로마다 두 구현을 만들지 않는다. `ScalarAssignment`가 결과 kind를 target에
결합하고 `QueryUpdatePlan`이 원 predicate·primary key·전체 source metadata와 assignment를 소유한다.
깊이 64, assignment를 합친 전체 scalar node 1024, concrete source metadata와 backend parameter 예산을 검증한다.
중복 target, 다른 모델/열 metadata, 빈 expression과 slice/window는 빈 assignment·빈 query에서도 거부한다.

상수와 같은 kind의 field 복사는 현재 scalar codec 전체에 적용한다. 숫자 연산은 같은 kind의 int64 또는 float64다.
문자열·Boolean·정수/float 사이의 암묵 변환을 하지 않는다. Integer 나눗셈은 native truncate-toward-zero,
나머지는 왼쪽 값의 부호를 따른다. NULL은 전파되고 untyped NULL은 상대 operand의 숫자 kind를 따른다.
Bare NULL의 직접 대입은 target의 kind를 가진 새 scalar로 정규화한다. Typed/dynamic 대입은 같은 AST를 쓰며
공유한 원 NULL은 바뀌지 않는다. 이미 kind를 선언한 NULL은 다른 target kind로 암묵 변환하지 않는다.
Non-null target의 NULL literal은 preflight에서 거부하지만 nullable field/expression을 대입할 때 실제 NULL인 행은
DB 제약으로 전체 statement가 실패한다. Float의 특수값은 [ADR-0068](0068-binary64-field-and-finite-json-boundaries.md)의 모델 범위를 따른다.

SQLite는 overflow한 정수 연산을 REAL로 승격할 수 있다. 각 integer field/연산 결과를 statement 내부의
stateless `godj_int64` 함수로 검사하여 중간값 손실과 affinity의 재반올림을 거부한다. 이 함수는 행을 Go로 읽어
계산하는 대체 구현이 아니며 SQL native 연산 뒤 storage class를 검사한다. 오류는 UPDATE 전체를 중단한다.
PostgreSQL은 native bigint overflow/zero-division 오류와 원 SQLSTATE를 보존한다. SQLite의 zero-division NULL은
nullable 여부에 따라 저장되거나 NOT NULL 오류가 된다. Backend별로 관찰한 차이를 공통 성공으로 합치지 않는다.

Decimal 상수는 선언된 precision/scale에 정확히 맞아야 한다. Decimal field 복사는 target이 source의 선언 범위를
좁히지 않을 때만 허용한다. GDJ-0126의 Decimal 계산식은 [ADR-0069](0069-exact-decimal-values-and-storage.md)의
source·중간/result·target precision을 분리하고 양 DB의 native scale/zero division 차이를 명시한다.
최종 assignment는 정확히 맞을 때만 저장하며 float 변환이나 native 반올림으로 초과 scale을 숨기지 않는다.
DateTime/Duration 산술은 아래 GDJ-0128 계약을 따른다. 집계 뒤 계산은 ADR-0091의 명시적 결과 단계로 연결한다.

## 시각과 기간 계산

[GDJ-0128](../../work/0128-temporal-expressions-and-deadline-preview.md)은 Duration ± Duration·부호 반전,
DateTime ± Duration·Duration + DateTime·DateTime − DateTime을 같은 AST에 연결한다. 각 operand의 kind와
결과 kind를 구분하며 temporal NULL은 명시적인 타입을 요구한다. Numeric bare NULL은 상대 숫자 kind로 새 node를
만들고 원본은 보존한다. `orm.DateTime`/`orm.Duration`은 각 값 타입의 sealed capability를 제공한다.
Date/Time 이동과 Duration scaling/division은 아래 GDJ-0131·0132 계약을 따른다. 나머지 function/subquery는 미완료다.

DateTime은 UTC 연도 1..9999와 microsecond를 유지한다. SQLite의 정확한 기간 연산은 int64 microseconds 범위를
확인하며 날짜 차이에 Go `time.Duration`이나 Julian-day float를 사용하지 않는다. Stateless SQLite 함수가 statement
안에서 canonical 시각 text·기간 storage class와 중간 결과를 검사한다. PostgreSQL은 month/year 없는 유한 INTERVAL과
전체 모델 day 범위를 검사하고, 시각 이동을 UTC timestamp에서 수행해 caller의 세션 timezone/DST 설정에 의존하지 않는다.
세션 설정을 변경하지 않으며 native 원인과 SQLSTATE를 보존한다.

기간 평균의 SQLite fractional microseconds는 후속 산술·부호 반전·집계에 그대로 전달한다. 일반 field는 REAL을 받지
않는다. Fractional 기간을 시각에 적용할 때에는 정수 epoch microseconds와 binary64의 정확한 유리수 값을 합친 뒤
그 DateTime 결과 경계에서 한 번 half-even으로 반올림한다. 원 epoch를 float로 변환하거나 기간을 미리 반올림하지 않는다.
기간의 raw fractional 결과도 full normalized model의 최소/최대 정수 microseconds 사이에 있어야 한다.
고정 Django의 SQLite 평균 이동과 native 범위 초과에 대한 의도적 차이는
[DEV-0022](../DEVIATIONS.md#dev-0022--temporal-계산의-정확한-범위와-평균-이동)에서 분류한다.

양쪽 operand를 검증한 뒤 NULL을 전파하며 손상 값·범위 밖의 중간값을 상쇄하거나 NULL로 숨기지 않는다. 선택되지 않은
CASE branch와 FILTER 제외 행은 실행하지 않는다. 조회·같은 행 UPDATE는 같은 검사 경로를 사용하며 target과 source
의미·statement rollback·원래 오류의 소유권을 유지한다. 이 계약의 채택, 작성한 코드와 실제 환경별 검증은 활성 work와
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에서 구분한다.

## 기간 배율과 중간 정밀도

[GDJ-0131](../../work/0131-duration-scaling-and-label-time-preview.md)의 `Duration × Number`, `Number × Duration`,
`Duration ÷ Number`는 Duration이며 `Duration ÷ Duration`은 Float이다. Number는 int64·finite binary64·canonical
Decimal의 원 domain을 유지한다. 모든 operand를 Float으로 바꾸면 큰 정수의 낮은 비트와 정확한 Decimal을 잃으므로
기간 배율의 중간값은 정확한 분수 microseconds로 유지한다. Float 배율은 그 binary64의 실제 값을 사용한다.
예를 들어 5µs에 Decimal 0.1을 곱한 0.5µs와 Float 0.1을 곱한 값은 같은 half-even 결과라고 가정하지 않는다.

반올림은 최종 Duration 읽기/쓰기에서 half-even으로 수행한다. `1µs ÷ 2 × 2`와 `1µs ÷ 3 × 3`은 1µs이며,
selected result·CASE·부호 반전·비교·정렬·그룹·집계는 중간 분수를 유지한다. DateTime 이동은 정수 epoch와 기간의
정확한 합을 그 DateTime 경계에서 반올림한다. Float 비율은 두 기간을 미리 Float으로 바꾸지 않고 나눗셈 결과에서
binary64로 변환하며 유한 범위 초과를 Infinity로 게시하지 않는다.

각 Duration operand와 연산 결과는 반올림 전에도 전체 모델의 최소/최대 microseconds 사이여야 한다.
손상·범위 초과 중간값을 NULL이나 뒤의 0 배율·상쇄로 숨기지 않는다. NULL 전파 전 기여 operand를 검사하며,
평가하지 않는 CASE/FILTER는 기존 지연 평가를 유지한다. 모델 Duration의 SQLite storage/literal int64 제한과
계산 결과의 전체 모델 범위는 구분한다. 쓰기는 최종 정수 microseconds의 SQLite 저장 범위를 추가로 확인한다.

새 분수 출처는 기존 native AVG 출처와 구분한다. 단지 새 기능을 추가했다는 이유로 기존 backend의 AVG를 다시
계산하거나 모든 Duration field의 물리 검사를 넓히지 않는다. 이 계약의 채택과 작성한 코드, 양 backend의 실제 실행,
독립 Django 비교·소비자 완료는 별개이며 활성 work와 TEST_EVIDENCE가 현재 구현·검증 범위를 소유한다.
고정 Django와 달라지는 배율·합계·시각 이동·기간 비율의 값과 오류는
[DEV-0024](../DEVIATIONS.md#dev-0024--기간-배율의-정확한-중간값과-최종-반올림)에 관측 사례별로 한정한다.

분수의 내부 query key는 기약분수의 동일성과 수치 순서를 byte 비교로 보존한다. 일반 모델 Duration의 저장 표현은
이 key를 받지 않는다. PostgreSQL은 NUMERIC pair를 산술 상태로, BYTEA key를 query 결과로 사용하며 CASE의 각
분기·비교의 양쪽·정적으로 빈 입력도 producer의 물리 타입을 유지한다. Float 배율은 실제 binary64 비트에서 해석하고,
비율은 정확한 정수 계산으로 Float significand를 반올림한다. Decimal 근사나 임의로 고정한 소수 자릿수로 분수 비율을
대체하지 않는다. DB의 NUMERIC 자원 한계를 넘으면 native 오류를 반환하며 정밀도를 낮춘 fallback으로 성공시키지 않는다.

Helpdesk 라벨별 조정 시간은 같은 collection의 각 present Duration을 배율로 곱하고 양수 divisor로 나눈 뒤 합산한다.
`elapsed_factor`와 `elapsed_divisor`는 canonical Decimal(6,4), 기본 1이다. Factor는 음수·±0을 허용하지만
divisor는 0과 음수를 거부한다. 공통 `CanonicalPositiveDecimal` query/header codec과 OpenAPI의 동일한 문자열
문법이 양수 제한을 소유한다. 사용자 입력을 Float으로 바꾸거나 DB의 0 나눗셈 오류로 검증을 미루지 않는다.
HTML/API는 `adjusted_elapsed_total`과 두 입력을 제공하고, 최종 합계만 half-even 반올림한다. 모든 집계는 같은
Category FILTER·읽기 snapshot에 속하며 NULL과 실제 0, 빈 라벨, 기존 권한과 전체 저장 상태를 유지한다.
이 소비자의 코드·schema 작성과 환경별 실행 완료는 TEST_EVIDENCE에서 구분한다.

## Date와 Time 산술

[GDJ-0132](../../work/0132-calendar-arithmetic-and-service-schedule-preview.md)는 Date/Time을 기존 산술 AST에 연결한다.
`Date ± Duration`과 `Duration + Date`는 Date를 UTC 자정으로 놓고 subday·음수·microsecond까지 유지하는 DateTime이다.
Date/DateTime 사이의 네 뺄셈 조합은 Duration이며 Date와 aware instant의 혼합도 UTC에서 계산한다.
`Time ± Duration`과 `Duration + Time`은 하루를 주기로 정규화한 Time이다. `Time - Time`은 같은 날짜에 놓은
두 시계의 부호 있는 Duration이며 자정 넘김을 추측하지 않는다. Date+Time이나 Time-DateTime처럼 정의하지 않은
조합은 typed API와 dynamic AST에서 거부한다. `orm.Date`/`orm.Time`은 결과 타입에 맞는 다음 연산을 제공한다.

분수 Duration은 시간 결과 경계까지 유지한다. 정수 Date/Time/DateTime과의 정확한 합·차를 microsecond half-even으로
반올림하고, Time만 그 뒤 하루 안으로 정규화한다. 유효한 최대 기간을 빼면서 먼저 음수 Duration으로 만들면 비대칭
모델 범위를 넘을 수 있으므로 원 피연산자를 검사한 뒤 직접 차이를 계산한다. 기존 native AVG와 새 분수 출처는
구분하며 AVG의 backend 의미를 임의로 바꾸지 않는다.

실제 원본의 물리 타입·Date 1..9999·Time 00:00:00..23:59:59.999999·Duration의 전체 모델 범위를 NULL 전파와
계산 전에 검사한다. PostgreSQL의 24:00이나 범위 밖 날짜를 modulo·추출·NULL로 숨기지 않는다. DateTime 결과도
UTC 1..9999다. PostgreSQL은 Date를 timezone 없는 timestamp로 바꿔 UTC wall clock에서 연산하며 session timezone과
DST의 영향을 받지 않는다. SQLite는 검증한 canonical 값과 정확한 정수/분수 microseconds를 사용한다.

같은 AST는 root·단일 관계·집계 전용 collection·CASE/FILTER·명시 결과 stage·group/predicate/order와 같은 행 UPDATE에
연결된다. Source/cardinality·selected scope·불변 소유권·입력 예산, statement/savepoint rollback과 context/cursor 오류를
유지한다. 고정 Django와의 결과 타입·timezone·NULL·정밀도·오류 차이는 [DEV-0025](../DEVIATIONS.md#dev-0025--date와-time-산술의-일관된-결과-도메인)에
한정해 기록하고, 독립 관측의 전체 원본 값·type·native SQL/변환 단계를 명시적으로 대조한다.

Helpdesk는 `Date + (Time - midnight)`로 서비스 시작을 만들고 `shift`를 더한 조정 시작, 저장 `elapsed`를 더한 종료,
`due_at - finish`인 기한 여유를 같은 read service에서 조회한다. 누락한 날짜/시계/기간을 기본값이나 서버 시각으로
채우지 않는다. 현재 Category·ViewTicket admission·하나의 읽기 snapshot·필터 뒤 DB page와 모든 저장/audit 보존을
유지한다. 이 계약의 구현과 환경별 검증 완료는 별개이며 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 실제 결과를 소유한다.

## UTC 날짜와 시계 추출

[GDJ-0129](../../work/0129-calendar-extraction-and-deadline-groups.md)은 닫힌 unary `FunctionExpression`을 같은 scalar AST에
추가한다. `DateTime(...).Date()/Time()`은 UTC Date/Time을, Date/DateTime의 `Year/ISOYear/Quarter/Month/Week/WeekDay/ISOWeekDay/Day`와
Time/DateTime의 `Hour/Minute/Second`는 int64 part를 반환한다. `Week`는 ISO week, `WeekDay`는 Sunday=1,
`ISOWeekDay`는 Monday=1이며 Second는 소수 초를 버린다. Typed `Date`/`Time` capability는 불가능한 part를 제공하지 않고
Dynamic은 binding에서 같은 서명을 검사한다. 일반 SQL 함수 이름·SQL 문자열이나 암묵 temporal 변환은 받지 않는다.

원 operand의 kind·field metadata·관계 cardinality·result scope와 깊이/node 한도를 그대로 유지한다. Typed NULL은
결과에 전파하며 nullable Date/Time은 자정/연도 1과 구분한다. 함수 결과는 projection·WHERE·정렬·group/HAVING·
COUNT/MIN/MAX와 숫자 SUM/AVG·명시적 후속 결과·같은 행 UPDATE에서 같은 compiler를 사용한다.
Collection은 완료된 집계 안에만, selected result는 자신의 후속 stage에만 허용한다.

작은 정수나 날짜를 추출하기 전에 실제 operand 전체를 검사한다. SQLite의 storage class·canonical Date/Time/DateTime과
PostgreSQL의 finite instant/date·연도 1..9999·00:00 이상 24:00 미만 clock 범위를 확인한다. 손상 값과 잘못된
중간 시각은 후속 필터나 NULL/작은 결과로 감추지 않는다. 실행되지 않은 CASE branch와 FILTER 제외 행은 평가하지 않는다.
PostgreSQL은 checked operand를 한 번 바인딩하고 UTC로 추출하며 session timezone을 변경하지 않는다. Numeric-to-integer
반올림 때문에 59.999999초가 60이 되지 않도록 Second의 소수 부분을 먼저 버린다.

기존 모델의 Date/Time output schema를 computed typed output과 공유하고 canonical query/header 입력은 별도의 엄격한
spelling 정책을 가진다. 명시적 timezone은 아래 GDJ-0133 계약을 따르며 locale 선택은 미완료다. Truncation과 Date/Time 이동은 아래 및 GDJ-0132 계약을 따른다.
고정 Django의 정상 값과 비정상 native source의 차이는 독립 관측과 [DEV-0023](../DEVIATIONS.md#dev-0023--calendar-추출의-원본-값-검사와-불가능한-서명-거부)으로 대조한다. 작성한 소비자/관측 코드와 실제 실행
상태는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)로 구분하며 기존 source의 PASS를 전이하지 않는다.

## 타입을 유지하는 구간 시작

[GDJ-0130](../../work/0130-calendar-truncation-and-period-groups.md)은 같은 unary 함수 AST에
`TruncYear/Quarter/Month/Week/Day/Hour/Minute/Second`를 연결한다. DateTime은 모든 단위를 UTC로 계산하고
DateTime을 반환한다. Date는 연·분기·월·주·일을, Time은 시·분·초를 지원하며 각각 Date/Time을 반환한다.
주 시작은 월요일이다. 작은 구성요소를 0 또는 월/일의 1로 바꾸며, 초 절삭은 microsecond를 버리고 반올림하지 않는다.
타입이 바뀌는 기존 `.Date()`/`.Time()`와 구분하며 typed NULL과 원본 domain·source·scope를 유지한다.

각 실제 operand 전체를 먼저 검사한다. 잘못된 입력이나 범위 밖 중간값을 유효한 구간으로 줄여 숨기지 않는다.
PostgreSQL은 Date를 timezone 없는 timestamp로, Time을 interval로 명시 변환하고 결과를 원 타입으로 돌린다.
DateTime은 UTC timestamp에서 계산한 뒤 UTC instant로 반환한다. 호출자의 세션 timezone을 바꾸지 않는다.
SQLite는 같은 canonical 입력 검사 뒤 Gregorian 날짜·시계 구성요소를 statement 안에서 계산한다.
고정 Django PostgreSQL의 Trunc를 감싼 CASE/집계에서 Python 반환 타입이 유지되지 않는 차이는
[DEV-0023](../DEVIATIONS.md#trunc를-감싼-식의-반환-타입)에 원본 그대로 기록하고, 명시적 UTC·native 타입/값을 확인해 비교한다.
GoDj의 Date/Time/UTC DateTime 결과 도메인은 CASE/집계 뒤에도 유지한다.
이 설계의 채택과 구현·환경별 검증은 [작업 기록](../../work/0130-calendar-truncation-and-period-groups.md)에서 구분한다.

## 조회 결과와 조건부 값

같은 scalar tree를 `ScalarResult`로 행 projection·조건·정렬·group key에, `ScalarAggregateResult`로 완료된 집계에 연결한다.
계산 결과에 가짜 FieldRef를 만들지 않는다. 결과 kind·nullable과 모든 원본 leaf의 kind·nullable·Decimal precision을
별도로 검증하며 CASE의 조건에만 사용한 field도 원 source와 일치해야 한다. 같은 read/write renderer가 tree를
순회하고 각 backend가 physical codec·식별자·parameter·native 연산을 소유한다.

`F`, `Value`, `NullValue`, 산술과 `Case(fallback, When(predicate, value)...)`는 typed projection에
`Optional[V]`를 제공한다. `Numeric`과 산술 결과의 sealed numeric capability는 int64/float64의 SUM/AVG·MIN/MAX를
연결한다. 이 두 kind의 AVG 반환형은 float64다. `orm.Decimal(operand)`의 정확한 산술과 집계는 AVG도 Decimal이다. Dynamic `BindExpression`/`ProjectDynamic`과 생성 Query의
`Expression`/`ProjectExpressions`는 같은 AST에서 kind를 가진 `query.Value`를 반환한다.
`BindPredicate`/생성 `ExpressionPredicate`도 metadata를 먼저 검증한다. 읽기에서 bare untyped NULL은 거부한다.

CASE는 명시적 default와 순서가 있는 같은 source의 조건을 갖는다. 첫 true 조건의 값을 선택하고 SQL UNKNOWN은
다음 조건으로 진행한다. Branch가 없으면 default 자체다. 조건과 값이 교차하는 전체 깊이/노드 예산을 유지하고
branch slice를 복사한다. Decimal assignment는 선택되지 않는 branch도 선언된 target precision에 맞는지 검사한다.
실행 중 선택되지 않는 row expression을 미리 계산하지 않지만 PostgreSQL의 constant folding 오류까지 숨기지는 않는다.

계산식 비교의 NOT는 SQL 3값 논리를 유지한다. 기존 field lookup의 nullable 보정을 복사하지 않으며
`F(nullable).Exact(value)`와 해당 field의 lookup은 이 경계를 의도적으로 구분한다. SQLite float 산술의 NaN→NULL과
PostgreSQL NaN/오류, integer overflow/zero-division 차이는 native 결과로 검증한다. 읽기의 Decimal source leaf와
Duration/숫자 physical 값은 각 backend에서 검사하고 결과 scanner가 자기 결과 domain을 다시 검증한다.
전체 read SQL·parameter 예산도 적용한다. DISTINCT projection의 정렬은 선택한 표현식이어야 한다.

결과 열 참조와 `Derive`의 명시적 후속 조회·재집계는 [ADR-0091](0091-grouped-results-and-having.md)을 따른다.
같은 scalar tree를 사용하지만 원 행 FieldRef와 결과 scope는 구분하며 선택 결과를 모델 UPDATE에 전달하지 않는다.
GDJ-0127의 collection computed operand는 같은 AST의 완료된 집계에서만 실행하며 [ADR-0091](0091-grouped-results-and-having.md)의
cardinality·JOIN 계약을 따른다. 일반 행 projection·computed WHERE·정렬·UPDATE는 여전히 single-valued 값만 받는다.
Named annotation과 dependency/재바인딩은 아래 GDJ-0134 확장에서 다룬다. Subquery/window와 위에 열거하지 않은 function은 아직 포함하지 않는다. GDJ-0128의
시각·기간 계산과 computed Duration SUM/AVG는 위 계약을 따르며, 독립 관찰의 지원 범위와
현재 제품 실행은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)로 구분한다.

## 단일 값 관계와 두 scalar 비교

GDJ-0124는 finite forward FK/OneToOne과 reverse OneToOne의 혼합 경로를 읽기 scalar operand로 사용한다.
원본 모델과 실제 related terminal의 metadata를 구분하며 optional ancestor나 없는 reverse 객체는 NULL이다.
같은 이름/종류의 root 필드를 만들어 related 값을 가장하지 않는다. Root table/key와 정규 경로를 소유한 plan이
검증 주체이며 field 목록만으로 관계를 승인할 수 없다. Collection 경로는 GDJ-0127의 완료된 집계 문맥에서만 허용한다.

Typed related field를 `AddExpressions`, `Case` 등에 직접 전달하고 `Scalar(related)`로 Optional projection·literal 비교·
정렬을 만든다. `Numeric(related)`는 기존 int64/float64 집계 capability를 연결한다.
`ExactExpressions(left, right)`와 네 ordered `...Expressions`는 같은 모델/값 타입의 operand를 받는다.
양쪽의 원본 metadata·선택 결과 scope·CASE의 숨은 조건·깊이/노드를 모두 검사한다. Boolean은 exact만 지원하고
JSON의 scalar-to-scalar 비교는 별도 값 비교 계약 전까지 거부한다. SQL UNKNOWN과 NOT를 기존 계산 비교와 같이 유지한다.

Dynamic `BindExpressionIn`/`BindPredicateIn`/`ProjectDynamicIn`은 source와 일치하는 sealed `BoundModel`을 받아
`DynamicF("ticket__closed")` 같은 경로를 해석한다. Root 전용 함수는 binding 없이 관계 이름을 추측하지 않는다.
생성 facade는 자기 project binding으로 같은 AST를 만들고 dynamic RHS도 `DynamicExpression`으로 전달한다.
선택 결과의 `DynamicColumn` 비교는 자기 결과 scope만 사용한다. 원본 관계를 후속 결과 단계로 직접 가져갈 수 없다.

Projection·WHERE·CASE·ordering·group/aggregate가 한 statement의 canonical JOIN을 공유한다. 계산 조건/선택값은
optional 행의 존재를 증명하지 않으므로 JOIN을 임의로 INNER로 바꾸지 않는다. 각 DB가 terminal의 physical codec과
산술/parameter 예산을 검사한다. 관련 값을 UPDATE assignment로 쓰는 것은 CASE 조건 안에 숨겨도 거부한다.
관련 비교로 원 UPDATE의 대상 행을 고르는 기존 membership 경로는 유지한다.

Helpdesk의 서비스 보고서 상태 조회는 보고서 완료와 연결 티켓의 닫힘을 비교한다. 두 독립 업무 상태의 차이를
오류로 판정하거나 자동 수정하지 않는다. HTML/API 모두 ViewServiceReport와 ViewTicket을 query 해석 전에 요구하며,
현재 category·총수·20개 페이지를 같은 읽기 snapshot으로 반환한다. 전체 Ticket 본문·digest·쓰기·audit를 사용하지 않는다.
같은 읽기 계약을 실제 양 DB·독립 SDK·브라우저 소비자로 검증하며 환경별 실행은 활성 work/Evidence에 기록한다.

## 선택, 실행과 동시성

한 statement의 모든 RHS는 수정 전 같은 행을 참조한다. 여러 assignment의 입력 순서로 갱신 후 값을 다시 읽지
않는다. 원 조건·관계/collection scope identity를 보존하고 ordering·distinct·eager·prefetch·읽기 row-lock 모양은
갱신 범위를 제한하지 않는다. Slice는 assignment가 비어도 거부한다. 빈 assignment나 정적으로 빈 query는
context·구성·session·capability를 검증한 뒤 I/O 없이 0을 반환한다.

SQLite는 갱신 전 membership을 materialized CTE로 고정하고 이름 충돌을 피한다. PostgreSQL의 실제 join 없는
predicate는 직접 UPDATE WHERE에 둔다. 잠금 대기 뒤 바뀐 행을 native 조건으로 재평가하고 RHS도 그 현재 행을
사용한다. Trimmed FK와 correlated NOT EXISTS도 같은 경계다. 실제 join의 membership은 statement snapshot을
따른다. 모든 조건을 애플리케이션의 미리 읽은 key 목록으로 바꾸지 않는다.

Owned transaction 또는 borrowed savepoint 안에서 실행한다. 부모의 source/session 결합, callback 한 번의 수명,
취소·child 사용 중인 parent·종료한 handle·read-only snapshot의 거부를 유지한다. Commit 확인 뒤 count를 게시하고
borrowed 결과는 부모 commit 전까지 provisional이다. 뒤쪽 실패와 확인 가능한 rollback은 원 행을 복원한다.
Literal COMMIT/rollback의 불확실성은 성공이나 자동 retry로 바꾸지 않는다. 단건 Patch와 같은 기존 scope guard를 쓴다.

## Helpdesk의 명령과 ORM 의미 구분

선택 티켓 우선순위 명령은 `POST /api/tickets/raise-priority/`와 Admin의 `raise-priority` 작업을 같은 writer에
연결한다. Category의 현재 선택 행을 읽어 변경 예정 집합을 정하고 Low/Normal에 `F(priority) + 1`을 한 statement로
적용한다. NULL은 별도 statement에서 Normal로 설정한다. 원 우선순위와 Category 조건을 함께 남겨 모든 예정 행이
실제로 일치하는지 확인한다. Urgent와 기존 choices 밖의 값은 그대로 두며 수정하지 않은 열·라벨도 보존한다.

전체 입력은 1..40개의 서로 다른 양수 int64 key다. 인증·CSRF·ChangeTicket·ViewLabel을 파싱 전에 확인하며 Admin은
ViewTicket도 요구한다. 모든 선택 행·현재 라벨 무결성·변경 행의 digest·전체 응답·실제 변경 audit가 하나의 relation
transaction에 속한다. Unchanged 행은 digest를 복구하거나 audit를 추가하지 않는다. API는 모든 선택 행을 입력
순서로 반환하고 Admin은 실제 변경 수를 표시한다. 뒤쪽 실패·취소·불확실한 결과를 부분 성공이나 재시도로 바꾸지 않는다.
NULL을 Normal로 올리는 업무 정책은 일반 scalar AST의 NULL 전파와 별개다.

## Cache와 검증 경계

성공한 UPDATE는 0건/빈 assignment도 호출 query 자신의 cache를 비운다. 같은 query의 복사본은 이 상태를
공유하며 derived query와 이미 반환한 model은 독립 snapshot이다. 보통의 실패는 기존 cache를 유지하고
commit/rollback unknown은 오래된 성공 cache를 게시하지 않도록 비운다. Borrowed update 후 부모 rollback이
이미 반환한 model을 되돌리거나 다른 query cache를 전역 무효화한다고 주장하지 않는다.

평가 generation이 바뀌면 이전 in-flight 읽기의 값/관계 graph를 새 cache로 게시하지 않고, 그 오류도 새 generation의
읽기로 전파하지 않는다. 이미 진행 중인
호출 자체의 결과와 이후 query cache의 소유권은 구별한다. Eager/prefetch는 원 parent query의 cache를 함께 지우지 않는다.

독립 [Django observer](../../conformance/runners/django/query_update_reference.py)와
[서버 잠금 observer](../../conformance/runners/django/query_update_concurrency_reference.py)는 Go source·expected를 읽지
않는다. 생성 소비자는 실제 양 DB의 행/count·오류/SQLSTATE·SQL 횟수·cache·부모 결과를 대조하고 명시적인
[Go 차이](../DEVIATIONS.md#dev-0021--query-update의-정확한-정수와-명시적-변환)를 별도 assertion으로 검증한다.
미지원 union·distinct-fields·projection update를 Django parity로 계산하지 않는다. Decimal은 GDJ-0126의 별도 native 관측과 명시적 deviation을 적용한다.
구현·현재 source의 실제 실행·전체 플랫폼·Helpdesk 업무 완료는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에서 구분한다.


## 명시적 IANA 시간대와 지역 달력

[GDJ-0133](../../work/0133-named-timezones-and-local-deadline-calendar.md)의 설계는 timezone을 불변 expression metadata로 둔다.
`timezone.Parse`가 고정 데이터 이름을 검증하고 `DateTime(...).InZone(zone)`는 Date/Time·각 part·Trunc 함수를 제공한다.
Dynamic도 같은 DateTime 서명을 binding에서 검사한다. Zone 자체는 scalar가 아니며 생략한 함수는 UTC를 유지한다.
절삭 결과는 UTC instant다. 그 결과에 적용할 다음 지역 달력 연산은 다시 명시해야 한다.

Process `TZ`/`ZONEINFO`, `time.Local`, DB session timezone과 서버에 설치된 timezone database는 규칙의 입력이 아니다.
고정 IANA archive의 모든 이름/alias를 지원 대상으로 두며 ZIP/hash/출처와 runtime package를 source binding에 포함한다.
공개 Location은 복사본이고, 이름 목록·marshal 결과 변경은 공유 catalog나 기존 query에 영향을 주지 않는다.

DateTime을 지역 civil components로 변환한 다음 추출/절삭한다. 원본 UTC instant와 변환된 지역 civil value 모두 전체
1..9999 범위를 검사한다. 절삭한 지역 시각은 `fold=0`으로 해석한다. 중복 시각은 전환 전 offset인 더 이른 instant,
존재하지 않는 시각도 전환 전 offset을 사용한다. 그 UTC 결과의 모델 범위도 검사하며 오류를 작은 part나 NULL로 숨기지 않는다.
이는 고정 Django의 `convert_trunc_expression` → `make_aware` → `ZoneInfo` 기본 fold 정책을 참조한 GoDj의 query 의미다.
서버 native 역변환의 overlap 선택에 맡기지 않고, SELECT 이후의 client 변환이나 row 재분류도 하지 않는다.

UTC 전환 `t`, 전후 offset `a`/`b`의 fold=0 지역 경계는 `t + max(a,b)`다. 고정 데이터의 이 경계를 정렬 검증하고
상한이 있는 immutable 전환표를 만든다. 과거 전환을 모두 보존하며 마지막 명시 전환 이후의 POSIX recurrence는
Gregorian 400년 주기로 표현한다. 현재 데이터의 마지막 명시 전환은 2087년이며 2400년까지의 역사와 그 뒤 한 주기를
보존해 미래를 9999년까지 같은 규칙으로 계산한다. 새 데이터가 이 경계나 예산·단조성을 위반하면 명시적으로 실패한다.
SQLite는 같은 표를 binary search하고 PostgreSQL은 bound numeric/integer 배열과 `width_bucket`을 사용한다.
PostgreSQL의 epoch 계산은 numeric이며 초 단위 정수 offset만 interval에 곱하므로 instant를 binary64로 변환하지 않는다.
함수 parameter도 원래 statement의 parameter 예산과 순서에 포함한다.

독립 참조는 공식 IANA 2025c source를 같은 `backzone/zone.tab` 정책으로 별도 컴파일한다.
제품 ZIP을 읽지 않으며 archive·생성 source·598개 파일·빌드 receipt와 Python/Django source를 결합한다.
Python ZoneInfo의 전체 이름 관측과 Django의 양 DB 쿼리를 구분한다. Django가 쓰는 PostgreSQL native 데이터도
관측 fixture의 실제 offset이 일치해야 비교하며 다른 배포 정책을 제품 의미 차이로 면제하지 않는다.
SELECT와 UPDATE에서 같은 UTC instant를 유지하는 결정과 원본 도메인 차이는
[DEV-0023](../DEVIATIONS.md#명시적-시간대의-변환-범위와-update)에 기록한다.

Helpdesk의 `time_zone`은 공통 `parameters.TimeZone()`와 `output.TimeZone()`의 불변 typed 값이다.
입력·출력은 같은 pinned 전체 enum과 데이터 version/hash를 게시한다. 부재만 명시적 UTC 기본값을 사용하며
빈/중복/알 수 없는 이름은 admission 이후, DB lookup 이전에 거부한다. `as_of`와 최초/최종 기한은 UTC instant를
유지하고, group key·기간과 bound는 지역 Date를 사용한다. DB grouping과 snapshot/page를 유지하며 app에서
재분류하지 않는다. 반환한 기간 소속은 지역 civil date로 검증한다. Local midnight 생성이나 24시간짜리 하루를
가정하지 않고, 9999년 마지막 기간의 exclusive end는 검증 안에서만 사용하는 year 10000 civil sentinel이다.

설계 채택과 구현·환경별 검증은 별개다. 현재 구현과 남은 범위는 작업 기록, 실제 실행은
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다. Localized naive 입력·표시 locale·일반 wall-time 산술·사용자 설정 저장은
이 query 기능의 완료로 간주하지 않는다. Python 객체·SQL 문자열을 복제하지 않고 외부 결과와 실패 의미를 비교한다.

출처: [고정 Django converter](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/backends/base/operations.py),
[make_aware](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/utils/timezone.py),
[ZoneInfo fold](https://docs.python.org/3/library/zoneinfo.html#using-zoneinfo), [TZif RFC 9636](https://www.rfc-editor.org/rfc/rfc9636.html).

## 명명된 scalar 참조

[GDJ-0134](../../work/0134-named-query-annotations.md)는 `ScalarAnnotation`으로 query가 소유한 불변 정의를
참조한다. 이름은 현재 정의를 찾는 입력이며 참조의 identity나 SQL alias가 아니다. 재정의 이후에도 앞서 만든
산술·CASE의 양 operand·함수·조건은 원래 정의를 유지한다. 같은 이름·값을 가진 다른 query 분기의 참조는
승인하지 않는다. Schema IR의 저장 필드와 명시적 ResultScope를 이름 저장소로 사용하지 않는다.

Source의 field·column 이름은 selection에서 빠진 뒤에도 예약한다. 식별자의 선두 숫자와 `__`는 허용하지 않는다.
고정 Django와 다른 세 이름 사례는 [DEV-0026](../DEVIATIONS.md#dev-0026--명명-조회의-예약-이름과-식별자-경계)에 한정한다.

명명 참조의 완전한 producer 확장 비용을 scalar 깊이/노드 한도에 포함하고, 현재 정의와 보존한 과거 정의 및
captured predicate를 전체 query 예산에 포함한다. 결과 kind/NULL·Decimal 원본 precision·Duration fraction과
native mean·시간대는 producer에서 이어받는다. 완료된 집계의 참조와 aggregate operand는 구분하며 같은
stage의 중첩 집계는 거부한다. 재집계는 명시적 결과 stage에서 선택한 cell을 사용한다.

현재 작성한 공통 경계는 명명 값을 원본 row assignment로 쓰거나 명명 query를 UPDATE/BulkUpdate source로
전달하는 것을 오류로 처리한다. Row write 권한을 이름이나 read decoder에서 얻지 않는다. API·생성 소비자와
환경별 검증은 활성 work 및 TEST_EVIDENCE를 따르며 이 설계 채택을 기능 완료로 계산하지 않는다.

### Go의 모델과 명명 결과

`orm.Annotate`/`Alias`는 원 모델 또는 projection과 scalar를 받아 `NamedQuery[M,R]`와
`NamedColumn[M,V]`를 반환한다. Aggregate는 `AnnotateAggregate`/`AliasAggregate`로 같은 정의 저장소에
bind한다. `NamedRow[M,R].Value`는 원래 모델·DTO이고, 명명 값은 해당 query의 column으로 `Get`한다.
모델 struct나 Schema IR에 임시 필드를 넣지 않으며 SQL alias나 같은 문자열은 다른 정의의 값을 읽을 권한이 아니다.

`Named`/`NamedCount`의 동적 배치는 입력 순서대로 앞선 정의를 해석한다. `Expression`, `Predicate`,
`ProjectExpressions`도 같은 Plan의 이름을 해석하며 batch 중 오류는 원 query를 바꾸지 않는다.
`NamesIn`은 정확히 같은 descriptor·metadata·source에 속한 project binding만 받는다. 이 binding을 사용하는
생성 facade에서는 forward/reverse/ManyToMany accessor도 예약된 이름이다. `SelectNamed`로 selection을
바꾼 뒤에도 project 권한을 유지하되, 선택하지 않은 명명 값을 다시 결과에 추가하지 않는다.

생성된 `Annotate<Model>` 함수와 `<Model>Query.Annotate`는 같은 generic runtime을 사용한다.
모델 행의 `Value`에는 일반 generated model wrapper가 들어가므로 명명 결과와 저장 모델을 함께 사용할 수 있다.
모델 decoder는 `ProjectionDescriptor`와 primary-key capability가 필요하다. 지원하지 않는 descriptor는
I/O 전에 오류를 반환하며 scalar projection을 명시적으로 선택할 수 있다. Eager/prefetch가 붙은 model source는
현재 named decoder로 처리하지 않고 오류를 반환한다.

명명 결과는 일반 ResultQuery의 cache·session·Page 경로를 사용한다. 각 행의 저장 필드와 추가 cell은 cursor가
다음 행으로 이동하기 전에 검사하고 가변 driver bytes를 복사한다. 모델 presence/key 오류·scan/iteration/close·
취소 실패에는 부분 결과나 성공 cache를 게시하지 않는다. DTO builder는 성공한 cursor 종료 뒤 실행하며
같은 cache에서 반환할 때마다 원 모델과 DTO를 다시 만든다. 모델 wrapper의 저장은 저장 필드만 처리하며
이미 읽은 명명 cell은 바꾸지 않는다. `Fresh` 조회에서 새 DB 값으로 계산한다.

Helpdesk 기한 미리보기는 `adjusted_due_at`을 등록하고 `remaining`이 그 소유된 정의를 참조하도록 구성한다.
같은 명명 값을 projection·최소 잔여 기간 조건·정렬·단일 statement Page에 사용한다. 이름은 애플리케이션의
고정 선언이며 HTTP 입력으로 받지 않는다. 기존 ViewTicket·Category 범위·한 읽기 snapshot과 전체 출력 검사를 유지한다.
