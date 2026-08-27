---
id: GDJ-0047
status: active
updated: 2026-08-27
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "2ffcc88961b41e7ca81f52a981322e3f5f9d01df"
depends_on: ["GDJ-0044", "GDJ-0046"]
contracts: ["AUT-009", "AUT-010", "AUT-011", "AUT-012", "AUT-013", "AUT-014", "AUT-015", "AUT-016", "API-011", "API-012", "Q-021"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "api/authentication.go"
  - "api/authentication_test.go"
  - "api/bearerauth/**"
  - "api/sessionauth/**"
  - "examples/article/apiapp/**"
  - "examples/article/article_api_e2e_test.go"
  - "examples/article/*bearer*_test.go"
  - "examples/article/internal/siteapp/**"
  - "conformance/articleapi/**"
  - "conformance/contracts/api-authentication-manifest.json"
  - "conformance/fixtures/godj-api-authentication-not-implemented.json"
  - "conformance/fixtures/godj-api-authentication-deviation-expected.json"
  - "conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/api-authentication-oracle.json"
  - "conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/SHA256SUMS"
  - "conformance/reference/drf/**"
  - "conformance/runners/django/article_api_fixture/**"
  - "conformance/runners/godj/**"
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/systemstate/attestations/**"
  - "conformance/systemstate/source_binding.go"
  - "conformance/systemstate/source_binding_test.go"
  - "conformance/README.md"
  - "docs/adr/0049-first-party-bff-and-bearer-api-authentication.md"
  - "docs/adr/README.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVIATIONS.md"
  - "docs/LICENSING.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0047-api-authentication-profiles-and-bearer-article-api.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0047 — API Authentication Profiles and Bearer Article API

## 목표

GDJ-0044의 Article JSON API와 GDJ-0045/0046의 durable session 기반을 보존하면서, API가 하나의 concrete session
runtime에 묶이지 않도록 다음 두 profile을 실제 사용자 흐름으로 검증합니다.

```text
First-party browser
→ HttpOnly durable session cookie + CSRF
→ 기존 Article JSON API

Independent client 또는 BFF
→ Authorization: Bearer <token>
→ injected Bearer verifier
→ 동일 auth.Principal + auth.Permission
→ 동일 Article JSON API
```

이 packet은 JWT를 session 대체품으로 만들거나 token issuance/storage를 구현하지 않습니다. 목표는 session과 Bearer가
같은 Principal/Permission core를 사용하되 request credential 추출, challenge와 CSRF 정책은 명시적으로 분리되는 최소 제품 경계를 만드는 것입니다.

## 이번 packet에서 결정하는 경계

- `api`는 application handler가 재사용할 `AuthenticatedHandler`와 construction-time error를 반환하는 최소 `Authentication`
  contract를 소유합니다.
- `api/sessionauth`는 그 contract를 구현하고 기존 JSON 403, permission, unsafe-method CSRF와 safe-response masked-token 의미를
  보존합니다. Current pre-alpha API는 `Require` construction failure를 명시적으로 반환하도록 재기준화할 수 있습니다.
- `api/bearerauth`는 exactly one `Authorization` field의 `Bearer` scheme만 읽습니다. Query, form/body, cookie나 session으로
  fallback하지 않습니다.
- Bearer credential은 RFC 6750 `b64token` grammar와 fixed 4,096-byte cap을 통과한 뒤에만 injected verifier에 전달됩니다.
  Scheme은 ASCII case-insensitive이고 delimiter는 one-or-more ASCII SP입니다. Tab, comma-joined/multiple field, empty token,
  interior `=` 또는 padding 뒤의 character, control/non-ASCII와 over-limit input은 verifier 전에 거부합니다. RFC grammar가 허용하는
  trailing `=` 개수는 Base64 decode/re-encode로 더 좁히지 않습니다.
- Missing 또는 unsupported authentication method는 JSON 401 `not_authenticated`와 exact
  `WWW-Authenticate: Bearer`를 반환하며 token-specific error를 합성하지 않습니다.
- Duplicate/malformed/over-limit Bearer request는 JSON 400 `not_authenticated`와 RFC 6750 `invalid_request` 의미의
  fixed Bearer challenge를,
  syntactically valid하지만 unknown/expired/revoked/inactive로 분류된 credential은 JSON 401과
  `Bearer error="invalid_token"` challenge를 반환하는 방향을 검증합니다.
- Authenticated principal의 permission 부족은 JSON 403과 `Bearer error="insufficient_scope"`를 반환하되 dynamic scope,
  realm, description, URI와 permission 이름을 header에 반사하지 않습니다.
- RFC 6750/9110 authority와 exact DRF 3.18 `TokenAuthentication` 관찰이 다르면 Phase A artifact에서 차이를 숨기지 않고
  `passing` 또는 sparse deviation을 결정합니다. Zero-deviation aggregate를 사전 가정하지 않습니다.
- Bearer unsafe request에는 CSRF 검증, CSRF cookie 발급과 masked-token response header를 적용하지 않습니다.
- Bearer verifier는 opaque redacted `Token`을 받아 `auth.Principal` 또는 uniform `auth.ErrInvalidCredentials`를
  반환합니다. 다른 error는 infrastructure failure이며 4xx로 바꾸거나 자동 retry하지 않습니다.
- Authorization은 existing deny-overlay를 유지합니다. Principal snapshot에 없는 permission을 configured Authorizer가 새로
  부여할 수 없습니다.
- `examples/article/apiapp`은 concrete `*api/sessionauth.Runtime` 대신 `api.Authentication`을 받고, 모든 route wrapper가
  constructor 안에서 성공한 뒤에만 application을 게시합니다.
- 하나의 Article API application은 construction-time에 exactly one profile을 선택합니다. Valid session cookie가 있어도 Bearer
  profile의 missing/invalid header를 구제하지 않으며 invalid Bearer가 cookie principal로 내려가지 않습니다.

## 보안 및 secret 경계

- Raw Bearer token은 DB, log, error, metric label, JSON, conformance artifact와 test failure text에 기록하지 않습니다.
- Opaque `Token`의 ordinary formatting, `%#v`와 JSON은 fixed redacted form만 게시합니다. `Token.Encoded()`는 injected
  verifier 호출 수명 안에서 verification을 위해서만 사용합니다.
- Framework-owned errors/challenges는 고정된 code와 문구만 사용하고 injected error text를 HTTP response에 직렬화하지 않습니다.
- Parser rejection은 verifier/authorizer/application handler 호출 0회이고, authentication/permission denial은 Article mutation
  0이어야 합니다.
- Request cancellation/deadline은 그대로 전달하며 verifier/authorizer failure 뒤 framework retry는 없습니다.
- BFF는 browser-facing cookie와 server-side token custody를 조합하는 deployment pattern입니다. Browser JavaScript에 refresh
  credential을 제공하는 API를 이번 packet에서 만들지 않습니다.

## 비목표

- JWT algorithm/issuer/audience/exp/nbf/iat/typ/kid 검증의 concrete 구현
- Opaque access-token persistence/introspection과 access-token issuance endpoint
- Refresh token digest, family/generation, rotation, reuse detection, revocation과 account-wide invalidation
- OAuth 2.0/OIDC authorization server/client, discovery, PKCE, device flow와 third-party consent
- Signing/validation key provider, JWKS fetch/cache, KMS/Vault와 deployment key rotation
- Basic auth, API key query parameter, form/body bearer, cookie-to-bearer fallback과 mixed authentication chain
- CORS/trusted-origin/production TLS/proxy policy, rate limiting, throttling, OpenAPI와 browsable API
- Session, system-state physical schema, Schema IR, migration format, DB constraint/CAS, ORM/generated ABI 변경
- Realtime/Channels, multi-DB/router, merge, release와 production rollout

## 기준 상태

- Activation baseline: GDJ-0046 terminal documentation commit
  `2ffcc88961b41e7ca81f52a981322e3f5f9d01df`, tree `dee452188c0219a9a5759fb694196dc48d008a2c`
- Product baseline: corrected GDJ-0046 frozen source `29d62469c9e6f5a6228d1578bf41b88e35eefef0`, tree
  `4f061289b240b4739ec43155b08b5909e95eddc0`; EVID-133 local final과 EVID-134/CI #153 exact 27/27·360/360 success
- Current reference/product: 21 sets/239 contracts/420 ordered bindings=`211 passing + 16 deviation + 12 oracle_locked`;
  20 adapters/227 contracts=`211 passing + 16 deviation`
- Existing `api` lower core는 session package를 import하지 않습니다. Concrete coupling은 Article API application의
  `*api/sessionauth.Runtime` field/constructor에 한정됩니다.
- Existing `auth.Principal`, `auth.Permission`과 deny-overlay `auth.Authorizer`는 session과 분리돼 있습니다.
- Q-021은 GDJ-0047이 답하는 bounded profile에 대해 `Partial`이고 Draft PR #1은 OPEN/DRAFT/unmerged입니다. Non-force push와 Draft PR refresh는 허용되지만
  merge/release/deploy는 범위 밖입니다.

## Reference와 authority

- HTTP authentication status/challenge: RFC 9110 sections 11.6.1 and 15.5.2
- Bearer header grammar, transport exclusivity와 error taxonomy: RFC 6750 sections 2.1, 3, 3.1
- OAuth deployment hardening의 future checklist: RFC 9700; 이번 resource-server adapter가 OAuth server support를 뜻하지 않음
- Framework comparison: exact DRF 3.18.0 + Django 6.1 profile의 `TokenAuthentication`을 Bearer keyword로 고정
- GoDj-only authority: common Go interface/package ownership, duplicate raw header handling, byte cap, redaction, no fallback와
  injected verifier error ownership은 Proposed ADR-0049

기존 `auth-session-manifest.json`과 `article-api-manifest.json` bytes/status는 바꾸지 않습니다. Phase A는 exact 10-contract
`api-authentication-manifest.json` 하나에 AUT-009..016과 API-011..012를 게시해 protocol-v2 set당 8..12 invariant를 지킵니다.
Reference artifact는 DRF-observable dimension과 GoDj proposal/RFC authority dimension을 명시적으로 구분하고, product handler가 없는 동안
10개 모두 `oracle_locked`와 payload-free `not_implemented` 상태여야 합니다.

## 계약

### API authentication profile — AUT-009..016

- `AUT-009`: common `api.Authentication`은 typed Principal/Permission handler와 construction-time failure를 보존하며 session profile을 회귀시키지 않음
- `AUT-010`: exactly-one bounded Bearer header grammar, duplicate/malformed/over-limit pre-verifier rejection과 no alternate transport
- `AUT-011`: missing/unsupported credential의 JSON 401과 exact Bearer challenge, redirect/cookie/secret output 0
- `AUT-012`: unknown/inactive/invalid credential의 uniform denial과 valid credential의 active Principal resolution
- `AUT-013`: deny-overlay permission evaluation과 insufficient-scope 403, denied handler/mutation 0
- `AUT-014`: valid unsafe Bearer request가 CSRF 없이 통과하고 CSRF cookie/header를 만들지 않음
- `AUT-015`: valid session cookie나 query/body token이 missing/invalid Bearer를 구제하지 않는 single-profile isolation
- `AUT-016`: credential/config/error formatting과 JSON/artifact redaction, cancellation과 verifier/authorizer no-retry ownership

### Bearer Article API — API-011..012

- `API-011`: valid Bearer principal이 기존 Article list/detail/create/update/delete route와 representation을 SQLite/PostgreSQL에서 재사용
- `API-012`: missing/malformed/invalid/permission-denied Bearer와 cookie fallback 시도가 Article mutation 0, secret occurrence 0을 보존

기존 API-001..010 CRUD/serializer/query semantics를 복제해 새 지원 범위처럼 부풀리지 않습니다. API-011/012는 authentication
profile이 동일 route/service/persistence 결과를 보존하는지만 측정합니다.

## Package 방향

```text
api → auth + web
api/sessionauth → api + web/sessionauth + auth
api/bearerauth → api + auth + web
examples/article/apiapp → api + articleapp
```

- `api`는 sessionauth/bearerauth 또는 Article을 import하지 않습니다.
- `auth`는 HTTP header, cookie, CSRF와 JWT package를 import하지 않습니다.
- `api/bearerauth`는 token format별 crypto/JWT library를 import하지 않고 injected verifier만 호출합니다.
- Article handler는 resolved Principal을 계속 explicit argument로 받으며 context나 mutable request slot에 숨기지 않습니다.

## 구현 단계

- [x] Activation: work/Proposed ADR/status, exact IDs, authority split와 allowed/excluded paths 고정
- [x] Phase A: independent DRF/RFC reference, exact 10-contract manifest/oracle/NI/checksum과 `oracle_locked` aggregate publication
- [x] Phase B: common API authentication contract, session adapter construction error 전환, Article interface/atomic route construction과 regression tests
- [x] Phase C: strict redacted `api/bearerauth` parser/verifier/authorizer와 adversarial unit tests
- [x] Phase D: SQLite full-flow/profile-isolation E2E와 oracle-blind Go actual
- [x] Phase E: digest-pinned PostgreSQL required E2E, product status classification와 global conformance publication
- [x] Source checkpoint: affected normal/race/CGO0/vet, generated drift, secret scan와 system-state source-bound attestation recapture
- [ ] Final frozen milestone: full `make ci`, Linux/386, repository-external clean copy, independent audit와 exact hosted matrix once
- [ ] Accepted/Verified/completed status and Draft PR terminal mirror after exact hosted success

## Source checkpoint 결과

- Exact source: `5469f41b2bb278feaedfc08b35798de7f0fd796d`, tree
  `21cb835366c10b64ace161ecd304139f694c7c0f`; detailed commands and hashes are in
  [EVID-135](../docs/status/TEST_EVIDENCE.md#evid-20260827-135--gdj-0047-bearer-authentication-product-and-postgresql-source-checkpoint).
- Product files: common `api.Authentication`, `api/sessionauth`, `api/bearerauth`, profile-neutral Article API and
  SQLite/PostgreSQL Bearer E2E; reference/publication files: exact ten-contract manifest/oracle/baseline, DEV-0009 sparse
  expectation/policy, oracle-blind Go actual and global Make/CI/protocol registry locks.
- Decision: RFC-priority invalid-token/insufficient-scope challenge differences are exact seven-selector DEV-0009;
  ADR-0049 remains Proposed and DEV-0009 remains Implemented until exact hosted acceptance.
- Verification: affected normal/race/CGO0/vet, generated drift, exact 21-adapter comparison, digest-pinned PostgreSQL
  normal/race/CGO0 and source-bound attestation passed. Independent audit found and closed one credential-canary false-green,
  then returned P0..P3=0.
- Not yet run: final full `make ci`, Linux/386, repository-external archive and exact hosted matrix.

## 검증 주기

- 매 source checkpoint: `gofmt`, affected compile/test, `go vet`와 focused redaction/adversarial repetition
- Reference checkpoint: exact DRF profile, manifest/oracle/NI validation, checksum과 historical artifact prefix/non-drift
- API checkpoint: `./api/... ./examples/article/apiapp/...` normal/race/CGO0/vet
- Backend checkpoint: SQLite test와 digest-pinned PostgreSQL required pass/no-skip, denial mutation 0와 same route/result sentinel
- Publication checkpoint: new set actual/expected comparison, global aggregate/registry/inventory and secret scan
- API/auth/examples/conformance source가 GDJ-0046 PostgreSQL attestation binding 범위에 들어가므로 final source에서 checked
  attestation과 sibling checksum을 다시 capture하고 required hosted lane이 byte-compare해야 함
- Final source freeze에서만 full/386/external archive/hosted matrix를 한 번 실행
- 문서-only activation은 link/frontmatter/status consistency와 `git diff --check`; 전체 product matrix를 반복하지 않음

## 완료 조건

- [x] Existing first-party session Article API의 status/header/CSRF/permission/persistence 의미와 call sites가 통과함
- [x] Common API authentication boundary가 typed-nil/nil handler/configuration을 request publication 전에 fail-closed함
- [x] Bearer parser가 exactly-one header, fixed cap/grammar와 no fallback을 verifier 호출 전에 보장함
- [x] Missing/malformed/invalid/permission denial의 status/challenge와 DRF/RFC 차이가 artifact/deviation에서 명시됨
- [x] Raw credential이 error/fmt/JSON/log/artifact/test diagnostic에 등장하지 않음
- [x] Unsafe Bearer mutation은 CSRF 없이 성공하지만 invalid/denied/fallback 요청의 handler와 DB mutation은 0임
- [x] SQLite/PostgreSQL에서 같은 Article route/service/representation을 session과 Bearer profile이 재사용함
- [x] JWT/opaque/refresh/OAuth 구현 없이 verifier injection만으로 수직 단면이 닫힘
- [x] Global manifest/registry/inventory와 source-bound PostgreSQL attestation이 exact current source에 맞음
- [ ] Final local/hosted gates와 independent audit가 통과하고 status/evidence/ADR/PR이 같은 frozen source를 가리킴

## 인수인계

현재 source checkpoint는 `5469f41b2bb278feaedfc08b35798de7f0fd796d`, tree
`21cb835366c10b64ace161ecd304139f694c7c0f`입니다. Exact reference/product aggregate는
22 sets/249 contracts/462 ordered bindings=`218 passing + 19 deviation + 12 oracle_locked`, product
21 adapters/237 contracts=`218 passing + 19 deviation`이며 AUT-012/013/015만 Implemented DEV-0009입니다.

Digest-pinned Linux/amd64 Go 1.26.5와 PostgreSQL 17.10에서 exact fingerprint, source-bound two-process sentinel과
Article Bearer E2E normal/race/CGO-disabled가 통과했습니다. Checked attestation은 1,134 bytes/SHA-256
`1504f07b83081cacbc35a213a54f681c82a7f1e740ed1802b1a276b734b32d1f`, source binding은
256 files/2,940,052 bytes/SHA-256 `caa773143c26f18efc6f9459593979781438ffe86f0cec1a4be7c4ab0c7ca67a`입니다.
독립 감사의 credential-scanner finding을 수정한 뒤 최종 P0..P3는 0이었습니다.

정확한 다음 작업은 이 source와 publication 문서를 한 checkpoint로 고정한 뒤 full `make ci`, Linux/386,
repository-external clean archive와 final audit를 한 번 실행하는 것입니다. 그 exact head를 non-force push하고 Draft PR #1의
hosted matrix까지 성공해야 ADR-0049 Accepted, DEV-0009 Verified와 work completed를 검토합니다. Merge/release/deploy는 계속 제외합니다.
