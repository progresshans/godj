# ADR-0065: 시각과 구분되는 Calendar Date

- 상태: Accepted
- 날짜: 2026-09-20
- 관련 작업: [GDJ-0087](../../work/0087-calendar-date-models.md)
- 보완: [DateTime](0061-datetime-field-and-canonical-instant-values.md), [Form](0043-safe-template-and-model-form-validation.md), [JSON](0046-json-serializer-and-session-authenticated-article-api.md)

## 날짜 값과 모델

방문 예정일처럼 시각·시간대가 없는 값은 `calendar.Date{Year, Month, Day}`로 표현한다. `schema.DateField`는 이 값,
nullable field는 `*calendar.Date`를 생성한다. Proleptic Gregorian의 연도 1..9999와 실제 존재하는 월·일만 유효하다.
Go zero literal은 invalid이고 NULL이 아니다. `calendar.New`와 `Parse`는 오류를 반환하며, `Valid`로 literal을 검사할 수 있다.
실패한 text/JSON decode는 기존 receiver를 보존한다. `time.Time`의 시각을 버리거나 UTC로 옮기는 변환을 모델 API에 숨기지 않는다.

공통 날짜의 text/JSON 표현은 정확한 `YYYY-MM-DD`다. Schema IR의 date field/default arm과 historical definition도 이 표현을 쓴다.
Date default는 생략한 Create의 application default이며 영속 SQL DEFAULT가 아니다. Invalid literal과 다른 scalar의 default는
정규화에서 거부한다. 정규화·wire scan·resource preflight·canonical digest가 같은 arm을 다루며 기존 scalar의 hash는 보존한다.

Query Value는 유효한 날짜의 canonical 문자열을 snapshot한다. Typed/dynamic 비교·IN·same-model F·정렬·projection·Min/Max는
같은 DB 독립 AST를 사용한다. Dynamic lookup은 `calendar.Date` 또는 그 scalar slice를 요구하고 문자열·시각을 날짜로 추측하지 않는다.
날짜와 시각의 typed predicate·writer·F 혼입은 컴파일되지 않는다. Nullable pointer는 기존 model/cache 복사 경계를 따른다.
Forward relation terminal은 nullable 날짜와 scalar lookup을 지원한다. Reverse terminal은 기존 nonnullable exact 범위를 따른다.

## 저장과 마이그레이션

SQLite와 PostgreSQL 모두 DATE 컬럼과 canonical 문자열 parameter를 사용한다. 고정 길이 표현의 비교·정렬은 날짜 순서와 같다.
각 backend는 선언과 catalog의 타입·nullability·identity·영속 default를 검사한다. Schema remake와 역사 상태에도 Date가 포함된다.
기존 행에 nullable/no-default 날짜를 추가할 수 있다. Required/default backfill이나 DateTime→Date의 데이터 변환을 자동 추론하지 않는다.
Helpdesk의 `0008_ticket_service_on`은 기존 행에 null을 유지하며 기존 migration을 다시 쓰지 않는다.

Scanner는 canonical string/bytes와 driver의 native midnight `time.Time`을 받는다. Native 값의 연·월·일을 그대로 취하며
UTC 변환으로 날짜를 바꾸지 않는다. 시·분·초·nanosecond가 있으면 오류로 처리한다. Invalid read는 이전 scan 값을 남기지 않는다.
Nonnullable NULL은 오류이고 nullable NULL·빈 aggregate는 값 없는 상태다. 외부 SQL writer가 저장한 비정규 데이터를 자동 교정하지 않는다.

## 입력 경계와 소비자

Form의 DateInput은 날짜 placeholder를 가진 text input이다. 고정 Django en-us DATE_INPUT_FORMATS의 달력·슬래시·영어 월 이름 형식을
허용하며 앞뒤 공백을 제거한다. 두 자리 연도는 00..68을 2000..2068, 69..99를 1969..1999로 해석한다. 정확히 빈 입력은 optional 날짜의
NULL이고 공백만 있는 입력은 invalid다. 서버 locale이나 timezone을 암묵적으로 읽지 않는다. Admin initial·snapshot·재검증·목록은 canonical
문자열을 사용하고, invalid raw input은 escaping한 상태로 다시 보여 준다.

JSON DateField는 고정 DRF ISO 입력 규칙의 달력·unpadded·compact·week date를 정규 날짜로 바꾼다. 앞뒤 공백을 일반적으로 제거하지 않고
datetime 문자열은 거부한다. 고정 Django date fallback이 허용하는 Unicode decimal calendar components와 마지막 단일 newline은
독립 reference대로 처리한다. Typed datetime도 명시적으로 거부한다. JSON parser의 NUL·중복 member·body/depth/string 예산은 유지한다.

생략/null/default·PUT/PATCH는 기존 serializer와 application 정책을 따른다. Helpdesk에서 생성 시 생략한 날짜는 null, 수정 시 생략은 보존,
명시적 null은 삭제다. 같은 값으로 정규화되는 PATCH는 실제 UPDATE를 만들지 않는다. 검증·권한·CSRF는 저장 전에 끝내고 실패한 transaction은
새 날짜나 성공 응답을 게시하지 않는다.

OpenAPI의 `string` / `format: date`는 canonical 날짜 wire를 기술하고 nullable은 별도 null branch다. 입력에서 허용하는 별칭은 canonical
표현으로 반환한다. 고정 ogen client는 `time.Time`으로 표현하지만 실제 wire는 연·월·일만 담는다. GoDj의 모델 타입은 계속 독립된 Date이며,
client 생성 파일을 수동 수정하지 않는다. 독립 module에서 canonical wire·연도 경계·null/생략과 실제 HTTP·DB를 확인한다.

## 출처와 검증 경계

Django 6.1 `DateField`, Form `DateField` / `BaseTemporalField`, `utils.dateparse.parse_date`, en-us formats와 DRF 3.18.0 `DateField`의
공개 결과를 참조했다. 양 프로젝트는 BSD-3-Clause이며 [고정 출처](../SOURCES.md)를 따른다. [독립 runner](../../conformance/runners/django/calendar_date_reference.py)는
GoDj 소스·기대 fixture를 읽지 않는다(`derived=false`). Python 3.14.3/UTC/en-us의 [raw 관찰](../../internal/calendardatetest/testdata/django61.json)에
model 67개, Form 120개, serializer 272개와 실제 SQLite migration·query·관계·aggregate 결과가 있다.

Go는 Form 120개와 DRF field 결과 268개를 대조한다. NUL 4개는 GoDj JSON parser에서 먼저 `invalid_document`로 거부하는 별도 경계를
assert하며 DRF field 오류와 같다고 세지 않는다. Python model의 datetime→date 변환 관찰은 원문에 보존하지만 Go 모델의 지원 coercion이 아니다.
실제 실행 source·환경·남은 검증은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.

일반 locale 선택·date transform/extraction·date arithmetic·auto_now/auto_now_add·TimeField·DurationField는 미완료다. 이 날짜 작업의 완료가
전체 날짜/시간 기능이나 프레임워크 완성을 뜻하지 않는다.
