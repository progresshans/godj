# ADR-0049: First-Party, BFF and Bearer API Authentication Profiles

- 상태: Proposed
- 날짜: 2026-08-26
- 관련 work/contract: [GDJ-0047](../../work/0047-api-authentication-profiles-and-bearer-article-api.md),
  AUT-009..016, API-011..012, Q-021
- 확장하는 ADR: [ADR-0044](0044-session-auth-csrf-and-bounded-article-admin.md),
  [ADR-0046](0046-json-serializer-and-session-authenticated-article-api.md)
- 대체하는 ADR: 없음

## 맥락

GoDj의 현재 Article JSON API는 durable HttpOnly session cookie, explicit Principal/Permission과 unsafe-method CSRF를 사용합니다.
이는 first-party browser와 Next.js를 포함한 same-site frontend에 적합하며 React라는 이유만으로 JWT가 필요한 것은 아닙니다.

하지만 모바일, independent SPA/client, BFF와 외부 resource client는 `Authorization: Bearer` profile을 필요로 할 수 있습니다.
현재 lower `api`와 `auth` core는 session package에 결합돼 있지 않지만 Article example의 constructor와 field가 concrete
`*api/sessionauth.Runtime`을 받습니다. 이 좁은 결합을 그대로 복제해 JWT verifier를 API core에 넣으면 session과 token이 서로 다른
Principal/Permission 체계를 만들거나, cookie/CSRF fallback이 애매한 authentication chain이 될 수 있습니다.

Bearer는 credential transport이고 JWT와 동의어가 아닙니다. JWT/opaque access token, refresh lifecycle과 OAuth/OIDC는 서로 다른
발급·검증·저장·rotation/revocation 결정을 요구합니다. 첫 수직 단면은 resource-server adapter와 실제 Article flow만 닫아야 합니다.

## 결정 기준

- First-party session/CSRF 의미와 existing Article API source compatibility를 가능한 범위에서 보존
- Authentication profile의 construction-time 선택과 no fallback
- RFC 6750/9110 header, status와 challenge 의미
- Raw Bearer credential의 bounded parsing과 secret-free diagnostics
- 공통 typed Principal/Permission 및 deny-overlay Authorizer 재사용
- JWT/opaque/OAuth package를 lower API core에 결합하지 않는 dependency 방향
- SQLite/PostgreSQL에서 같은 Article handler/persistence를 재사용하는 검증 가능성
- DRF 관찰과 RFC authority가 다를 때 sparse deviation으로 정직하게 표현할 수 있는가

## 고려한 선택지

### 선택지 A — Session만 유지하고 token auth는 future application이 직접 구현

현재 code change는 없지만 각 application이 Authorization parsing, 401 challenge, redaction, permission과 CSRF bypass를 반복합니다.
같은 Principal core를 사용한다는 보장이 없고 Article API의 concrete session dependency도 남습니다.

### 선택지 B — Common API authentication contract와 strict injected Bearer adapter

Lower `api`가 typed authenticated handler와 construction-time wrapper contract만 소유합니다. Session과 Bearer adapter는 각자의
credential/CSRF/challenge 정책을 갖고 같은 Principal/Permission을 반환합니다. Bearer token format은 injected verifier가 소유하므로
JWT나 opaque token을 core에 고정하지 않습니다.

### 선택지 C — Session, cookie, API key와 Bearer를 순서대로 시도하는 authentication chain

한 endpoint가 여러 client를 편하게 받지만 invalid Bearer가 valid cookie로 내려가는 downgrade/fallback 의미, CSRF 적용 순서,
challenge owner와 logging surface가 복잡해집니다. Credential conflict가 public behavior가 되고 profile isolation을 잃습니다.

### 선택지 D — JWT verifier/issuer와 refresh storage를 한 번에 구현

Access claim policy, signing/validation key ring, issuer/audience, clock skew, durable refresh family와 reuse detection까지 즉시 결정할 수 있습니다.
반면 system schema/migration/key provider와 revocation policy를 동시에 넓혀 resource-server adapter 검증을 지연시킵니다.

## 제안 결정

선택지 B를 GDJ-0047 prototype 방향으로 제안합니다. Phase A reference와 Phase B~E product evidence가 통과한 뒤 Accepted 여부를 결정합니다.

1. `api`는 다음 최소 public contract를 소유합니다.

   ```go
   type AuthenticatedHandler func(*web.Request, auth.Principal) (web.Response, error)

   type Authentication interface {
       Require(auth.Permission, AuthenticatedHandler) (web.Handler, error)
   }
   ```

   `error`는 typed-nil adapter, nil handler와 invalid runtime을 route publication 전에 거부하기 위한 construction failure입니다.
2. `api/sessionauth`는 local handler type을 제거하고 `api.AuthenticatedHandler`를 직접 받아 이 contract를 구현하며 existing JSON
   403, permission, unsafe CSRF와 safe response token/cookie 의미를 보존합니다. Current pre-alpha `Require`는 handler-only 반환에서
   `(web.Handler, error)`로 재기준화합니다. 외부 callsite가 없는 compatibility alias는 남기지 않습니다.
3. `api/bearerauth`는 package-owned opaque `Token`과 `Verifier` interface를 제공합니다.

   ```go
   type Token struct {
       encoded string
   }

   func (Token) Encoded() string

   type Verifier interface {
       Verify(context.Context, Token) (auth.Principal, error)
   }
   ```

   Verifier는 request context와 token을 받아 active `auth.Principal`, `auth.ErrInvalidCredentials` 또는 infrastructure error를
   반환합니다.
