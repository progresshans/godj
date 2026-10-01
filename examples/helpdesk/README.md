# Helpdesk 소비자

Category–Ticket–ServiceReport 관계 모델에 선택형 Form/Admin, 읽기 전용 Category Admin, 인증·CSRF API를 연결한다.
`Application`은 caller가 제공한 backend를 사용한다. 테스트는 실제 migration, HTTP CRUD, 재시작과 권한 교체를 검증한다.

Ticket.labels는 기존 TicketLabel을 explicit through로 선언한다. `0020_ticket_labels`는 관계 metadata만 추가하며
기존 연결 행·ID·sequence를 보존한다. Generated forward/reverse collection 접근자를 사용할 수 있다.
Ticket Form/Admin은 Category 내 모든 후보의 다중 선택을 제공하고, API는 labels 정수 배열을 받는다.
기존 TicketLabel CRUD도 같은 연결 행을 사용한다. 구현과 환경별 통합 상태는 [GDJ-0099](../../work/0099-many-to-many-and-ticket-label-collections.md)를 따른다.

`New(backend, categoryID)`는 선택 Category와 Admin 구성을 만들고 I/O를 수행하지 않는다.

## 여러 티켓 생성하기

`AdminRegistry(helpdesk.AdminConfig{AppendAudit: runtime.AppendAudit})`로 만든 Ticket 목록은 **Create multiple tickets**
링크와 GET/POST `/admin/tickets/collection-set/create/`를 제공한다. 일반 Ticket 폼의 16개 scalar/라벨 입력을 사용하며
1..40개 티켓을 함께 생성한다. 빈 추가 행은 생략하고 기존 티켓·Category·PK·서버 digest를 입력받지 않는다.
Site의 모델 조회 권한과 AddTicket·ViewLabel, active staff·CSRF를 확인한다. 오류는 원래 행에 재표시하고
전체 commit을 확인한 뒤 목록으로 이동한다. 동적 행 추가/삭제는 입력값과 선택한 라벨을 보존한다.

API는 `POST /api/tickets/bulk/`에서 `TicketCreate` 객체 배열을 받는다. AddTicket·ViewLabel과 구성된 인증/CSRF를
입력 파싱 전에 확인하며 query parameter는 거부한다. 예를 들어 다음 요청은 입력 순서의 두 Ticket과 201을 반환한다.

```json
[{"subject":"First ticket","labels":[1]},{"subject":"Second ticket","closed":true}]
```

배열은 1..40개, 전체 body는 163,840 byte·깊이 16·65,536개 값까지이며 각 항목의 compact JSON은 4,096 byte까지다.
각 필드의 생략/null/default·고유성·JSON 입력 정책은 아래의 단건 생성과 같다. `id`, `category`,
`external_payload_digest` 등 read-only/unknown 입력은 거부한다. Field 진단의 `params` 배열에 있는 `index` 항목은
0부터 시작하는 원래 입력 위치를 문자열로 담는다. 저장 직전의 native 고유성 충돌은 행을 추측하지 않는 `__all__/unique`다. 전체 진단이 출력 예산을
초과하면 `__all__/too_many_errors`와 개수로 요청 전체를 거부하며 일부 진단만 성공처럼 반환하지 않는다.

두 표면은 같은 writer를 사용한다. 현재 Category·모든 라벨·입력 집합/DB의 UUID 고유성을 확인한 뒤 20행 native
batch로 저장한다. 라벨 연결, 저장된 JSON의 digest, 전체 응답 준비와 행마다 하나의 add audit를 같은 transaction에
포함한다. 뒤쪽 batch·라벨·출력·audit·취소 실패는 전체 rollback이며 부분 성공·충돌 생략·자동 재시도는 없다.
전체 JSON 결과는 1 MiB·깊이 16·65,536개 값 한도를 적용한다. Commit/rollback 결과 불확실성은 실행 오류다.

Admin은 40행의 선택 목록을 표시하므로 `admin.SiteConfig.RenderLimits: helpdesk.AdminRenderLimits()`와
`web.Config.MaxResponseBytes: helpdesk.TicketEditorMaxResponseBytes`를 함께 설정한다. 요청은 64 KiB·1,024개 값·값당
4 KiB이며, 선택지는 최대 256개다. 기본 출력 예산을 넘는 HTML을 잘라 반환하지 않는다.

## 여러 티켓을 함께 편집하기

`application.TicketEditor`는 GET/POST `/tickets/edit/`의 HTML 편집 화면을 제공한다. Subject·Closed·External reference·Labels를
한 페이지에서 수정하고 새 티켓과 삭제 요청을 함께 저장한다. 그 밖의 기존 scalar는 보존한다. 생성도 서버가 지정한 Category를
사용하며 Category/PK는 editable input이 아니다. 두 개의 빈 추가 행은 AddTicket이 있을 때만 표시한다.

```go
editor, err := application.TicketEditor(helpdesk.TicketEditorConfig{
    Auth: webAuth,
    AppendAudit: runtime.AppendAudit,
})
if err != nil { return err }
routes = append(routes, editor.Routes()...)
// web.Config에서 MaxResponseBytes: helpdesk.TicketEditorMaxResponseBytes를 사용한다.
```

