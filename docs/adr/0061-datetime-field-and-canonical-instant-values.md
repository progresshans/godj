# ADR-0061: DateTimeField와 시각 값의 정규화

- 상태: Accepted
- 날짜: 2026-09-19
- 관련 작업: [GDJ-0074](../../work/0074-datetime-field-and-model-time-values.md)
- 보완: [정수 scalar](0059-signed-integer-field-and-model-growth.md), [Form](0043-safe-template-and-model-form-validation.md), [OpenAPI](0058-model-derived-openapi-and-operation-ownership.md)

## 공통 시각 값

`schema.DateTimeField`는 Go `time.Time`, nullable field는 `*time.Time`으로 생성한다. 공통 의미는 UTC 기준 연도 1부터
9999까지의 순간이며 microsecond 정밀도를 사용한다. 작은 자릿수는 반올림하지 않고 버린다. Offset·location과 monotonic
clock 정보는 equality·query cache·default identity의 일부가 아니다. Go zero time은 연도 1의 유효한 값이고 NULL이 아니다.
Offset을 UTC로 변환한 결과가 범위를 벗어나면 I/O 전에 오류를 반환한다.

Schema IR에 datetime kind와 독립 default scalar arm을 둔다. DSL의 concrete time default는 정규화하고 historical definition은
`YYYY-MM-DDTHH:MM:SS.ffffffZ`의 정확한 문자열만 허용한다. Default는 생략한 Create의 application 값이며 영속 SQL DEFAULT가 아니다.
Query AST의 DateTime scalar는 같은 의미의 Unix microseconds를 보유한다. Dynamic query는 `time.Time`을 요구하고 임의 문자열을
시각으로 추측하지 않는다. Invalid time scalar를 NULL로 바꾸지 않는다.

Generated Create/Patch는 선택된 time 값을 정규화해 저장하고 반환한다. Save는 기존 mutation 계약에 따라 caller model을 임의로
다시 쓰지 않으며 DB parameter를 정규화한다. Unselected 값은 변경하지 않는다. 읽어 온 값은 항상 canonical UTC이고 nullable pointer는
model/cache 경계에서 복사한다. Typed/dynamic comparison·same-model F·projection·Min/Max는 공통 AST를 사용한다. AST의 IN도 DateTime scalar를 지원한다.
관계 terminal은 기존 nonnullable implicit-exact 범위에 시각을 추가한다.

## Backend 소유권

SQLite는 DATETIME column에 UTC `YYYY-MM-DD HH:MM:SS.ffffff` 고정 길이 text parameter를 저장한다. Calendar 순서와 문자열 순서가
일치하므로 offset이나 소수초 길이에 따라 sort/filter 결과가 바뀌지 않는다. Column의 native `time.Time`과 MIN/MAX의 text 반환을
모두 scanner가 처리한다. NULL aggregate와 유효한 zero time을 구분한다. PostgreSQL은 TIMESTAMP WITH TIME ZONE에 canonical
native time parameter를 전달하며 catalog의 timestamptz·precision·nullability·identity·persistent default를 확인한다.

공통 write helper가 backend value encoder를 받는다. SQLite 표현을 공통 AST에 넣거나 PostgreSQL에 SQLite text parameter를 전달하지
않는다. SQLite schema remake와 양 DB migration은 동일한 historical kind를 사용한다. Helpdesk는 기존 migration을 보존한 채 nullable
due_at을 0004로 추가하며 기존 행은 NULL로 남긴다. 이 정책은 외부 SQL writer가 저장한 비정규 문자열까지 자동 교정한다는 뜻이 아니다.

## Form/Admin과 JSON

Form의 DateTimeInput은 UTC 의미를 표시하는 text input이다. ISO calendar date-only·offset-free datetime은 UTC로 해석하며 explicit
offset은 실제 순간으로 변환한다. 정확히 빈 입력은 nullable field의 NULL이고 공백만 있는 입력은 invalid다. 고정 Django/Python에서
관찰한 `24:00:00`은 다음 날 자정으로 처리한다. 날짜·시간이 실제로 유효한지 확인하고 지원 연도 밖으로 넘기지 않는다.
Admin initial value·list·snapshot은 canonical 문자열을 사용하고 raw invalid input은 기존 HTML escaping을 유지한다.

JSON은 explicit offset을 가진 RFC3339 문자열을 요구한다. Date-only·naive datetime·앞뒤 공백·24시·NUL을 받아들이지 않는다.
Typed serializer 값으로 cleaning한 뒤 UTC 고정 여섯 자리 소수초 문자열을 출력한다. 생략·null·default와 partial update 정책은 기존
serializer 계약을 따른다. OpenAPI는 string의 `format: date-time` 및 `x-godj-datetime`의 UTC·정밀도·범위·truncate 정책을 기술하고,
nullable 값은 별도 null branch를 가진다. 고정 ogen v1.24.0의 기본 encoder는 소수초를 생략하므로, 문서에 지원되는
`x-ogen-time-format` RFC3339Nano layout을 함께 게시해 입력 정밀도를 보존한다. 생성 파일을 손으로 수정하지 않는다.
다른 generator는 표준 format을 사용할 수 있지만 이 extension의 처리를 보장하지 않는다. 고정 ogen client는 이를 time.Time으로 생성한다. HTTP/parser 자원 한도는 유지한다.

## 출처와 남은 범위

저장소 lock의 Django 6.1/Python 3.14.3 환경에서 model DateTimeField, forms DateTimeField, `django.utils.dateparse`와 양 DB adapter를
확인했다. Django는 BSD-3-Clause다. [Runner](../../conformance/runners/django/datetime_field_reference.py)의 독립 roster는
`derived=false`이며 UTC 설정의 required/optional 48개 관찰을 보존한다. Python 3.12/3.13 compatibility lane에서는
24시 입력 2개를 거부하는 버전별 결과도 명시적으로 비교하며, Go의 기준 결과는 계속 고정 Python 3.14.3이다. 이 중 NUL 뒤 입력을 버리는 Python parser 결과 2개와의 차이는
[DEV-0011](../DEVIATIONS.md#dev-0011--datetime-입력의-nul을-거부하고-문자열-전체를-해석)로 명시한다. 이를 compatibility PASS로 세지 않는다.

모든 locale 입력·ISO week/basic 표기·named-zone/DST 정책, USE_TZ=false에 해당하는 별도 local-time 저장, date extraction/transform,
임의 timezone query, auto_now/auto_now_add, TimeField/DurationField는 미완료 범위다. DateField의 별도 달력 의미는
[ADR-0065](0065-calendar-date-field-and-input-boundaries.md)에서 추가했다. 지원하지 않는 Form 표현은 오류로
남기며 임의 local timezone이나 서버 설정으로 추측하지 않는다. 기능 카탈로그의 완성 목표는 유지한다. 실행 결과와 환경은
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.
