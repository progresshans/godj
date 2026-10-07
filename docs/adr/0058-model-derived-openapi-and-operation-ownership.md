# ADR-0058: 모델 기반 OpenAPI와 operation 소유권

- 상태: Accepted
- 날짜: 2026-09-12
- 관련 작업: [GDJ-0070](../../work/0070-model-derived-openapi.md), [GDJ-0071](../../work/0071-api-schema-identity-and-generated-client.md), [GDJ-0113](../../work/0113-typed-api-response-shapes.md), [GDJ-0114](../../work/0114-typed-query-parameters.md), [GDJ-0115](../../work/0115-typed-json-body-inputs.md), [GDJ-0116](../../work/0116-typed-endpoint-declarations.md), [GDJ-0117](../../work/0117-typed-path-and-ordered-inputs.md)
- 보완: [JSON API](0046-json-serializer-and-session-authenticated-article-api.md), [인증 profile](0049-first-party-bff-and-bearer-api-authentication.md)

## 맥락과 선택

Article client가 요청과 응답 형태를 알려면 handler와 serializer 설정을 함께 읽어야 했다. 별도의 수동 OpenAPI 파일은
field·route·permission 변경에서 쉽게 어긋난다. [개발 기준](../DEVELOPMENT_CRITERIA.md)에 따라 기존 모델과 실제 endpoint
선언에서 읽을 수 있는 문서를 만든다. 실제 소비자에서 필요한 typed 입력/출력 연결은 같은 선언을 사용하며,
DTO 생성·DI·범용 viewset은 각각 필요한 업무와 의미를 확인해 설계한다.

## 결정

`api/openapi`는 immutable `Schema`와 `Document`를 제공한다. `RequestSchema`는 실제 serializer Spec의 writable allowlist,
null·required·default와 full/partial 차이를 투영한다. `ModelResponseSchema`는 ModelEncoder가 출력하는 전체 선택 필드의
존재·타입·null·readOnly와 encoder가 검사하는 문자열 최대 길이를 투영한다. 응답 encoder가 적용하지 않는 입력
trim·blank·default 규칙은 응답에 붙이지 않는다.
모델 의미의 원본은 Schema IR이고 이 기능은 기존 `serializers.FromModel` 경계를 소비한다.

`IntegerRange(minimum, maximum)`는 signed int64 안의 inclusive 범위를 표현하고 역전된 범위를 설정 오류로 거부한다.
Category Label의 limit/offset 페이지는 이 schema를 사용해 실제 parser와 client의 numeric bounds를 함께 명시한다.
Query string의 반복 parameter·ASCII 숫자 표기 같은 lexical 정책은 endpoint parser가 소유한다.

JSON Schema는 GoDj parser와 완전히 같은 검증기가 아니다. Trim 이후 제약은 `x-godj-normalization`에 기록한다.
Canonical 정수 표기, duplicate member, NUL·문자열/전체 body byte·depth 제한과 application validation은 runtime이 소유한다.
OpenAPI를 생성하거나 문서를 검증했다고 해당 안전성 검증을 생략하지 않는다.

Operation은 실제 `web.Route`, 같은 handler에 적용한 permission, query·body·response 선언을 함께 갖는다.
Article과 Helpdesk는 한 번 구성한 API의 이 원본에서 route를 반환하고 문서를 만든다. 문서 생성은 handler나 Authentication.Require를 다시 실행하지 않는다.
Web의 route compiler로 경로·이름·route language 충돌을 검사하고 OAS의 template/operation 고유성도 검사한다.
최종 application이 installed namespace·다른 route와의 충돌, middleware·handler와 문서의 일치를 보장한다.
`DescribeRoutePath`의 parameter kind를 소비해 int64와 bounded str을 구분한다. String의 code-point 길이·문자 grammar,
별도 `x-godj-max-bytes`와 router의 최종 lexical/전체 URL 한도를 명시한다. 같은 template의 method별 converter도 유지한다.
문자열과 숫자의 겹치는 route나 서로 다른 placeholder 이름으로 같은 OAS path shape를 만드는 선언은 거부한다.
Bounded 문자열 경로의 의미는 [ADR-0045](0045-closed-parameterized-routing-and-reverse.md)를 따른다.

