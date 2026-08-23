---
id: GDJ-0043
status: active
updated: 2026-08-24
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "9099a5306f805fe382bdbc4671262cbe87f4216a"
depends_on: ["GDJ-0038", "GDJ-0041", "GDJ-0042"]
contracts: ["WEB-021", "WEB-022", "WEB-023", "WEB-024", "WEB-025", "WEB-026", "WEB-027", "FRM-001", "FRM-002", "FRM-003", "FRM-004", "FRM-005", "AUT-001", "AUT-002", "AUT-003", "AUT-004", "AUT-005", "AUT-006", "AUT-007", "AUT-008", "ADM-001", "ADM-002", "ADM-003", "ADM-004", "ADM-005", "ADM-006", "ADM-007", "ADM-008", "ADM-009", "ADM-010", "Q-014", "Q-015"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "NOTICE.md"
  - "validation/**"
  - "forms/**"
  - "templates/**"
  - "sessions/**"
  - "auth/**"
  - "web/sessionauth/**"
  - "admin/**"
  - "internal/compiletest/**"
  - "examples/article/adminapp/**"
  - "examples/article/webapp/**"
  - "examples/article/cmd/site/**"
  - "examples/article/*admin*_test.go"
  - "examples/article/*fullstack*_test.go"
  - "conformance/templateform/**"
  - "conformance/authadmin/**"
  - "conformance/contracts/template-form-manifest.json"
  - "conformance/contracts/auth-session-manifest.json"
  - "conformance/contracts/article-admin-manifest.json"
  - "conformance/fixtures/godj-template-form-not-implemented.json"
  - "conformance/fixtures/godj-auth-session-not-implemented.json"
  - "conformance/fixtures/godj-article-admin-not-implemented.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/template-form-oracle.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/auth-session-oracle.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/article-admin-oracle.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS"
  - "conformance/runners/django/**"
  - "conformance/runners/godj/**"
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/SHA256SUMS"
  - "conformance/README.md"
  - "docs/adr/0043-safe-template-and-model-form-validation.md"
  - "docs/adr/0044-session-auth-csrf-and-bounded-article-admin.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/DEVIATIONS.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0043-safe-template-validation-session-auth-and-article-admin.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Safe Template, Validation, Session/Auth, and Article Admin Vertical Slice

## 사용자에게 보이는 결과

미리 generate/migrate된 Article 프로젝트를 `godj runserver`로 실행한 뒤, 한 번의 브라우저 흐름으로 다음 작업을
수행합니다.

```text
GET  /admin/login/
POST /admin/login/                       → session rotation + authenticated redirect
GET  /admin/articles/?q=go               → stable list/search/page
GET/POST /admin/articles/add/             → validated Article create
GET/POST /admin/articles/change/?id=1     → validated Article update
GET/POST /admin/articles/delete/?id=1     → confirmation + Article delete
GET  /admin/articles/history/?id=1        → process-lifetime semantic history
POST /admin/articles/action/publish/      → selected rows only, atomic publish
POST /admin/logout/                       → session flush + anonymous request
```

화면은 safe DTL subset으로 render되고 모든 unsafe POST는 CSRF 검증을 거칩니다. Article row는 SQLite와
PostgreSQL 17에 실제로 저장되지만 첫 slice의 user/session/audit state는 명시적인 server-process store를 사용합니다.

## 목표

- Django 6.1 exact profile에서 template/form, auth/session/CSRF, Article Admin의 30개 observable contract를 고정합니다.
- 임의 Go method/function 호출과 reflection을 노출하지 않는 safe DTL subset과 startup-time immutable engine을 만듭니다.
- Schema IR metadata에서 Char/Boolean/nullable/default/max-length의 form 구조를 만들고 stable validation error를 반환합니다.
- Bound/unbound, cleaned/initial/changed data와 cross-field validation을 DB I/O 없이 수행합니다.
- CSPRNG server-side session, expiry/rotation/flush, typed principal/permission과 constant-time password verification을 구현합니다.
- Safe-method exemption, CSRF cookie secret/masked form token/supported header token, login-time secret rotation과 replay rejection을 구현합니다.
- Startup-time immutable Admin registry와 Article typed adapter로 list/search/add/change/delete/history/one action을 연결합니다.
- 기존 static Web router를 유지하고 current generated Article Create/Patch/Manager와 SQLite/PostgreSQL backend를 재사용합니다.
- 개발 중에는 affected gate만 실행하고 final frozen source에서 full/386/external/audit/hosted matrix를 한 번 닫습니다.

## 비목표

- Django Template Language 전체, arbitrary callable/property reflection, custom tag/filter ecosystem, i18n 또는 loader cache
- Django Admin DOM/CSS/JavaScript/widget byte parity, inlines, autocomplete, date hierarchy 또는 multi-model discovery
- Multipart/file upload, FormSet/ModelFormSet, widget ecosystem, localization 또는 generic ModelForm autosave
- Django user/group/content-type/admin-log table, object permission, password reset, OAuth, remote user 또는 Django password hash wire 호환
- Durable/distributed session, user, message와 audit storage; restart 뒤 기존 로그인/history 보존
- Production/non-loopback/TLS/proxy deployment, Windows 또는 hostile multi-process session coordination
- General dynamic route parameter/reverse API, background task, streaming 또는 request-wide implicit transaction
- Schema IR/Query AST/ORM/generated ABI/codegen/migration format/backend entry 변경
- Raw SQL runtime store, `runserver` auto-generate/auto-migrate/reload 또는 existing DB adoption/repair
- API/Realtime/GIS/i18n와 M5/M6 전체 완료

## 선행 조건과 기준 상태

- Activation baseline: `9099a5306f805fe382bdbc4671262cbe87f4216a`, tree
  `afdda323e1f83f5c67a6a6d87cd3215874d03a53`; clean worktree
- Hosted product baseline: `2bfdbd50ade74c76713a3e1f08ce64ae7abe3dd9`, tree
  `292b82a042afe4af205c5caa5d4b541309d53ee7`; EVID-122/run `32659704239` exact
  27/27 jobs, 358/358 steps, failure/cancel/skip/annotation 0
- [ADR-0038](../docs/adr/0038-minimal-web-core-request-lifetime-and-representation.md)의 immutable Web/request-local DTO,
  [ADR-0041](../docs/adr/0041-typed-scalar-comparisons-and-field-references.md)의 Article filtering과
  [ADR-0042](../docs/adr/0042-project-linked-runserver-and-article-development-loop.md)의 generated-aware runserver를 보존합니다.
- Existing Web router는 namespaced static route만 지원합니다. 첫 slice는 query `id`를 사용하며 dynamic path API를 요구하지 않습니다.
- Current IR은 Auto/Char/Boolean/ForeignKey뿐이므로 framework system table을 억지로 추가하지 않고 explicit Store port를 사용합니다.
- Existing generated `ArticleCreate`/`ArticlePatch`는 private field를 가지므로 form-to-model 변환은 Article app-owned typed closure가 소유합니다.
- Draft PR #1은 OPEN/DRAFT/unmerged이며 merge/release/production rollout은 이 work의 권한 밖입니다.

## Django Reference / Contract

Reference는 current exact `django-6.1-sqlite-darwin-arm64` profile을 그대로 사용합니다. Auth/Admin settings는 별도
deterministic process에서 한 번 구성하고 기존 15개 reference set/oracle을 rewrite하지 않습니다. 결과는 raw HTML/secret이 아니라
rendered semantic fragment, ordered form error, auth state, normalized redirect/cookie category, Article DB state와 audit event로 비교합니다.

### Template/Form manifest — exact 12

- `WEB-021`: scalar render와 missing variable empty
- `WEB-022`: dict/attribute/list-index dotted lookup precedence와 lookup-call metric
- `WEB-023`: autoescape와 explicit safe value
- `WEB-024`: if truth, ordered for와 empty branch
- `WEB-025`: closed `default`/`length`/`lower` filters
- `WEB-026`: unknown tag/filter, underscore access와 unclosed grammar의 construction-time structured error
- `WEB-027`: Django callable exposure observation; GoDj no-call policy는 ADR acceptance 전 deviation candidate
- `FRM-001`: unbound와 bound-empty validation lifecycle
- `FRM-002`: Article valid clean, strip, typed Boolean와 nullable summary
- `FRM-003`: required/max-length/NUL stable field code/order
- `FRM-004`: deterministic cross-field non-field error와 invalid cleaned-data exclusion
- `FRM-005`: invalid write 0, valid create/update row delta와 mutation metrics

### Auth/Session/CSRF manifest — exact 8

- `AUT-001`: anonymous principal/no permission/session write 0
- `AUT-002`: valid login, session rotation과 secret-free observation
- `AUT-003`: invalid/inactive login remains anonymous with auth-state write 0
- `AUT-004`: logout flush and subsequent anonymous request
- `AUT-005`: normalized cookie path/HttpOnly/SameSite/Secure/expiry/delete semantics
- `AUT-006`: anonymous redirect, authenticated-no-permission 403와 safe `next` boundary
- `AUT-007`: unsafe POST missing/wrong CSRF is 403 with mutation 0
- `AUT-008`: form/header token acceptance, login rotation과 pre-login replay rejection

### Article Admin manifest — exact 10

- `ADM-001`: anonymous/nonstaff/staff permission access matrix and safe redirect
- `ADM-002`: stable Article list columns/order/page counts and action availability
- `ADM-003`: query search exact ordered IDs/count and invalid-query DB invariance
- `ADM-004`: change form field order, typed initial values and allowed operations
- `ADM-005`: invalid edit error order, sticky submitted values and mutation 0
- `ADM-006`: valid add row/event and redirect category
- `ADM-007`: valid edit changed fields/row/event and redirect category
- `ADM-008`: confirmed delete row/event and unsafe/missing permission mutation 0
- `ADM-009`: ordered add/change/delete actor/action/object/changed-fields semantic history
- `ADM-010`: selected-only atomic publish, affected count/message and unselected invariance

Full DOM/CSS/whitespace/JS, exact localized prose, raw cookie/session/CSRF/password bytes, Django table names and content-type
primary keys are noncontracts. Tokens, session IDs, passwords와 password hashes는 oracle/actual/log에 직렬화하지 않습니다.

## 설계와 package 경계

아래 화살표는 importer에서 imported dependency를 향합니다.

```text
forms → validation
forms/model → forms + validation + schema/ir
templates
sessions
auth
web/sessionauth → web + sessions + auth
admin → apps + web/sessionauth + auth + forms/model + templates + db/orm/ir
examples/article/adminapp → admin + generated Article/project
```

- `validation`은 stable code/field/params와 immutable ordered errors를 소유하고 표시 문구는 renderer가 소유합니다.
- `forms`는 bound lifecycle과 cleaned values를 소유하며 `forms/model`이 normalized IR metadata를 structural form spec으로 투영합니다.
- `templates`는 String/Bool/Integer/List/Object/Safe 같은 closed value algebra만 resolver에 노출합니다.
- `sessions`는 Store/Manager/Record와 CSPRNG ID, absolute/idle expiry, rotate/delete를 소유합니다. 첫 actual store는 memory-only입니다.
- `auth`는 Principal/Permission/Authenticator/Authorizer/password hasher를 소유하며 Admin을 import하지 않습니다.
- `web/sessionauth`는 cookie/CSRF/login/logout와 typed authenticated handler adapter를 소유합니다. Principal을 context나 Request에 숨기지 않습니다.
- `admin`은 generic registration을 startup에서 erased immutable handler로 봉인합니다. Metadata authority는 `ir.Model` 하나입니다.
- Article adapter는 generated typed filter/create/patch/delete와 app-local `interface { articleproject.Backend; db.Atomic }`를 사용합니다.
  Confirmed commit 뒤에만 audit event를 게시합니다. Commit outcome unknown은 audit append/retry 없이 reconciliation-required로 닫습니다.
- Lower `web`, `orm`, `db`, `schema/ir`, generated code는 새 상위 package를 import하지 않습니다.

## 구현 단계

### Phase A — reference and decision freeze

- [ ] exact 12/8/10 manifests, independent Django scenarios/oracles와 payload-free not-implemented fixtures
- [ ] registry/manifest/profile/provenance/comparison mutation gates and two-process deterministic bytes
- [ ] existing 15 reference oracle no-rewrite gate
- [ ] public compile prototypes, package import-direction gate와 WEB-027 deviation decision

### Phase B — safe template and validation/forms

- [ ] startup parse/load, closed value resolver, autoescape/safe value and resource caps
- [ ] variable/if/for-empty, inheritance/block/include, closed filter, URL/CSRF capability
- [ ] ordered validation errors, bound/unbound, cleaned/initial/changed data and cross-field hook
- [ ] IR Char/Boolean/nullable/default/max-length mapping and Article typed Create/Patch mapping

### Phase C — session/auth/CSRF

- [ ] concurrent memory Store, expiry/rotation/flush and bounded cookie codec
- [ ] constant-time credential verification, inactive/permission/safe-next behavior
- [ ] CSRF safe-method policy, cookie secret, masked form token/header token, rotation and replay rejection
- [ ] secret-free error/log surface, size/resource limits and race tests

### Phase D — Article Admin actual

- [ ] immutable registration/startup checks and static namespaced route set
- [ ] login/logout, list/search/page and add/change/delete/history views
- [ ] selected-only publish action in `db.Atomic`, stable messages and semantic audit events
- [ ] global `godj runserver` SQLite and PostgreSQL 17 full user-flow actual

### Phase E — frozen hardening

- [ ] affected normal/race/CGO-disabled/vet and relevant generated drift at each checkpoint
- [ ] SQLite/PostgreSQL actual, security/error/restart boundaries and inventory locks
- [ ] final source-only full `make ci`, all-package Linux/386 and repository-external clean copy once
- [ ] independent final audits and exact submitted-head hosted matrix once
- [ ] ADR acceptance, bounded Verified/status/evidence/Draft PR mirror only after those gates

## 완료 조건

- [ ] 사용자가 login부터 logout까지 exact Article Admin flow를 수행합니다.
- [ ] 30개 contract가 explicit passing/deviation status와 oracle-blind actual을 가지며 false-green mutation gate를 통과합니다.
- [ ] Arbitrary Go callable/underscore/private value와 unknown template grammar가 startup 또는 render 전에 fail-closed합니다.
- [ ] Invalid form/auth/CSRF/permission request는 Article mutation 0이고 secret을 diagnostic에 노출하지 않습니다.
- [ ] Confirmed-success add/change/delete/publish가 expected Article row와 process-lifetime audit event만 변경하고,
  commit outcome unknown은 자동 재시도나 success audit 없이 reconciliation-required로 닫습니다.
- [ ] SQLite/PostgreSQL 17 actual이 동일한 bounded semantic flow를 통과합니다.
- [ ] Affected/final frozen cadence와 independent audit가 통과합니다.
- [ ] CURRENT/work/matrix/evidence/ADR/PR이 같은 exact bytes와 비목표를 가리킵니다.

## 진행 기록

- [x] code topology, current metadata/write/Web/runserver boundary 조사
- [x] exact 30-contract proposal와 false-green boundary 조사
- [x] active work와 Proposed ADR-0043/0044 activation
- [ ] Phase A reference/compile checkpoint
- [ ] Phase B/C product checkpoint
- [ ] Phase D SQLite/PostgreSQL actual checkpoint
- [ ] Phase E frozen hardening and handoff

## 수정 파일

- Activation: 이 work, ADR-0043/0044, ADR/work indexes, CURRENT/ROADMAP/OPEN_QUESTIONS/IMPLEMENTATION_MATRIX
- Product/reference/evidence paths는 각 checkpoint 뒤 exact commit과 함께 이 절에 기록합니다.

## 결정된 사항

- 2026-08-24: 여러 작은 packet 대신 template/form/auth/admin을 한 사용자 흐름으로 묶은 wide vertical batch를 사용합니다.
- 2026-08-24: existing static Web routes와 query `id`를 사용해 dynamic router API를 이번 slice에 추가하지 않습니다.
- 2026-08-24: Schema IR/system migration 확장 대신 session/user/audit는 explicit process store, Article만 actual DB persistence를 사용합니다.
- 2026-08-24: generated Create/Patch private state를 reflection으로 우회하지 않고 Article-owned typed conversion closure를 사용합니다.
- 2026-08-24: arbitrary Go callable 자동 호출은 지원하지 않는 안전 경계를 ADR-0043의 Proposed decision으로 검증합니다.

## 미결정/Blocker

- External blocker는 없습니다.
- `WEB-027`은 Django callable behavior와 의도적으로 달라질 가능성이 있으므로 ADR-0043 acceptance와 좁은 deviation record 전에는
  passing으로 분류하지 않습니다.
- Exact password hasher parameters, cookie names와 route names는 Phase A compile/security tests 뒤 ADR current implementation에 고정합니다.
- Durable session/user/audit schema와 restart persistence는 current IR 확장 없이 구현하지 않으며 후속 packet입니다.

## 테스트 증거

- Evidence ID: activation-only, 없음
- Command: docs consistency gate만 실행 예정
- Result: 제품/contract test를 아직 실행하지 않음
- Not run: Phase A reference, product, DB actual, full/386/hosted

## 위험과 rollback

- Public API: Phase A external compile fixture 전에 exact generic registration/Value/Form API를 Accepted로 고정하지 않습니다.
- Import cycle: lower package가 `admin/auth/forms/templates`를 import하면 gate가 실패해야 합니다.
- Security: raw secret serialization, callable exposure, unsafe redirect, CSRF bypass와 session fixation은 P0/P1 gate입니다.
- Data: Article action은 `db.Atomic`을 사용하고 process audit의 restart 비내구성을 명시합니다. Commit outcome unknown은
  committed 여부를 추측하거나 audit로 success를 합성하지 않고 no-retry/reconciliation-required로 반환합니다.
- Backend: runtime raw SQL/system table을 추가하지 않고 existing SQLite/PostgreSQL public backend boundary만 사용합니다.
- Rollback: activation은 docs-only이고, 각 product checkpoint는 독립 commit이므로 accepted/publication 전 revert 가능합니다.

## 다음 정확한 작업

`conformance/internal/protocol`의 manifest limit과 current Django settings initialization을 기준으로 exact 12/8/10 manifests,
separate deterministic Auth/Admin runner와 payload-free not-implemented fixtures를 구현합니다. 동시에 Phase A external compile
prototype으로 `templates`, `forms/model`, `sessions`, `auth`, `web/sessionauth`, `admin`의 dependency direction을 검증합니다.

## 결과와 인수인계

현재는 activation 상태입니다. Product code, passing contract, Accepted ADR 또는 M5/M6 완료를 주장하지 않습니다. Integration
owner가 public API/ADR/CURRENT를 단독 소유하고 reference, template/form, auth/session lanes는 서로 다른 파일을 병렬 수정합니다.
