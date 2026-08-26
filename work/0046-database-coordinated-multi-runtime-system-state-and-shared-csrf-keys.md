---
id: GDJ-0046
status: active
updated: 2026-08-26
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "996c00a5fb4d634b5dc7bef4c5385f2353a89979"
depends_on: ["GDJ-0038", "GDJ-0045"]
contracts: ["SYS-013", "SYS-014", "SYS-015", "SYS-016", "SYS-017", "SYS-018", "SYS-019", "SYS-020", "Q-020"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "db/db.go"
  - "db/sqlite/**"
  - "db/postgres/**"
  - "query/error.go"
  - "query/*_test.go"
  - "systemstate/**"
  - "sessions/*_test.go"
  - "web/sessionauth/**"
  - "api/sessionauth/*_test.go"
  - "admin/*_test.go"
  - "examples/article/articleapp/**"
  - "examples/article/adminapp/**"
  - "examples/article/apiapp/**"
  - "examples/article/internal/siteapp/**"
  - "examples/article/cmd/site/**"
  - "examples/article/*admin*_test.go"
  - "examples/article/*api*_test.go"
  - "examples/article/*e2e_test.go"
  - "conformance/systemstate/**"
  - "conformance/contracts/system-state-manifest.json"
  - "conformance/contracts/manifest.json"
  - "conformance/fixtures/godj-system-state-not-implemented.json"
  - "conformance/fixtures/godj-system-state-deviation-expected.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS"
  - "conformance/runners/django/system_state_decisions.py"
  - "conformance/runners/django/runner.py"
  - "conformance/runners/django/tests/test_scenarios.py"
  - "conformance/runners/django/tests/test_system_state_scenarios.py"
  - "conformance/runners/godj/**"
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/runserverproduct/**"
  - "conformance/README.md"
  - "docs/adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/DEVIATIONS.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0046-database-coordinated-multi-runtime-system-state-and-shared-csrf-keys.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0046 — Database-Coordinated Multi-Runtime System State and Shared CSRF Keys

## 사용자에게 보이는 결과

동일한 SQLite 파일 또는 PostgreSQL schema를 사용하는 두 GoDj Article Runtime이 동시에 살아 있어도 같은 durable credential,
session과 audit state를 안전하게 공유합니다. 한 Runtime에서 로그인한 browser는 다른 Runtime에 라우팅돼도 인증과 CSRF 검증을
계속 통과하고, 경쟁하는 session touch/rotate/logout과 Article Admin mutation은 database가 정한 한 선형화 순서로 처리됩니다.

```text
explicit migrate
→ process A와 process B를 같은 DB/schema + 같은 deployment CSRF key set으로 시작
→ A login/session 발급 → B authenticated Admin/API request 성공
→ A/B concurrent touch/create/rotate/logout과 Article mutation
→ monotonic session state, bounded capacity/audit, old bearer resurrection 0
→ staged CSRF active-key rotation 중 old/new Runtime 상호 검증
→ clean stop/restart 뒤 같은 의미 보존
```

## 목표

- Process-local mutex가 아니라 같은 database transaction 안의 backend coordination fence가 cooperative multi-runtime correctness를
  소유하게 합니다.
- GDJ-0045의 credential bootstrap, digest-only session Create/Touch/Rotate/Delete/capacity/reap, audit append/prune와 Article+
  audit transaction을 여러 Runtime에서 선형화합니다.
- PostgreSQL 17에서는 schema+versioned domain으로 유도한 blocking transaction advisory lock을, SQLite에서는 pinned connection의
  `BEGIN IMMEDIATE`를 사용하되 하나의 backend-neutral callback-once 계약으로 수렴합니다.
- `web/sessionauth`에 active key 하나와 bounded validation key set을 가진 opaque/redacted CSRF key ring을 주입해 같은 deployment의
  여러 Runtime과 staged key rotation을 지원합니다.
- 실제 두 process와 두 backend에서 barrier 기반 경쟁, cancellation, rollback과 commit-outcome-unknown no-retry를 검증합니다.

## 이번 packet에서 결정하는 경계

- `db`에는 additive coordinated-atomic SPI를 둡니다. Callback은 backend가 구성한 database/schema coordination domain의 fence를
  얻은 뒤 정확히 한 번 호출되고, fence 획득 전 cancellation/failure에서는 0회이며 framework가 callback을 자동 retry하지 않습니다.
- PostgreSQL coordination은 ordinary query AST에 raw lock SQL을 노출하지 않고 backend 내부에서 transaction-scoped advisory lock을
  얻습니다. Lock identity는 exact `godj/postgres/coordinated-atomic/v1` domain과 backend schema의 length-delimited digest에서
  결정하며 migration revision-lock domain과 재사용하지 않습니다. 한 transaction은 두 domain 중 하나만 얻고 nested/cross-domain
  backend transaction은 caller contract에서 금지합니다.
- SQLite coordination은 ordinary `db.Atomic` 의미를 조용히 바꾸지 않고 별도 pinned-connection `BEGIN IMMEDIATE` 경계로 구현합니다.
  SQLite database 전체 writer를 직렬화하는 더 강한 contention은 허용하지만 correctness를 약화하지 않습니다. 첫 구현은 backend가
  BUSY/LOCKED acquisition을 자동 retry하지 않고 configured driver busy timeout만 기다리며, 실패하면 callback 0회로 원인을 보존합니다.
- `systemstate.Backend`는 coordinated-atomic capability를 요구하고 `Runtime.withAtomic`은 local mutex 뒤 그 database fence를 사용합니다.
  Local mutex는 같은 Runtime의 contention 감소만 소유합니다.
- `Open`의 final readiness/cardinality/bootstrap 판단도 같은 coordinated transaction에서 다시 수행합니다. Password hashing처럼
  transaction 밖에서 안전하게 계산할 수 있는 비밀 파생은 lock 보유 시간을 늘리지 않습니다.
- Existing `sessions.Store`, `sessions.Manager`, Article repository/Admin/API public contract는 유지합니다. Session raw bearer는 계속
  transient input일 뿐 DB/log/error/artifact에는 domain-separated digest만 남습니다.
- Concurrent logout과 rotation은 database가 확정한 순서에 대해 선형화합니다. Logout-first면 rotate가 실패하고 old bearer는
  부활하지 않습니다. Rotate-first 뒤 old-ID logout이 새 replacement family까지 취소하는 강한 의미는 이번 계약이 아닙니다.
- CSRF key ring은 immutable active key와 bounded verification key set입니다. 모든 key는 exact 32-byte opaque material이고
  String/GoString/JSON/error surface는 redacted됩니다. Verification은 bounded key 전체를 constant-time comparison하며 active key만
  새 token MAC을 만듭니다.
- Zero key-ring config의 기존 process-local CSPRNG behavior는 개발/single-runtime 경계로 남길 수 있지만, multi-runtime Article
  composition과 conformance actual은 명시적으로 같은 injected ring을 사용합니다.
- Commit literal error는 기존 `commit_outcome_unknown`이고 retry/synthetic success를 만들지 않습니다. Fence acquisition failure와
  callback/rollback failure도 secret-free stable error ownership을 가져야 합니다. Acquisition은 새 public error code를 만들지 않고
  context cancellation 또는 backend cause를 보존하며 rollback 불확실성만 기존 `transaction_outcome_unknown`으로 승격합니다.

## 비목표

- General `Unique`/index/constraint Schema IR, `IntegerField`, revision column/CAS와 migration definition/codegen 변경
- Direct SQL 또는 구버전/non-cooperative writer 방어, external DB client fencing과 online schema migration 중 serving
- Session family/generation/tombstone, rotate-first 뒤 family-wide logout, global account revocation과 background reaper
- Distributed cache, leader election, Redis session backend, cross-database transaction과 multi-DB router
- DB에 CSRF/cookie signing key 저장, KMS/Vault/provider SDK, automatic key distribution과 unbounded key history
- Cookie signing, Admin notice, password reset, JWT/OAuth/OIDC access/refresh-token key를 CSRF key ring 하나로 합치기
- Bearer/JWT/opaque token, refresh rotation/reuse detection, OpenAPI/browsable API와 별도 permission system
- Production server/TLS/proxy/load-balancer health, merge, release와 production rollout

## 선행 조건과 기준 상태

- Activation baseline: `996c00a5fb4d634b5dc7bef4c5385f2353a89979`, tree
  `ebf73aaca349dfd56fdaf2cba0806ab03054cd09`; GDJ-0045 terminal documentation-only descendant
- Baseline CI #147/run `32837709461`은 exact `996c00a...`에서 27/27 jobs·359/359 steps와
  failure/cancel/skip 0으로 통과했습니다. 이는 activation ancestry 확인이며 새 product EVID나 SYS-013..020 proof가 아닙니다.
- Hosted product proof: `e673b3a11d4d0d7e2f8a55fdb3c58d24b965ff35`, tree
  `917d36f8ef4458740c377904f4f93597c7c906ec`; EVID-129/CI #146 exact 27/27 jobs·359/359 steps,
  PostgreSQL required 16/16·skip 0
- [ADR-0047](../docs/adr/0047-explicit-single-runtime-system-state.md)은 one-runtime/sequential restart를 Accepted했고
  Q-020을 broader multi-runtime에 대해 `Partial`로 남겼습니다.
- Current SQLite `db.Atomic`은 deferred transaction이고 PostgreSQL은 READ COMMITTED입니다. `Runtime.withAtomic`의 mutex는 Runtime
  instance 하나에만 적용되므로 서로 다른 Runtime의 check-then-act를 직렬화하지 않습니다.
- Existing `auth.Principal`/`Authorizer`와 `api/sessionauth` adapter boundary는 Bearer/JWT 후속을 막지 않으므로 Q-021 구현보다 이
  database/key 기반을 먼저 닫습니다.
- Draft PR #1은 OPEN/DRAFT/unmerged입니다. Non-force push와 PR refresh는 허용되지만 merge/release는 범위 밖입니다.

## Exact contract range

SYS-013..020은 Django 내부 구조를 모사하지 않는 GoDj operational decision contract입니다. Phase A reference authority는 Proposed
ADR-0048이고 Phase B~E의 oracle-blind database/process observation은 그 결정을 검증합니다. SYS-001..012의 observation semantics와
canonical legacy subsuite bytes 및 DEV-0008을 조용히 재작성하지 않습니다.

- `SYS-013` / `godj.system_state.coordinated_atomic_fence` / commit: cross-process coordinated transaction,
  fence-before-callback, callback once/zero, cancel/rollback/commit-unknown과 no retry
- `SYS-014` / `godj.system_state.concurrent_admin_bootstrap` / commit: concurrent empty credential bootstrap의 exactly-one
  durable row와 identical material success/mismatch failure
- `SYS-015` / `godj.system_state.concurrent_session_capacity` / commit: concurrent session Create와 global capacity/reap의
  digest uniqueness·bounded count
- `SYS-016` / `godj.system_state.concurrent_touch_monotonicity` / commit: two-Runtime out-of-order Touch의 monotonic
  accessed/idle expiry와 stale overwrite 0
- `SYS-017` / `godj.system_state.concurrent_session_rotation` / commit: concurrent Rotate/Touch/Delete의 atomic publication,
  exactly-one winner와 명시적 logout linearization
- `SYS-018` / `godj.system_state.concurrent_article_audit` / rollback: concurrent Article Admin DML과 audit append/prune의
  same-transaction commit/rollback 및 global bound
- `SYS-019` / `godj.system_state.shared_csrf_key_ring` / evaluation: shared active/validation CSRF ring의
  cross-Runtime handoff, staged rotation과 unrelated/removed key rejection
- `SYS-020` / `godj.system_state.two_process_backend_restart` / environment: 실제 두 process의 SQLite/PostgreSQL
  same-DB barrier 경쟁, clean stop/reopen과 secret leak 0

Phase A publication 전에는 이 ID를 global aggregate에 등록하거나 `passing`으로 표현하지 않습니다. Phase A에서는 같은
system-state reference set에 `oracle_locked`로만 append하고 product adapter/count는 바꾸지 않습니다. Reference artifact와 Go actual은
별도 생성 경로를 사용하고 exact bytes/checksum을 함께 게시하며 legacy SYS-001..012 canonical subsuite hash를 별도로 잠급니다.

## 설계 가설과 package 방향

```text
systemstate.Runtime
  → local mutex (contention only)
  → db.CoordinatedAtomic(callback)
      ├─ SQLite: pinned connection + BEGIN IMMEDIATE + callback + COMMIT/ROLLBACK
      └─ PostgreSQL: tx + blocking advisory_xact_lock(fixed domain, schema) + callback + COMMIT/ROLLBACK

web/sessionauth.Config
  → optional opaque CSRFKeyRing
      ├─ active key: sign new masked token
      └─ bounded validation keys: verify staged old/new token
```

- `db.Atomic`은 그대로 남고 ordinary application callers의 transaction timing을 바꾸지 않습니다.
- Backend coordination 구현은 query/migration public IR을 확장하지 않습니다. Migration fence와 runtime coordination의 lock ordering은
  live migration/serving 비목표 안에서도 deadlock을 만들지 않도록 backend tests로 고정합니다.
- `systemstate.Runtime.Atomic`을 Article backend로 사용하는 기존 hook 구조 덕분에 Article DML과 audit도 같은 fence에 자동 합류합니다.
- Key ring은 secret provider가 아니라 immutable already-loaded material입니다. Env/file/KMS loading은 composition owner가 담당하고
  framework error는 secret bytes나 caller carrier를 보존하지 않습니다.

## 구현 단계

- [x] Activation — baseline, SYS-013..020, Proposed ADR-0048, allowed paths와 비목표 고정
- [ ] Phase A — decision manifest/reference/NI/checksum과 protocol artifact invariants
- [ ] Phase B — coordinated-atomic SPI, SQLite/PostgreSQL implementation과 callback/fault/cancel unit tests
- [ ] Phase C — systemstate Open/session/audit/Article integration과 same-process two-Runtime barrier tests
- [ ] Phase D — opaque CSRF key ring, site composition과 cross-Runtime/staged-rotation tests
- [ ] Phase E — distinct two-process SQLite actual과 required PostgreSQL 17.10 actual, secret scan과 no-skip sentinel
- [ ] Checkpoint — affected normal/race/CGO0/vet, generated/artifact drift와 backend canary
- [ ] Final frozen milestone — full `make ci`, Linux/386, repository-external clean copy, independent audit와 exact hosted matrix once
- [ ] Accepted/Verified/completed status와 Draft PR terminal mirror

Phase B의 SQLite/PostgreSQL 구현은 파일 소유권을 나눠 병렬화할 수 있습니다. Public SPI, conformance registry, ADR, CURRENT와
integration wiring은 integration owner 한 명만 수정합니다.

## 완료 조건

- [ ] SYS-013..020이 decision reference와 oracle-blind Go actual에서 예상 classification으로 검증됨
- [ ] 같은 DB/schema의 두 Runtime이 credential/session/capacity/audit/Article check-then-act를 DB fence 아래 선형화함
- [ ] PostgreSQL과 SQLite에서 callback once/zero, cancellation, rollback과 commit-unknown no-retry가 검증됨
- [ ] Concurrent touch가 timestamp를 뒤로 돌리지 않고 rotate는 exactly-one replacement만 게시함
- [ ] Logout/rotate 결과가 명시된 linearization contract와 일치하고 old bearer가 다시 만들어지지 않음
- [ ] Shared key ring의 cross-Runtime token, staged rotation과 removed-key rejection이 secret-free하게 통과함
- [ ] Raw bearer, CSRF key/cookie/token, password와 DB URL이 DB payload/artifact/log/error/diagnostic에 노출되지 않음
- [ ] SQLite와 digest-pinned PostgreSQL 17.10에서 실제 두 process required sentinel이 skip 0으로 통과함
- [ ] Schema IR/definition/state/digest/codegen/generated ABI와 sessions.Store/API public behavior가 drift하지 않음
- [ ] CURRENT/work/matrix/evidence/ADR/PR이 같은 frozen source와 명시적 한계를 가리킴

## 위험과 rollback

- SQLite manual transaction cleanup이 불확실하면 physical connection을 pool에 돌려보내지 않습니다.
- SQLite wait-success actual은 composition이 명시한 bounded busy timeout을 사용합니다. Timeout이 없는 BUSY/LOCKED는 callback 0회
  acquisition failure이며 framework retry를 만들지 않습니다.
- PostgreSQL advisory lock identity는 collision 시 false contention만 만들고 concurrent escape를 만들지 않아야 합니다.
- Fence를 잡은 채 password hashing, network call 또는 application callback 밖의 unbounded 작업을 하지 않습니다.
- Callback/commit을 자동 retry하면 session rotate/Article audit가 중복될 수 있으므로 retry ownership을 추가하지 않습니다.
- Key ring material은 직렬화/formatting API를 제공하지 않고 validation key count를 hard cap합니다.
- 일반 IR이나 public Store signature가 필요해지면 Phase B 전에 작업을 멈추고 packet/ADR을 재검토합니다.

## 다음 정확한 작업

1. SYS-013..020 decision manifest/reference/not-implemented/checksum과 legacy SYS-001..012 subsuite invariant를 Phase A로 게시합니다.
2. `db/db.go`, `db/sqlite`, `db/postgres`에 additive SPI와 backend-specific fault tests를 구현합니다.
3. Source checkpoint 전에 `systemstate.Runtime`을 새 SPI로 전환하지 않고 backend contract 자체를 normal/race로 검증합니다.
