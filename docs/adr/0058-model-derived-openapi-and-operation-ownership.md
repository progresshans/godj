# ADR-0058: 모델 기반 OpenAPI와 operation 소유권

- 상태: Accepted
- 날짜: 2026-09-12
- 관련 작업: [GDJ-0070](../../work/0070-model-derived-openapi.md), [GDJ-0071](../../work/0071-api-schema-identity-and-generated-client.md)
- 보완: [JSON API](0046-json-serializer-and-session-authenticated-article-api.md), [인증 profile](0049-first-party-bff-and-bearer-api-authentication.md)

## 맥락과 선택

Article client가 요청과 응답 형태를 알려면 handler와 serializer 설정을 함께 읽어야 했다. 별도의 수동 OpenAPI 파일은
field·route·permission 변경에서 쉽게 어긋난다. [개발 기준](../DEVELOPMENT_CRITERIA.md)에 따라 기존 모델과 실제 endpoint
선언에서 읽을 수 있는 문서를 만든다. 이 흐름에 필요하지 않은 binder·DTO 생성·DI·범용 viewset은 도입하지 않는다.

## 결정

`api/openapi`는 immutable `Schema`와 `Document`를 제공한다. `RequestSchema`는 실제 serializer Spec의 writable allowlist,
null·required·default와 full/partial 차이를 투영한다. `ModelResponseSchema`는 ModelEncoder가 출력하는 전체 선택 필드의
존재·타입·null·readOnly와 encoder가 검사하는 문자열 최대 길이를 투영한다. 응답 encoder가 적용하지 않는 입력
trim·blank·default 규칙은 응답에 붙이지 않는다.
모델 의미의 원본은 Schema IR이고 이 기능은 기존 `serializers.FromModel` 경계를 소비한다.

JSON Schema는 GoDj parser와 완전히 같은 검증기가 아니다. Trim 이후 제약은 `x-godj-normalization`에 기록한다.
Canonical 정수 표기, duplicate member, NUL·문자열/전체 body byte·depth 제한과 application validation은 runtime이 소유한다.
OpenAPI를 생성하거나 문서를 검증했다고 해당 안전성 검증을 생략하지 않는다.

Operation은 실제 `web.Route`, 같은 handler에 적용한 permission, query·body·response 선언을 함께 갖는다.
Article과 Helpdesk는 한 번 구성한 API의 이 원본에서 route를 반환하고 문서를 만든다. 문서 생성은 handler나 Authentication.Require를 다시 실행하지 않는다.
Web의 route compiler로 경로·이름·route language 충돌을 검사하고 OAS의 template/operation 고유성도 검사한다.
최종 application이 installed namespace·다른 route와의 충돌, middleware·handler와 문서의 일치를 보장한다.

인증은 실제 adapter가 선택적으로 제공하는 공개 transport description에서 가져온다. Session unsafe operation은 session cookie,
CSRF cookie, masked header를 같은 security requirement 안의 AND로 표현한다. Safe 응답의 새 CSRF header는 선택적인 응답
metadata다. Bearer는 HTTP bearer만 표시하고 JWT 형식·issuer·cookie fallback을 추측하지 않는다.
인증 response header는 profile이 소유해 caller의 중복·덮어쓰기를 거부한다. Credential·token·key·verifier 내부 설정은 포함하지 않는다.
Description을 제공하지 않는 custom authentication은 기존 API 실행에 사용할 수 있으나 문서 생성은 명시적으로 실패한다.

`New`는 전체 선언을 확인한 뒤 OpenAPI 3.1.1 JSON을 결정적으로 게시한다. 반환 byte·route slice는 복사하고 schema 값은
기존 immutable JSON 표현을 사용한다. 오류 시 부분 문서를 반환하지 않는다. 문서 조회 route와 공개 권한은 application이 선택한다.
Article은 authenticated loopback site의 `GET /api/openapi.json`에 Article view 권한을 적용한다.

## Schema 정체성과 정책의 공유

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

Session 검증은 실제 adapter·cookie·CSRF를 사용하되 로그인 과정은 parent fixture가 준비한 session을 사용한다.
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