4. `Token`은 `Encoded` verification accessor 외에 material을 공개하지 않고 ordinary/Go formatting과 JSON을 fixed redacted form으로 만듭니다.
   Framework error/challenge/artifact는 raw value나 injected cause text를 포함하지 않습니다.
5. Bearer adapter는 exactly one `Authorization` field만 읽고 case-insensitive `Bearer` + `1*SP` + RFC 6750 `b64token`을
   fixed maximum 4,096 bytes 안에서 허용합니다. Duplicate/joined field, empty/control/non-ASCII, invalid alphabet/padding와 over-limit은
   verifier 호출 전에 실패합니다.
6. Cookie, session, query와 form/body access token은 대체 credential source가 아닙니다. 한 application은 constructor에서 session 또는
   Bearer profile 하나를 선택하며 invalid/missing Bearer를 다른 profile로 구제하지 않습니다.
7. HTTP semantics의 우선 authority는 RFC 6750과 RFC 9110입니다.
   - credential/지원 scheme 없음: JSON 401 + `WWW-Authenticate: Bearer`, error parameter 없음
   - malformed/duplicate/over-limit Bearer: JSON 400 `not_authenticated` + fixed `Bearer error="invalid_request"`
   - syntactically valid invalid/inactive credential: JSON 401 + fixed `Bearer error="invalid_token"`
   - permission 부족: JSON 403 + fixed `Bearer error="insufficient_scope"`
   Dynamic realm/scope/description/URI와 permission/token material은 반사하지 않습니다.
8. Exact DRF 3.18 Bearer-keyword `TokenAuthentication` 결과도 독립 관찰합니다. DRF가 malformed request나 permission challenge에서
   RFC-priority proposal과 다르면 이를 복사하거나 숨기지 않고 contract comparison과 sparse deviation 후보로 기록합니다.
9. Bearer profile은 모든 HTTP method에서 CSRF를 호출하지 않고 CSRF cookie/header도 발급하지 않습니다. Session profile은 기존 CSRF
   의미를 유지합니다.
10. Authorization은 Principal snapshot permission을 먼저 확인하고 configured `auth.Authorizer`가 추가 deny만 할 수 있습니다.
    Verifier/Authorizer cancellation과 infrastructure error는 retry 없이 framework error로 전달되며 invalid credential 4xx로
    오분류하지 않습니다.
11. Article API constructor는 `api.Authentication`을 받고 모든 protected route wrapper를 성공적으로 만든 뒤에만 immutable application을
    게시합니다. Handler, serializer, query와 repository는 profile과 무관하게 재사용합니다.
12. First-party browser는 durable session cookie+CSRF를 기본 profile로 유지합니다. BFF는 browser cookie를 소유하고 server-to-server
    Bearer를 전달할 수 있지만 GoDj가 BFF token custody나 OAuth client를 이번 ADR에서 구현하지 않습니다.

## 결과

- Session과 Bearer가 별도 permission system을 만들지 않고 같은 Principal snapshot과 Article handlers를 재사용합니다.
- Invalid Bearer가 session cookie로 downgrade되는 모호함이 없습니다.
- JWT/opaque verifier를 나중에 adapter 뒤에 추가할 수 있지만 lower API core와 Article app은 token format을 알지 않습니다.
- Construction-time error 반환으로 existing API session wrapper call sites가 작게 변경됩니다. Pre-alpha current-only publication에서
  한 번 재기준화하고 compatibility shim을 남기지 않습니다.
- DRF parity보다 RFC semantics를 우선하는 selector가 생길 수 있으므로 Phase A observer와 deviation publication이 acceptance 전
  필수입니다.
- Fixed 4,096-byte limit은 첫 bounded profile의 제품 계약입니다. 더 큰 token/profile은 silent widening이 아니라 후속 결정이 필요합니다.

## 의도적으로 결정하지 않은 것

- JWT access token claim/algorithm/key validation과 concrete library
- Opaque token DB/introspection, token issuance와 client registration
- Refresh token digest/family/generation/rotation/reuse detection/revocation
- OAuth/OIDC authorization server/client, discovery/JWKS/PKCE/device flow
- Account disable/password/permission change가 이미 발급된 access token에 미치는 policy
- Shared signing key provider, key ID/rotation, KMS/Vault와 compromise response
- Authentication chain, optional anonymous API, Basic/API-key와 CORS/trusted-origin policy
- Browser JavaScript token storage, production BFF implementation, OpenAPI/browsable API와 Realtime

## 검증

- Exact 10-contract reference-only Phase A에서 RFC/DRF/GoDj authority dimension과 status/challenge 차이를 고정합니다.
- Common interface compile tests는 session/Bearer conformity, typed nil, nil handler와 partial route publication 0을 검증합니다.
- Bearer unit tests는 header grammar/cap/duplicate, verifier call 0/1, invalid/infrastructure distinction, permission deny-overlay,
  CSRF 호출 0, cookie/query/body no-fallback과 fixed challenge를 검증합니다.
- Secret tests는 all fmt verbs used by product, JSON/error wrapping, HTTP body/header와 conformance observation에서 marker occurrence 0을 요구합니다.
- Article SQLite/PostgreSQL E2E는 valid Bearer CRUD 결과, permission별 403, invalid denial mutation 0과 existing session regression을 검증합니다.
- Affected normal/race/CGO0/vet와 final full/386/external archive/exact hosted matrix를 work packet 주기에 따라 실행합니다.
