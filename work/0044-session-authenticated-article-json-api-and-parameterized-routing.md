---
id: GDJ-0044
status: active
updated: 2026-08-24
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "f99c200a3c5e36b391aabf6634a94acd79bba69b"
depends_on: ["GDJ-0041", "GDJ-0042", "GDJ-0043"]
contracts: ["WEB-028", "WEB-029", "WEB-030", "WEB-031", "WEB-032", "WEB-033", "WEB-034", "WEB-035", "API-001", "API-002", "API-003", "API-004", "API-005", "API-006", "API-007", "API-008", "API-009", "API-010", "Q-016"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "NOTICE.md"
  - "web/**"
  - "api/**"
  - "serializers/**"
  - "validation/**"
  - "auth/**"
  - "web/sessionauth/**"
  - "examples/article/apiapp/**"
  - "examples/article/articleapp/**"
  - "examples/article/internal/**"
  - "examples/article/adminapp/**"
  - "examples/article/cmd/site/**"
  - "examples/article/*api*_test.go"
  - "conformance/articleapi/**"
  - "conformance/contracts/parameter-routing-manifest.json"
  - "conformance/contracts/article-api-manifest.json"
  - "conformance/fixtures/godj-parameter-routing-not-implemented.json"
  - "conformance/fixtures/godj-article-api-not-implemented.json"
  - "conformance/fixtures/godj-parameter-routing-deviation-expected.json"
  - "conformance/fixtures/godj-article-api-deviation-expected.json"
  - "conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/**"
  - "conformance/profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json"
  - "conformance/reference/drf/**"
  - "conformance/runners/django/**"
  - "conformance/runners/godj/**"
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/README.md"
  - "docs/adr/0045-closed-parameterized-routing-and-reverse.md"
  - "docs/adr/0046-json-serializer-and-session-authenticated-article-api.md"
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
  - "work/0044-session-authenticated-article-json-api-and-parameterized-routing.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0044 — Session-authenticated Article JSON API and Parameterized Routing

## 목표

GDJ-0043의 process-lifetime session/auth/CSRF와 실제 Article SQLite/PostgreSQL persistence를 재사용해 다음 사용자 흐름을
하나의 넓은 수직 단면으로 구현합니다.

```text
Admin login → rotated session/CSRF cookie
→ authenticated GET /api/articles/?search=go&published=true&ordering=-id&page=1 에서 새 masked CSRF token 획득
→ POST /api/articles/
→ GET /api/articles/<id>/
→ PUT/PATCH /api/articles/<id>/
→ DELETE /api/articles/<id>/
→ DB 재조회와 permission/CSRF/mutation 결과 확인
```

Reference는 exact DRF 3.18.0 + Django 6.1 + CPython 3.14.3으로 고정합니다. GoDj는 Python API나 DRF 내부
객체를 포팅하지 않고 JSON 결과, validation/error order, HTTP status/header, authentication/permission/CSRF, DB row delta와
route/reverse 의미를 비교합니다.

## 이번 packet에서 결정하는 경계

- Existing static `web.Route`와 `Application.Reverse(name)` 호출은 계속 동작합니다.
- Parameter route는 `0|[1-9][0-9]*` grammar의 canonical non-negative signed 64-bit decimal ID 한 종류만 허용합니다. Arbitrary regex, catch-all,
  encoded slash, float/UUID/string converter와 handler callback converter는 추가하지 않습니다.
- Exact static path가 parameter route보다 declaration order와 무관하게 우선하고 그 static path의 method set이 405를
  결정하므로 dynamic fallback은 없습니다. 서로 같은 path language와 겹치는 method를 가진 parameter declaration은 startup에서
  fail-closed합니다.
- Parameter 값은 borrowed `web.Request` lifetime 안에서 typed accessor로만 읽습니다. Context나 raw map에 숨기지 않습니다.
- Reverse는 route 이름과 closed typed argument를 받아 canonical decimal path를 만듭니다. Missing/extra/wrong-kind/overflow는
  요청 I/O 전에 structured error입니다.
- `serializers`는 reflection-free immutable field/validation/representation 경계를 제공하고 `validation` primitive를 재사용합니다.
- First Article serializer는 explicit field order `id,title,published,summary`와 app-owned typed conversion을 사용합니다.
  Raw `map[string]any`, struct reflection, generic autosave와 generated ABI 변경은 허용하지 않습니다.
- `api`는 JSON parser/renderer, bounded body, stable error envelope와 page envelope를 소유합니다. First packet의 parser와
  renderer는 JSON 하나뿐이며 silent content negotiation fallback을 만들지 않습니다.
- API middleware는 `/api/` subtree의 Web 404/405 representation만 JSON error로 바꾸고 status와 sorted `Allow`는 보존합니다.
  Lower Web의 default plain-text 404/405와 non-API routes는 바꾸지 않습니다.
- API session wrapper는 anonymous denial을 login redirect가 아닌 JSON 403으로 반환합니다. Authenticated unsafe method는
  GDJ-0043 CSRF 검증을 그대로 통과해야 하며 permission은 view/add/change/delete별로 평가합니다.
- Login은 CSRF secret을 회전하고 cookie는 HttpOnly이므로 pre-login token을 재사용하지 않습니다. 첫 authenticated safe API
  response가 fresh masked token을 제공합니다. 이 신규 response header에는 existing configured CSRF request-header 이름
  `X-GoDj-CSRFToken`을 재사용하고, unsafe request가 token을 같은 request header로 돌려줍니다.
  Raw cookie secret은 노출하지 않습니다.
- Article persistence는 SQLite/PostgreSQL에 같은 typed repository/service port로 연결합니다. Invalid/unauthorized/CSRF-failed
  request의 Article DB mutation은 0이어야 합니다. Authenticated session load가 기존 idle-expiry lifecycle에 따라 access time을
  touch하는 것은 이 측정에서 제외하며 AUT 계약을 그대로 보존합니다.

정확한 exported 이름과 package placement는 Phase A reference와 Phase B compile prototype 뒤 ADR-0045/0046에 동결합니다.

## 비목표

- OpenAPI/schema generation. DRF built-in OpenAPI ownership은 deprecated 상태를 포함해 별도 결정합니다.
- Browsable API, HTML form renderer, multipart/file upload와 content negotiation breadth
- Token/Basic/OAuth/JWT authentication, durable/distributed session/user와 password wire compatibility
- Nested/related/hyperlinked serializer, list serializer write, bulk CRUD, arbitrary custom fields/validators
- Throttling, versioning, metadata, caching, conditional request, streaming, async/background task
- General regex/string/UUID/path converter, route mount/subrouter와 wildcard/catch-all
- Realtime/Channels/WebSocket/SSE, GIS와 M7/M8 전체 완료
- Schema IR, Query AST, ORM/generated ABI, codegen, migration format/backend entry 변경
- Production/non-loopback/TLS/proxy deployment, merge, release와 production rollout

## 기준 상태

- Activation baseline: `f99c200a3c5e36b391aabf6634a94acd79bba69b`, tree
  `9a6509fc08923972c60ffde3f52482240dcdf9be`; GDJ-0043 terminal documentation-only descendant
- Hosted product baseline: `5eda0a458302948a91d48292f666e2cd5eac350c`, tree
  `127e937ae6a6ecc09cf0d2b50cc71fc04e0e3f4a`; EVID-124 / CI #134 exact 27/27 jobs·358/358 steps
- GDJ-0043의 session/auth/CSRF와 Article repository/Admin flow는 Accepted/hosted-verified지만 system state는
  process-lifetime입니다.
- Existing Web Core는 named static route와 static reverse만 지원합니다. Dynamic path를 query-string ID로 우회하지 않고
  이번 packet에서 additive closed parameter route를 검증합니다.
- QRY-034..053, typed pagination building block과 SQLite/PostgreSQL Article query/write path를 보존합니다.
- Draft PR #1은 OPEN/DRAFT/unmerged입니다. Non-force push와 PR refresh는 허용되지만 merge/release는 범위 밖입니다.

## Exact reference profile

- Profile ID=`drf-3.18.0-django-6.1-sqlite-darwin-arm64`; 기존 Django-only profile/oracle bytes는 수정하지 않음
- Isolated `conformance/reference/drf` lock의 `djangorestframework==3.18.0`
- DRF tag commit `11875a38f483cea69d8ef2fd9ede6b96fb602ec4`
- Wheel `djangorestframework-3.18.0-py3-none-any.whl`, SHA-256
  `381fc44d3249c9565c5f723850855b734e99030eb30957a49f506d3fe11d7dcb`
- `Django==6.1`, commit `fe0a859f537d4238cf49fca39073513206f83122`
- uv-managed CPython 3.14.3, SQLite 3.50.4, UTC/C/en-us와 별도 exact Darwin arm64 profile
- Renderer=`JSONRenderer`, parser=`JSONParser`, authentication=`SessionAuthentication`, fixed custom per-method permission,
  pagination=`PageNumberPagination` with fixed page size, router=`SimpleRouter` trailing slash
- Article QuerySet는 primary-key deterministic ordering을 명시하고 list/create/retrieve/update/partial_update/destroy만 게시
- Exact reference ViewSet의 custom `get_success_headers`는 created ID를 named detail reverse에 전달해 `Location`을 만들며,
  oracle은 absolute origin을 버리고 canonical path/query만 보존합니다. GoDj response는 처음부터 relative URI를 반환합니다.

DRF 3.18.0 release note는 2026-08-07 release와 Django 6.1 지원을 명시합니다. Moving `main`, latest alias와 Channels
version은 이 reference의 authority가 아닙니다.

## 계약

Exact contract range는 WEB-028..035와 API-001..010, 두 set의 합계 18개입니다.

### Parameterized Web — exact 8

- `WEB-028`: existing exact static route/reverse behavior와 parameter route의 additive coexistence
- `WEB-029`: closed non-negative integer parameter match와 borrowed typed request accessor
- `WEB-030`: static-route precedence와 declaration-order independence
- `WEB-031`: named reverse의 canonical integer interpolation 및 missing/extra/wrong-kind/overflow rejection
- `WEB-032`: duplicate/ambiguous parameter language의 construction-time rejection
- `WEB-033`: invalid name/converter/pattern/resource cap의 construction-time rejection
- `WEB-034`: trailing slash, invalid integer와 encoded separator의 404 의미
- `WEB-035`: matched parameter path의 method 405와 stable sorted `Allow`

### Article JSON API — exact 10

- `API-001`: JSON-only parser/renderer, bounded exactly-one body, deterministic error field order와 empty 204 body
- `API-002`: Article representation과 full/partial validation의 required/read-only/unknown/null/empty/default 의미
- `API-003`: anonymous/session/permission/CSRF denial의 JSON 403, post-login masked-token refresh, no redirect/`WWW-Authenticate`와 Article DB mutation 0
- `API-004`: deterministic primary-key list, bounded search/published/order filter와 invalid-query 경계
- `API-005`: fixed PageNumber pagination의 count/relative-next/relative-previous/results와 invalid/out-of-range page
- `API-006`: valid POST create의 201/canonical relative Location/representation과 exactly-one row delta
- `API-007`: detail GET representation과 missing/invalid primary-key 404
- `API-008`: PUT full replacement, invalid Article DB mutation 0과 exact row change
- `API-009`: PATCH supplied-field-only update, omitted default 보존과 explicit null/empty row change
- `API-010`: DELETE 204 empty body, permission/CSRF와 repeated missing 404

Raw credential/session/CSRF/cookie bytes, localized prose, JSON whitespace, SQL string, DRF class/module identity, Python
exception repr와 pagination link의 absolute origin은 noncontract입니다. Link path/query와 relative GoDj response는 contract에
남기며 trusted-origin 설정 없이 raw Host를 반사하지 않는 차이를 숨기지 않습니다.

## 설계와 package 방향

```text
serializers → validation
api → web + serializers
api/sessionauth → api + web/sessionauth + auth
examples/article/articleapp → generated Article/project + db/orm
examples/article/adminapp → articleapp + admin audit/registration
examples/article/apiapp → api + api/sessionauth + articleapp
```

- Lower `web`는 `api`, serializer, auth 또는 Article을 import하지 않습니다.
- Generic serializer가 application I/O를 실행하지 않습니다. Article typed adapter가 validation 뒤 explicit create/update/delete를
  호출합니다.
- API error renderer가 backend/internal error를 그대로 직렬화하지 않습니다. Cancellation/deadline은 기존 context ownership을
  유지하고 unexpected internal error는 Web Core의 500 경계로 전달합니다.
- JSON decode는 bounded bytes, exactly one top-level object, duplicate key와 unknown field rejection을 보장합니다.
- Pagination/filter/order는 immutable query plan과 count/page query의 소유권을 명시합니다.
- Page link는 raw `Host`를 반사하지 않는 canonical relative request URI입니다. DRF oracle의 absolute origin은 path/query로
  normalize하며 production trusted-origin/AllowedHosts 설정이 생기기 전에는 absolute API URL을 합성하지 않습니다.

## 구현 단계

- [x] Activation: work/ADR/status/reference provenance와 exact contract range 고정
- [ ] Phase A: DRF 3.18.0 dependency/profile, independent reference scenarios, manifest/oracle/NI/checksum lock
- [ ] Phase B: closed parameter router/reverse prototype와 affected Web tests
- [ ] Phase C: serializer/JSON/session-auth API core와 negative/security tests
- [ ] Phase D: Article API SQLite/PostgreSQL end-to-end, GoDj actual adapter와 zero-diff/deviation classification
- [ ] Phase E: affected normal/race/CGO0/vet, generated drift와 backend canary
- [ ] Final frozen milestone: full `make ci`, Linux/386, repository-external clean copy, independent audit와 exact hosted matrix once
- [ ] Accepted/Verified/completed status and Draft PR terminal mirror after exact hosted success

## 검증 주기

- 매 source checkpoint: gofmt, compile, affected package tests와 관련 conformance lock
- Web checkpoint: `./web/...` normal/race/CGO0/vet와 external compile surface
- API checkpoint: `./serializers/... ./api/... ./examples/article/apiapp/...` normal/race/CGO0/vet
- Backend checkpoint: SQLite actual, digest-pinned PostgreSQL required pass/no-skip와 restart-preserving Article row checks
- Final source freeze에서만 full/386/external archive와 hosted matrix를 한 번 실행
- 문서-only activation/append는 link/frontmatter/status consistency와 `git diff --check`; recursive full product matrix를 만들지 않음

## 완료 조건

- [ ] 두 set의 exact 18 contracts가 reference artifact에 독립 관찰되고 GoDj actual은 oracle-blind하게 생성됨
- [ ] Passing/deviation/locked 합계와 global registry/inventory가 fail-closed하게 일치함
- [ ] Existing static route/Admin/Web/runserver behavior와 generated source가 drift하지 않음
- [ ] Anonymous/permission/CSRF/invalid JSON/validation failures의 Article DB mutation이 0임; valid session access touch는 기존 AUT lifecycle대로 허용
- [ ] SQLite와 PostgreSQL 17에서 같은 list/create/detail/PUT/PATCH/delete 의미가 통과함
- [ ] Public API에 raw `any`, reflection, arbitrary callable converter와 secret serialization ingress가 없음
- [ ] Final frozen local/hosted gates와 independent audit가 통과함
- [ ] CURRENT/work/matrix/evidence/ADR/PR이 같은 exact bytes와 명시된 비목표를 가리킴

## 현재 상태와 다음 정확한 작업

GDJ-0044가 유일한 active packet이고 ready packet은 0입니다. ADR-0045/0046은 Proposed이며 API/Web source 또는
contract status는 아직 바꾸지 않았습니다. 다음 작업은 Phase A에서 기존 root lock을 보존한 isolated DRF 3.18.0 dependency와
exact profile provenance를 lock하고 18개 independent reference scenario/manifest/not-implemented artifact를 생성하는 것입니다. 그 artifact를 먼저
검증한 뒤 Phase B parameter router prototype을 병렬로 통합합니다.
