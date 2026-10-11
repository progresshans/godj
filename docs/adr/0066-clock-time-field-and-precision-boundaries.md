# ADR-0066: 날짜와 시간대 없는 clock Time

- 상태: Accepted
- 날짜: 2026-09-20
- 관련 작업: [GDJ-0088](../../work/0088-clock-time-models.md)
- 보완: [Calendar Date](0065-calendar-date-field-and-input-boundaries.md), [UTC DateTime](0061-datetime-field-and-canonical-instant-values.md)

## 값과 모델

`clock.Time{Hour, Minute, Second, Microsecond}`는 날짜·시간대 없는 comparable 값이다. 시 0..23·분/초 0..59·microsecond 0..999999를
지원한다. 자정은 유효한 zero value이고 nullable model의 `*clock.Time(nil)`과 구분된다. `New`·`Parse`는 error를 반환하고 잘못된
literal/default·decode를 거부한다. 실패한 decode는 receiver를 보존한다. `time.Time`이나 Date의 정보를 암묵적으로 버리는 API는 없다.

Canonical 표현은 `HH:MM:SS`이며 microsecond가 0이 아니면 여섯 자리 fraction을 붙인다. Public parser와 Schema IR/default는 이
표현만 받는다. Clock과 DateTime·Date의 IR/Query Value arm은 별도다. Date와 마찬가지로 query 값은 canonical text를 snapshot하고
Typed/dynamic 비교·IN·같은 모델의 F·projection·Min/Max·forward scalar relation은 공통 AST를 따른다. Reverse는 기존 nonnullable exact
범위를 따른다. Dynamic 경로는 clock.Time과 그 slice를 요구하며 string·datetime을 추측하지 않는다.

미지원 clock/다른 scalar payload·잘못된 metadata는 정규화·생성·mutation 전에 거부한다. Historical definition의 strict wire와
resource preflight/size/digest는 Time arm을 포함한다. 새 raw payload가 active kind와 무관하게 문자열·aggregate 예산에 포함되며,
기존 scalar의 canonical hash bytes는 바꾸지 않는다. 새로운 패키지는 격리 generated module과 실행 증거 source binding에도 포함한다.

## 저장과 마이그레이션

SQLite·PostgreSQL은 TIME 컬럼과 canonical clock 문자열 parameter를 사용한다. PostgreSQL catalog는 `time`이며 `timetz`와 구분한다.
Application default는 생략한 Create에서 적용하며 영속 SQL DEFAULT가 아니다. Nullable/no-default 필드는 기존 행에 NULL로 추가한다.
Helpdesk의 `0009_ticket_service_at`은 이전 migration을 다시 쓰지 않는다. 시간대/DateTime 변환이나 임의 backfill은 자동 추론하지 않는다.

Scanner는 SQL TIME의 string/bytes, seconds와 1..6자리 fraction을 받는다. 고정 pgx database/sql codec은 zero에도 여섯 자리를 출력할 수
있으므로 public canonical parser보다 SQL text 표현 범위가 넓다. 날짜·zone·24:00·범위 초과·정밀도 초과·time.Time은 거부한다.
실패한 scan은 과거 값을 비우고 nullable Valid도 해제한다. Nonnullable NULL은 오류다. GoDj SQLite writer는 한 값에 한 canonical text를
저장한다. 외부 SQL writer가 다른 text spelling으로 저장한 값의 비교 의미까지 정규화한다고 주장하지 않는다.

## Form/Admin과 JSON

