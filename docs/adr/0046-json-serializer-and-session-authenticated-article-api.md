# ADR-0046: JSON Serializer and Session-authenticated Article API

- 상태: Proposed
- 날짜: 2026-08-24
- 관련 work/contract: [GDJ-0044](../../work/0044-session-authenticated-article-json-api-and-parameterized-routing.md), API-001..010, Q-016, M7
- 선행 결정: [ADR-0043](0043-safe-template-and-model-form-validation.md),
  [ADR-0044](0044-session-auth-csrf-and-bounded-article-admin.md),
  [ADR-0045](0045-closed-parameterized-routing-and-reverse.md)
- 대체하는 ADR: 없음

## 맥락

Form과 Admin은 normalized validation, explicit typed Article conversion과 session/auth/CSRF를 제공하지만 JSON parser/renderer,
serializer partial lifecycle, API error/page envelope 또는 API-specific authentication response를 제공하지 않습니다. Existing
`sessionauth.Require`는 browser Admin을 위해 anonymous request를 login으로 redirect합니다. 이를 API에 그대로 쓰면 DRF
SessionAuthentication의 403 의미와 JSON client expectation을 모두 깨뜨립니다.

Reflection 기반 generic serializer나 `map[string]any` public API는 generated model의 private state를 우회하고 field order,
number precision, null/omitted와 application I/O ownership을 숨깁니다. 첫 slice는 explicit Article typed adapter로 의미를 검증한
뒤에만 generated ModelSerializer를 검토해야 합니다.

## Proposed 결정

1. `serializers`는 immutable field order, input presence/null/value, ordered stable validation errors와 full/partial mode를
   소유하며 common `validation` primitive를 재사용합니다. Form public API/lifecycle과는 분리합니다.
2. Public serializer ingress는 bounded JSON object와 declared fields입니다. Raw `any`, reflection, struct tag discovery,
   arbitrary callback I/O와 generic autosave는 허용하지 않습니다.
3. JSON decoder는 exactly one top-level object, duplicate/unknown field rejection, bounded bytes/depth/string length와 trailing-data
   rejection을 적용합니다. First renderer/parser는 JSON 하나뿐입니다.
4. Article representation은 ordered `id,title,published,summary`; `id`는 read-only, summary의 omitted/null/empty를 구분합니다.
   POST/PUT은 full validation, PATCH는 supplied field만 검증·변경하며 omitted default를 적용하지 않습니다.
5. `api`는 stable JSON error/page response를 만들고 internal cause, SQL, credential/session/CSRF/cookie bytes를 직렬화하지 않습니다.
   Exact localized prose와 JSON whitespace는 contract가 아니며 stable code/field/order가 contract입니다.
   `/api/` subtree middleware는 lower Web의 404/405 status와 sorted `Allow`를 보존한 채 representation만 JSON으로 바꾸며
   non-API route의 default plain-text response는 수정하지 않습니다.
6. API-specific session wrapper는 principal과 permission을 explicit typed argument로 전달합니다. Anonymous와 permission denial은
   redirect/`WWW-Authenticate` 없는 JSON 403입니다.
7. Login은 CSRF secret을 회전하고 accepted cookie policy는 HttpOnly이므로 pre-login token을 재사용하거나 cookie secret을
   JavaScript에 노출하지 않습니다. First authenticated safe API response는 fresh masked token을 제공합니다. 이 신규 response
   header에는 existing configured CSRF request-header 이름 `X-GoDj-CSRFToken`을 재사용하고, unsafe
   POST/PUT/PATCH/DELETE는 token을 같은 request header에 실어 existing `sessionauth.VerifyCSRF`로 검증합니다. Denial, invalid JSON과
   validation error에서는 Article DB mutation I/O가 0입니다. Valid session load의 existing idle-expiry access touch는 이
   측정에서 제외하며 Accepted AUT lifecycle을 바꾸지 않습니다.
8. First permission mapping은 list/retrieve=`view`, POST=`add`, PUT/PATCH=`change`, DELETE=`delete`입니다. Object permission과
   Authorizer grant widening은 지원하지 않습니다.
9. List는 primary-key deterministic ordering, fixed PageNumber pagination, bounded search/published/order whitelist를 사용합니다.
   Next/previous는 raw Host header를 반사하지 않는 canonical relative request URI입니다. DRF absolute origin은 oracle에서
   path/query로 normalize하고 trusted base URL/AllowedHosts가 생기기 전에는 absolute link를 합성하지 않습니다. Count/page
   query ownership은 explicit repository port에 남습니다.
10. Article create/update/delete는 app-owned typed port가 current generated ORM과 SQLite/PostgreSQL backend를 호출합니다.
    Serializer나 generic API core는 DB transaction/retry/audit를 숨겨 실행하지 않습니다.
11. First REST surface는 SimpleRouter-style trailing slash의 list/create/retrieve/update/partial_update/destroy뿐입니다.
    Browsable API, OpenAPI와 format suffix는 게시하지 않습니다.
12. POST success는 representation의 field set에 `url`을 추가하지 않습니다. Exact reference ViewSet이 custom
    `get_success_headers`에서 created ID의 named detail reverse로 `Location`을 만들고 oracle은 origin을 제거한 canonical
    path/query를 보존합니다. GoDj는 raw Host를 반사하지 않는 같은 relative URI를 반환합니다.

정확한 exported 이름과 error envelope는 Phase A reference/Phase C compile prototype 뒤 Accepted 전환 때 동결합니다.

## 결과

- GDJ-0043 browser session을 실제 JSON client flow에 재사용하면서 redirect와 API denial을 구분합니다.
- Serializer validation은 Form과 primitive를 공유하지만 partial/representation lifecycle과 public API는 독립적입니다.
- First Article adapter는 반복 코드가 있지만 generated ABI나 reflection을 성급하게 고정하지 않습니다.

## 비목표

- OpenAPI, browsable API, HTML/multipart parser, renderer negotiation breadth
- Nested/related/hyperlinked/list-write serializer와 bulk CRUD
- Token/Basic/OAuth/JWT, throttling, versioning, metadata와 object permission
- Durable/distributed auth/session, API-wide implicit transaction/retry와 async/streaming
- M7 전체 완료, Realtime/Channels와 production readiness

## 검증 계획

- [ ] DRF 3.18.0 + Django 6.1 exact API-001..010 reference artifact
- [ ] JSON parser/serializer full/partial/error determinism and resource caps
- [ ] Anonymous/permission/CSRF denial JSON 403 and terminal Article DB mutation 0
- [ ] SQLite/PostgreSQL Article list/create/detail/PUT/PATCH/delete actual flow
- [ ] Oracle-blind GoDj adapter, sparse deviation policy and global inventory lock
- [ ] normal/race/CGO0/vet, full/386/external-copy and exact hosted matrix
