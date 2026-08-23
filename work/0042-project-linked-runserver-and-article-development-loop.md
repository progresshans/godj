---
id: GDJ-0042
status: active
updated: 2026-08-24
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "052de65cae20ea0b80dfa337629e6da198abc827"
depends_on: ["GDJ-0037", "GDJ-0038", "GDJ-0041"]
contracts: ["WEB-011", "WEB-012", "WEB-013", "WEB-014", "WEB-015", "WEB-016", "WEB-017", "WEB-018", "WEB-019", "WEB-020", "Q-010", "Q-017"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "cmd/godj/**"
  - "internal/projectcheck/**"
  - "internal/projectgenerate/**"
  - "internal/compiletest/**"
  - "examples/article/godj.toml"
  - "examples/article/cmd/site/**"
  - "examples/article/*runserver*_test.go"
  - "conformance/runserverproduct/**"
  - "conformance/README.md"
  - "docs/adr/0042-project-linked-runserver-and-article-development-loop.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0042-project-linked-runserver-and-article-development-loop.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Project-linked Runserver and Article Development Loop

## 사용자에게 보이는 결과

미리 생성·migration된 프로젝트에서 전역 CLI 하나로 generated-aware Article 개발 서버를 실행합니다. 아래 database
environment 이름은 Phase B에서 exact example-owned 이름을 고정하기 전의 계획 예시입니다.

```text
GODJ_ARTICLE_SQLITE_DATABASE=./article.sqlite3 \
  godj runserver --project ./godj.toml --addr 127.0.0.1:8000

→ current generated bundle을 read-only로 확인
→ 별도 project runtime package를 build
→ GET /articles/?q=go&min_id=1&title_matches_summary=true
→ Ctrl-C에서 request drain, listener/backend close, child reap와 temp cleanup
```

PostgreSQL은 project-owned environment로 URL/schema를 전달하고 같은 flow를 실행합니다. `runserver`는 generated
source를 게시하거나 migration을 실행하지 않습니다.

## 목표

- Existing nearest/explicit `godj.toml` selection 뒤 optional generated-aware runtime package를 선택합니다.
- `godj runserver [--project ...] [--addr ...]`의 작은 closed CLI와 exact loopback address 경계를 만듭니다.
- Declaration runner를 한 번 사용해 current `GeneratedBundle`을 계산하고 committed bundle을 read-only preflight합니다.
- Runtime build 전과 build 뒤 bundle identity를 확인하며 stale/missing/mixed/interrupted publication은 server build/start 전에 닫습니다.
- Shell 없이 isolated external workspace에서 `go build -buildvcs=false -mod=readonly`로 runtime binary를 만듭니다.
- Long-lived child stdout/stderr를 stream하고 process-group SIGINT, bounded grace, conditional SIGKILL, direct reap와 cleanup을 소유합니다.
- 실제 SQLite와 hosted PostgreSQL 17 Article child HTTP flow를 검증합니다.
- Repeated start/stop, port reuse, unexpected child exit와 cleanup failure를 fail-closed하게 검증합니다.

## 비목표

- Auto-generate, auto-migrate, repair, retry, watch/reload 또는 source rebuild
- General project command dispatcher, custom commands, persistent runner/build cache와 installed lifecycle
- Non-loopback binding, TLS, production server tuning, proxy/header trust
- Dynamic routing, request transaction, streaming/websocket/SSE와 background task
- DTL, Form, CSRF/session/Auth/Admin/API
- Database settings의 framework-wide 공개 형식; Article DB environment는 example-owned입니다.
- Windows runtime/path/process semantics, parent crash/SIGKILL scavenging과 independently daemonized descendant 보장
- Query/ORM/Schema IR/codegen ABI/Migration Definition·State·backend entry 변경
- Q-010/Q-017 전체 해결 또는 M5 전체 완료

## 선행 조건과 기준 상태

- Activation baseline: `052de65cae20ea0b80dfa337629e6da198abc827`, tree
  `c365b492bfb008f80a73718f7033a6edb40d4c30`; GDJ-0041 terminal docs-only commit, clean worktree
- Hosted product baseline: `e97a4e319047bc156a78fac94e5c2d021e4dcdfe`, tree
  `bcba40b731a5ed3e6554174e40cad62938e4b710`; EVID-118/run `32647746430` exact 27/27 jobs·341/341 steps
- [ADR-0004](../docs/adr/0004-cli-and-project-binary.md)의 global/project ownership,
  [ADR-0022](../docs/adr/0022-project-runtime-and-global-migration-check.md)의 selector/build isolation,
  [ADR-0036](../docs/adr/0036-project-schema-generated-bundle-and-recoverable-publication.md)의 current bundle과
  [ADR-0038](../docs/adr/0038-minimal-web-core-request-lifetime-and-representation.md)의 separate generated-aware runtime를 보존합니다.
- Existing `examples/article/cmd/site`는 direct `serve --listen --database`와 graceful `web.Server`를 구현했습니다.
- Existing short-lived `executeOwnedProcess`는 buffered output과 fixed cancellation behavior 때문에 runtime server에 재사용하지 않습니다.
- Dirty 사용자 파일은 없고 Draft PR #1은 OPEN/DRAFT/unmerged로 유지합니다.

## Django Reference / Contract

WEB-011..020은 Django 내부 runserver 구현이나 Python process shape의 호환 계약이 아닙니다. Django의 친숙한 command
경험을 Go build/process model에 맞게 재설계한 Go-native product contract입니다. 기존 Article handler의 Django-derived
QRY-034..053 결과와 exactly-two-query instrumentation은 재사용하지만, black-box child HTTP만으로 query count를 새로
증명했다고 주장하지 않습니다.

- `WEB-011`: exact runserver argv, nearest/explicit selection, optional strict `runserver_package`와 loopback address
- `WEB-012`: current bundle preflight; drift/missing/mixed/interrupted 상태에서 runtime build/start와 DB I/O 0
- `WEB-013`: no-shell readonly isolated runtime build, project-tree no-write와 종료 후 private temp residue 0
- `WEB-014`: pre-migrated SQLite actual child의 Article advanced HTTP flow
- `WEB-015`: PostgreSQL 17 actual child의 같은 Article flow와 durable DB state
- `WEB-016`: closed failure/exit taxonomy, secret-free framework diagnostic와 application output ownership
- `WEB-017`: clean SIGINT drain, listener/backend close, direct child reap와 conditional force-kill
- `WEB-018`: unexpected exit/output failure/held pipe에서 bounded group cleanup와 orphan/temp 0의 지원 범위
- `WEB-019`: repeated start/stop, port reuse와 race-safe invocation-local coordinator
- `WEB-020`: implicit generate publication/migration/reload/retry/background task 0

Strong stale 검사는 current ProjectSpec이 필요하므로 declaration runner build/run은 수행됩니다. `WEB-012`의 child 0은
runtime server child를 뜻하며 arbitrary user `init()` side effect 0을 주장하지 않습니다. Private build/cache temp는 실행
중 존재할 수 있고 종료 뒤 GoDj-owned residue 0만 보장합니다.

## 설계와 가설

- Descriptor format 1은 exact `package` 뒤 optional `runserver_package = "./cmd/site"` 한 줄을 허용합니다. 필드가
  없으면 migration/generate는 계속 유효하고 runserver만 `runserver_not_configured`로 닫습니다. 두 package는 달라야 합니다.
- CLI 허용형은 `runserver`, `runserver --addr X`, `runserver --project P`,
  `runserver --project P --addr X`뿐입니다. 기본은 `127.0.0.1:8000`; 첫 slice는 exact IPv4 loopback literal과
  canonical port 0..65535만 허용합니다.
- One retained project selection과 one external workspace에서 declaration runner build/run → bundle generation →
  `CheckRoot` → runtime build → `CheckRoot` → runtime start 순서를 사용합니다.
- Build는 isolated environment를 사용하지만 runtime은 project database/secrets를 위해 snapshotted ambient environment를 받습니다.
  Global CLI는 DB URL/schema를 해석하거나 argv/output에 복제하지 않습니다.
- Runtime argv는 exact `<binary> serve --listen <addr>`입니다. Direct project runtime stdout/stderr는 user stream이며
  framework pre-start/build diagnostic은 category/code만 노출합니다.
- Clean operator SIGINT 뒤 child가 Web drain을 마치고 0으로 나가면 global exit 0입니다. Grace를 넘겨 force-kill하면
  `project_interrupted`/130, context cancellation과 cleanup/runtime failure는 structured nonzero입니다.
- Same-user continuous source mutation, ABA, independent daemonization과 parent crash scavenging은 첫 slice의 지원 claim 밖입니다.

## 구현 단계

### Phase A — contract and boundary

- [x] active work/Proposed ADR-0042, exact WEB-011..020과 allowed paths
- [ ] descriptor/argv/address parser tests와 current-bundle loader helper 경계
- [ ] long-lived runtime process owner feasibility and failure taxonomy

### Phase B — product and SQLite user flow

- [ ] one-selection preflight/runtime build orchestration
- [ ] streaming child, SIGINT/drain/reap/cleanup and repeat tests
- [ ] actual global CLI + pre-migrated SQLite Article child HTTP E2E
- [ ] stale/mixed/interrupted/build/runtime failure no-write/no-start gates

### Phase C — PostgreSQL and hardening

- [ ] Article site project-owned SQLite/PostgreSQL environment selection
- [ ] hosted PostgreSQL 17 required runserver sentinel with skip 0
- [ ] four-coordinate portable lifecycle/inventory lock as needed
- [ ] frozen milestone full/386/repository-external clean-copy and independent audit once
- [ ] exact-head hosted completion, ADR acceptance and terminal status mirror

## 완료 조건

- [ ] 사용자가 global `godj runserver`로 current generated-aware Article server를 실행하고 clean Ctrl-C로 종료합니다.
- [ ] WEB-011..020이 actual product tests에서 모두 passing이며 과장된 child/query/write claim이 없습니다.
- [ ] Stable stale/mixed/interrupted bundle은 runtime build/start·DB I/O 전에 project-tree no-write로 닫힙니다.
- [ ] SQLite와 PostgreSQL 17 actual child가 같은 bounded Article response를 냅니다.
- [ ] SIGINT/unexpected exit/repeat에서 direct child reap, bounded group cleanup와 private temp residue 0을 검증합니다.
- [ ] Affected normal/race/CGO0/vet/generated drift와 final full/386/external clean-copy가 cadence에 맞게 통과합니다.
- [ ] ADR/status/matrix/evidence와 Draft PR이 같은 frozen bytes와 남은 비목표를 가리킵니다.

## 검증 cadence

- 매 변경: gofmt, compile, affected package normal/race/CGO-disabled, relevant generate check
- Phase B checkpoint: coordinator race/count, actual SQLite external child sentinel, no-rewrite와 Linux/386 compile
- Phase C/final frozen source: PostgreSQL required sentinel, full `make ci`, entire 386 and repository-external clean-copy once
- Exact submitted head: hosted matrix once; terminal docs-only append는 consistency gate만 사용

## 진행 기록

- [x] 사용자 흐름과 기존 code/process boundary 조사
- [x] work/ADR/contract activation
- [ ] parser/preflight/process implementation
- [ ] SQLite/PostgreSQL actual child verification
- [ ] hardening, final evidence와 인수인계

## 수정 파일

- Activation: 이 work, ADR-0042, ADR/work indexes, CURRENT/ROADMAP/OPEN_QUESTIONS/IMPLEMENTATION_MATRIX
- Product/verification paths는 frontmatter `allowed_paths` 안에서 checkpoint마다 exact roster를 기록합니다.

## 결정된 사항

- 2026-08-24: declaration runner와 generated-aware runtime package를 계속 분리합니다.
- 2026-08-24: optional runtime package는 public `project.Config`, ProjectSpec, Schema IR 또는 generated ABI에 넣지 않습니다.
- 2026-08-24: short-lived buffered process owner와 별개인 streaming long-lived child owner를 사용합니다.
- 2026-08-24: no auto-generate/migrate/reload, loopback-only와 ambient application runtime env를 첫 경계로 둡니다.

## 미결정/Blocker

- External blocker는 없습니다.
- ADR-0042는 Phase A/B 실제 parser/process/E2E proof 전까지 Proposed입니다.
- Exact application environment names와 PostgreSQL site opening shape는 Phase B/C에서 example-owned 경계로 고정합니다.