인증은 실제 adapter가 선택적으로 제공하는 공개 transport description에서 가져온다. Session unsafe operation은 session cookie,
CSRF cookie, masked header를 같은 security requirement 안의 AND로 표현한다. Safe 응답의 새 CSRF header는 선택적인 응답
metadata다. Bearer는 HTTP bearer만 표시하고 JWT 형식·issuer·cookie fallback을 추측하지 않는다.
인증 response header는 profile이 소유해 caller의 중복·덮어쓰기를 거부한다. Credential·token·key·verifier 내부 설정은 포함하지 않는다.
`AuthenticatedOnly`는 `api.PrincipalAuthentication`의 명시적 capability를 요구하고 모든 permission 필드와 배타적이다.
빈 permission만으로 인증 전용 route를 추론하지 않는다. 문서는 `x-godj-authenticated-only: true`를 출력하며,
Session 인증/CSRF의 403은 유지하고 Bearer 인증 전용 route에는 permission 403을 자동 추가하지 않는다.
Handler 구성/현재 principal resolution은 해당 authentication adapter가 소유한다.
`CSRFOnly`는 Session profile의 명시적 `api.CSRFAuthentication` capability를 요구한다. Principal/model permission과
혼합하지 않고 login session의 읽기·touch·cleanup·인가를 실행하지 않는다. Unsafe 요청은 handler 전에 origin과 CSRF pair를
검사하고, safe handler 응답은 masked token을 게시한다. 문서는 `x-godj-csrf-only: true`와 safe bootstrap의 빈 security를
출력하며 인증 403을 임의로 추가하지 않는다. Unsafe operation은 CSRF cookie/header의 AND와 403을 유지한다.

`SessionCookieRequired`는 CSRF-only handler가 별도의 서버 proof를 검사할 때 opaque session cookie의 전송을 명시한다.
`x-godj-session-cookie-required: true`와 해당 security scheme을 출력하지만 authenticated principal을 의미하지 않는다.
Proof 자체의 현재 상태 검사는 application handler가 소유한다. 이 구분으로 reset request는 익명으로 호출하고,
reset proof/completion은 같은 생성 client가 보관한 서버 proof cookie를 전송할 수 있다. Safe 요청에 CSRF cookie가
없으면 새 cookie/header pair가 나올 수 있으므로 이후 unsafe 요청은 마지막으로 받은 pair를 사용한다.
Description을 제공하지 않는 custom authentication은 수동 handler에 사용할 수 있으나 문서 생성은 명시적으로 실패한다.
Typed endpoint를 포함한 Article/Helpdesk API는 같은 transport를 준비 때 확인하므로 API 초기화부터 description을 요구한다.

`New`는 전체 선언을 확인한 뒤 OpenAPI 3.1.1 JSON을 결정적으로 게시한다. 반환 byte·route slice는 복사하고 schema 값은
기존 immutable JSON 표현을 사용한다. 오류 시 부분 문서를 반환하지 않는다. 문서 조회 route와 공개 권한은 application이 선택한다.
Article은 authenticated loopback site의 `GET /api/openapi.json`에 Article view 권한을 적용한다.

## Schema 정체성과 정책의 공유

GDJ-0113의 `api/output`은 업무 DTO의 공개 property와 typed getter를 한 번 선언하는 닫힌 `Shape[T]`를 제공한다.
`New`가 nested 선언·nil reader·중복 이름·자원 설정·schema graph를 검증한 뒤 불변 `Output[T]`를 반환한다.
Object의 필드는 항상 required이고 pointer Nullable은 nil/null과 값을 구분하며, nil slice Array는 빈 배열이다.
int64 범위와 배열 길이는 같은 선언에서 실제 출력과 schema에 적용한다. 잘못된 DTO·getter/field·출력 타입은 compile 오류다.
임의 schema와 encoder를 짝짓거나 struct reflection으로 공개 범위를 늘리는 escape hatch는 제공하지 않는다.

