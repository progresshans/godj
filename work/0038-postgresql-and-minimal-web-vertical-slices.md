---
id: GDJ-0038
status: active
updated: 2026-08-23
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "681b07132be5772286b0c960756719aed59a2079"
depends_on: ["GDJ-0037"]
contracts: ["DB-PG-001", "DB-PG-002", "DB-PG-003", "DB-PG-004", "DB-PG-005", "DB-PG-006", "DB-PG-007", "DB-PG-008", "DB-PG-009", "DB-PG-010", "WEB-001", "WEB-002", "WEB-003", "WEB-004", "WEB-005", "WEB-006", "WEB-007", "WEB-008", "WEB-009", "WEB-010", "Q-010", "Q-011", "Q-012", "Q-013", "Q-017"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - ".gitignore"
  - "Makefile"
  - "NOTICE.md"
  - "LICENSE.pgx"
  - "go.mod"
  - "go.sum"
  - "apps/**"
  - "settings/**"
  - "web/**"
  - "query/**"
  - "orm/**"
  - "db/**"
  - "migrations/**"
  - "schema/**"
  - "project/**"
  - "internal/compiletest/**"
  - "examples/article/**"
  - "conformance/postgresproduct/**"
  - "conformance/migrationrelationproduct/**"
  - "conformance/internal/protocol/**"
  - "conformance/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/LICENSING.md"
  - "docs/TESTING.md"
  - "docs/adr/0037-postgresql-current-contract-backend.md"
  - "docs/adr/0038-minimal-web-core-request-lifetime-and-representation.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0038-postgresql-and-minimal-web-vertical-slices.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# PostgreSQL and Minimal Web Vertical Slices

## 사용자에게 보이는 결과

이 packet은 두 개의 독립 구현 lane을 병렬로 진행한 뒤 하나의 Article 흐름에서 합칩니다.

```text
ProjectSpec
→ generate
→ migrate PostgreSQL
→ generated CRUD / ForeignKey query
→ transaction and restart
```

```bash
go run ./examples/article/cmd/site serve \
  --listen 127.0.0.1:8000 \
  --database ./article.sqlite3
```

Web lane은 먼저 SQLite에서 완결하고 PostgreSQL backend가 안정된 뒤 같은 application factory에 backend를 주입하는
smoke를 추가합니다. 기존 declaration runner와 `godj.toml` bootstrap은 그대로 유지합니다.

## 목표

- PostgreSQL 17 current profile의 actual query/write/transaction/schema/migration/revision backend를 구현합니다.
- 현재 generated facade와 current-only Schema IR/Definition/State 형식을 그대로 사용합니다.
- SQLite의 SQL, transaction, migration과 relation behavior를 회귀 없이 보존합니다.
- 구현 방식이 노출된 migration capability 이름을 backend-neutral 의미로 바꿉니다.
- immutable settings/app registry, static named router, synchronous middleware, request/response와 graceful server를
  Go-native Web Core로 구현합니다.
- Article generated model을 request-local facade에서 읽어 app-owned DTO와 `html/template`로 응답하는 실제 HTTP
  수직 단면을 만듭니다.
- 공통 경계만 통합 담당이 수정하고 PostgreSQL/Web 구현 경로를 병렬 소유합니다.
- 작은 수정마다 full matrix를 반복하지 않고 affected → checkpoint → final milestone gate로 검증합니다.

## 비목표

- MySQL/MariaDB/Oracle, multi-DB/router, multi-schema tenant와 implicit `search_path`
- PostgreSQL REL-007/008 project-aware PROTECT/SET_NULL delete
- OneToOne/ManyToMany, explicit PK identity-sequence reconciliation, upsert/savepoint/public locking AST
- migration writer/autodetector, 기존 임의 database adoption/repair와 general retry
- global `godj runserver`, descriptor runtime-package split 또는 `godj.toml` 확장
- dynamic URL segment/converter/include, streaming response, automatic request transaction/background task
- DTL, Form/Auth/Admin/API, wrapper reflection/automatic JSON 또는 Q-017 general raw-model UX 해소
- literal power-loss, distributed transaction과 production readiness 주장

## 선행 조건과 baseline

- Baseline `681b07132be5772286b0c960756719aed59a2079`, tree
  `f1a2fcc501c0e3b30d2df5facb76b36e53c2c05f`은 GDJ-0037 completion docs descendant입니다.
- Baseline CI #104/run `32444841140`은 exact 26/26 jobs·326/326 steps, annotations 0,
  clean-worktree 24/24와 no-rewrite 10/10을 통과했습니다.
- GDJ-0037의 project bundle/publication은 completed/hosted-verified이고 Q-010은 `Partial`, Q-017은 P1/open입니다.
- Current product는 12/127=`122 passing + 5 deviation + 0 oracle_locked`, relation 12/12이며 MIG-075..086은 계속
  `oracle_locked`/unregistered입니다.
- PostgreSQL/Web은 이 activation 전까지 `Not started`였습니다.

## 고정 공통 경계

### Insert result

`query.InsertPlan`은 optional returned key를 불변 값으로 소유합니다.

```go
func NewInsertPlan(table string, assignments []Assignment) InsertPlan
func NewInsertPlanReturningKey(table string, assignments []Assignment, key FieldRef) InsertPlan
func (InsertPlan) ReturningKey() (FieldRef, bool)
```

- Generated `Manager.Create`와 `Save` insert는 exact AutoField key를 지정합니다.
- SQLite는 기존 parameterized SQL과 `LastInsertId`를 유지합니다.
- PostgreSQL은 exact returned key로 `RETURNING`을 생성합니다.
- PostgreSQL raw insert에 returned key가 없거나 explicit key assignment가 있으면 I/O 전에 structured unsupported
  error를 반환합니다. Explicit identity sequence reconciliation은 후속입니다.

### Migration capability

`MigrationCapabilities.RemoveForeignKeyByTableRemake`를 `RemoveForeignKey`로 flag-day rename합니다.
alias/shim은 두지 않습니다. SQLite는 table remake를 계속 사용하고 PostgreSQL은 native constraint drop을 사용합니다.
Persisted current format과 generated facade는 바뀌지 않습니다.

### Backend injection

Web application factory는 `db.Queryer + db.Mutator`의 기존 public boundary만 받습니다. PostgreSQL/Web lane은 서로의
내부 패키지를 import하지 않습니다. Existing Article generated 12-file bundle과 manifest는 frozen input입니다.

## PostgreSQL 결정 경계

- Driver는 `github.com/jackc/pgx/v5` v5.10.0의 `database/sql` bridge를 사용합니다.
- 지원 major는 PostgreSQL 17이며 hosted reference profile은 다음 16-field fingerprint로 고정합니다.
  `170010|UTF8|UTF8|c|<null>|C|C|UTC|on|on|read committed|off|off|on|on|origin`.
  Local 17.5 checkpoint는 첫 필드만 `170005`인 exact profile을 사용합니다.
- `postgres.Config{URL, Schema}`가 explicit schema를 요구합니다. 모든 user/control object를 schema-qualified로
  접근하고 connection `search_path`는 `pg_catalog`, timezone은 UTC로 고정합니다.
- PostgreSQL 63-byte identifier와 현재 GoDj identifier 규칙을 I/O 전에 검증합니다.
- AutoField는 `BIGINT GENERATED BY DEFAULT AS IDENTITY`, CharField는 `VARCHAR(n)`, Boolean은 `BOOLEAN`입니다.
- ForeignKey는 explicit deterministic constraint name과 `NO ACTION`을 사용합니다.
- 하나의 revision-fenced transaction이 schema operation, recorder transition과 revision advance를 함께 commit합니다.
- contended/stale/integrity, SQLSTATE와 commit durability를 backend-neutral taxonomy로 변환합니다.
- Literal COMMIT 오류는 이미 durable한 write를 배제하지 않으므로 `CodeCommitOutcomeUnknown`이고 자동 retry하지 않습니다.
- 서버/driver/profile, lock 순서, control bootstrap과 retry ownership은
  [ADR-0037](../docs/adr/0037-postgresql-current-contract-backend.md)이 소유합니다.

## Web 결정 경계

- `apps.Registry`, `settings.Settings`, router와 `web.Application`은 startup 뒤 immutable/concurrent-safe입니다.
- Static route는 uppercase method, clean absolute path 또는 그 canonical path의 single trailing-slash form과
  `<app-label>:<name>`을 사용합니다. 두 path는 서로 다른 exact route입니다.
- Unknown path는 404, known path의 method mismatch는 deterministic 405/`Allow`입니다.
- Middleware는 선언 순서에서 첫 항목이 outermost이고 downstream을 최대 한 번 동기 호출합니다.
- `web.Request`는 handler/middleware call 동안만 유효한 borrowed value이며 HTTP request context가 I/O cancellation의
  유일한 authority입니다.
- Response는 handler 성공 뒤 한 번에 쓰는 bounded immutable buffer입니다. Handler error에서 partial body는 0이고
  sanitized 500을 반환합니다.
- Backend pool은 application-lived일 수 있지만 generated facade/QuerySet은 request마다 `project.Using(backend)`으로
  새로 만듭니다.
- Representation은 `wrapper.Unwrap()` 뒤 app-owned `ArticleView` DTO로 명시 변환합니다. JSON/template에 wrapper를
  직접 노출하거나 serialization 중 DB I/O를 하지 않습니다.
- Declaration runner에는 generated import를 추가하지 않고 별도 `examples/article/cmd/site` binary를 둡니다.
- Request lifetime/DTO/router/server 의미는
  [ADR-0038](../docs/adr/0038-minimal-web-core-request-lifetime-and-representation.md)이 소유합니다.

## 계약 roster

### PostgreSQL

- `DB-PG-001`: exact server/driver/UTC/C locale/schema profile
- `DB-PG-002`: current scalar and one-hop relation query compiler/executor
- `DB-PG-003`: generated AutoField `INSERT ... RETURNING`, Create/Save/update/delete
- `DB-PG-004`: Atomic commit/rollback/cancel and callback-bound Session
- `DB-PG-005`: REL-001..006, REL-009..012 read/write subset, excluding project delete
- `DB-PG-006`: transactional schema editor and catalog/FK verification
- `DB-PG-007`: loaded latest/target/no-op apply/unapply/reapply
- `DB-PG-008`: two-connection/process revision fence single-winner/contended/stale
- `DB-PG-009`: operation/recorder/commit/cancel failure and unknown durability
- `DB-PG-010`: close/reopen and actual server restart resume

### Web

- `WEB-001`: settings/app snapshot, order and immutable lookup
- `WEB-002`: named static route and reverse
- `WEB-003`: unknown route 404
- `WEB-004`: method mismatch 405/Allow
- `WEB-005`: synchronous middleware order and once-only downstream
- `WEB-006`: sanitized handler 500 and partial-body 0
- `WEB-007`: request cancellation propagation
- `WEB-008`: wrapper → DTO → escaped Article HTML
- `WEB-009`: request-local QuerySet cache isolation and concurrent requests
- `WEB-010`: in-flight drain, bounded shutdown and listener/goroutine cleanup

## 병렬 소유권

| Lane | Exclusive implementation paths | Integration-only paths |
|---|---|---|
| Shared portability | `query/**`, `orm/**`, required `migrations/**`, `db/sqlite/**` | public freeze/status |
| PostgreSQL | `db/postgres/**`, later `conformance/postgresproduct/**` | `go.mod`, `go.sum`, CI |
| Web | `apps/**`, `settings/**`, `web/**`, `examples/article/webapp/**`, `examples/article/cmd/site/**`, Article HTTP tests | docs/CI |
| Integration | ADR/work/status, dependencies, Makefile/CI and final cross-smoke | all shared decisions |

같은 공개 API, ADR, CURRENT와 workflow를 둘 이상의 lane이 동시에 수정하지 않습니다.

## 구현 단계

### Phase A — boundary freeze and portability

- [x] GDJ-0038/ADR-0037/ADR-0038 activation boundary 작성
- [x] pgx v5.10.0 dependency pin
- [x] InsertPlan returned key와 SQLite exact regression
- [x] semantic `RemoveForeignKey` capability rename
- [x] external compile tests for frozen shared API

### Phase B — PostgreSQL query/write/Atomic

- [x] strict config/schema/identifier/profile
- [x] scalar and current one-hop relation query compiler
- [x] Query/Insert/Update/Delete and SQLSTATE mapping
- [x] callback-bound Atomic/Session lifecycle
- [x] actual PostgreSQL 17 local integration

### Phase C — Minimal Web Core

- [x] immutable apps/settings
- [x] static named router, middleware, request/response and application
- [x] graceful server lifecycle
- [x] Article DTO/template/runtime binary
- [x] real loopback HTTP + SQLite E2E

### Phase D — PostgreSQL migration/restart

- [x] schema editor/type/FK/catalog mapping
- [x] recorder/revision/control bootstrap
- [x] revision-fenced session/transaction and failure taxonomy
- [x] apply/unapply/reapply and close/reopen
- [x] two-process contention and actual server restart resume

### Phase E — integration and hardening

- [x] Article Web PostgreSQL smoke without changing generated bundle
- [x] required actual-service workflow and exact 12-sentinel/no-skip lock
- [x] affected normal/race/CGO0/vet and generated drift
- [x] final full/386/repository-external source-clean-copy local milestone gate once
- [x] independent P0..P3 frozen-byte audit
- [x] source-frozen CURRENT/MATRIX/TEST_EVIDENCE/work handoff
- [ ] non-force push, hosted PostgreSQL 17.10 result mirror and work completion

## 검증 cadence

매 변경은 gofmt, compile, affected tests와 generated drift만 실행합니다. Phase checkpoint에서 해당 lane의
normal/race/CGO-disabled/vet과 실제 DB/HTTP canary를 실행합니다. Full `make ci`, all-package 386와 hosted matrix는
final frozen milestone에서 한 번 실행합니다. 문서-only mirror는 link/frontmatter/status/diff만 검사합니다.

PostgreSQL final gate는 실제 query/write/transaction/schema/migration/recorder/revision과 close/reopen/server restart를
실행해야 합니다. Service가 뜨기만 한 job이나 skip-only integration은 지원 증거가 아닙니다.

## 현재 체크포인트와 다음 정확한 작업

Phase A/B/C source commit `c0ee1d4...`의 descendant인 exact source commit
`cb90f7a69d70c131ccf8868fb83efcf7bd7c2548`, tree
`2528710760de889c0f05166e9e702f92d4633483`에 Phase D/E local candidate를 동결했습니다. Explicit-schema
DDL/catalog, recorder/revision bootstrap, pinned fenced transaction, apply/unapply/reapply, process contention,
close/reopen와 actual server restart resume가 같은 mandatory migration ABI 위에 구현됐습니다. Article generated
CRUD/HTTP와 generated relation 흐름도 PostgreSQL actual DB에서 실행됩니다.

[EVID-107](../docs/status/TEST_EVIDENCE.md#evid-20260823-107--gdj-0038-postgresql-migration-and-web-integration-source-frozen-local-checkpoint)은
local PostgreSQL 17.5 exact 16-field profile, required actual normal 12/12·skip 0, actual race/CGO-disabled, vet,
generate/protocol, `prepare` history/rows 1 → server stop/start → `resume`/`verify` history/rows 2와 cleanup을 기록합니다.
Exact source commit의 final `make ci`, all-package Linux/386 compile-only와 repository-external 736-file source-clean-copy
gate도 통과했고 independent final source audit는 P0/P1/P2/P3=`0/0/0/0`입니다.

다음 정확한 작업은 이 source-frozen mirror를 documentation commit으로 만들고 Draft PR #1에 non-force push한 뒤,
hosted PostgreSQL 17.10 exact profile과 12 required sentinel을 포함한 exact-head matrix를 확인하는 것입니다. Hosted
success와 terminal mirror 전에는 PostgreSQL/Web support, `Verified`, GDJ-0038 completion 또는 Q-011/Q-017 해결을
주장하지 않습니다.