같은 `webAuth`를 Admin과 공유할 때 `AllowedNextPaths`에 `helpdesk.TicketEditorPath`를 추가하고,
`admin.SiteConfig.AdditionalNextPaths`에도 같은 경로를 선언한다. Session/CSRF cookie 경로는 두 화면을 모두 포함해야 한다.
편집기는 cookie 범위나 로그인 복귀 경로가 맞지 않으면 시작 시 거부한다. `AppendAudit`는 필수이며 제공된 session에 기록해야
한다. 별도 transaction을 열거나 commit하지 않는다. 위 Runtime의 callback은 scalar·관계·삭제와 같은 transaction에 기록한다.

ViewTicket·ChangeTicket·ViewLabel을 데이터 조회 전에 확인한다. AddTicket/DeleteTicket은 해당 조작에 별도로 필요하며
Authorizer의 거부도 적용한다. 기존 ticket writer와 같이 인증이 승인한 불변 principal을 요청에서 사용한다. 정책 변경은 후속
인증에 적용되며 이미 승인된 요청을 소급해서 취소한다고 주장하지 않는다. POST는 CSRF 검증 뒤에만 제품 transaction에 들어간다.

`p`는 1부터 시작하는 페이지다. 서버는 지정 Category의 ID 순서로 기존 20행을 읽고 제출 PK를 이 현재 집합 안에 연결한다.
순서를 바꿀 수 있지만 누락/중복/외부 PK나 추가 행의 PK는 DELETE와 무관하게 전체 거부한다. GET 이후 대상이 이동·삭제되거나
페이지 구성이 바뀌면 관리 개수/identity 검사를 통해 재검토하게 한다. 행의 값 자체는 저장 transaction에서 다시 읽으며 별도
optimistic revision 입력은 제공하지 않는다. 동일 행의 동시 입력은 기존 Ticket 편집과 같은 현재 row/선택 field 저장 의미다.

공통 `InlineSpec`이 canonical project FK와 서버 Category를 결합한다. 각 행의 hidden category는 현재 부모를 확인하는 값이다.
빈 값/생략은 서버 Category를 사용하고 다른 부모·중복값 제출은 DELETE나 빈 추가 행이어도 전체 `invalid_parent`로 거부한다.
오류 화면의 hidden category에는 항상 서버 값을 표시한다. 부모를 선택하거나 티켓을 다른 Category로 옮기는 입력이 아니다.

GET은 한 read snapshot에서 행·관계·선택지를 읽는다. POST는 같은 category/cohort/선택지를 하나의 `AtomicRelation`에서
다시 읽고 Formset/모델 검증, 기존 행 삭제/변경, 새 행의 native bulk 생성·라벨 연결과 감사 기록을 수행한다. 삭제는 프로젝트의 완전한
PROTECT/CASCADE 정책을 borrowed session에서 실행한다. 변경 없는 기존 행은 데이터/audit 쓰기를 생략한다. 고유성 검사·관계
저장·감사 기록·취소가 실패하면 앞서 수행한 행의 변경도 outer transaction이 롤백한다. Runtime을 사용하면 다른 cooperative
write와 같은 DB/schema fence를 사용한다. 다른 connection에서 이 규약을 우회하는 직접 SQL까지 인가/직렬화를 보장하지 않는다.

Form/identity/확인된 데이터 거부는 HTTP 200으로 오류와 원래 입력을 안전하게 재표시한다. 확실히 롤백된 삭제 보호/고유성 오류도
같은 흐름을 사용한다. Parser 오류는 400, 권한/CSRF 거부는 403, 없는 Category는 404다. 운영 오류·rollback/commit 결과 불확실성은
성공 redirect나 입력 오류로 바꾸지 않고 자동 재시도하지 않는다. Commit이 확인된 경우에만 같은 페이지로 303 redirect한다.

같은 제출 안의 중복 External reference는 UUID 표기가 달라도 Formset 단계에서 거부하므로 행 쓰기/audit에 도달하지 않는다.
선택 페이지 밖의 기존 값이나 model clean 이후의 충돌은 최종 transaction의 기존 DB 고유성 검사가 계속 확인한다.