`Model`은 `ModelEncoder.Spec()`과 동일 encoder의 출력 경로를 함께 소비한다. 모델 의미의 새 원본이나 별도 Spec은 없으며
입력 default·trimming·choices를 출력에 다시 적용하지 않는다. 허용한 필드·nullable·길이·codec·computed field를 보존한다.
Named Shape 자체를 재사용할 때 component를 공유하고 별도 identity의 같은 이름은 내용이 같아도 거부한다.
`Components`로 여러 prepared output의 component를 모으며 OpenAPI의 기존 closed graph·깊이·개수·byte 검사를 재사용한다.
수동 schema·다른 API와의 최종 충돌은 기존 document 조합이 소유한다.

출력은 요청별 lazy `serializers.Projection`으로 방문하고 기존 JSON writer 하나가 전체 bytes/depth/value 예산을 계산한다.
컨테이너 길이·남은 값/깊이 예산을 먼저 확인하며 중첩 모델, collection과 opaque JSON도 같은 예산에 속한다.
Context 취소·reader/값/자원 오류에는 부분 bytes·HTTP 응답을 반환하지 않는다. 출력 getter는 순수하며 호출 동안 DTO와
가변 필드를 안정적으로 유지할 책임은 application에 있다. 선언과 완성된 결과는 호출자 slice·DTO 변경에서 분리한다.
이 연결은 인가·읽기 snapshot·transaction·commit 전 출력 검증을 옮기거나 추측하지 않는다.

Helpdesk의 집계 요약과 Ticket/Category 상세가 첫 소비자다. Ticket은 IR에서 온 encoder를 재사용하고 CategorySummary는
기존 두 필드의 업무 projection을 유지한다. Typed JSON body는 아래 GDJ-0115에서 연결한다.
Endpoint 연결은 아래 GDJ-0116이 소유하며 viewset·추가 출력 primitive·optional omission은 별도 범위다.
이 설계의 채택과 환경별 실행 완료는 구분하며 검증 결과는 TEST_EVIDENCE 한 곳에 기록한다.

GDJ-0114의 [typed query 입력](../../api/parameters/README.md)은 닫힌 scalar codec·presence policy·typed DTO setter에서
runtime 변환과 OpenAPI parameter를 함께 준비한다. Canonical int64와 선행 0을 허용하는 unsigned digits를 명시적으로
구분하며 문자열은 trim하지 않는 UTF-8 byte/empty/NUL 정책을 갖는다. Required·검증된 omission default·Optional pointer는
서로 다른 선언이다. Query 부재를 JSON null로 바꾸지 않으며 일반 JSON Schema의 값 범위와 URL lexical/byte 정책을
별도 metadata로 표현한다. 기본값은 실제 입력 도메인에서 검증한 뒤 schema default로 내보낸다.

초기화한 선언은 불변이고 1 MiB 이하 raw query·128 parameters로 제한한다. 이름과 URL 구조를 먼저 확인하고 scalar는
선언 순서로 검증한다. 전체 검증이 끝나기 전에는 setter를 호출하지 않으며 실패에는 부분 DTO를 노출하지 않는다.
Setter는 순수하고 DTO 포인터를 보관하지 않는다. 반환 DTO·Optional 값과 parameter slice는 각 호출자가 소유한다.
순수 parsing에 I/O를 추가하지 않고 실제 request/body·context·인가·DB/transaction 수명은 해당 업무 owner에 남긴다.

Helpdesk 요약은 기존 canonical 정수·진단 순서·snapshot을, Label/티켓–Label 목록은 기존 digits·default·전체 query 오류와
count/items 별도 읽기를 유지한다. 인증 이후 parsing 순서와 Category/관계 scope도 유지한다. Ticket 본문 검색의 raw-segment
parser는 오류/semicolon 의미가 달라 이번 연결에 포함하지 않는다. Header/path binder나 endpoint 전체 구성을
완료한 것으로 계산하지 않으며 별도 호환 profile을 만들지 않는다.

