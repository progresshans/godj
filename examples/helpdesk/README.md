# Helpdesk 소비자

Category–Ticket 관계 모델에 선택형 Form/Admin, 읽기 전용 Category Admin, 인증·CSRF API를 연결한다.
`Application`은 caller가 제공한 backend를 사용한다. 테스트는 실제 migration, HTTP CRUD, 재시작과 권한 교체를 검증한다.

`New(backend, categoryID)`는 선택 Category와 Admin 구성을 만들고 I/O를 수행하지 않는다.
`application.API(authentication)`으로 API를 한 번 조합한 뒤 `api.Routes()`를 Web 설정에 연결한다.
`api.OpenAPI()`는 같은 operation과 인증 구성에서 OpenAPI 3.1 문서를 만든다. 문서 제공 경로와 권한은 caller가 정한다.
문서화 profile이 없는 custom authentication도 Routes에 사용할 수 있지만 OpenAPI 구성은 명시적으로 실패한다.

문서의 `Ticket`, `TicketCreate`, `TicketDetail`, `CategorySummary`는 명시적인 component 이름이다.
Ticket 응답과 생성 입력은 실제 ModelEncoder와 Bind가 사용하는 serializer spec에서 파생하며,
CategorySummary는 직접 출력하는 id/name 구조를 기술한다.
`GET /api/tickets/`는 선택 Category의 티켓을 ID 오름차순으로 최대 20개 담은 배열이며 query parameter를 무시한다.
`POST /api/tickets/`는 subject/details/closed/priority/resolution/due_at/reviewed/service_on/service_at/elapsed/effort/expected_cost/external_reference/external_payload를 받아 Ticket과 201을 반환하고 Location header를 추가하지 않는다.
subject는 필수이며 생략한 closed는 false, 생략하거나 null로 지정한 details는 null이다. 빈 details 문자열은 null과 구별한다.
priority 입력은 1(Urgent), 0(Normal), -1(Low)과 null이다. 생략/null은 null이며 0은 실제 값이다. Form/Admin은 같은 모델에서 Select와 표시명을 만든다.
ORM 저장·조회는 signed int64 범위를 유지하고 과거 목록 밖 값도 API 응답·Admin 편집 화면에 보존한다.
resolution은 여러 줄 Text다. API에서 생략/null은 null, 빈 문자열은 빈 값이다.
Admin은 HTML을 escape한 textarea로 표시하고 빈 제출을 빈 문자열로 저장한다. 모델의 저장 길이 제약은 없다.
due_at은 nullable 시각이다. API는 offset을 포함한 RFC3339를 받아 UTC·여섯 자리 microsecond로 반환한다. 생략/null은 null이다.
Admin은 offset이 없으면 UTC로 해석하고 빈 제출은 null로 저장한다. 연도 1..9999를 지원하며 세부 의미는 [ADR-0061](../../docs/adr/0061-datetime-field-and-canonical-instant-values.md)을 따른다.
reviewed는 nullable Boolean으로 API의 true/false/null과 Admin의 Yes/No/Unknown을 구분한다.
service_on은 방문 예정일이다. 날짜만 있는 ISO 입력을 받아 `YYYY-MM-DD`로 반환하며 연도 1..9999·윤년을 검증한다.
Admin은 달력/슬래시/영어 월 이름 날짜를 받고 빈 입력을 null로 저장한다. 시각·시간대로 날짜를 바꾸지 않는다.
[Date 의미와 입력 경계](../../docs/adr/0065-calendar-date-field-and-input-boundaries.md)를 따른다.
service_at은 날짜·시간대 없는 방문 시간이다. 자정은 유효한 값이고 NULL과 구분한다. API는 ISO clock 입력을 받아
`HH:MM:SS` 및 nonzero microsecond의 여섯 자리 fraction으로 반환한다. Offset 입력은 clock을 UTC로 바꾸지 않고 offset만 제거한다.
Admin은 소수초 초기값을 보존하며 hour/minute/second 입력을 검증하고 빈 입력은 null로 저장한다.
[Time 의미와 입력 경계](../../docs/adr/0066-clock-time-field-and-precision-boundaries.md)를 따른다.
elapsed는 nullable 작업 시간이다. API는 `[days ]HH:MM:SS[.ffffff]` 문자열을 반환하며 입력 의미와 DB별 한도는
[Duration 경계](../../docs/adr/0067-duration-model-range-and-number-input.md)를 따른다. effort는 nullable binary64 작업량이다.
Form/API는 finite 값만 받고, API는 JSON 숫자를 반환한다. ORM에 직접 저장한 non-finite 값의 API/Admin 출력은 오류다.
[Float 경계](../../docs/adr/0068-binary64-field-and-finite-json-boundaries.md)를 따른다.
expected_cost는 nullable Decimal(14, 2) 예상 비용이다. 정확한 JSON 숫자 또는 소수 문자열을 받아 두 자리 문자열로 반환한다.
예를 들어 `0.1`은 `"0.10"`이 된다. `1.230`은 반올림하지 않고 거부하며 허용 범위는 -999999999999.99..999999999999.99다.
Admin은 step=0.01과 두 자리 초기값을 사용하고 빈 제출은 null로 저장한다. ±0의 부호만 바꾸면 UPDATE하지 않는다.
[Decimal의 입력·저장 경계](../../docs/adr/0069-exact-decimal-values-and-storage.md)를 따른다.
external_reference는 nullable 외부 UUID 참조다. API는 UUID 표기 별칭과 0..2^128-1의 정확한 JSON 정수, bool의 0/1을 받으며
소수·지수 token은 거부한다. 출력은 lowercase hyphenated 문자열이고 zero UUID와 null을 구분한다.
Admin은 별칭을 같은 값으로 비교하고 canonical 초기값을 표시하며 빈 제출은 null이다.
OpenAPI 표준 schema는 canonical client 문자열을 기술하고 추가 입력 정책은 x-godj-uuid에 기록한다.
[UUID 값과 입력 경계](../../docs/adr/0070-uuid-model-values-and-storage.md)를 따른다.
external_payload는 nullable 외부 JSON 데이터다. Object/array/string/bool/number를 그대로 받고 큰 정수와 숫자 token을 보존한다.
문자열 안의 JSON 문법은 다시 해석하지 않는다. 명시적 null은 SQL NULL로 비우며 PUT/PATCH 생략은 기존 값을 보존한다.
빈 object/array/string은 null과 다른 값이며 JSON 내부 빈 key도 허용한다. Duplicate key·잘못된 Unicode·NUL은 거부한다.
Admin은 Textarea와 실패 원문을 제공하고 object 순서·1.0/1e0 같은 Form 표기 차이로 UPDATE하지 않는다.
Stored JSON null의 tag와 원래 숫자 표기는 다른 필드만 바꾸는 Form에서도 유지한다.
Form/API payload는 4096 byte·깊이 14로 제한한다. Native JSONB의 숫자 표기는 저장 중 변할 수 있으므로
실제 DB 결과를 다시 읽어 detail/list 응답 한도까지 transaction 안에서 검사하고 실패하면 생성·수정 모두 rollback한다.
[JSON 값과 입력 경계](../../docs/adr/0071-json-values-and-native-storage-boundaries.md)를 따른다.
`PUT /api/tickets/<id>/`는 subject가 필수이고 생략한 closed는 false다. `PATCH`는 제출한 필드만 수정하며 default를 넣지 않는다.
양쪽 모두 생략한 nullable field는 보존하고 명시적 null은 비운다. ChangeTicket 권한·CSRF·category 범위를 검사하며,
같은 transaction에서 현재 행을 확인하고 바뀐 값만 저장한다.
본문은 4096바이트로 제한하고 중복 JSON member와 뒤따르는 데이터를 거절한다.
기본 조합에는 Accept negotiation middleware가 없으므로 문서도 406을 광고하지 않는다.

