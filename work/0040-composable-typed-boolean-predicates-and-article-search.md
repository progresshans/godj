---
id: GDJ-0040
status: active
updated: 2026-08-23
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "253455d734ec683c469beed44f94f7b8a8c0bec3"
depends_on: ["GDJ-0039"]
contracts: ["QRY-034", "QRY-035", "QRY-036", "QRY-037", "QRY-038", "QRY-039", "QRY-040", "QRY-041", "QRY-042", "QRY-043", "Q-011"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "NOTICE.md"
  - "query/**"
  - "orm/**"
  - "db/sqlite/**"
  - "db/postgres/**"
  - "examples/article/**"
  - "internal/compiletest/**"
  - "conformance/queryexpression/**"
  - "conformance/runners/django/query_expression_scenarios.py"
  - "conformance/runners/django/tests/test_query_expression_scenarios.py"
  - "conformance/runners/django/runner.py"
  - "conformance/runners/django/tests/test_scenarios.py"
  - "conformance/runners/django/tests/test_runner_safety.py"
  - "conformance/runners/godj/runner.go"
  - "conformance/runners/godj/query_expression_scenarios.go"
  - "conformance/runners/godj/query_expression_scenarios_test.go"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/contracts/query-expression-manifest.json"
  - "conformance/fixtures/godj-query-expression-not-implemented.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/query-expression-oracle.json"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS"
  - "conformance/SHA256SUMS"
  - "conformance/README.md"
  - "conformance/internal/protocol/**"
  - "docs/adr/0040-composable-typed-boolean-predicates-and-article-search.md"
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
  - "work/0040-composable-typed-boolean-predicates-and-article-search.md"
  - "work/0039-typed-projection-scalar-aggregate-and-stable-pagination.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Composable Typed Boolean Predicates and Article Search

## 사용자에게 보이는 결과

Article 목록에서 제목/요약 OR 검색과 제외 조건을 stable pagination/report에 함께 적용합니다.

```text
GET /articles/?q=go&published=true&exclude_title=draft&offset=0&limit=20
→ (title IContains q OR summary IContains q)
→ AND published = true
→ AND NOT title IContains exclude_title
→ stable typed projection + Count/Max report
→ SQLite/PostgreSQL에서 같은 response
```

## 목표

- Flat `[]Condition`을 authoritative immutable Boolean predicate tree 하나로 교체합니다.
- `orm.And`, `orm.Or`, `orm.Not` typed composition과 기존 `Filter` implicit-AND 의미를 구현합니다.
- Typed/dynamic scalar leaf가 같은 tree로 수렴하고 cross-model/malformed input을 pre-I/O 거부합니다.
- SQLite/PostgreSQL compiler가 parenthesized DFS와 deterministic argument/placeholder order를 공유합니다.
- Nullable NOT의 Django 6.1 observable truth table을 oracle로 고정합니다.
- 기존 conjunctive relation predicate는 보존하고 relation leaf under OR/NOT은 structured unsupported로 닫습니다.
- Projection, Count/Max, distinct/order/offset/limit와 QuerySet cache/rows/context lifetime을 그대로 재사용합니다.
- Article GET search를 정확히 두 DB query의 request-local DTO/aggregate 흐름으로 구현합니다.
- QRY-034..043을 pinned Django reference와 실제 GoDj ORM/SQLite adapter로 검증하고 PostgreSQL parity는 actual
  integration으로 별도 검증합니다.

## 비목표

- F expression, field-to-field comparison/arithmetic와 새 lookup 종류
- Relation predicate를 포함한 OR/NOT과 related-column projection/aggregate
- Unicode/collation 일반화와 SQL 문자열 동일성
- Annotation/grouping/having, dynamic Q string, subquery/window
- Bulk mutation, locking, transaction-bound QuerySet와 request transaction
- Form/validation, CSRF/session/Auth/Admin/API, runserver와 dynamic routing
- Generated facade ABI 변경, Schema/Migration format 변경과 새 compatibility reader
- M4 전체 완료 또는 Q-011 전체 해결

## 선행 조건과 기준 상태

- Exact baseline: `253455d734ec683c469beed44f94f7b8a8c0bec3`, tree
  `d3f1bba2784edec544495475cfd8d6226209608a`
- Baseline hosted proof: EVID-110 / CI run `32634741186`, exact 27/27 jobs·341/341 steps·failure/skip 0
- [ADR-0040](../docs/adr/0040-composable-typed-boolean-predicates-and-article-search.md) Accepted
- [ADR-0039](../docs/adr/0039-typed-projection-scalar-aggregate-and-stable-pagination.md) projection/aggregate/pagination
  경계와 generated facade ABI v2를 보존합니다.
- Phase A reference aggregate는 exact 15 sets/161 unique contracts+scenarios/210 ordered cross-bindings이며
  `134 passing + 5 deviation + 22 oracle_locked`입니다. Product aggregate는 13 adapters/139 contracts의
  `134 passing + 5 deviation + 0 oracle_locked`로 불변입니다.
- Dirty 사용자 파일은 없고 Draft PR #1은 merge/release하지 않습니다.

## Django Reference / Contract

Exact profile은 Django 6.1, SQLite 3.50.4, pinned Python/UTC/C locale를 유지합니다. Scenario source와 GoDj actual은
expected oracle/static artifact를 읽지 않습니다.

| ID | Observable contract |
|---|---|
| QRY-034 | 두 non-null scalar exact leaf의 OR result/order |
| QRY-035 | escaped ASCII `icontains` OR result와 input immutability |
| QRY-036 | `(A OR B) AND C` precedence와 reusable predicate |
| QRY-037 | non-null scalar `NOT` result |
| QRY-038 | nullable exact/`icontains`/`isnull` negation truth table |
| QRY-039 | variadic·repeated `Filter`의 canonical implicit AND |
| QRY-040 | nested connector order, source chain과 child storage immutability |
| QRY-041 | composite predicate 뒤 distinct/stable order/offset/limit |
| QRY-042 | result에 없는 filtered field를 사용하는 typed projection |
| QRY-043 | composite filtered source의 empty/nonempty Count/Max |

PostgreSQL placeholder/escaping, tree depth/node cap, cross-model compile rejection, relation OR/NOT rejection,
rows/close/context failure는 Go-native gates이며 Django parity claim에 포함하지 않습니다.

## 설계와 고정 경계

- Query Plan에는 where tree 하나만 저장하고 flat condition mirror/fallback을 두지 않습니다.
- Same-kind `AND`/`OR`는 ordered flatten하며 `NOT`은 unary입니다.
- `And`/`Or` public signature는 최소 두 typed predicate를 요구하고 empty/single connector를 만들지 않습니다.
- Maximum depth 64, total node 1,024를 넘으면 terminal 전에 `invalid_plan`입니다.
- Compiler는 full/projection/direct·derived aggregate에 같은 where traversal을 사용합니다.
- Existing relation leaf는 root conjunction에서만 허용하고 `OR`/`NOT` descendant면 I/O 전 unsupported입니다.
- Article `q`/`exclude_title`은 각각 256 bytes 이하입니다. Duplicate/malformed/cap 초과 query parameter는
  DB I/O 0의 400입니다.
- Search page projection과 report aggregate는 각각 한 query, 합계 정확히 두 query이며 두 query 사이 snapshot은
  보장하지 않습니다.
- `schema/**`, `migrations/**`, `codegen/**`, `web/**`는 수정하지 않습니다.

## 구현 단계

### Phase A — contract/decision

- [x] QRY-034..043 independent Django scenarios/tests
- [x] manifest/oracle/static fixture/checksum과 provenance
- [x] Phase A status는 locked/reference-only로 유지하고 bytes를 실측

### Phase B — one core tree

- [x] immutable query expression/tree와 validation/resource bounds
- [x] typed ORM And/Or/Not 및 Filter canonicalization
- [x] typed/dynamic convergence, cache/cancellation/rows regression

### Phase C — parallel backend and user flow

- [x] SQLite recursive compiler와 actual product adapter
- [x] PostgreSQL recursive placeholder/compiler와 actual integration
- [x] Article bounded search/400/query-count/rendered parity

### Phase D — hardening

- [x] affected normal/race/CGO0/vet/generated drift
- [x] final full/386/repository-external source-clean-copy once
- [x] independent frozen-byte audit
- [ ] non-force push, exact-head hosted result와 terminal mirror

## 검증 cadence

- Phase A: Python/reference/artifact tests와 protocol registration만 실행합니다.
- Phase B: `query`, `orm`, compiletest affected normal/race/CGO0/vet을 실행합니다.
- Phase C: backend/Article/conformance affected gates와 actual PostgreSQL을 병렬 실행합니다.
- Phase D frozen source에서 full/386/source-clean-copy/independent audit을 한 번 실행하고 exact-head hosted를 닫습니다.
- Docs-only activation/terminal append는 link/frontmatter/status/`git diff --check`만 사용하며 product matrix를
  재귀적으로 반복하지 않습니다.

## 완료 조건

- [x] Article composite search가 SQLite/PostgreSQL에서 같은 bounded response를 냅니다.
- [x] QRY-034..043 oracle-blind actual 10/10이 locked Django result와 일치합니다.
- [x] Predicate tree는 immutable/capped이고 typed/dynamic/Filter chain이 한 AST로 수렴합니다.
- [x] Projection/Count/Max와 full model query가 같은 where 의미를 사용합니다.
- [x] Relation OR/NOT, invalid/cross-model/over-limit input이 pre-I/O fail-closed합니다.
- [x] Rows/error/context/cache lifecycle과 exactly-two-query HTTP contract가 통과합니다.
- [ ] Final local/hosted evidence와 status/matrix/handoff가 같은 frozen bytes를 가리킵니다.

## 현재 체크포인트와 다음 정확한 작업

Phase A reference commit `fe4996f...`는 expected artifact를 읽지 않는 QRY-034..043 Django scenario 10개와
oracle/static fixture를 고정했습니다. Product source commit
`86d6b1696466e9f36d95f971f9adf0541de5b5f9` (tree `88a7496c6b38f7a5d24ad9606709c0418aae9f75`)은
authoritative immutable expression tree, typed `orm.And`/`orm.Or`/`orm.Not`, SQLite/PostgreSQL recursive
compiler와 bounded Article search를 구현했습니다. Actual conformance commit
`0ec6f38583d10a866298b7248fe0b9682fd5a0cf` (tree `98d6d94390bad6d4166142caea3e59373a34cda0`)은
oracle-blind GoDj/SQLite adapter를 등록하고 QRY-034..043을 10/10 `passing`으로 전환했습니다.

Current manifest/oracle/static fixture는 8,075/41,264/1,715 bytes이고 SHA-256은 각각
`e4160851da2e0820dc4f9f2e8c9e9c2d4d372cde426622b4fea5def51739ea69`,
`8b087a394b52620b84d510d6981e77171179ac3690fda738261bf64bea00583e`,
`0df907357fcab944272eb45158189e68520e3567678c57995e05c5a0feccbffb`입니다. 두 독립 actual은 각각
41,134 bytes/SHA-256 `20b5cf0a332d9d85394a2021fc0b1e8839f9e57994b9c278a7f8bcce8e5f918a`로 byte-identical했고
locked oracle와 protocol difference 0입니다. Reference aggregate는 15/161/210=
`144 passing + 5 deviation + 12 oracle_locked`, product는 14 adapters/149 contracts=
`144 passing + 5 deviation`입니다.

Affected normal/race/CGO-disabled/vet/generated drift, SQLite actual, local PostgreSQL 17.5 normal/race actual,
`make conformance-check`, `make godj-conformance`, format/diff와 두 독립 source/conformance audit가 통과했습니다.
[EVID-112](../docs/status/TEST_EVIDENCE.md#evid-20260823-112--gdj-0040-boolean-predicate-and-article-search-phase-bc-local-checkpoint)는
source-frozen affected checkpoint를 기록합니다. 이어서 source-changing fix 없이 full `make ci`, Linux/386 82-package
compile과 repository-external 775-file archive gate가 모두 통과했고
[EVID-113](../docs/status/TEST_EVIDENCE.md#evid-20260823-113--gdj-0040-frozen-source-final-local-gates)에 고정했습니다.
다음 정확한 작업은 문서 checkpoint를 commit하고 non-force push/Draft PR 갱신 뒤 고유 exact-head hosted result와
terminal mirror를 닫는 것입니다.

## 위험과 rollback

- Nullable NOT을 raw SQL `NOT`으로 단순화하면 NULL row 의미가 달라질 수 있습니다.
- Relation leaf를 OR/NOT에 허용하면 현재 INNER JOIN pre-scan이 root row를 잃을 수 있습니다.
- Recursive node/child storage가 노출되면 plan immutability와 race safety가 깨집니다.
- PostgreSQL placeholder counter와 aggregate derived-table traversal이 갈라지면 result가 backend별로 달라집니다.
- 문제가 생기면 GDJ-0039 hosted baseline의 flat AND behavior로 source commit을 되돌릴 수 있지만, parallel legacy
  path나 runtime flag는 남기지 않습니다.

## 미결정/Blocker

- Blocker 없음.
- Related Boolean composition/projection, F expression, transaction/locking/bulk와 Form/validation은 각각 별도 packet입니다.