GDJ-0115의 [typed JSON 입력](../../api/input/README.md)은 같은 `serializers.Spec`의 full/partial 검증 결과와
`RequestSchema`를 `input.Body[T]`로 연결한다. 모델 의미는 `FromModel`이 Schema IR에서 가져온다. Writable field마다
닫힌 typed codec과 `Presence[V]` setter를 연결하고 준비 시 누락·중복·unknown/read-only·Kind/nullability 불일치와
nil/zero 선언을 거부한다. Read-only field의 입력 거부는 원 Spec에 유지한다. String/Email/URL/Slug·수치·temporal·
Decimal·UUID·Binary·JSON·integer-list는 정리된 값의 Go type만 옮기며 새로운 JSON 입력 정규화 규칙을 만들지 않는다.

Full의 default는 effective present 값이고 Partial 생략에는 default를 적용하지 않는다. Nullable codec은 `*V`로
null을 드러내며 Presence의 bool과 함께 생략을 구분한다. 기존 omission default를 choices·Email 등 제출 규칙으로
재검증하지 않는다. JSON literal-null default와 model-null, 원 진단/unknown 이름 순서도 Spec.Bind의 의미를 보존한다.
전체 검증/변환 성공 전에는 setter를 호출하지 않는다. Setter는 Spec 순서로 실행하며 순수·동시 사용 안전·DTO pointer
비보관 계약을 갖는다. Nullable pointer와 integer-list는 요청마다 소유하고 불변 scalar text는 안전하게 공유한다.

실제 body I/O는 공통 Parser의 media·byte·JSON aggregate 예산을 사용한다. 읽기 전/사이·decode 이후와 typed 검증·
변환·반환 경계에서 context를 확인한다. Reader의 원 오류와 취소를 보존하고 이를 client validation 400으로 바꾸지
않는다. Borrowed body를 닫거나 detached goroutine을 시작하지 않으며 진행 중인 Read 해제는 transport/body owner의
책임이다. 늦은 취소에도 부분 DTO를 반환하지 않지만 setter의 외부 부작용을 취소하는 transaction은 추론하지 않는다.
Label과 ServiceReport의 create/PUT/PATCH·ensure/save는 이 연결을 사용한다. 인가·CSRF·대상/관계 scope·고유성·저장과
audit/출력 원자성은 기존 업무가 소유한다. 경로·배열 연결은 아래 GDJ-0117/0118이 다루고 header·자동 viewset은 후속 범위다.

GDJ-0116의 [typed endpoint](../../api/endpoint/README.md)는 같은 Query/Body·Output과 명시한 admission·상태에서
typed handler와 OpenAPI operation을 함께 준비한다. Route와 전체 schema graph는 기존 OpenAPI/Web validator로 검사하며
초기화에서 getter·setter·handler·I/O를 실행하지 않는다. 실제 adapter에 All/Any·Authenticated·익명 CSRF를 연결하고
같은 detached permission snapshot을 문서에 쓴다. 실제 AuthenticationDescriber와 필요한 adapter capability를 요구하며
알 수 없는 profile·없는 capability·zero 선언은 준비 오류다.
인증/CSRF/인가가 입력을 읽기 전에 끝나며 입력별 bounded parsing과 기존 client error envelope를 보존한다.

`Output.Prepare`는 현재 context와 전체 응답 budget으로 encode한 완성된 `Prepared[T]`를 만든다.
준비 결과는 DTO를 보관하지 않으며 구조에도 T를 결합해 명시적 다른 DTO 타입 변환을 compile로 거부한다.
별도 비영 크기 identity가 정확한 Output과 예산에 결합한다. Output 복사본은 identity를 공유하지만 같아 보이는 독립
선언에서 만든 결과는 거부한다. Endpoint는 준비 응답의 owner·선언한 JSON 2xx 상태·정확한 단일 Content-Type을 검사한다.
204/205·streaming·다른 성공 DTO union은 이 연결에서 지원하지 않는다. 추가 header 정책은 Web/application/adapter가 소유한다.