`GET /api/tickets/<id>/`는 `ViewTicket` 권한과 서버가 배정한 Category 범위를 확인한 뒤 티켓과 Category를
하나의 JOIN 조회로 반환한다. 응답의 `ticket`은 id/subject/details/closed/category/priority/resolution/due_at/reviewed/service_on/service_at/elapsed/effort/expected_cost/external_reference/external_payload, `category`는 id/name만 포함한다.
티켓 조회 권한에는 그 티켓의 Category 이름 조회가 포함된다. 별도 Category Admin은 `ViewCategory` 권한을 요구한다.
없는 티켓과 다른 Category의 티켓은 모두 404이며, 인증·권한 거부 시 제품 데이터 조회를 실행하지 않는다.

```sh
go test ./examples/helpdesk -count=1
go run ./cmd/godj generate --check --project examples/helpdesk/godj.toml
```

선언 runner는 생성·makemigrations 명령을 제공한다. `modeldef` 변경 후 같은 생성 명령에서 `--check`를 빼면 생성물을 갱신한다.
`go run ./cmd/godj makemigrations --project examples/helpdesk/godj.toml`은 선언과 `migrations/`의 차이를 작성한다.
`0001_initial`부터 `0004_ticket_due_at`까지 보존한다. `0005_alter_ticket_priority`는 선택값을 추가하고 `0006_alter_ticket_priority`는 표시명·순서를 변경한다. 두 metadata 변경은 DDL을 만들지 않는다. `0007_ticket_reviewed`와 `0008_ticket_service_on`은 각각 nullable Boolean과 Date를 추가하고 기존 행은 null로 유지한다. `0009_ticket_service_at`, `0010_ticket_elapsed`, `0011_ticket_effort`, `0012_ticket_expected_cost`는 Time·Duration·Float·Decimal을 추가하며 기존 행은 null이다. `0013_alter_ticket_expected_cost`는 기존 비용을 보존하면서 Decimal(12, 2)를 Decimal(14, 2)로 늘린다. 새 한도에만 맞는 값이 남아 있으면 역방향 변경은 반올림 없이 실패하고, 명시적으로 값을 수정한 뒤 다시 시도할 수 있다. `0014_ticket_external_reference`는 UUID, `0015_ticket_external_payload`는 JSON을 추가하며 기존 행은 null이다. `MigrationSources()`의 전체 source를 loader에 전달한다.
테스트는 0001의 기존 행, 필드 추가/역방향, 0004의 범위 밖 priority 값에 0005·0006 적용/역방향/재적용, Form/Admin/API의 선택값 검증과 기존 값 보존을 다룬다.
PostgreSQL 검증은 `GODJ_TEST_POSTGRES_URL`과 명시적인 `GODJ_REQUIRE_POSTGRES=1`로 실행하며, CI의 pinned service가 소유한다.

관계와 함께 읽는 목록은 같은 query의 `Count(ctx)`로 페이지네이션할 수 있다. 예를 들어
`project.Using(backend)`의 `ModelsTicket.SelectRelated(objects.ModelsTicket.Related.Category)`에서 Count를 호출한 뒤
Offset·Limit·All을 적용한다. Cold Count는 category 객체를 읽지 않고 필터와 슬라이스의 의미를 보존한다.
이미 All을 마친 query의 Count는 그 eager cache를 재사용한다. 실제 SQLite/PostgreSQL 소비자는 기존 데이터에서 이 흐름을 검사한다.

카테고리 이름으로 Ticket을 찾을 때도 같은 relation binding을 사용한다.
`relations.ModelsTicket.Category.Name.IContains("HARDWARE")`와
`relations.ModelsTicket.Category.Name.In("Hardware & repairs", "Other")`를 `Filter`에 전달할 수 있다.
Dynamic 경로는 `category__name__icontains`, `category__name__in`이며 입력을 받는 경계에서는 필요한 lookup policy를 제공한다.
선택한 category의 materialization과 Count는 같은 필터를 유지한다.