제출 상한은 40행·64 KiB·총 1,024개 값·값 하나당 4 KiB다. 선택지 query는 257개에서 잘라 256개 초과를 명시적으로 거부하며
선택지를 조용히 누락하지 않는다. HTML escaping으로 확장되는 선택지를 포함해 template/Web 출력 한도를
`TicketEditorMaxResponseBytes`(8 MiB)로 함께 설정한다. 기본 Web 1 MiB를 그대로 사용하면 유효한 큰 페이지가 거부될 수 있다.
임의 index/필드·중복 query는 parser에서 거부한다. 일반 field의 중복값은 Form 진단으로 남기고 category는 위의 전체 admission을
적용한다. 이 편집기의 삭제 우선 정책은 제품 writer가 소유한다. 일반 모델 여러 행의 native 저장 순서는 [SavePlan](../../forms/model/README.md#여러-행의-저장과-지연-저장)을 따른다. 새 부모/자식과 Admin inline은 아래 별도 화면에서 제공한다.

## JSON API

`application.API(helpdesk.APIConfig{Authentication: authentication, AppendAudit: runtime.AppendAudit})`로 API를 한 번 조합한 뒤
`api.Routes()`를 Web 설정에 연결한다. Label 확보·티켓별 Report 저장·여러 티켓 생성에는 transaction 안의 감사 기록이 필수다.
`runtime`을 Application의 backend로 전달하고, `AppendAudit`는 제공된 session만 사용한다.
이 설정이 기존 모든 CRUD 쓰기에 공통 감사를 추가하는 것은 아니다.
`api.OpenAPI()`는 같은 operation과 인증 구성에서 OpenAPI 3.1 문서를 만든다. 문서 제공 경로와 권한은 caller가 정한다.
문서화 profile이 없는 custom authentication도 Routes에 사용할 수 있지만 OpenAPI 구성은 명시적으로 실패한다.

문서의 `Ticket`, `TicketCreate`, `TicketDetail`, `CategorySummary`는 명시적인 component 이름이다.
Ticket 응답과 생성 입력은 실제 ModelEncoder와 Bind가 사용하는 serializer spec에서 파생하며,
CategorySummary는 직접 출력하는 id/name 구조를 기술한다.
`GET /api/tickets/`는 선택 Category의 티켓을 ID 오름차순으로 최대 20개 담은 배열이다.
선택 query `search`는 subject 또는 전체 external_payload의 literal 부분 문자열을 검색하고,
`source`는 external_payload.source의 TEXT를 검색한다. 비어 있지 않은 두 조건은 AND, 빈 값은 필터 생략이다.
예를 들어 `?search=vendor&source=partner`는 subject/JSON에 vendor가 있고 source에 partner가 있는 티켓을 찾는다.
대소문자 처리·JSON의 TEXT 표현은 backend 의미를 따른다. `%`, `_`, 역슬래시도 문자로 검색한다.
각 값은 UTF-8 64 byte, 전체 raw query는 2048 byte까지이며 unknown/duplicate parameter·잘못된 escape·UTF-8·NUL은
400 validation_error다. 인증·ViewTicket 권한을 먼저 확인하며 잘못된 query로 제품 데이터를 조회하지 않는다.
Admin의 기본 검색도 subject와 전체 external_payload를 함께 검색한다. 모든 경로에서 배정한 Category 범위를 유지한다.
목록 응답은 전체 65536개 값 예산을 사용하며 1 MiB·깊이 16·container 한도는 유지한다. 개별 입력의 예산을 목록 전체에 적용하지 않는다.
`POST /api/tickets/`는 subject/details/closed/priority/resolution/due_at/reviewed/service_on/service_at/elapsed/effort/expected_cost/external_reference/external_payload/external_url/labels를 받아 Ticket과 201을 반환하고 Location header를 추가하지 않는다.
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
Non-null 외부 참조는 Category를 넘어 모든 티켓에서 고유하다. SQL NULL은 여러 행에 허용하지만 zero UUID는 실제 고유값이다.
인증·CSRF·권한과 대상 확인 뒤 사전 검증하며 자기 티켓은 제외한다. 중복 API 입력은 HTTP 400 `validation_error`와
`external_reference/unique` 진단을 반환한다. 사전 조회 후 발생한 실제 저장 충돌은 `__all__/unique`이며 같은 transaction의
다른 필드 수정도 rollback한다. Admin은 제출 원문을 escape해 보존하고 field/non-field 오류를 표시한다.
DB 조회·rollback·결과 불확실성 오류는 입력 오류로 바꾸지 않는다. [고유성과 오류 소유권](../../docs/adr/0072-column-uniqueness-and-constraint-ownership.md)을 따른다.
external_payload는 nullable 외부 JSON 데이터다. Object/array/string/bool/number를 그대로 받고 큰 정수와 숫자 token을 보존한다.
문자열 안의 JSON 문법은 다시 해석하지 않는다. 명시적 null은 SQL NULL로 비우며 PUT/PATCH 생략은 기존 값을 보존한다.
빈 object/array/string은 null과 다른 값이며 JSON 내부 빈 key도 허용한다. Duplicate key·잘못된 Unicode·NUL은 거부한다.
Admin은 Textarea와 실패 원문을 제공하고 object 순서·1.0/1e0 같은 Form 표기 차이로 UPDATE하지 않는다.
Stored JSON null의 tag와 원래 숫자 표기는 다른 필드만 바꾸는 Form에서도 유지한다.
Form/API payload는 4096 byte·깊이 14로 제한한다. Native JSONB의 숫자 표기는 저장 중 변할 수 있으므로
실제 DB 결과를 다시 읽어 detail/list 응답 한도까지 transaction 안에서 검사하고 실패하면 생성·수정 모두 rollback한다.
[JSON 값과 입력 경계](../../docs/adr/0071-json-values-and-native-storage-boundaries.md)를 따른다.
`PUT /api/tickets/<id>/`는 subject가 필수이고 생략한 closed는 false다. `PATCH`는 제출한 필드만 수정하며 default를 넣지 않는다.
양쪽 모두 생략한 nullable field는 보존하고 명시적 null은 비운다. ChangeTicket·ViewLabel 권한·CSRF·category 범위를 검사하며,
같은 transaction에서 현재 행을 확인하고 바뀐 값만 저장한다.
본문은 4096바이트로 제한하고 중복 JSON member와 뒤따르는 데이터를 거절한다.
기본 조합에는 Accept negotiation middleware가 없으므로 문서도 406을 광고하지 않는다.

`GET /api/tickets/<id>/`는 `ViewTicket` 권한과 서버가 배정한 Category 범위를 확인한 뒤 티켓과 Category를
하나의 JOIN으로 읽고, 같은 Category의 라벨 키를 한 번의 추가 prefetch로 읽는다.
응답의 `ticket`은 id/subject/details/closed/category/priority/resolution/due_at/reviewed/service_on/service_at/elapsed/effort/expected_cost/external_reference/external_payload/labels, `category`는 id/name만 포함한다.
티켓 조회 권한에는 그 티켓의 Category 이름과 해당 범위의 라벨 ID 조회가 포함된다. 별도 Category/Label 조회는 각각 `ViewCategory`/`ViewLabel`을 요구한다.
없는 티켓과 다른 Category의 티켓은 모두 404이며, 인증·권한 거부 시 제품 데이터 조회를 실행하지 않는다.

ServiceReport는 티켓별 선택적인 작업 보고서다. `ticket`은 필수 OneToOne(PROTECT), `summary`는 필수 여러 줄 Text,
`completed`는 기본 false다. `0017_service_report` migration은 기존 Category/Ticket과 별도 table을 만든다.
보고서를 삭제해도 티켓은 남으며 보고서가 남아 있는 티켓의 Admin 삭제는 보호 메시지로 거부된다.
Backend는 Queryer/Mutator/Atomic과 RelationAtomic을 제공한다. Runtime을 사용하면 relation 삭제도 일반 cooperative 쓰기와 같은 fence를 따른다.

| 경로 | 동작 | 권한 |
| --- | --- | --- |
| GET `/api/service-reports/` | 선택 Category의 보고서를 ID 순서로 최대 20개, query parameter 미지원 | ViewServiceReport |
| POST `/api/service-reports/` | ticket/summary/completed 생성, 201 | AddServiceReport |
| GET `/api/service-reports/<id>/` | 보고서 상세 | ViewServiceReport |
| PUT/PATCH `/api/service-reports/<id>/` | 전체/부분 수정과 ticket 재할당 | ChangeServiceReport |
| DELETE `/api/service-reports/<id>/` | 티켓을 보존하고 보고서 삭제, body 없는 204 | DeleteServiceReport |
| GET `/api/tickets/<id>/service-report/` | 보고서 또는 정상 부재 `null` | ViewServiceReport |
| PUT `/api/tickets/<id>/service-report/` | 티켓 보고서 생성/갱신/무변경, `{report, created}` 201/200 | AddServiceReport + ChangeServiceReport + ViewServiceReport |

쓰기에는 CSRF가 필요하고 body는 4096 byte까지다. 보고서 ID 경로의 PUT은 ticket/summary가 필수이고 생략한 completed는 false다.
PATCH는 제공한 필드만 바꾼다. `id`는 입력할 수 없고 summary는 앞뒤 공백을 제거한다.
모든 보고서 경로는 티켓을 통해 배정한 Category 범위를 검사한다. 없는/다른 Category의 객체 조회는 404,
선택할 수 없는 ticket 입력은 `ticket/invalid_choice`다. 기존 scoped 티켓에 보고서가 없으면 200/null이다.
사전 중복은 `ticket/unique`, native 제약 충돌은 `__all__/unique`이며 rollback 실패·취소·조회 오류를 입력 오류로 바꾸지 않는다.

Admin은 `/admin/service-reports/`에서 같은 CRUD를 제공한다. 티켓 선택지는 같은 Category의 subject를 표시하므로
보고서 쓰기 권한에 ViewTicket이 추가로 필요하다. 요청마다 권한을 확인한 선택지를 만들며 전역 Form Spec에 결과를 저장하지 않는다.
저장 전 선택지를 다시 읽고 transaction 안에서도 현재 membership·고유성을 확인한다.
API는 scoped ticket key를 직접 받으며 티켓 subject를 열거하지 않는다. 보고서 view 권한에는 그 관계 key와 부재 조회가 포함된다.
티켓 경로의 PUT은 summary와 completed만 받으며 completed 생략은 false다. 기존 보고서의 ID와 ticket을
보존하고, 같은 입력을 반복하면 UPDATE와 audit를 추가하지 않는다. 현재 티켓의 Category 범위·저장·출력 준비·
add/change audit는 같은 transaction에서 완료한다. PostgreSQL에서는 Ticket의 NO KEY UPDATE와 Report의
행 잠금을 사용하며, SQLite의 native 충돌은 재시도하지 않고 반환한다. 일반 POST는 계속 중복 보고서를 거부한다.

GET/POST `/admin/service-reports/collection/save/`는 기존 보고서 선택 없이 티켓별 보고서를 저장한다.
같은 Category의 티켓 선택지는 ViewTicket을 추가로 요구하고 저장 직전에 다시 검증한다. 생성·변경·무변경
메시지는 확인된 commit 결과를 따른다. API는 Ticket subject를 공개하지 않는 명시적 key 입력이다.

명시적 component `ServiceReport`, `ServiceReportCreate`, `ServiceReportUpdate`, `ServiceReportPatch`,
`ServiceReportSave`, `ServiceReportSaveResult`와 독립 ogen client가 같은 계약을 소비한다.

Label은 Category별 이름 사전이다. `0018_label`은 기존 행을 보존하면서 name(Char 64)·category(FK PROTECT)와
이름 있는 `(category, name)` 고유 제약을 추가한다. 같은 Category 안에서만 이름을 고유하게 저장하고 다른 Category의 같은 이름은 허용한다.
라벨이 남아 있으면 그 Category의 삭제는 PROTECT로 거부하며, 라벨 삭제는 Category·Ticket을 보존한다.

| 경로 | 동작 | 권한 |
| --- | --- | --- |
| GET `/api/labels/` | 선택 Category의 라벨 검색·페이지 조회 | ViewLabel |
| POST `/api/labels/` | name 생성, 201 | AddLabel |
| POST `/api/labels/ensure/` | 같은 이름을 재사용하거나 생성, 200/201 | AddLabel + ViewLabel |
| GET `/api/labels/<id>/` | 라벨 상세 | ViewLabel |
| PUT/PATCH `/api/labels/<id>/` | 전체/부분 이름 수정 | ChangeLabel |
| DELETE `/api/labels/<id>/` | 라벨 삭제, body 없는 204 | DeleteLabel |

목록은 `{items, count, limit, offset}`이며 ID 순서다. `limit`은 기본 20, 범위 1..100이고 `offset`은 기본 0, 범위 0..2147483647이다.
두 값은 ASCII 10진 숫자로 받는다. `search`는 64 UTF-8 byte 이내의 literal name substring이며 빈 값은 필터를 적용하지 않는다.
Count는 검색 결과의 전체 개수다. Count와 items는 별도 조회이므로 동시 변경 시 페이지가 이동할 수 있다.
Unknown/duplicate parameter·잘못된 encoding·NUL·2048 byte를 넘는 query string은 data 조회 전에 400으로 거부한다.

쓰기 입력에는 name만 있다. 공백 제거 뒤 비어 있으면 거부하고 최대 64 Unicode code point를 허용한다.
Category와 id는 입력할 수 없다. PUT은 name이 필수이며 PATCH는 생략을 보존하고 빈 객체는 현재 scoped 라벨을 반환한다.
Category는 서버가 배정하고 저장 transaction에서 존재/범위를 확인한다. Form이 제외한 category도 공통 ORM의 전체 조합 검사에 포함한다.
사전 중복은 `__all__/unique_together`, 사전 조회 뒤 native 충돌은 `__all__/unique`다. 값이나 기존 행 식별자를 진단에 넣지 않는다.
Query/driver/취소·reload 오류는 rollback하고, rollback/commit 결과가 불명확하면 입력 오류나 성공으로 바꾸거나 자동 재시도하지 않는다.

Admin `/admin/labels/`는 같은 저장 경로와 개별 권한을 사용한다. Form의 name 외 입력은 거부하며 중복 시 안전하게 escape한 제출 원문을 유지한다.
모든 unsafe 요청에는 CSRF가 필요하다. Label view 권한은 선택한 Category의 식별자를 포함하며 별도 Category Admin의 ViewCategory를 대신하지 않는다.
OpenAPI의 `Label`, `LabelCreate`, `LabelUpdate`, `LabelPatch`, `LabelList`와 고정 ogen의 독립 client가 이 경로·페이지 범위·진단을 소비한다.

Label 확보는 일반 생성과 별개의 명시적 동작이다. `/api/labels/ensure/`는 같은 name 입력을 받아
`{"label": {"id": ..., "name": ..., "category": ...}, "created": true|false}`를 반환한다.
새 Label과 add 감사 기록이 함께 commit된 경우에만 201/true를 반환한다. 선택 Category에 이미 같은 이름이 있으면
그 ID와 기존 값을 200/false로 반환하고 감사 기록을 추가하지 않는다. Query parameter와 id/category 입력은 거부한다.
다른 Category의 같은 이름은 재사용하지 않는다. 일반 `POST /api/labels/`의 중복 거부는 유지한다.

`AdminRegistry`에 감사 callback을 제공하면 Label 목록에 **Find or create label** 링크가 나타난다.
GET/POST `/admin/labels/collection/ensure/`는 모델에서 파생한 name Form을 사용하며 빈 목록에서도 실행할 수 있다.
기존 객체의 ID나 revision을 요구하지 않는다. AddLabel·ViewLabel, Admin의 staff/site 접근과 deny overlay를 확인하며
POST는 CSRF 검증 뒤에만 업무 transaction으로 들어간다. 일반 `Registry()`에는 이 감사 의존 동작을 등록하지 않는다.
성공은 서명된 목록 알림으로 redirect하고 생성과 기존 객체 재사용을 구분한다. 입력 오류는 같은 Form에 안전하게 표시한다.

두 표면은 같은 업무 함수를 호출한다. 요청에서 승인한 불변 principal의 두 권한과 현재 Category를 부모 transaction에서
다시 확인하고, `(category, name)` 조회와 명시적인 생성 입력을 결합한다. `GetOrCreate`는 부모의 savepoint를 사용하며
실제 INSERT unique 충돌을 확실히 rollback한 경우에만 한 번 다시 조회한다. Label 출력 준비와 새 객체의 감사 기록도
부모 transaction 안에서 끝낸다. 출력·감사·취소·scope 변경의 실패는 전체를 rollback하며, 불확실한 commit/rollback은
결과 객체나 성공 redirect를 게시하지 않는 서버 오류다. 자동 재시도하지 않는다. 확정된 commit 뒤 도착한 취소로 성공을 번복하지 않는다.
같은 Runtime/DB coordination 규약을 따르는 요청의 직렬화와 이를 우회하는 직접 SQL은 구분한다.
`LabelEnsureResult` schema와 독립 client는 두 성공 상태·필수 bool·기존 ID 재사용을 소비한다.

TicketLabel은 `0019_ticket_label`이 추가하는 명시적 연결 모델이다. ticket/label FK는 CASCADE이고 `(ticket, label)`은
이름 있는 고유 제약이다. Category를 복사해 저장하지 않고, 양쪽 endpoint에 서버가 배정한 Category가 모두 선택 범위인지 확인한다.
직접 ORM으로 만든 잘못된 교차 Category 연결도 Admin/API에서 조회·수정·삭제 대상으로 노출하지 않는다.

| 경로 | 동작 | 권한 |
| --- | --- | --- |
| GET `/api/ticket-labels/` | 연결 페이지, 양쪽 endpoint ID만 표시 | ViewTicketLabel |
| POST `/api/ticket-labels/` | 두 키의 연결 생성, 201 | AddTicketLabel + ViewTicket + ViewLabel |
| GET `/api/ticket-labels/<id>/` | 연결 상세 | ViewTicketLabel |
| PUT/PATCH `/api/ticket-labels/<id>/` | 두 키 전체/부분 수정 | ChangeTicketLabel + ViewTicket + ViewLabel |
| DELETE `/api/ticket-labels/<id>/` | 두 endpoint를 보존하고 연결만 삭제, 204 | DeleteTicketLabel |
| DELETE `/api/tickets/<id>/` | 티켓과 그 연결 삭제, 라벨 보존, 204 | DeleteTicket |

연결 목록은 Label과 같은 `{items, count, limit, offset}`·numeric 범위를 사용하며 검색 매개변수는 받지 않는다.
Admin `/admin/ticket-labels/`도 검색 없이 목록을 제공한다. 추가/수정은 두 권한을 모두 확인한 뒤 Category 내 Ticket subject와
Label name의 선택지를 구성한다. 저장 transaction에서 두 endpoint와 전체 조합을 다시 확인한다. 빈 PATCH와 self update도
범위를 확인하며 UPDATE는 실행하지 않는다. PUT은 두 키가 필수다. id/category는 입력할 수 없다.
API 인증·CSRF는 한 번 실행하고 mutation 권한, ViewTicket, ViewLabel을 순서대로 모두 검사한 뒤 body나 DB에 접근한다.
OpenAPI는 primary `x-godj-permission`과 AND 조건인 `x-godj-additional-permissions`를 같은 선언에서 게시한다.
조회/연결 해제에는 링크 권한이 관계 ID를 포함하며 endpoint 이름을 공개하지 않는다. 기존 ServiceReport의 ID 입력 권한은 유지한다.

양쪽 ID는 양수여야 한다. 없거나 선택 범위 밖이면 해당 `ticket/invalid_choice`, `label/invalid_choice`를 값 없이 반환한다.
조합 중복은 `__all__/unique_together`, 사전 검사 이후 native 충돌은 `__all__/unique`다. 취소·조회/저장/재조회 실패·불확실한
commit/rollback을 성공이나 입력 오류로 바꾸지 않으며 자동 재시도하지 않는다. 일반 쓰기는 같은 transaction에서 검사하지만
외부의 직접 ORM/SQL Category 재할당에 대한 영구적인 cross-table 제약이나 잠금을 추가하지 않는다.

Ticket/Label 삭제는 공통 관계 삭제기를 사용한다. 연결 행만 CASCADE로 정리하고 반대 endpoint와 다른 연결은 유지한다.
ServiceReport가 Ticket을 보호하면 연결 삭제 전에 전체 삭제를 거부한다. API의 확정된 보호 결과는 400 `__all__/protected`이며
rollback 불확실성이나 취소와 함께 온 보호 오류는 500이다. 모든 unsafe 요청에 CSRF가 필요하다.
`TicketLabel`, `TicketLabelCreate`, `TicketLabelUpdate`, `TicketLabelPatch`, `TicketLabelList`와 독립 ogen client가 CRUD·권한·CSRF·
양쪽 삭제·PROTECT·endpoint 보존을 소비한다. Ticket.labels는 같은 through를 사용하는 일반 ManyToMany 선언과 Set을 소비한다.

Ticket의 `labels`는 선택적인 non-null int64 JSON 배열이다. POST 생략은 빈 집합, PUT/PATCH 생략은 기존 집합 보존,
명시적 `[]`는 전체 해제다. 순서와 중복 입력은 허용하지만 저장·응답은 ID 오름차순의 중복 없는 집합이다.
응답에는 항상 `labels`가 있고 빈 집합은 `[]`다. 숫자 문자열·소수·bool·null·중첩 배열은 거부한다.
POST는 AddTicket+ViewLabel, PUT/PATCH는 ChangeTicket+ViewLabel을 본문 해석 전에 확인한다.
전체 desired set의 모든 양수 키가 선택 Category에 있어야 하며 하나라도 부적합하면 scalar와 전체 집합을 모두 보존한다.
유지한 연결은 ID를 유지한다. 연결 자체 CRUD의 Add/ChangeTicketLabel 권한과 owner 편집 권한은 별도다.

Admin의 선택 누락은 빈 집합으로 해석한다. Bounded HTML 본문에서 CSRF 토큰을 추출한 뒤 인증·권한을 확인하고,
그 이후에 필드 검증·후보/owner 조회를 실행한다. JSON API의 본문 읽기 전 검사와 이 transport 순서를 혼동하지 않는다.
두 경로 모두 저장 transaction에서 현재 owner Category·전체 후보 Category와 admitted principal의 쓰기 권한을 다시 확인한다.
Scalar·collection Set·실제 응답 재조회/encode를 같은 RelationAtomic에 묶어 늦은 실패·취소를 rollback한다.
Commit/rollback outcome unknown은 성공이나 확정된 400/404로 바꾸거나 재시도하지 않는다.
Runtime을 통해 쓰는 두 연결의 전체 교체는 같은 DB fence를 따른다. 비협력 raw writer까지 이 보장을 확장하지 않는다.

```sh
go test ./examples/helpdesk -count=1
go run ./cmd/godj generate --check --project examples/helpdesk/godj.toml
```

선언 runner는 생성·makemigrations 명령을 제공한다. `modeldef` 변경 후 같은 생성 명령에서 `--check`를 빼면 생성물을 갱신한다.
`go run ./cmd/godj makemigrations --project examples/helpdesk/godj.toml`은 선언과 `migrations/`의 차이를 작성한다.
`0001_initial`부터 `0004_ticket_due_at`까지 보존한다. `0005_alter_ticket_priority`는 선택값을 추가하고 `0006_alter_ticket_priority`는 표시명·순서를 변경한다. 두 metadata 변경은 DDL을 만들지 않는다. `0007_ticket_reviewed`와 `0008_ticket_service_on`은 각각 nullable Boolean과 Date를 추가하고 기존 행은 null로 유지한다. `0009_ticket_service_at`, `0010_ticket_elapsed`, `0011_ticket_effort`, `0012_ticket_expected_cost`는 Time·Duration·Float·Decimal을 추가하며 기존 행은 null이다. `0013_alter_ticket_expected_cost`는 기존 비용을 보존하면서 Decimal(12, 2)를 Decimal(14, 2)로 늘린다. 새 한도에만 맞는 값이 남아 있으면 역방향 변경은 반올림 없이 실패하고, 명시적으로 값을 수정한 뒤 다시 시도할 수 있다. `0014_ticket_external_reference`는 UUID, `0015_ticket_external_payload`는 JSON을 추가하며 기존 행은 null이다. `MigrationSources()`의 전체 source를 loader에 전달한다.
테스트는 0001의 기존 행, 필드 추가/역방향, 0004의 범위 밖 priority 값에 0005·0006 적용/역방향/재적용, Form/Admin/API의 선택값 검증과 기존 값 보존을 다룬다.
`0016_alter_ticket_external_reference`는 UUID 참조에 고유성을 추가한다. 기존 중복이 있으면 데이터와 적용 이력을 보존한 채
실패하며 명시적으로 중복을 정리한 후 재시도한다. 테스트는 실제 양 DB의 실패·수정·재시도·재접속과 Admin/API 중복 거부를 확인한다.
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


Identity 전환 소비자는 기존 system `0001` operator의 권한 CAS를 먼저 실행한 뒤 새 migration graph와 `AdoptOperator`를 명시적으로
적용한다. 재접속한 Admin/API 및 collection 동시 수정 runtime은 `OpenIdentity`를 사용한다. Staff는 이전 입력에 명시하며 기존
permission에서 추론하지 않는다. 호스트 선언은 재사용 identity 앱의 전체 관계·삭제 graph를 포함하고 외부 앱 파일은 생성하지 않는다.

## Admin에서 티켓과 보고서를 함께 편집하기

보고서 저장은 공통 `SavePlan`의 변경/추가/삭제 선택에 기존 권한·고유값·선택 열 쓰기·감사 기록을 연결한다. Callback의 오류를 그대로 outer transaction에 전달한다.

기존 `Registry()`의 독립 CRUD 화면 외에, `AdminRegistry`로 티켓의 서비스 보고서를 같은 화면에 연결할 수 있다.

```go
registry, err := application.AdminRegistry(helpdesk.AdminConfig{
    AppendAudit: runtime.AppendAudit,
})
```

이 registry를 `admin.SiteConfig.Registry`와 `admin.SiteAllowedNextPaths`에 함께 전달한다. `AppendAudit`는 전달받은
session에만 기록해야 한다. 별도 transaction을 열면 부모·자식과 감사 기록의 원자성이 깨진다. Registry 생성은 I/O를 하지
않고 기존 Application/Registry를 변경하지 않는다. 공통 계약은 [Admin inline](../../admin/inlines.md)을 따른다.

새 티켓과 optional 보고서를 한 POST로 만들거나 기존 티켓·보고서를 함께 수정/삭제한다. OneToOne의 추가 행은 한 개이며,
부모 key/category·자식 current 집합·권한·고유성을 쓰기 transaction에서 확인한다. 기존 보고서를 삭제하면서 새 보고서를
같은 부모에 추가하는 요청도 삭제 전 DB 고유성 검사를 우회하지 않는다. 부모·자식·양쪽 감사 기록은 같은 transaction을
사용하고 뒤의 실패는 모두 롤백한다. Report 삭제는 incoming relation이 없는 이 모델의 typed Delete를 사용한다.
긴 보고서 본문을 감사 label로 복사하지 않고 안정된 `Service report` 표시명을 사용한다.

View/Add/Change/Delete는 별도 권한이다. 조회 전용 행의 위조 입력은 기존 값을 바꾸지 않으며 Add-only 사용자는 기존
보고서를 읽을 수 없다. 저장 당시의 부모/자식 scope가 달라진 경우 다시 검증한다. 현재 인증 모델은 요청에서 받아들인
immutable principal과 deny overlay를 유지하며 계정의 권한 변경은 후속 세션에 적용된다. Request 중간의 별도 계정 재인증이나
revision 없는 모델의 일반적 동시 편집 충돌 감지를 이 예제에서 보장한다고 주장하지 않는다.

## 외부 참조 URL

Ticket의 `external_url`은 nullable/blank `URLField`이며 기본 저장 길이는 200자다. `0022_ticket_external_url`은
기존 행을 NULL로 유지하고 역사 migration을 보존한다. Admin은 URL 입력을 렌더링하고 `example.com/path`나
`//example.com/path`를 `https://example.com/path`로 정리한다. Admin 입력 Form은 `novalidate`로 브라우저의 선행 문법 차단을 끄고
서버의 required·정규화·문법 검증을 따른다. 빈 Admin 입력은 NULL로 저장한다.
JSON 생성/수정은 공백을 정리하지만 스킴이 없는 주소를 거부한다. `""`·`null`·생략은 각각 빈 문자열·NULL·기존 값 보존이다.
일반 ORM이 저장한 문법상 잘못된 문자열도 조회/출력하며 이를 활성 HTML 링크로 바꾸지 않는다.
기존 Category 범위·현재 쓰기 권한·CSRF·transaction과 rollback 규칙을 동일하게 적용한다.


### 외부 payload 지문

`external_payload_digest`는 기본 비편집 BinaryField다. 응답과 Admin 조회에는 padded base64로 표시하고,
API의 직접 제출은 `read_only`, Admin의 입력 allowlist 우회는 요청 오류로 거부한다. 서버의 일반 ORM 할당은 가능하다.
새 `0023_ticket_external_payload_digest`는 기존 행에 NULL을 추가하며 이미 있는 JSON의 지문을 임의로 채우지 않는다.
이후 앱이 실제 Ticket scalar를 저장하면, 같은 transaction의 행 잠금 아래 DB에 저장된 JSON을 다시 읽고 SHA-256을 계산한다.
PostgreSQL JSONB의 숫자 정규화가 반영된 값을 사용하며 JSON null과 SQL NULL을 구분한다. SQL NULL에는 지문이 없다.
읽기·동일 값 제출·관계만의 변경은 숨은 Ticket 쓰기를 만들지 않는다. 지문 실패·출력 검증·취소는 앞선 payload/관계 저장과 함께 rollback한다.
이 값은 관찰용 지문이며 인증 서명이 아니다. 앱 밖의 서버 ORM 쓰기는 자신의 파생값 갱신을 소유한다.