Handler는 transaction 안에서 출력 준비를 끝내고 성공한 commit 뒤에만 결과를 반환한다. Endpoint는 나중에 encode하지
않으며 handler가 보고한 오류/취소에는 준비 결과를 버린다. Handler의 nil 오류는 확정된 결과를 뜻하며,
그 뒤의 request context 취소만으로 이를 실패로 바꾸지 않는다. 이미 확인된 commit을 불확실한 쓰기나 재시도 신호로
되돌리지 않는 기존 transaction 계약을 보존한다. `Reject`의 직접 반환만 선언한 4xx로 바꾸고 wrapped/joined 오류를
풀어 expected rejection으로 만들지 않는다. 취소와 동시에 일어난 입력/업무 오류는 내부 cause를 함께 보존한다.
Helpdesk 요약의 읽기 snapshot과 Label ensure의 reload·출력·audit·commit 순서가 이 연결을 소비한다.
각 operation과 component의 최종 충돌은 application 문서에서 검사하며 동일 Input/Named Shape만 component identity를 공유한다.
경로와 입력 결합은 GDJ-0117, 배열 body는 GDJ-0118이 연결하며 header·일반 CRUD/viewset은 후속 범위로 유지한다.

GDJ-0117은 기존 router의 Int64/String accessor를 닫힌 `Input[int64]`/`Input[string]`으로 연결한다.
새 parser나 path schema를 만들지 않는다. Typed path가 있으면 route의 모든 parameter를 같은 이름·converter로
정확히 한 번 선언해야 하며 중복·누락·불일치는 startup 오류다. 0 이상 Int64의 양수 PK 규칙과 객체 존재 여부는 업무가 소유한다.
문자열은 이미 decode된 하나의 bounded segment를 복사하고 재해석하지 않는다.

`Sequence[A,B]`는 입력을 선언한 순서로 읽고 전부 성공한 `Pair[A,B]`만 handler에 넘긴다. Query parser/body는
각각 하나이며 중복 읽기나 NoQuery/parameter 충돌은 준비 때 거부한다. `Resolve[A,B]`는 앞선 닫힌 입력을 받은
명시적 업무 조회/검증 단계다. Admitted Principal과 borrowed request/context를 전달하고 startup에서는 실행하지 않는다.
Callback은 body를 소비하거나 request를 보관하지 않으며 쓰기를 commit하지 않는다. 최종 handler가 transaction을 소유한다.
실패·진단·취소는 뒤 단계를 중단한다. Resolve의 직접 `Reject`만 선언한 client 실패이고 일반 오류나 callback의 직접
parser 오류를 닫힌 입력 parser의 400/413/415로 오인하지 않는다. 원 오류/cancellation cause는 내부에서 유지한다.

조합 깊이는 64, 총 실행 단계는 4096으로 제한하며 반복 공유한 단계도 매 실행을 센다. 작은 DAG를 중복 호출해
실행량이 급증하는 조합도 준비 때 거부한다. New/Collect가 모은 입력 graph는 4096 identities로 제한한다. 공통 JSONBody의 identity를
Sequence/Resolve/NoQuery를 넘어 추적하므로 같은 입력을 독립 pipeline에 넣어도 component 하나를 공유한다.
같은 이름의 별도 선언은 계속 거부하며 OpenAPI의 component/byte 제한은 별도로 유지한다.

Article PUT/PATCH는 DRF의 기존 대상 확인 우선을 유지하고 Helpdesk Label 수정은 양수 ID 확인 뒤 body를 검증한다.
두 업무는 transaction 안에서 최종 응답을 준비하고 commit 확인 후만 반환한다. Article의 neutral repository는
`UpdateAndPrepare`/`PatchAndPrepare`로 같은 변경 kernel을 사용하며 detached row/changed fields를 준비 callback에
전달한 뒤 기존 mutation hook을 실행한다. No-op도 출력 준비는 수행하지만 mutation hook과 DML은 생략한다.
출력 실패·hook 실패·취소는 rollback하고 nil/반복/미완료·삼켜진 callback은 성공 결과를 내보내지 않는다.
확인된 commit 뒤 취소는 성공을 번복하지 않으며 불확실한 transaction/rollback과 joined 오류는 404로 바꾸지 않는다.
Model/IR·full/partial default/nullable와 API의 공개 operation/schema 의미는 기존 선언에서 이어받는다.

