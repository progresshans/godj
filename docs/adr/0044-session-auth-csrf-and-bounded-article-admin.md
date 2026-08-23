# ADR-0044: Server-side Session/Auth/CSRF and Bounded Article Admin

- 상태: Proposed
- 날짜: 2026-08-24
- 관련 work/contract: [GDJ-0043](../../work/0043-safe-template-validation-session-auth-and-article-admin.md), AUT-001..008, ADM-001..010, Q-015, M6
- 선행 결정: [ADR-0038](0038-minimal-web-core-request-lifetime-and-representation.md),
  [ADR-0042](0042-project-linked-runserver-and-article-development-loop.md),
  [ADR-0043](0043-safe-template-and-model-form-validation.md)
- 대체하는 ADR: 없음

## 맥락

Current Web Core는 borrowed synchronous Request, static named routes와 immutable startup state를 제공하지만 request principal, session,
CSRF 또는 Admin registry를 제공하지 않습니다. Lower `web`가 `auth/admin`을 import하면 dependency direction이 역전되고, principal을
untyped context value에 숨기면 handler가 authentication I/O/error ownership을 알 수 없습니다.

Current Schema IR은 framework user/session/audit table에 필요한 시간, general integer와 string primary-key field breadth가 없습니다.
Runtime raw SQL table을 backend-specific하게 만들거나 `runserver`가 자동 migration하면 current format/backend/project ownership을
우회합니다. 첫 Admin slice는 이 문제를 숨기지 않고 process-lifetime system state와 durable Article data를 구분해야 합니다.

Django Admin의 핵심 가치는 exact DOM/CSS가 아니라 login, permission, list/search, validated CRUD, history/action과 safe POST 흐름입니다.
한편 generated Article facade는 Queryer+Mutator만 요구하고 `db.Atomic`을 포함하지 않으므로 action transaction은 application
integration 경계에서 composite capability로 받아야 합니다.

## 결정 기준

- Session fixation, CSRF, unsafe redirect와 secret logging을 fail-closed하게 방지
- Auth principal/permission을 typed explicit handler boundary로 전달
- Immutable startup registration and same metadata authority as Forms/ORM
- Invalid/unauthorized request에서 Article mutation 0
- SQLite/PostgreSQL에 같은 Article CRUD/action 의미
- Admin DOM/CSS보다 familiar user flow와 semantic event를 우선
- Current Schema IR/migration/generated/backend ABI를 변경하지 않음
- Process-lifetime state의 durability limit를 정직하게 노출

## 고려한 선택지

### Lower Web Request에 mutable auth/session state를 내장

Convenient하지만 `web`가 upper auth policy를 알게 되고 unauthenticated application까지 auth dependency를 갖습니다. 채택하지 않습니다.

### Context value에 principal/session을 숨김

Import cycle은 피하지만 key/type/I/O/error ownership이 암묵적이고 borrowed lifetime 이후 오용하기 쉽습니다. 채택하지 않습니다.

### Framework system table을 raw SQL로 자동 생성

Durability는 얻지만 Schema IR/migration/backend abstraction을 우회하고 startup이 schema를 몰래 변경합니다. 채택하지 않습니다.

### Typed wrapper + explicit Store ports + bounded process state

`web/sessionauth`가 request cookie와 Store를 읽고 typed authenticated handler를 호출합니다. First memory store의 restart limit를
명시하고 Article persistence만 current generated ORM/backend를 사용합니다. 이 선택지를 prototype합니다.

## Proposed 결정

1. `sessions`는 opaque CSPRNG session ID, immutable Record, absolute/idle expiry와 `Load/Create/Rotate/Delete` Store boundary를
   소유합니다. Manager는 ID entropy, expiry, fixation-safe rotation과 flush를 검증합니다. First product store는 concurrent
   memory-only이며 process restart/다중 process 공유를 지원하지 않습니다.
2. Session cookie는 bounded name/path/domain configuration, HttpOnly, SameSite, Secure policy와 exact expiry/delete semantics를
   사용합니다. Loopback HTTP example은 Secure=false라는 개발 경계를 명시하고 production cookie policy를 주장하지 않습니다.
3. `auth`는 Principal, exact string Permission, CredentialAuthenticator, Authorizer와 injectable PasswordHasher를 소유합니다.
   Article example은 startup-provided one-admin credential을 current secure PBKDF2-SHA256 implementation으로 memory에 보관합니다.
   Username/password/hash/session/token은 diagnostic, URL, oracle/actual payload에 포함하지 않습니다.
4. Authentication 성공은 session ID와 CSRF cookie secret을 회전합니다. Invalid/inactive credential은 uniform failure와 auth-state write 0을
   반환합니다. Logout은 auth와 unrelated session values를 flush하고 prior cookie를 삭제한 뒤 Admin login으로 redirect합니다.
   Django의 response-in-place logout과 다른 이 actual `admin.Site` redirect 및 current persistent cookie/delete semantics는
   `DEV-0004`에 exact difference로 고정합니다.
5. CSRF는 safe method를 exempt하고 anonymous safe GET에서도 session write 없이 CSPRNG cookie secret을 발급할 수 있습니다. Unsafe
   method는 cookie secret과 masked form token 또는 supported header token을 constant-time 검증합니다. Login은 cookie secret을
   회전하므로 missing/malformed/wrong/pre-login token은 403과 mutation 0입니다. Token/source body size를 cap합니다.
6. `web/sessionauth`는 `Require(permission, AuthenticatedHandler)`처럼 typed Principal을 explicit argument로 넘깁니다. Context나
   `web.Request`에 principal을 몰래 저장하지 않고 lower `web` package를 변경하지 않습니다.
