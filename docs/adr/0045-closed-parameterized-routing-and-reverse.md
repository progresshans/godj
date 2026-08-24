# ADR-0045: Closed Parameterized Routing and Reverse

- 상태: Accepted
- 날짜: 2026-08-24
- 관련 work/contract: [GDJ-0044](../../work/0044-session-authenticated-article-json-api-and-parameterized-routing.md), WEB-028..035, Q-016, M7
- 선행 결정: [ADR-0038](0038-minimal-web-core-request-lifetime-and-representation.md),
  [ADR-0042](0042-project-linked-runserver-and-article-development-loop.md)
- 대체하는 ADR: 없음

## 맥락

Current `web.Route`는 exact static path만 받고 `Application.Reverse(name)`도 static path만 반환합니다. Admin은 detail ID를
query string으로 전달해 이 제약을 정직하게 유지했지만 conventional resource API는 `/api/articles/<id>/`와 같은 detail
path가 필요합니다. Arbitrary regex나 callback converter를 직접 열면 route construction이 application code execution과
unbounded matching surface를 갖고, parameter를 raw context/map에 저장하면 borrowed request의 type/lifetime 경계가 약해집니다.

## 결정

1. Existing static route 선언과 static reverse 호출은 source-compatible하게 유지합니다.
2. First parameter grammar는 canonical absolute path segment와 named signed 64-bit non-negative decimal converter 하나뿐입니다.
   Decimal grammar는 `0|[1-9][0-9]*`이며 empty, sign, leading zero, overflow, encoded slash/backslash, NUL/control과 dot
   segment는 match하지 않습니다.
3. Parameter name은 route 안에서 유일한 identifier여야 합니다. Arbitrary regex, string/path/UUID converter, catch-all과 user callback
   converter는 지원하지 않습니다.
4. Router는 startup에서 서로 같은 path language와 겹치는 method를 가진 parameter pattern을 fail-closed합니다. Exact static
   path는 declaration order와 무관하게 parameter path보다 우선하고 그 exact path의 method set이 405를 결정하므로 dynamic
   fallback은 없습니다.
5. Match된 값은 borrowed `web.Request`의 typed accessor로만 읽고 release 뒤에는 접근할 수 없습니다. Raw `map[string]string`,
   context handoff와 reflection conversion은 public API가 아닙니다.
6. Parameter reverse는 name과 closed typed argument를 받아 canonical decimal segment를 생성합니다. Missing/extra/wrong-kind,
   overflow와 path injection은 response I/O 전에 structured error입니다.
7. Trailing slash는 선언 bytes와 exact match합니다. Invalid converter value는 404, path pattern이 맞고 method만 다르면 sorted
   `Allow`를 포함한 405입니다.
8. Parameter 개수, pattern bytes, segment count와 input path bytes는 explicit cap으로 제한합니다.

게시된 public 이름은 `web.Route.Path`의 `<int64:name>` 문법, `web.Int64Argument`,
`Application.ReverseWith`/`Request.ReverseWith`와 `Request.Int64Parameter`입니다. `ReverseArgument`의 내부 표현과
converter kind는 닫혀 있어 application이 임의 converter를 구성할 수 없습니다.

## 결과

- Static Web/Admin 호출을 깨지 않고 conventional detail URL을 만들 수 있습니다.
- Route parameter는 닫힌 numeric capability이며 arbitrary application parsing/execution 권한이 아닙니다.
- String/UUID/path converter와 mount/subrouter가 필요하면 별도 contract와 ambiguity proof가 필요합니다.

## 비목표

- General regex, glob, wildcard/catch-all, host/subdomain routing
- String/slug/UUID/date/path converter와 user-defined callback converter
- Nested router/mount, middleware-per-route와 automatic REST route generation
- Query parameter binding, request body decoding 또는 API representation

## 검증 계획

- [x] WEB-028..035 exact reference/decision artifact와 negative ambiguity corpus
- [x] Static precedence, typed accessor lifetime와 reverse canonicalization
- [x] 404/405/Allow/trailing slash/encoded separator 의미
- [x] Fuzz/resource caps, normal/race/CGO0/vet와 external compile gate
- [x] Existing static Web/Admin/runserver regression and exact hosted matrix

WEB-028/029는 [DEV-0006](../DEVIATIONS.md#dev-0006--closed-int64-route-type-and-stricter-numeric-grammar)의
exact sparse selectors를 제외하고 reviewed product expectation과 일치합니다. WEB-030..035는 passing입니다.
Final local/hosted evidence는 [EVID-125](../status/TEST_EVIDENCE.md#evid-20260824-125--gdj-0044-article-api-frozen-local-checkpoint)와
[EVID-126](../status/TEST_EVIDENCE.md#evid-20260824-126--gdj-0044-exact-head-hosted-completion)입니다.