GDJ-0118의 `input.ListBody[T]`는 같은 Body/Spec의 full/partial 정책과 typed 변환을 객체 배열에 적용한다.
전체 parser 예산과 각 compact item 예산을 구분하며 단건 parser의 크기를 배열 전체에 재사용하지 않는다.
선언한 count 0..1024 범위와 item schema를 runtime/OpenAPI에 함께 사용하고 업무는 더 좁은 범위를 선택한다.
Field 진단은 모든 행에서 입력 순서로 수집하며 `index`는 바깥 행, 기존 integer-list의 `index`는 `item_index`다.
단건 진단은 기존 위치를 유지한다. 최대 16,384개 진단 뒤에도 전체 count·문자열/위치 유효성과 취소를 확인하고,
초과에는 count를 포함한 전체 거부를 반환한다. 잘린 오류 목록이나 부분 DTO slice를 완성된 결과로 게시하지 않는다.

선택적인 row validator는 Spec/typed 변환이 성공한 행에서만 실행하며 순수·동시 사용 안전·비변경·비보관 계약이다.
Setter와 validator는 I/O/commit을 하지 않는다. 다른 행이 invalid여도 유효한 행의 callback은 실행될 수 있으며,
내부 오류나 취소는 뒤 행을 중단한다. Callback의 parser 형태 오류가 요청의 client parser 오류로 바뀌지 않는다.
`endpoint.JSONListBody`는 bare JSONBody의 named item identity를 공유한다. 감싼 정책을 버리는 파생은 거부하고
NoQuery/Sequence/Resolve를 배열 바깥에 적용한다. 입력 진단과 직접 Reject는 같은 endpoint ErrorLimits를 사용한다.
명시한 SummarizeValidationErrors는 validation의 resource overflow에만 작은 표준 오류/count를 적용하며,
내부/잘못된 진단·rollback/unknown outcome과 다른 오류 code를 400 요약으로 바꾸지 않는다.

Helpdesk는 단건과 여러 티켓 생성에 같은 typed Body를 연결한다. 기존 1..40 rows·항목/전체 예산, 현재 Category/labels,
고유성·저장 JSON/digest·전체 출력·audit·commit 의미를 유지한다. 단건도 하나의 prepared transaction owner에서
최종 출력을 준비한다. Article의 공개 `BulkCreateAndPrepare`는 모든 후보와 전체 입력 집합/DB의 slug 고유성을
INSERT 전에 확인하고 20행 native batches를 하나의 borrowed transaction에 넣는다. SQL NULL은 서로 충돌하지 않고
non-null 빈 slug는 고유성 값이다. 반환 key/count를 확인한 뒤 최종 행을 입력 순서로 다시 읽고 detached 전체 결과를
준비한 다음 기존 mutation hook을 한 번 실행한다. API는 Session/Bearer의 Add permission과 필요한 CSRF 뒤 같은
ArticleCreate 입력으로 1..40개를 생성한다. 실패·출력/hook 오류·취소·잘못된 callback 수명과 unknown에는 부분 결과를
게시하지 않으며 자동 재시도하지 않는다. 이는 Article application의 bounded atomic bulk 정책이며 DRF의 기본 list
serializer나 ORM BulkCreate 자체에 per-object save/hook 정책을 추가하는 뜻이 아니다.

`NamedSchema`와 `Ref(name)`은 caller가 선택한 명시적인 타입 정체성을 보존한다. 구조가 같아도 자동으로 합치지 않고
local component 참조를 그대로 출력한다. Component 이름은 1–128 bytes의 `[A-Za-z0-9._-]+`이며,
전체 256개 중 `GoDjAPIError` 하나는 공통 오류가 소유한다. 같은 이름의 재선언, 미해결·원격 참조와 모든 순환은 거부한다.
참조를 포함한 경로는 schema 64 nodes, 각 inline schema는 4096 nodes로 제한하고 기존 JSON·전체 문서 byte budget도 적용한다.
Property 이름이나 default/annotation의 데이터 안에 있는 `$ref`를 참조로 오인하지 않는다. 초기화 이후 catalog는 불변이다.

`api.JSONPolicy`는 실제 middleware 묶음과 OpenAPI의 negotiation 설명이 공유하는 불변 설정이다. Zero policy는
406을 광고하지 않는다. Article은 같은 인스턴스의 middleware를 설치하고 Helpdesk는 기존처럼 policy를 설치하지 않는다.
Web route compiler가 정적 prefix의 전체/일부/미적용을 판정하며 한 dynamic route의 일부에만 적용되는 정책은 문서 생성에서
거부한다. Middleware·인증과 application이 공유하는 실패 status에는 application 전용 필수 header를 약속할 수 없다.

