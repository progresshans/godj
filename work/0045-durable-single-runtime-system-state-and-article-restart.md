---
id: GDJ-0045
status: completed
updated: 2026-08-25
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "99014e1dbc8169b9ae9e0d5b6d592f808e4d8b07"
depends_on: ["GDJ-0038", "GDJ-0043", "GDJ-0044"]
contracts: ["SYS-001", "SYS-002", "SYS-003", "SYS-004", "SYS-005", "SYS-006", "SYS-007", "SYS-008", "SYS-009", "SYS-010", "SYS-011", "SYS-012", "Q-020"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "systemstate/**"
  - "auth/error.go"
  - "auth/*_test.go"
  - "sessions/error.go"
  - "sessions/manager.go"
  - "sessions/memory.go"
  - "sessions/record.go"
  - "sessions/store.go"
  - "sessions/*_test.go"
  - "web/sessionauth/error.go"
  - "web/sessionauth/runtime.go"
  - "web/sessionauth/*_test.go"
  - "api/error.go"
  - "api/*_test.go"
  - "api/sessionauth/runtime.go"
  - "api/sessionauth/*_test.go"
  - "admin/audit.go"
  - "admin/registry.go"
  - "admin/*_test.go"
  - "examples/article/articleapp/**"
  - "examples/article/adminapp/**"
  - "examples/article/internal/siteapp/**"
  - "examples/article/cmd/site/**"
  - "examples/article/*admin*_test.go"
  - "examples/article/*api*_test.go"
  - "examples/article/*e2e_test.go"
  - "conformance/systemstate/**"
  - "conformance/contracts/system-state-manifest.json"
  - "conformance/fixtures/godj-system-state-not-implemented.json"
  - "conformance/fixtures/godj-system-state-deviation-expected.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS"
  - "conformance/reference/django/**"
  - "conformance/runners/django/**"
  - "conformance/runners/godj/**"
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/runserverproduct/**"
  - "conformance/README.md"
  - "docs/adr/0047-explicit-single-runtime-system-state.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVIATIONS.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0045-durable-single-runtime-system-state-and-article-restart.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0045 — Durable Single-Runtime System State and Article Restart

## 목표

GDJ-0043/0044의 process-lifetime user/session/audit를 명시적으로 migration되는 current-only system schema로 옮겨,
다음 실제 사용자 흐름을 SQLite와 PostgreSQL에서 재시작까지 보존합니다.

```text
explicit Article + godj_system migrate
→ process A: one-time admin bootstrap → login/session rotation → Admin Article mutation + authenticated API read
→ clean stop and all handles close
→ process B: same DB + cookie로 principal/permission/history 복구
→ restart 전 CSRF token 거부 → safe GET의 fresh token으로 API unsafe mutation 성공
→ logout and clean stop
→ process C: copied old cookie가 Admin/API 모두 anonymous
```

Admin-originated Article mutation과 Admin audit append/prune은 같은 `db.Atomic` 안에서 commit 또는 rollback합니다. 이 packet은 DB/schema당
동시에 살아 있는 `systemstate.Runtime` 하나와 완전히 종료된 process 사이의 순차 재시작만 지원합니다.
One-runtime은 lease/fence로 강제되지 않는 operator topology precondition이며 두 번째 `Open`을 자동 차단한다는 의미가 아닙니다.

## 이번 packet에서 결정하는 경계

- Framework system table은 runtime startup에서 만들지 않습니다. Caller가 existing
  `definition.Load` + `migrations.Executor.Migrate`로 Article과 `godj_system.0001_initial`을 명시 적용합니다.
- `godj_system`은 현재 Auto/Char/Boolean IR만 사용합니다. 이번 packet 때문에 `Unique`, 새 field kind, definition wire,
  digest domain, generated ABI를 바꾸지 않습니다.
- System schema는 admin credential, session, audit 세 모델입니다. Time/object ID/permissions/changed fields는
  versioned bounded canonical string payload로 저장하며 unknown version, malformed 또는 oversize는 fail-closed합니다.
- Admin table은 0 또는 정확히 1행만 허용합니다. Empty에서만 one-time bootstrap하고, restart의 동일 material은 zero-write
  검증입니다. Username/principal/password/permission/active mismatch와 2+행은 기존 row를 고치지 않고 startup을 닫습니다.
- Session bearer 자체는 DB, log, error, observation에 저장하지 않습니다. Domain-separated SHA-256 digest만 lookup key로 쓰고
  0/1 결과만 허용합니다. Duplicate는 자동 선택·삭제하지 않고 fail-closed합니다.
- 하나의 runtime mutex와 DB transaction이 bootstrap, session create/rotate/touch/delete/reap와 ordered audit append/prune writer를
  직렬화합니다. DB-enforced uniqueness나 non-cooperating direct SQL writer 방어는 이번 지원 범위가 아닙니다.
- Mutex는 현재 supported topology의 cooperative check-then-act correctness를, DB transaction은 commit/rollback과 outcome-unknown
  원자성을 소유합니다. Future multi-runtime correctness에는 DB constraint/lock/CAS와 shared capacity/prune coordination이 필요합니다.
- `sessions.Manager`의 CSPRNG ID, absolute/idle expiry, monotonic touch, fixation-safe rotate, collision retry, capacity와 flush 의미를
  유지합니다. Durable adapter가 immutable `Record`를 복원할 수 있는 최소 current-only restore SPI만 추가합니다.
- Auth는 startup에서 검증한 한 admin credential을 existing immutable authenticator로 materialize합니다. User/group 변경 UI,
  authentication backend breadth와 password rehash/upgrade는 추가하지 않습니다.
- CSRF signing key는 계속 process-local CSPRNG입니다. Durable session이 살아 있어도 restart 전 masked token은 거부하고,
  authenticated safe request가 같은 CSRF cookie에 대해 새 masked token을 제공합니다.
- Article repository의 기존 mutation 의미는 transaction-scoped hook/kernel로 한 번만 유지합니다. Admin은 Article DML 뒤 같은
  borrowed `db.Session`에서 redacted audit event를 쓰며 hook 실패는 둘 다 rollback합니다.
- API write는 기존 app-owned repository와 API error/CSRF 의미를 유지하며 Django Admin-style audit를 합성하지 않습니다.
  Restart E2E의 API lane은 durable authentication/CSRF와 Article row persistence만 검증합니다.
- Confirmed commit만 success/history로 게시합니다. `commit_outcome_unknown`은 retry, synthetic success/audit, verified-commit 주장을
  만들지 않고 reconciliation-required 상태로 반환합니다.
- Startup readiness는 current applied migration identity와 required query/strict decode를 확인합니다. Arbitrary physical drift,
  online repair/adoption, raw catalog inspector 또는 startup auto-migrate를 일반 기능으로 주장하지 않습니다.

## 비목표

- 동시에 두 개 이상 runtime/process가 같은 system schema를 쓰는 구성, distributed session과 leader election
- DB unique constraint, direct SQL/non-cooperating writer 방어와 online schema repair
- Schema IR/definition/state/digest/codegen ABI 변경, migration autodetector와 general framework settings
- User/group/content-type/object permission, password change/reset/rehash와 Django password/table wire compatibility
- Persistent CSRF signing key. Restart 전 masked token을 계속 수용하는 것은 이번 GoDj 정책이 아닙니다.
- API write의 Django Admin-style audit, object-level permission, token/OAuth/JWT와 broader Admin/API
- Background session reaper. Expired row는 bounded load/create/reap 경계에서만 정리합니다.
- Unknown commit 자동 reconciliation, retry, exactly-once claim와 contiguous audit sequence
- Production/non-loopback/TLS/proxy/cookie deployment, merge, release와 production rollout
- M6/M7 전체 완료, Realtime/GIS/i18n/multi-DB/MySQL/Oracle 지원

## 기준 상태

- Activation baseline: `99014e1dbc8169b9ae9e0d5b6d592f808e4d8b07`, tree
  `fd416a0156d158fc518fbb1ad998f513ba079cdc`; GDJ-0044 terminal source `d9c1971...`의 documentation-only descendant
- Hosted product source: `d9c19712cefde9bf4b2672ad1a0fc90a9dd02a92`, tree
  `2e5c52ff162dc74d6281c1927750be34be329c69`; EVID-125/126와 CI #142 exact 27/27
- Corrected frozen GDJ-0045 source: `6243682e8ec6c94913dda0162cce101b39af354d`, tree
  `98076ea6d469a6405851cc51f2b245806f4230da`; first submitted `8c80c64...` run `32830533384`의 PostgreSQL
  scan collision/Python stale digest lock을 분리한
  [EVID-128](../docs/status/TEST_EVIDENCE.md#evid-20260825-128--gdj-0045-first-hosted-lock-failures-and-corrected-local-refreeze)의
  corrected full/386/1,016-file repository-external archive와 독립 audit 통과
- Hosted-tested submitted descendant: `e673b3a11d4d0d7e2f8a55fdb3c58d24b965ff35`, tree
  `917d36f8ef4458740c377904f4f93597c7c906ec`; EVID-129/CI #146 exact 27/27 jobs·359/359 steps와
  PostgreSQL 17.10 required 16/16·skip 0 통과
- Current hosted-verified publication aggregate는 product 20 sets/219 contracts=`203 passing + 16 deviation`이고,
  reference-only MIG-075..086을 포함하면 21/231/420=`203 passing + 16 deviation + 12 oracle_locked`입니다.
- Existing site는 every-start password hash, `MemoryAuthenticator`, `MemoryStore`, process audit ring을 조합하므로 restart 뒤
  credential/session/history가 사라집니다.
- Existing `sessions.Manager`, `web/sessionauth.Runtime`, `api/sessionauth.Runtime`, Article repository와 SQLite/PostgreSQL
  `db.Atomic`은 재사용합니다.
- Draft PR #1은 OPEN/DRAFT/unmerged입니다. Non-force push와 PR refresh는 허용되지만 merge/release는 범위 밖입니다.

## Exact contract range

Phase A에서 하나의 exact `system-state` set, SYS-001..012를 고정합니다. Django-observed public behavior와 GoDj-owned
operational decision을 같은 profile에 두되 provenance를 contract별로 분리합니다.

- `SYS-001` explicit current system migration과 startup schema gate; auto-DDL/listener/bootstrap mutation 0
- `SYS-002` one-time admin bootstrap, identical restart zero-write와 mismatch/corrupt/duplicate fail-closed
- `SYS-003` durable credential/permission restart authentication
- `SYS-004` login rotation 뒤 same-cookie Admin/API session restart
- `SYS-005` monotonic touch, idle/absolute expiry와 expired-row deletion
- `SYS-006` capacity, bounded reap와 atomic session rotate fault rollback
- `SYS-007` digest-only bearer storage, current-only bounded codecs와 secret-free observation
- `SYS-008` logout 뒤 다음 process의 old-cookie denial과 resurrection 0; Admin은 Django browser redirect,
  API는 Accepted ADR-0046의 JSON 403 boundary
- `SYS-009` restart CSRF: stale token 거부와 fresh-token success
- `SYS-010` Article DML 뒤 audit fault에서 same-transaction rollback
- `SYS-011` durable newest-bounded audit history와 monotonic non-contiguous sequence
- `SYS-012` commit outcome unknown의 no-retry/no-synthetic-success 경계

SYS-003/004/008/009/010/011은 Django 6.1 public semantics를 관찰합니다. SYS-008의 session deletion/restart는
Django authority이고 API denial status는 기존 Accepted ADR-0046의 JSON 403 authority입니다. SYS-001/002/005/006/007/012는
ADR-0047의 GoDj decision contract입니다. SYS-009의 stale token 한 lane만 Verified DEV-0008이며 fresh-token lane과 나머지
dimension은 zero-diff여야 합니다.

Phase A observer-only 상태에서는 이 12개를 global aggregate에 더하지 않았습니다. Current source publication은 manifest,
oracle, exact DEV-0008 policy, global registry와 Makefile adapter를 함께 열어 reference 21 sets/231 contracts/420 ordered
bindings=`203 passing + 16 deviation + 12 oracle_locked`, product 20/219=`203 passing + 16 deviation`으로 고정합니다.
MIG-075..086만 locked/unregistered이며 frozen registry 출력이 문서와 다르면 실행 결과를 권위로 삼습니다.

## 설계와 package 방향

```text
migrations definition/executor → explicit godj_system schema only
systemstate → query + db + sessions + auth + admin semantic event
web/sessionauth + api/sessionauth → injected durable Store/authenticator
articleapp repository → db.Atomic + transaction-scoped mutation hook
adminapp service → articleapp hook + durable audit writer
siteapp → systemstate composition; no memory fallback and no auto-migrate
```

- `sessions.Record`는 public mutable field를 만들지 않고 snapshot/restore 한 쌍으로 durable adapter에 필요한 current state만 전달합니다.
- `admin.PreparedEvent`는 secret-free immutable snapshot/getter를 제공하며 durable adapter가 validation을 복제하지 않습니다.
- Article hook은 borrowed `db.Session` 안에서만 호출되며 nested `Atomic`, post-commit append와 duplicated CRUD kernel을 만들지 않습니다.
- Memory session/auth/audit 구현은 package unit test용으로 남을 수 있지만 product site에는 flag/fallback/shim으로 남기지 않습니다.
- PostgreSQL audit AutoField sequence는 rollback에도 gap이 생길 수 있습니다. Ordering은 strictly increasing이며 contiguous가 아닙니다.

## 구현 단계

- [x] Activation: bounded single-runtime scope, Proposed ADR/DEV, SYS-001..012와 owner paths 고정
- [x] Phase A — contract/reference: exact manifest, independent Django oracle, payload-free NI, checksum과 mixed-provenance protocol lock
- [x] Phase B — current explicit system migration, SQLite migrate/reopen/no-op와 missing-schema fail-closed implemented; PostgreSQL
      compiler/integration source wired and required live PostgreSQL execution passed in hosted Phase E
- [x] Phase C — durable bootstrap/session plus Touch↔Rotate monotonicity, duplicate-cookie logout and secret-surface hardening implemented
- [x] Phase D — transactional audit, bootstrap commit-unknown classification and oracle-blind Go actual implemented
- [x] Phase E — SQLite distinct-process A/B/C and local secret scans pass; pinned PostgreSQL child-process required lane passes hosted exact 16/16 with required skip 0
- [x] Checkpoint local gates: affected normal/race/CGO0/vet, generated drift, exact Django oracle, local SQLite actual와
      corrected full/386/external audit pass; required PostgreSQL와 hosted gate pass in EVID-129
- [x] Final frozen local milestone: corrected source `6243682...`에서 full `make ci`, Linux/386,
      repository-external archive와 independent audit pass; exact submitted-head hosted matrix passes in EVID-129
- [x] Accepted/Verified/completed status and Draft PR terminal mirror after exact hosted success

Phase A/B와 Phase C/D는 file ownership을 나눠 병렬로 진행합니다. Public protocol, ADR, CURRENT와 integration wiring은
integration owner 한 명만 수정합니다.

## 검증 주기

- 매 source checkpoint: gofmt, compile, affected package test와 relevant artifact/no-rewrite gate
- System checkpoint: `./systemstate/... ./sessions/...` normal/race/CGO0/vet와 SQLite file-backed actual
- Article checkpoint: `./examples/article/articleapp/... ./examples/article/adminapp/...` rollback/unknown fault tests
- Restart checkpoint: distinct child PID, listener/backend close, same DB/cookie handoff, raw secret/log/temp leak 0
- Backend checkpoint: digest-pinned PostgreSQL 17.10 required tests, no skip/continue-on-error와 unique schema cleanup
- Final source freeze에서만 full/386/external archive와 hosted matrix를 한 번 실행
- Documentation-only activation/append는 link/frontmatter/status consistency와 `git diff --check`; product matrix를 재귀 실행하지 않음

## 완료 조건

- [x] SYS-001..012 exact artifact가 independent Django/decision authority와 oracle-blind Go actual에서 검증됨
- [x] Completion classification은 expected `11 passing + SYS-009 one deviation`이고 local unexpected difference는 0임
- [x] Explicit system migration 없이는 listener/bootstrap/DDL이 0인 채 startup이 fail-closed함
- [x] Raw password는 durable state/artifact/log/error에 없고 encoded password hash는 credential column 밖의 artifact/log/error에 노출되지 않음
- [x] Raw session bearer/CSRF secret·token/process key와 DB URL이 durable payload/artifact/log/error에 노출되지 않음
- [x] Exported bootstrap/site config와 auth/session/systemstate/API/Admin configuration error의 JSON/`%#v` 진단 표면이 raw secret을 직렬화하지 않음
- [x] Same cookie가 clean restart 뒤 Admin/API principal/permission을 복구하고 logout 뒤 다음 restart에서 부활하지 않음
- [x] Duplicate Cookie header에도 제시된 canonical session bearer가 server-side flush되고 copied bearer가 restart 뒤 거부됨
- [x] Touch와 Rotate가 교차해도 replacement의 accessed/idle timestamp가 마지막 confirmed touch보다 뒤로 가지 않음
- [x] 같은 CSRF cookie가 process 경계를 통과한 상태에서 stale token은 Article mutation 0으로 거부되고 safe GET의 fresh token은 성공함
- [x] Article DML과 Admin audit가 같은 transaction에서 commit/rollback하며 unknown outcome을 retry하지 않음
- [x] Bootstrap commit outcome unknown은 schema-missing으로 오분류하거나 자동 retry하지 않고 reconciliation-required marker를 보존함
- [x] SQLite/PostgreSQL에서 distinct-process restart sentinel이 skip 0으로 통과함 — SQLite local PASS, PostgreSQL hosted exact required 16/16·skip 0
- [x] Go actual의 process/secret metric은 같은 PID reopen이나 상수가 아니라 실제 subprocess·scan 관찰값에서 산출됨
- [x] Schema IR/definition/digest/generated ABI와 existing AUT/ADM/API behavior가 local affected gates에서 drift하지 않음
- [x] CURRENT/work/matrix/evidence/ADR/deviation/PR이 같은 frozen bytes와 비목표를 가리킴

## 현재 상태와 다음 정확한 작업

GDJ-0045는 completed이고 active/ready packet은 0/0입니다. Corrected frozen source
`6243682e8ec6c94913dda0162cce101b39af354d`, tree `98076ea6d469a6405851cc51f2b245806f4230da`의 local final은
[EVID-128](../docs/status/TEST_EVIDENCE.md#evid-20260825-128--gdj-0045-first-hosted-lock-failures-and-corrected-local-refreeze)에,
exact submitted head `e673b3a11d4d0d7e2f8a55fdb3c58d24b965ff35`, tree
`917d36f8ef4458740c377904f4f93597c7c906ec`의 hosted result는
[EVID-129](../docs/status/TEST_EVIDENCE.md#evid-20260825-129--gdj-0045-corrected-exact-head-hosted-completion) / CI #146
run `32833586028`에 기록했습니다. 필수 GitHub Actions 27/27 jobs·359/359 steps, PostgreSQL 17.10 exact
16/16·skip 0, 네 relation 좌표 1,034/1,034·skip 0와 네 Python semantic digest가 통과했습니다. ADR-0047은
Accepted, DEV-0008은 Verified이고 Q-020은 one-runtime/sequential-restart 답만 `Partial`입니다. Multi-runtime,
DB-enforced uniqueness/CAS, shared deployment key ring, JWT/OAuth와 production topology는 이 완료에 포함되지 않습니다.
Draft PR #1은 OPEN/DRAFT/unmerged이며 merge/release/deployment는 수행하지 않았습니다. 다음 work packet은 이 terminal
경계를 바꾸지 않는 별도 activation에서 선택합니다.
