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
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
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
environment 이름은 Phase B에서 고정해 actual SQLite/PostgreSQL gate가 사용하는 exact example-owned 이름입니다.

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
- `WEB-012`: current bundle preflight; drift/missing/mixed/interrupted 상태에서 runtime build/start와 GoDj-owned Article runtime DB I/O 0
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

## 구현 계약과 경계

- Descriptor format 1은 exact `package` 뒤 optional `runserver_package = "./cmd/site"` 한 줄을 허용합니다. 필드가
  없으면 migration/generate는 계속 유효하고 runserver만 `runserver_not_configured`로 닫습니다. 두 package는 달라야 합니다.
- CLI 허용형은 `runserver`, `runserver --addr X`, `runserver --project P`,
  `runserver --project P --addr X`뿐입니다. 기본은 `127.0.0.1:8000`; 첫 slice는 exact IPv4 loopback literal과
  canonical port 0..65535만 허용합니다.
- One retained project selection과 one external workspace에서 declaration runner build/run → bundle generation →
  `CheckRoot` → runtime build → `CheckRoot` → runtime start 순서를 사용합니다.
- Build/runner는 private cache/temp/home을 사용하고 safe local module proxy가 있으면 `GOPROXY` 앞에 추가하지만, 그 밖의
  non-private ambient environment는 유지합니다. 따라서 application DB 변수도 declaration/runtime build에서 보일 수 있고 이
  경계는 untrusted project/toolchain credential sandbox가 아닙니다. Runtime은
  snapshotted ambient environment 전체를 받으며 global CLI는 DB URL/schema를 해석하거나 argv/output에 복제하지 않습니다.
- Runtime argv는 exact `<binary> serve --listen <addr>`입니다. Direct project runtime stdout/stderr는 user stream이며
  framework pre-start/build diagnostic은 category/code만 노출합니다.
- Clean operator SIGINT 뒤 child가 Web drain을 마치고 0으로 나가면 global exit 0입니다. Grace를 넘겨 force-kill하면
  `project_interrupted`/130, context cancellation과 cleanup/runtime failure는 structured nonzero입니다.
- Same-user continuous source mutation, ABA, independent daemonization과 parent crash scavenging은 첫 slice의 지원 claim 밖입니다.

## 구현 단계

### Phase A — contract and boundary

- [x] active work/Proposed ADR-0042, exact WEB-011..020과 allowed paths
- [x] descriptor/argv/address parser tests와 current-bundle loader helper 경계
- [x] long-lived runtime process owner feasibility and failure taxonomy

### Phase B — product and SQLite user flow

- [x] one-selection preflight/runtime build orchestration
- [x] streaming child, SIGINT/drain/reap/cleanup and repeat tests
- [x] actual global CLI + pre-migrated SQLite Article child HTTP E2E
- [x] stale/mixed/interrupted/build/runtime failure no-write/no-start gates

### Phase C — PostgreSQL and hardening

- [x] Article site project-owned SQLite/PostgreSQL environment selection
- [ ] hosted PostgreSQL 17 required runserver sentinel with skip 0 — digest-pinned local actual과 CI lock 완료, exact-head hosted pending
- [ ] four-coordinate portable lifecycle/inventory lock — exact pass/no-skip wiring과 local protocol 통과, hosted coordinates pending
- [x] frozen milestone full/386/repository-external clean-copy and independent audit once
- [ ] exact-head hosted completion, ADR acceptance and terminal status mirror

## 완료 조건

- [x] 사용자가 global `godj runserver`로 current generated-aware Article server를 실행하고 clean Ctrl-C로 종료합니다.
- [x] WEB-011..020이 bounded unit/process/SQLite/PostgreSQL actual evidence에서 locally passing이며 증거 소유권을 분리합니다.
- [x] Stable stale/mixed/interrupted bundle은 runtime build/start·SQLite Article runtime DB file creation 전에 project-tree no-write로 닫힙니다.
- [x] SQLite와 digest-pinned PostgreSQL 17.10 actual child가 같은 bounded Article response를 냅니다.
- [x] SIGINT/unexpected exit/repeat에서 direct child reap, bounded group cleanup와 private temp residue 0을 검증합니다.
- [x] Affected normal/race/CGO0/vet와 Phase A/B generated drift가 cadence에 맞게 통과합니다.
- [x] Final frozen full/386/external clean-copy가 한 번 통과합니다.
- [ ] ADR/status/matrix/evidence와 Draft PR이 같은 frozen bytes와 남은 비목표를 가리킵니다.

## 검증 cadence

- 매 변경: gofmt, compile, affected package normal/race/CGO-disabled, relevant generate check
- Phase B checkpoint: coordinator race/count, actual SQLite external child sentinel, no-rewrite와 Linux/386 compile
- Phase C/final frozen source: PostgreSQL required sentinel, full `make ci`, entire 386 and repository-external clean-copy once
- Exact submitted head: hosted matrix once; terminal docs-only append는 consistency gate만 사용

## 진행 기록

- [x] 사용자 흐름과 기존 code/process boundary 조사
- [x] work/ADR/contract activation
- [x] parser/preflight/process implementation
- [x] SQLite/PostgreSQL 17.10 local actual child verification
- [ ] hosted hardening, terminal evidence와 인수인계

## 수정 파일

- Activation `937229e5c3f377572c271fdd30662c2a44463ad1`: 이 work, ADR-0042, ADR/work indexes,
  CURRENT/ROADMAP/OPEN_QUESTIONS/IMPLEMENTATION_MATRIX