## 외부 생성 client 검증

`api/openapi/consumertest`는 별도 module의 고정된 ogen v1.24.0에서 Article Bearer·Session과 Helpdesk Session client를 생성한다.
입력 schema/config/tool/dependency lock과 생성 파일을 함께 관리한다. Framework runtime module에는 generator 의존성을 추가하지 않는다.
실제 API 문서와 입력 파일의 byte 일치, offline 재생성의 정확한 파일 집합·내용, 별도 module compile, 실제 HTTP 흐름과 최종 DB를
하나의 integration checkpoint가 검사한다. 부모가 race이면 consumer도 race로 빌드한다. 필수 check 누락·중복, 잘린 출력,
실패 종료와 consumer stderr를 성공으로 취급하지 않는다. 도구의 일반 진단은 실행 결과와 구분한다.

Article/Helpdesk Session 검증은 parent fixture의 session을 사용하고 Identity 관리 profile은 parent의 실제 HTTP login으로 준비한다.
`accountsession` profile은 child가 제품 `/account/login/` Form을 제출한 뒤 session cookie를 받아 generated CSRF/password
operation을 호출한다. 독립 모듈은 GoDj를 import하지 않는다. Header·cookie rotation·다른 session 거부·제품 logout과
부모의 최종 SQLite 상태를 검사하고 required `Set-Cookie` 및 synthetic 503/no-retry decoder를 별도로 확인한다.
같은 account profile은 익명 reset request에서 실제 Memory sender 메일을 읽고 제품 token entry/redirect로 proof를 얻어
generated proof/completion까지 호출한다. 메일 조회는 해당 부모 fixture만 제공하는 별도 capability 보호 경로이며
제품 route나 schema에 포함하지 않는다. 부모는 reset 뒤 password/revision/session/audit를 별도 runtime에서 확인한다.
생략/null/value·false, full request default, relation 범위와 권한·취소를 실제 서버에서 검증하고 int64 최대/overflow·잘못된
response 거부는 별도 wire fixture로 검사한다. 이는 고정된 한 Go generator와 명시한 흐름의 호환성 근거다.

Helpdesk의 nullable Boolean과 PUT/PATCH는 [GDJ-0086](../../work/0086-nullable-boolean-models.md)에서 연결한다.
`TicketUpdate`는 subject를 요구하고 생략한 closed의 false default를 적용한다. `TicketPatch`는 모든 필드의 생략을
보존하며 default를 적용하지 않는다. 생략한 nullable 필드는 양쪽 모두 기존 값을 유지하고 명시적 null은 값을 지운다.
입력 Boolean은 JSON true/false/null만 받는다. 응답의 reviewed는 항상 존재하며 Boolean 또는 null이다.
Helpdesk 수정 operation은 인증·CSRF·변경 권한, 양수 ID 확인, 입력 검증, transaction 내부 대상/category 확인과 변경 순서다.
따라서 유효한 양수 ID의 잘못된 입력은 대상 조회보다 먼저 400이 된다. 이는 해당 Helpdesk handler의 명시적 순서이며
기존 Article의 대상 확인 우선 규칙이나 프레임워크 전체의 generic update 정책을 바꾸지 않는다.

## 결과와 남은 범위

Article의 full/partial 입력, pagination, Location/Allow, Session/Bearer 오류와 HEAD/204·plain 500 표현을 client가 조회할 수 있다.
문서용 type/field 정의를 별도로 복제하지 않으며 기존 parsing·대상 확인·permission·transaction 순서는 유지한다.
HTML docs UI, 순환 schema·더 넓은 vocabulary/인증 profile, 배포형 SDK·다른 언어 generator 지원은 후속 소비자 요구로 선택한다.
이번 기능은 Django의 새 differential behavior contract를 주장하지 않는다.

규격 근거는 [OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)이다.
실행 범위와 외부 validator 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 둔다.
