# ADR-0067: Duration 모델 범위와 숫자 입력의 소유권

- 상태: Accepted
- 날짜: 2026-09-20
- 관련 작업: [GDJ-0089](../../work/0089-duration-models.md)

## 모델 값과 저장 범위

`duration.Duration{Days, Microseconds}`는 normalized day/subday의 comparable 값이다. Days는 -999999999..999999999,
Microseconds는 0..86399999999다. Zero는 실제 0 기간이며 nullable의 pointer nil과 구분한다. `New`는 음수·초과 subday를
정규화하고 `Parse`는 canonical `[days ]HH:MM:SS[.ffffff]`만 받는다. Zero day는 생략하고 nonzero 소수초는 여섯 자리다.
실패한 decode는 receiver를 보존한다. Date/Time/DateTime, int64 nanosecond인 Go `time.Duration`과 암묵적으로 변환하지 않는다.

Django timedelta의 전체 모델 범위를 SQLite의 int64 microsecond 한도로 축소하지 않는다. `TotalMicroseconds`는 정확한 정수 계산 뒤
좁은 저장 표현의 overflow를 별도 error로 반환한다. PostgreSQL은 native INTERVAL의 day와 microsecond에 분리해 전달하며,
SQLite는 BIGINT microseconds로 전달한다. DB의 parameter compiler가 저장 범위를 검사하므로 큰 모델 값을 truncate하거나 float로 저장하지 않는다.
정상 모델이 해당 backend 범위를 벗어나면 SQL statement를 실행하기 전에 실패한다. Application transaction의 시작과는 별개다.

pgx database/sql이 interval을 text로 반환하므로 PostgreSQL backend는 물리 연결마다 IntervalStyle=postgres를 설정·검증한다.
Duration 결과가 포함될 때만 interval column을 찾아 backend-owned scanner로 정규화한 뒤 ORM에 전달한다. 음수 day와 양수 clock,
int64 time component의 극값을 checked arithmetic으로 처리한다. Month/year를 가진 외부 interval, infinity와 모델 범위 초과는 오류다.
이 경계는 calendar-aware interval 연산의 지원이 아니다. 실패한 nullable scan은 이전 값과 Valid를 비운다.

## IR·생성·소비자

Duration은 IR/default와 Query AST의 별도 arm이다. 정규화된 모델 의미를 typed/dynamic 비교·IN·F·projection·Min/Max, forward relation,
기존 nonnullable reverse exact 경로가 공유한다. Default는 generated Create의 application default이며 persistent SQL DEFAULT가 아니다.
Canonical migration wire/size/digest·project wire·raw resource guard는 active/inactive Duration payload를 모두 처리한다.
기존 scalar의 hash bytes를 보존하고 새 패키지를 격리 module dependency와 실행 증거의 source binding에 포함한다.

Helpdesk `0010_ticket_elapsed`는 기존 migration을 수정하지 않고 nullable 필드를 추가한다. Form/Admin·JSON create/update와 독립 client가
같은 모델을 사용한다. PUT/PATCH에서 생략은 보존하고 null은 비우며 canonical 값이 같으면 UPDATE를 생략한다. 권한·CSRF·category 검증,
transaction rollback과 재접속의 저장 의미를 기존 흐름과 함께 검사한다.

## 입력과 JSON 숫자

Form은 Django의 DurationField처럼 TextInput을 사용하고 빈 입력을 null/required로 구분한다. Standard day/clock, ISO week/day/time,
PostgreSQL day/time alias와 음수 정규화를 지원한다. Standard fractional seconds는 처음 여섯 자리를 취한다. ISO fractional components는
고정 CPython timedelta의 정수 누적과 fractional microsecond의 half-even rounding을 따른다. 모델 canonical parser와 입력 grammar를 구분한다.

공통 JSON 값은 모든 허용 숫자를 float64로 바꾸지 않는다. Canonical int64는 기존 Integer이고, 그 밖의 JSON number는 원문 token을
보존하는 immutable Number다. `AsInteger`는 decimal/exponent를 coercion하지 않는다. JSON number의 문법과 독립 byte 한도를 검사하며
거대 exponent를 전개하지 않는다. 기본 숫자 길이 1024 bytes, hard cap 4096 bytes이며 문서·깊이·값 수·중복 key·UTF-8/NUL 제한을 유지한다.
이 변경은 기존의 문서 단계 정수 전용 거부를 대체한다. Integer/String/Boolean 등의 필드가 허용하는 값의 범위는 그대로 필드가 검사한다.

Duration serializer는 숫자를 받는 위치에서만 pinned DRF의 `str(JSON-decoded number)` 규칙을 적용한다. Decimal/exponent token은 그때
Python float/문자열 profile로 해석한다. 예를 들어 JSON `1e2`는 100초, `1e-5`는 invalid이며 문자열 `"1e2"`도 invalid다.
공통 Number가 원문을 잃는 것은 아니다. 이 transport 입력의 제한된 float 사용을 모델 값·DB·canonical 응답의 정밀도 손실로 확장하지 않는다.
Typed serializer Time은 DRF의 문자열 변환처럼 clock components를 기간 입력으로 해석하지만 ORM/model default의 타입 경계는 유지한다.

OpenAPI는 canonical string·pattern·length와 null branch를 사용한다. 별도 ogen module의 String 값과 명시적 Validate, 응답 validator,
HTTP·DB의 음수/극값/소수초·생략/null을 검사한다. 서버의 더 넓은 coercion grammar가 모든 생성 client의 입력 타입이라는 뜻은 아니다.

## 근거와 검증 범위

Django 6.1의 DurationField·duration/dateparse(Form 포함), DRF 3.18.0의 DurationField(BSD-3-Clause), CPython 3.14.3 timedelta(PSF),
pgx v5.10.0 interval 및 database/sql adapter(MIT)를 참조했다. [기준 출처](../SOURCES.md), [라이선스](../LICENSING.md)를 따른다.
[독립 runner](../../conformance/runners/django/duration_reference.py)는 기대 fixture나 Go 코드를 읽지 않는다.
Model 67·Form 106·serializer 272·실제 JSON 숫자 15, SQLite add/reopen/update/reverse·query/relation 각 9와 int64 저장 경계를 보존한다.
실제 source·환경·실행 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 기록한다.
Duration 산술·DateTime/Date 간 연산·일반 validators·추가 backend와 전체 프레임워크의 완성을 이 필드의 연결만으로 선언하지 않는다.
