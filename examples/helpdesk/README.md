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
`POST /api/tickets/`는 subject/details/closed/priority/resolution/due_at을 받아 Ticket과 201을 반환하고 Location header를 추가하지 않는다.
subject는 필수이며 생략한 closed는 false, 생략하거나 null로 지정한 details는 null이다. 빈 details 문자열은 null과 구별한다.
priority 입력은 1(Urgent), 0(Normal), -1(Low)과 null이다. 생략/null은 null이며 0은 실제 값이다. Form/Admin은 같은 모델에서 Select와 표시명을 만든다.
ORM 저장·조회는 signed int64 범위를 유지하고 과거 목록 밖 값도 API 응답·Admin 편집 화면에 보존한다.
resolution은 여러 줄 Text다. API에서 생략/null은 null, 빈 문자열은 빈 값이다.
Admin은 HTML을 escape한 textarea로 표시하고 빈 제출을 빈 문자열로 저장한다. 모델의 저장 길이 제약은 없다.
due_at은 nullable 시각이다. API는 offset을 포함한 RFC3339를 받아 UTC·여섯 자리 microsecond로 반환한다. 생략/null은 null이다.
Admin은 offset이 없으면 UTC로 해석하고 빈 제출은 null로 저장한다. 연도 1..9999를 지원하며 세부 의미는 [ADR-0061](../../docs/adr/0061-datetime-field-and-canonical-instant-values.md)을 따른다.
본문은 4096바이트로 제한하고 중복 JSON member와 뒤따르는 데이터를 거절한다.
기본 조합에는 Accept negotiation middleware가 없으므로 문서도 406을 광고하지 않는다.

`GET /api/tickets/<id>/`는 `ViewTicket` 권한과 서버가 배정한 Category 범위를 확인한 뒤 티켓과 Category를
하나의 JOIN 조회로 반환한다. 응답의 `ticket`은 id/subject/details/closed/category/priority/resolution/due_at, `category`는 id/name만 포함한다.
티켓 조회 권한에는 그 티켓의 Category 이름 조회가 포함된다. 별도 Category Admin은 `ViewCategory` 권한을 요구한다.
없는 티켓과 다른 Category의 티켓은 모두 404이며, 인증·권한 거부 시 제품 데이터 조회를 실행하지 않는다.

```sh
go test ./examples/helpdesk -count=1
go run ./cmd/godj generate --check --project examples/helpdesk/godj.toml
```

선언 runner는 생성·makemigrations 명령을 제공한다. `modeldef` 변경 후 같은 생성 명령에서 `--check`를 빼면 생성물을 갱신한다.
`go run ./cmd/godj makemigrations --project examples/helpdesk/godj.toml`은 선언과 `migrations/`의 차이를 작성한다.
`0001_initial`부터 `0004_ticket_due_at`까지 보존한다. `0005_alter_ticket_priority`는 선택값을 추가하고 `0006_alter_ticket_priority`는 표시명·순서를 변경한다. 두 metadata 변경은 DDL을 만들지 않는다. `MigrationSources()`의 전체 source를 loader에 전달한다.
테스트는 0001의 기존 행, 필드 추가/역방향, 0004의 범위 밖 priority 값에 0005·0006 적용/역방향/재적용, Form/Admin/API의 선택값 검증과 기존 값 보존을 다룬다.
PostgreSQL 검증은 `GODJ_TEST_POSTGRES_URL`과 명시적인 `GODJ_REQUIRE_POSTGRES=1`로 실행하며, CI의 pinned service가 소유한다.

관계와 함께 읽는 목록은 같은 query의 `Count(ctx)`로 페이지네이션할 수 있다. 예를 들어
`project.Using(backend)`의 `ModelsTicket.SelectRelated(objects.ModelsTicket.Related.Category)`에서 Count를 호출한 뒤
Offset·Limit·All을 적용한다. Cold Count는 category 객체를 읽지 않고 필터와 슬라이스의 의미를 보존한다.
이미 All을 마친 query의 Count는 그 eager cache를 재사용한다. 실제 SQLite/PostgreSQL 소비자는 기존 데이터에서 이 흐름을 검사한다.
