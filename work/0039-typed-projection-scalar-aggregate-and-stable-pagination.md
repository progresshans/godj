---
id: GDJ-0039
status: active
updated: 2026-08-23
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "187638f9b3904162d510138d4b9f89f004168eb6"
depends_on: ["GDJ-0038"]
contracts: ["QRY-022", "QRY-023", "QRY-024", "QRY-025", "QRY-026", "QRY-027", "QRY-028", "QRY-029", "QRY-030", "QRY-031", "QRY-032", "QRY-033", "Q-011"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "NOTICE.md"
  - "query/**"
  - "orm/**"
  - "db/sqlite/**"
  - "db/postgres/**"
  - "codegen/**"
  - "examples/article/**"
  - "conformance/querybreadth/**"
  - "conformance/runners/django/query_breadth_scenarios.py"
  - "conformance/runners/django/tests/test_query_breadth_scenarios.py"
  - "conformance/runners/godj/query_breadth_scenarios.go"
  - "conformance/contracts/query-breadth-manifest.json"
  - "conformance/fixtures/godj-query-breadth-not-implemented.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/query-breadth-oracle.json"
  - "conformance/SHA256SUMS"
  - "conformance/README.md"
  - "conformance/internal/protocol/**"
  - "docs/adr/0039-typed-projection-scalar-aggregate-and-stable-pagination.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0039-typed-projection-scalar-aggregate-and-stable-pagination.md"
  - "work/0038-postgresql-and-minimal-web-vertical-slices.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Typed Projection, Scalar Aggregate, and Stable Pagination

## 사용자에게 보이는 결과

Article 목록을 full-model load 한 가지에서 검색·리포트 흐름으로 넓힙니다.

```text
GET /articles/?published=true&offset=20&limit=20
→ typed filter
→ distinct + stable ID ordering + offset/limit
→ ID/title/published ArticleListRow projection
→ matching count + latest ID aggregate
→ SQLite/PostgreSQL에서 같은 response
```

## 목표

- `query.Plan`의 source field authority와 actual result shape를 분리합니다.
- typed scalar DTO projection, count/max aggregate, distinct와 offset을 immutable AST/ORM에 구현합니다.
- 기존 QuerySet cache ownership과 rows/error/context 수명을 보존합니다.
- SQLite/PostgreSQL compiler가 같은 result semantics와 structured error를 제공합니다.
- generated project facade에서 model-specific generic bridge를 게시합니다.
- Article request-local flow를 projection/aggregate/stable pagination으로 확장합니다.
- QRY-022..033을 pinned Django 결과와 실제 두 backend product adapter로 검증합니다.
- core/backend/generated/final의 큰 checkpoint만 사용해 구현 속도를 높입니다.

## 비목표

- Q AND/OR/NOT, F expression, field arithmetic
- bulk mutation, locking과 transaction-bound QuerySet
- annotation/grouping/having, dynamic values, subquery/window
- related-column projection/aggregate와 select-related/DTO 조합
- Web Core pagination abstraction, MySQL/추가 backend
- M4 전체 완료 또는 Q-011 전체 해결

## 고정 경계

[ADR-0039](../docs/adr/0039-typed-projection-scalar-aggregate-and-stable-pagination.md)을 따릅니다.

- Source fields는 filter/order/full-model validation authority입니다.
- Result shape는 full model, scalar projection, scalar aggregate 중 하나입니다.
- Projection/aggregate는 model result cache와 독립입니다.
- Cold Count만 aggregate로 최적화하고 warm Count cache 재사용은 보존합니다.
- Aggregate는 sliced/distinct logical source를 대상으로 하며 필요하면 derived table로 컴파일합니다.
- PostgreSQL distinct projection/order mismatch는 result를 몰래 넓히지 않고 pre-I/O fail-closed합니다.
- 새 result type은 top-level generic function과 generated bridge가 소유합니다.

## 계약 roster

| ID | 경계 |
|---|---|
| QRY-022 | typed projection field order와 exact row decode |
| QRY-023 | projection에 없는 source field filter/order |
| QRY-024 | projection empty/non-empty와 cache independence |
| QRY-025 | distinct projection duplicate removal |
| QRY-026 | stable ordered offset/limit와 out-of-range empty |
| QRY-027 | negative/overflow offset pre-I/O error |
| QRY-028 | cold aggregate Count와 warm model-cache Count |
| QRY-029 | filter/distinct/offset/limit 뒤 Count |
| QRY-030 | empty Count와 nullable Max |
| QRY-031 | filtered Count/Max aggregate result |
| QRY-032 | cancellation/scan/iteration/close failure ownership |
| QRY-033 | SQLite/PostgreSQL result parity와 cross-model compile rejection |

## 병렬 소유권

| Lane | 독점 경로 | 통합 경계 |
|---|---|---|
| Core/API | `query/**`, `orm/**` | integration owner only |
| SQLite | `db/sqlite/**` | frozen Plan/result API 소비 |
| PostgreSQL | `db/postgres/**` | frozen Plan/result API 소비 |
| Contract/Article | 새 query-breadth conformance와 `examples/article/**` | public/generator API 소비 |
| Generated ABI | `codegen/**`, generated fixtures/manifest | integration owner가 한 번에 재생성 |

## 구현 단계

### Phase A — result-shape와 API freeze

- [ ] QRY-022..033 reference/protocol roster와 provenance
- [ ] source/result separation, distinct/offset immutable Plan
- [ ] typed projection/aggregate compile-positive/negative gate
- [ ] existing full-model/relation AST regression

### Phase B — backend 병렬 구현

- [ ] SQLite projection/distinct/offset/derived aggregate compiler
- [ ] PostgreSQL projection/distinct/offset/derived aggregate compiler
- [ ] rows/cancellation/scan/close failure와 cache independence
- [ ] actual SQLite/PostgreSQL parity

### Phase C — generated Article 수직 단면

- [ ] model-specific facade generic bridges
- [ ] whole-project candidate compile와 one-shot generated publication
- [ ] Article query parsing/page cap/DTO/report response
- [ ] SQLite/PostgreSQL loopback HTTP E2E

### Phase D — final hardening

- [ ] affected normal/race/CGO0/vet와 generated drift
- [ ] final full/386/repository-external source-clean-copy once
- [ ] independent frozen-byte audit
- [ ] non-force push, exact-head hosted result와 terminal mirror

## 검증 cadence

매 변경은 gofmt, compile, affected tests와 generated drift만 실행합니다. Phase A, B/C, final의 세 checkpoint에서
범위를 넓히며 full `make ci`, all-package 386, source-clean-copy와 hosted matrix는 final frozen milestone에서 한 번
실행합니다. 문서-only activation/completion은 link/frontmatter/status/diff gate만 사용합니다.

## 현재 체크포인트와 다음 정확한 작업

GDJ-0038 exact product head `187638f9...`가 CI run `32626539049`의 27/27 jobs·341/341 steps를 통과한 clean
baseline에서 이 packet을 활성화했습니다. 다음은 Phase A에서 source/result authority를 먼저 분리하고 typed
projection/aggregate 및 distinct/offset external compile gate를 통과시키는 것입니다. Relation paths, mutation,
locking 또는 Web Core API를 같은 checkpoint에서 열지 않습니다.
