# ADR-0038: Minimal Web Core Request Lifetime and Representation

- 상태: Accepted
- 날짜: 2026-08-21
- 관련 work/contract: [GDJ-0038](../../work/0038-postgresql-and-minimal-web-vertical-slices.md), WEB-001..010, Q-011, Q-014, Q-017
- 선행 결정: [ADR-0012](0012-queryset-evaluation-cache-ownership.md), [ADR-0033](0033-forward-foreign-key-assignment-save-and-cache-ownership.md), [ADR-0036](0036-project-schema-generated-bundle-and-recoverable-publication.md)

## 맥락

GoDj에는 Web runtime package가 없습니다. Existing `godj.toml` project runner는 generated output이 missing/broken이어도
declaration을 load할 수 있어야 하므로 generated model을 import하는 serve path를 같은 binary에 넣으면 GDJ-0037 bootstrap
계약을 깨뜨립니다.

Generated QuerySet은 성공 결과를 cache하고 relation wrapper는 private lazy/cache state를 소유합니다. Application-global
generated facade를 재사용하거나 wrapper를 JSON/template에 직접 노출하면 요청 사이 stale result와 serialization 중
implicit I/O가 생길 수 있습니다. 첫 Web slice 전에 request lifetime과 representation 경계를 작게 고정해야 합니다.

## 결정 기준

- Go `context.Context`, `net/http`와 explicit error를 보존할 것
- Startup state를 immutable하게 만들고 global mutable registry를 두지 않을 것
- Request 사이 QuerySet/cache를 공유하지 않을 것
- Handler error에서 partial response를 쓰지 않을 것
- Serialization/template evaluation 중 DB I/O를 하지 않을 것
- Declaration runner와 generated bootstrap graph를 바꾸지 않을 것
- DTL/ASGI/WSGI 또는 full Django URL semantics를 첫 slice에서 주장하지 않을 것

## 결정

1. `apps.Registry`는 ordered `apps.Config{Name, Label}` snapshot을 immutable하게 소유합니다. Duplicate name/label을
   거부하고 input/getter slice를 clone하며 `init()` registration을 사용하지 않습니다.
2. `settings.Settings`는 project name과 installed app registry의 immutable startup snapshot입니다. Environment parser,
   secret/database aliases와 hot reload는 후속입니다.
3. `web.Application`은 immutable settings, static routes, middleware와 logger를 소유하며 concurrent request-safe입니다.
4. 첫 Router는 exact uppercase method, clean absolute path 또는 그 canonical path의 single trailing-slash form과
   `<installed-app-label>:<route-name>`만 지원합니다. Trailing slash 유무는 서로 다른 exact route이고 duplicate name
   또는 method+path를 거부합니다. Unknown path는 404, known path의 method mismatch는 deterministic 405와 `Allow`를
   반환합니다.
5. Middleware는 선언 순서에서 첫 항목이 outermost인 synchronous chain입니다. Downstream handler를 최대 한 번
   호출하며 framework-owned background goroutine이나 automatic retry를 만들지 않습니다.
6. `web.Request`는 handler/middleware invocation 동안만 유효한 borrowed value입니다. HTTP request context가 DB/network
   I/O cancellation의 유일한 authority입니다. Transaction `db.Session`을 callback 밖이나 application state에 저장하지
   않습니다.
7. Backend pool은 application lifetime일 수 있지만 handler는 매 요청 `project.Using(backend)`으로 새 facade/QuerySet을
   만듭니다. QuerySet/cache/wrapper를 application global에 보관하지 않습니다.
8. `web.Response`는 bounded immutable buffer입니다. Handler가 성공한 뒤에만 header/status/body를 한 번 씁니다.
   Handler error는 detail을 노출하지 않는 sanitized 500이고 partial body는 0입니다.
9. Server shutdown은 canceled serve context를 재사용하지 않고 별도 bounded cleanup context로 in-flight request를 drain한
   뒤 반환합니다. Context cancellation뿐 아니라 permanent listener/serve failure에서도 같은 bounded drain과 force-close를
   완료하고 나서 반환합니다. Application owner가 server 종료 뒤 backend를 닫습니다.
10. Model representation은 `wrapper.Unwrap()`으로 clone된 raw model을 얻은 뒤 app-owned DTO로 명시 변환합니다.
    JSON과 template에는 DTO만 전달하며 wrapper automatic unwrap/reflection/`MarshalJSON` codegen은 만들지 않습니다.
11. First Article slice는 startup에서 `html/template`를 error-returning parse하고 escaped HTML을 만듭니다. 이는 Go-native
    example adapter이며 DTL 구현/compatibility가 아닙니다.
12. Generated packages를 import하는 별도 `examples/article/cmd/site` runtime binary를 둡니다. Declaration runner,
    `godj.toml`, global `godj runserver`와 project descriptor protocol은 변경하지 않습니다.

## Public 첫 경계

- `apps.New`, immutable `Registry.All/Lookup`
- `settings.New`, immutable `Settings.ProjectName/Apps`
- `web.NewApplication`, static `Route`, `Handler`, `Middleware`, `Application.Reverse/ServeHTTP`
- borrowed `Request.Context/HTTP/Settings/Apps/Reverse`
- bounded `NewResponse`/`HTML`
- `NewServer`와 one-lifecycle `Serve(context.Context, net.Listener)`

## 비목표

- Dynamic route segment/converter/include와 parameterized reverse
- Streaming/file response, websocket/SSE, automatic request transaction와 async hooks/tasks
- CSRF/session/auth/form/admin/API, static/messages와 DTL
- General model embedding/promotion/raw wrapper JSON UX
- Global `godj runserver`와 production server tuning

## 결과

- Minimal Web Core가 generated bootstrap과 독립된 package/runtime graph를 가집니다.
- Request-local facade로 QuerySet result cache가 요청 사이 공유되지 않습니다.
- DTO boilerplate가 생기지만 lazy relation state와 serialization I/O가 명시적으로 분리됩니다.
- Q-011의 synchronous request subset만 닫히며 transaction container/async lifetime은 `Partial`로 남습니다.
- Q-017의 Web representation 선택은 bounded하지만 general raw-model/facade UX는 P1/open으로 남습니다.
