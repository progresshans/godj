# ADR-0058: 모델 기반 OpenAPI와 operation 소유권

- 상태: Accepted
- 날짜: 2026-09-12
- 관련 작업: [GDJ-0070](../../work/0070-model-derived-openapi.md)
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
Article은 이 원본에서 route를 반환하고 문서를 만든다. 문서 생성은 handler나 Authentication.Require를 다시 실행하지 않는다.
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

## 결과와 남은 범위

Article의 full/partial 입력, pagination, Location/Allow, Session/Bearer 오류와 HEAD/204·plain 500 표현을 client가 조회할 수 있다.
문서용 type/field 정의를 별도로 복제하지 않으며 기존 parsing·대상 확인·permission·transaction 순서는 유지한다.
HTML docs UI, 생성 SDK, 더 넓은 schema vocabulary·인증 profile과 Helpdesk 문서화는 후속 소비자 요구로 선택한다.
이번 기능은 Django의 새 differential behavior contract를 주장하지 않는다.

규격 근거는 [OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)이다.
실행 범위와 외부 validator 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 둔다.