- Product `23b1936f46c20e46e4aa689dc6387a78a9847877`: `cmd/godj/main_unix.go`,
  `cmd/godj/runserver_unix_test.go`, `conformance/runserverproduct/{doc.go,product_unix_test.go}`,
  `examples/article/{godj.toml,cmd/site/main.go,cmd/site/main_test.go}`,
  `internal/projectcheck/{descriptor.go,descriptor_test.go,runserver_args.go,runserver_args_test.go,runserver_package_unix.go,runserver_process_unix.go,runserver_process_unix_test.go,runserver_run_test.go,runserver_run_unix.go,runserver_types.go}`
- PostgreSQL/CI `60da43b64cbc763f0700841ed821401e9a7253e0`: `.github/workflows/ci.yml`, `Makefile`,
  `conformance/runserverproduct/{postgres_unix_test.go,workflow_wiring_test.go}`
- Offline clean-runner correction `6101140ef58578ad899c6699fa208b90bc527f81`:
  `conformance/runserverproduct/product_unix_test.go`
- Clean-checkout interrupted fixture correction `2a61376cdc15cc7a2481210dbf6d3f105517c7a2`:
  `conformance/runserverproduct/product_unix_test.go`
- Backend/listener close ownership correction `810149fd90ecf0b3a9cb7b4b98344476082ce769`:
  `examples/article/cmd/site/{main.go,main_test.go}`
- First-hosted timeout correction `2b4993854301e623e6d34fcb2a02c3dee76f5f15`:
  `.github/workflows/ci.yml`, `conformance/internal/protocol/migration_project_check_artifacts_test.go`,
  `conformance/runserverproduct/workflow_wiring_test.go` and this work packet's exact allowed path
- Source-checkpoint documentation mirror: `conformance/README.md`,
  `docs/{ARCHITECTURE.md,BACKEND_MATRIX.md,CAPABILITY_CATALOG.md,DEVELOPER_EXPERIENCE.md,OPEN_QUESTIONS.md,ROADMAP.md,TESTING.md}`,
  `docs/adr/0042-project-linked-runserver-and-article-development-loop.md`,
  `docs/status/{CURRENT.md,IMPLEMENTATION_MATRIX.md,TEST_EVIDENCE.md}` and
  `work/{0042-project-linked-runserver-and-article-development-loop.md,README.md}`

## 결정된 사항

- 2026-08-24: declaration runner와 generated-aware runtime package를 계속 분리합니다.
- 2026-08-24: optional runtime package는 public `project.Config`, ProjectSpec, Schema IR 또는 generated ABI에 넣지 않습니다.
- 2026-08-24: short-lived buffered process owner와 별개인 streaming long-lived child owner를 사용합니다.
- 2026-08-24: no auto-generate/migrate/reload, loopback-only와 ambient application runtime env를 첫 경계로 둡니다.
- 2026-08-24: Article database selection은 SQLite 단일 env 또는 PostgreSQL URL/schema exact pair 중 하나만 허용하며
  둘을 함께 주거나 pair가 불완전하면 application 시작 전 secret-free error로 닫습니다.
- 2026-08-24: portable CI는 SQLite/stale/forced-cleanup 세 sentinel만 pass/no-skip으로 잠그고 PostgreSQL의 의도적
  skip은 허용합니다. PostgreSQL 17.10 job만 `GODJ_REQUIRE_POSTGRES=1`과 별도 exact pass/no-skip sentinel을 소유합니다.
- 2026-08-24: copied stale fixture는 `go mod tidy`를 실행하지 않고 current root `go.mod` dependency snapshot과
  `go.sum`을 복사한 뒤 `-mod=readonly` declaration build만 수행합니다. Clean hosted cache에서 test-only transitive
  dependency를 조회하지 않습니다.
- 2026-08-24: interrupted-publication fixture는 Git이 빈 디렉터리를 보존하지 않는 clean checkout에서도
  `.godj/transactions`를 스스로 만들고 sentinel을 기록합니다.
- 2026-08-24: four-coordinate product-project job은 cold macOS Intel에서 20분을 넘으므로 30분 budget을 사용합니다.
  다른 matrix timeout과 각 runserver `go test -timeout=15m`는 유지하고 central/runserver lock으로 exact 값을 고정합니다.

## 미결정/Blocker

- External blocker는 없습니다.
- ADR-0042는 Phase A/B/C local product proof와 final frozen local gate가 통과했지만 exact-head hosted proof 전까지
  Proposed입니다.
- First submitted head `46a57aa...`의 run `32657774073`은 26 jobs success 뒤 macOS Intel product job의 exact 20분
  cap에서 취소됐으므로 hosted success가 아닙니다. Product source는 `810149fd90ecf0b3a9cb7b4b98344476082ce769`,
  tree `682b037e71040e7373d8da303cc618207abd4643`이고 current correction/frozen head는
  `2b4993854301e623e6d34fcb2a02c3dee76f5f15`, tree `fd22754e7bc51057b1e0219c7e92f22f5ec37a7a`입니다.
  EVID-121의 affected/full/386/803-file external archive와 두 final audit가 통과했습니다. 다음 작업은 docs-only
  evidence descendant를 비강제 푸시하고 corrected exact submitted-head hosted matrix를 한 번 완료하는 것입니다.
- P3 비차단 제한은 reserved port release 뒤 외부 선점 가능성과 PostgreSQL `CREATE SCHEMA` 성공 직후 ambiguous
  disconnect에서 disposable schema residue 가능성입니다. 성공 증거를 false-green으로 만들지는 않습니다.