7. Redirect `next`는 registered local absolute path만 허용합니다. Scheme-relative, absolute URL, control byte와 unknown route는 safe
   Admin index로 대체합니다.
8. `admin`은 startup-time immutable Registry/Site를 만들고 generic ModelConfig를 private erased handler로 봉인합니다. Config는
   normalized `ir.Model`, explicit list/search/order/CRUD/action closures와 permission names를 가집니다. Duplicate model/route/action,
   unknown field와 missing capability는 startup error입니다.
9. First route set은 existing static router를 유지해 query `id`를 사용합니다. Login/logout, Article list/search/page,
   add/change/delete confirmation/history와 one publish action만 제공합니다. Dynamic path/reverse API는 추가하지 않습니다.
10. Article typed adapter는 generated fields/query/Create/Patch/Delete를 직접 사용합니다. Publish action은 app-local composite
    `interface { articleproject.Backend; db.Atomic }`에서 selected IDs를 deterministic order로 update하고 전부 commit하거나 전부
    rollback합니다. Bulk SQL이나 raw SQL은 사용하지 않습니다. Commit outcome unknown은 Article state를 추측하거나 자동 재시도하지
    않고 reconciliation-required error로 반환합니다.
11. Admin audit는 actor ID, action, object ID, changed field names와 sequence를 저장하는 process-lifetime in-memory append-only log입니다.
    변경된 field 값, credential, password/hash, session/CSRF/token은 기록하지 않습니다. 객체 식별용 bounded `object_repr`/display
    label은 보존하며 application data를 포함할 수 있습니다. Confirmed Article DB commit 뒤 non-failing memory append를 publish합니다.
    Commit outcome unknown에서는 success event를 합성하지 않으며 audit가 DB outcome을 증명한다고 주장하지 않습니다. Restart
    durability도 주장하지 않습니다.
12. Exact Django DOM/CSS/table schema가 아니라 normalized status/location, ordered semantic view model, DB row delta, permission/CSRF
    outcome와 audit event를 비교합니다.
13. First Admin breadth는 Article 한 모델과 selected-only `publish` action입니다. Django fixture의 세 registered model 및
    `delete_selected`/`publish_selected` action breadth와의 차이는 `DEV-0005`로 고정하며 미구현 breadth를 passing으로 합성하지 않습니다.

## 결과

- Auth/session policy가 lower Web Core에 침투하지 않고 authenticated handler에 명시적으로 나타납니다.
- First Article Admin user flow는 실제 SQLite/PostgreSQL data를 바꾸지만 user/session/history는 restart 시 사라집니다.
- Django Admin의 familiar workflow를 검증하면서 Go template/Go type system과 현재 static router에 맞는 UI/URL을 사용할 수 있습니다.
- Durable system state, group/content-type/object permission과 advanced Admin은 후속 IR/migration/product work가 필요합니다.
- One-row loop action은 bulk SQL보다 느리지만 bounded selected count cap과 atomicity를 명확히 검증할 수 있습니다.

## 의도적으로 결정하지 않은 것

- Durable/distributed session, cache/backend adapters와 session migration
- Django password hash wire compatibility/upgrade, groups/content types/object permission와 password reset
- General message framework, multi-site Admin, model discovery, inlines/autocomplete/date hierarchy
- Full DOM/CSS/JS/widget parity와 arbitrary custom Admin view/template override
- Production proxy/TLS/cookie deployment, non-loopback bind와 multi-process coordination
- Dynamic route parameters, raw SQL system tables와 runserver auto-migrate

## 검증

- [x] AUT-001..008 and ADM-001..010 pinned Django semantics, secret-free oracles and payload-free baselines
- [x] CSPRNG failure, expiry/rotation/fixation/flush, concurrent memory Store and cookie policy tests
- [x] Password constant-time shape, inactive/unpermissioned user and safe-next/open-redirect negative tests
- [x] CSRF safe/unsafe, cookie/masked/header, anonymous session-write 0, replay/rotation/body caps and mutation-0 tests
- [x] Admin registration/config clone/duplicate/unknown capability tests
- [x] Article add/change/delete/history/selected publish normal/failure/rollback/commit-unknown with stable event and no-retry ownership
- [x] Actual Site SQLite/pinned PostgreSQL user flow and local normal/race/CGO0/vet
- [ ] Final frozen full/386/external-copy audit and exact submitted-head hosted matrix

## 현재 구현 상태

상태는 계속 Proposed입니다. Product source와 actual `admin.Site`/typed Article/SQLite integration은 구현됐습니다. Current
working-tree candidate에서 AUT-001..008은 `DEV-0004`, ADM-001..010은 `DEV-0005` 아래 exact `godjcheck`를 통과했고,
`AUT-004` logout redirect는 surrogate가 아니라 Site login/logout/cookie 경계에서 관찰했습니다. CSRF missing/wrong/form/header와
pre-login replay도 actual add/change POST 및 SQLite before/after로 검증했습니다. SQLite와 pinned PostgreSQL 17 login-to-logout flow,
scoped 993/993/skip-0 inventory와 local normal/race/CGO0/vet이 통과했습니다. Session/user/audit는 여전히 process-lifetime이며 durable
auth/session 또는 M6 completion을 주장하지 않습니다. Final frozen full/386/external-copy audit와 exact submitted-head hosted matrix가
pending이므로 아직 Accepted 또는 bounded Verified로 승격하지 않습니다.
