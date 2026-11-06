# ADR-0045: Closed Parameterized Routing and Reverse

> 결정 이유를 보존한 기록이다. 현재 API·지원 범위는 [현행 아키텍처](../ARCHITECTURE.md)와
> [구현 현황](../status/IMPLEMENTATION_MATRIX.md)를 따른다. 옛 내부 파일 구성·단계별 검증 절차는 현재 호환 요구가 아니다.
> 당시의 전체 기록과 실행 증거는 [고정 원문](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr/0045-closed-parameterized-routing-and-reverse.md)에 있다.

- 상태: Accepted
- 날짜: 2026-08-24
- 관련 work/contract: [GDJ-0044](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/work/0044-session-authenticated-article-json-api-and-parameterized-routing.md), WEB-028..035, Q-016, M7
- 선행 결정: [ADR-0038](0038-minimal-web-core-request-lifetime-and-representation.md),
  [ADR-0042](0042-project-linked-runserver-and-article-development-loop.md)
- 대체하는 ADR: 없음

## 맥락

이 결정을 처음 채택할 때 `web.Route`는 exact static path만 받고 `Application.Reverse(name)`도 static path만 반환합니다. Admin은 detail ID를
query string으로 전달해 이 제약을 정직하게 유지했지만 conventional resource API는 `/api/articles/<id>/`와 같은 detail
path가 필요합니다. Arbitrary regex나 callback converter를 직접 열면 route construction이 application code execution과
unbounded matching surface를 갖고, parameter를 raw context/map에 저장하면 borrowed request의 type/lifetime 경계가 약해집니다.

## 결정

1. Existing static route 선언과 static reverse 호출은 source-compatible하게 유지합니다.
2. First parameter grammar는 canonical absolute path segment와 named signed 64-bit non-negative decimal converter 하나뿐입니다.
   Decimal grammar는 `0|[1-9][0-9]*`이며 empty, sign, leading zero, overflow, encoded slash/backslash, NUL/control과 dot
   segment는 match하지 않습니다.
3. Parameter name은 route 안에서 유일한 identifier여야 합니다. 초기에는 integer converter만 허용했으며,
   2026-09-28의 아래 보완에서 bounded string converter를 추가했습니다. Arbitrary regex/path/UUID, catch-all과 user callback은 지원하지 않습니다.
4. Router는 startup에서 서로 같은 path language와 겹치는 method를 가진 parameter pattern을 fail-closed합니다. Exact static
   path는 declaration order와 무관하게 parameter path보다 우선하고 그 exact path의 method set이 405를 결정하므로 dynamic
   fallback은 없습니다.
5. Match된 값은 borrowed `web.Request`의 typed accessor로만 읽고 release 뒤에는 접근할 수 없습니다. Raw `map[string]string`,
   context handoff와 reflection conversion은 public API가 아닙니다.
6. Parameter reverse는 name과 closed typed argument를 받아 canonical decimal segment를 생성합니다. Missing/extra/wrong-kind,
   overflow와 path injection은 response I/O 전에 structured error입니다.
   Static과 parameter reverse 모두 decoded literal을 URL path로 escape합니다. Percent·공백·Unicode literal도 요청의
   decoded Path로 되돌아와 같은 route를 찾으며, escape된 결과에도 path byte cap을 적용합니다.
7. Trailing slash는 선언 bytes와 exact match합니다. Invalid converter value는 404, path pattern이 맞고 method만 다르면 sorted
   `Allow`를 포함한 405입니다.
8. Parameter 개수, pattern bytes, segment count와 input path bytes는 explicit cap으로 제한합니다.

게시된 public 이름은 `web.Route.Path`의 `<int64:name>` 문법, `web.Int64Argument`,
`Application.ReverseWith`/`Request.ReverseWith`와 `Request.Int64Parameter`입니다. `ReverseArgument`의 내부 표현과
converter kind는 닫혀 있어 application이 임의 converter를 구성할 수 없습니다.

## 결과

- Static Web/Admin 호출을 깨지 않고 conventional detail URL을 만들 수 있습니다.
- Route parameter는 닫힌 typed capability이며 arbitrary application parsing/execution 권한이 아닙니다.
- 추가 converter와 mount/subrouter는 별도의 의미와 ambiguity 검증이 필요합니다.

## 비목표

- General regex, glob, wildcard/catch-all, host/subdomain routing
- 무제한 string/slug/UUID/date/path converter와 user-defined callback converter
- Nested router/mount, middleware-per-route와 automatic REST route generation
- Query parameter binding, request body decoding 또는 API representation

## 2026-09-28 — bounded string 경로와 token 진단

Reset link의 base64url principal ID와 점을 포함하는 token은 정수 경로가 아니다. `<str:name>`를 같은 compiler에
추가하고 `StringArgument`/`Request.StringParameter`로 전달한다. 정수·문자열을 서로 강제 변환하지 않는다.
내부 parameter description은 name뿐 아니라 kind·최대 decoded byte 수를 소유한다. 호환용 이름 목록을 병행하지 않는다.

- String은 비어 있지 않은 UTF-8 **512 bytes 이하**의 한 segment다. `/`, `\`, ASCII C0/DEL과 `.`/`..`를 거부한다.
  Unicode·공백·percent·query/fragment punctuation은 값으로 보존한다. Reverse는 한 번 URL-escape하고 request는 다시 decode하지 않는다.
  전체 decoded/raw request path와 escaped reverse 결과의 기존 4096-byte 한도, parameter/segment 수 한도를 유지한다.
- String과 int64는 `0`을 공유하므로 같은 method의 겹치는 dynamic language는 선언 순서와 무관하게 거부한다.
  Parameter와 literal의 교차에는 해당 converter를 사용하고, 최소 공통 경로도 전체 byte 한도에 들어야 한다.
  Static path의 모든 method 우선권과 입력별 sorted Allow, trailing slash 규칙은 유지한다.
- Prefix coverage도 같은 matcher를 사용한다. JSON policy가 string 값 중 일부에만 적용되면 OpenAPI 구성을 거부한다.
  OpenAPI path parameter는 실제 converter kind를 따른다. String의 minLength/maxLength·문자 grammar와
  `x-godj-max-bytes`를 기술한다. JSON Schema의 길이는 code point이므로 최종 byte 검사는 router가 소유한다.
  ECMAScript `$`의 끝 개행 앞 match는 terminal guard로 막는다.
- 경로에 token이 올 수 있으므로 Web의 기본 실패 로그는 원본 path 대신 matched route name을 남긴다.
  Dispatch 전 실패와 미매칭 요청은 빈 route다. `ReverseArgument`의 모든 fmt 출력도 값을 가린다.
  Application이 스스로 token을 error/detail에 넣거나 별도 access log에 기록하지 않아야 한다.

고정 Django 6.1의 URL resolver/str converter/reverse를 [독립 runner](../../conformance/runners/django/string_routing_reference.py)로
관찰했다. Django의 str은 slash 이외의 비어 있지 않은 문자열을 허용한다. GoDj의 dot/control/backslash/byte 한도 거부는
의도한 차이다. [fixture](../../web/testdata/string-routing-django61.json)에 upstream commit·세 source hash·BSD-3-Clause 출처를 남긴다.
검증 범위와 환경은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)를 따른다. Reset 제품 consumer의 완료를 뜻하지 않는다.