GoDj TimeInput은 text input이며 초기값과 변경 감지에서 microsecond를 보존한다. 고정 Django 기본 TimeInput은
`supports_microseconds=false`여서 Form이 초기 소수초를 제거한다. GoDj는 그 손실을 채택하지 않으며, 독립 reference에는 기본 위젯과
`supports_microseconds=true`인 명시적 MicrosecondTimeInput의 결과를 모두 보존한다. Go Form은 후자의 cleaned/error/changed 결과와
대조한다. 두 profile의 initial 변경 감지 차이는 [DEV-0014](../DEVIATIONS.md#dev-0014--timeinput의-초기-microsecond와-변경-감지를-보존)로 기록하며 숨기거나 실행에서 skip하지 않는다.

Form은 고정 en-us의 `%H:%M`, `%H:%M:%S`, `%H:%M:%S.%f` 입력을 받고 바깥 공백을 제거한다. Fraction은 1..6자리이며 offset은 거부한다.
정확히 빈 optional 입력은 NULL이고 공백만 있는 입력은 invalid다. Admin은 canonical initial·snapshot·재검증을 사용하며 잘못된 raw
입력을 escaping해서 다시 보여 준다. 소수초가 있는 저장 값을 읽기만 하거나 같은 값으로 제출해서 버리지 않는다.

JSON TimeField는 고정 Python 3.14.3/DRF ISO profile의 compact·unpadded·leading T·fraction·offset 입력을 정규화한다. Offset은 UTC로
변환하지 않고 clock components를 유지한다. 6자리 뒤 fraction은 truncate하며 24:00의 zero minute/second/microsecond는 midnight다.
초 생략 fraction은 거부한다. Python 3.12/3.13의 24:00 거부·초 생략 fraction 허용은 별도 compatibility profile에 기록한다.
일반 trim이나 datetime→clock 추출은 없다. Django regex fallback의 Unicode decimal components와 끝 단일 newline을 다룬다.
CPython이 일부 malformed suffix를 관대하게 받는 동작까지 일반적인 입력 문법으로 채택하지 않는다. JSON의 NUL·중복 member·예산 제한은
field validation 전에 적용한다. Python의 typed aware time은 raw 관찰로 남기지만 zone이 없는 Go 값의 지원 기능으로 세지 않는다.

Helpdesk 방문 시간 `service_at`은 생성 시 생략/null이면 NULL, 수정 시 생략하면 보존하고 null이면 비운다. 정규화한 같은 값은 UPDATE를
만들지 않는다. Form/API 권한·CSRF·category 범위·검증 전 I/O 제한·transaction rollback을 유지한다.

## OpenAPI와 독립 client

OpenAPI registry의 `format: time`은 RFC3339 full-time이므로 offset이 없는 Go clock에 사용하지 않는다. String·clock pattern·length와
`x-godj-time`의 timezone/precision/input normalization 정책을 기술한다. Nullable은 별도 null branch다. Canonical request/response를
문서화하며 server가 받는 ISO 별칭은 canonical 응답으로 돌아온다. 고정 ogen client는 nullable/optional string으로 값을 표현한다.
생성 파일을 수동 수정하지 않는다. Generated response decoder의 schema validation과 명시적 request.Validate를 구분한다.
현재 generator의 request encoder가 Validate를 자동 호출한다고 가정하지 않는다. 실제 서버 입력 검증은 독립적으로 유지한다.

## 출처와 검증 경계

Django 6.1 model/Form TimeField·dateparse·en-us formats와 DRF 3.18.0 TimeField(BSD-3-Clause),
CPython 3.14.3 `time.fromisoformat`(PSF license), pgx v5.10.0 TimeCodec(MIT)를 참조한다.
[독립 runner](../../conformance/runners/django/clock_time_reference.py)는 GoDj 소스나 기대 fixture를 읽지 않는다.
[Raw 관찰](../../internal/clocktimetest/testdata/django61.json)은 model 85개·Form 기본/소수초 보존 각각 152개·serializer 344개,
실제 SQLite 기존 행 추가·재연결·갱신·역방향·root/relation query 각 9개·Min/Max를 포함한다.
고정 외부 링크는 [SOURCES](../SOURCES.md), 실행 source·환경·실패와 미완료 검증은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.
Duration·timezone/locale 선택·시간 연산·transform·auto_now 등의 미완료 범위와 전체 프레임워크 완성을 이 field의 완료로 대체하지 않는다.
