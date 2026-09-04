---
id: GDJ-0041
status: completed
updated: 2026-08-24
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "136e82572206eef7fd04931ae94dffb5ff0660e2"
depends_on: ["GDJ-0040"]
contracts: ["QRY-044", "QRY-045", "QRY-046", "QRY-047", "QRY-048", "QRY-049", "QRY-050", "QRY-051", "QRY-052", "QRY-053", "Q-011"]
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
  - "docs/adr/0041-typed-scalar-comparisons-and-field-references.md"
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
  - "work/0041-typed-scalar-comparisons-field-references-and-article-filtering.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Typed Scalar Comparisons, Field References, and Article Filtering

## 사용자에게 보이는 결과

Article 목록에서 기존 Boolean 검색에 ID 범위와 같은 row의 field 비교를 함께 적용합니다.

```text
GET /articles/?q=go&min_id=10&max_id=100&title_matches_summary=true
→ existing Boolean search + typed inclusive ID range
→ title = same-row summary field reference
→ stable typed projection + Count/Max report
→ SQLite/PostgreSQL에서 같은 response
→ request당 정확히 두 DB queries
```

## 목표

- Integer/String scalar의 `gt`/`gte`/`lt`/`lte` lookup을 typed literal API와 dynamic literal boundary에 추가합니다.
- `orm.F(typedField)`가 만드는 immutable same-model/same-kind field reference를 exact/range RHS로 운반합니다.
- Query AST는 literal/list/field RHS 중 정확히 하나만 갖고 source binding과 malformed union을 pre-I/O 검증합니다.
- Nullable field-reference 비교와 홀수 `NOT`의 Django 6.1 observable truth table을 고정합니다.
- SQLite/PostgreSQL은 field RHS를 identifier로 compile하고 argument/placeholder 순서를 결정적으로 보존합니다.
- 기존 Boolean tree, projection, Count/Max, pagination과 Article request-local DTO를 그대로 재사용합니다.
- QRY-044..053을 pinned Django reference와 oracle-blind GoDj/SQLite actual로 비교하고 PostgreSQL parity를 별도 actual로 검증합니다.

## 비목표

- Field arithmetic, functions, transforms, annotation/grouping/having
- Relation-path field reference, cross-model reference와 join promotion
- Date/time/decimal/UUID lookup, locale 또는 Unicode collation 일반화
- Dynamic string에서 field reference를 해석하는 parser
- Update/bulk mutation, locking, transaction-bound QuerySet, subquery/window
- Form/validation, CSRF/session/Auth/Admin/API, runserver와 dynamic routing
- Generated facade ABI, Schema/Migration persisted format와 compatibility reader 변경
- M4 전체 완료 또는 Q-011 전체 해결

## 선행 조건과 기준 상태

- Product baseline: `136e82572206eef7fd04931ae94dffb5ff0660e2`, tree
  `84f24feed0c1fde641aa196f6d4f581404820c42`
- Baseline hosted proof: EVID-115 / CI run `32642341459`, exact 27/27 jobs·341/341 steps·failure/cancel/skip 0
- Terminal documentation descendant: `b9144153ae2dbb18ea0206121f6345ade1f6c5dc`, tree
  `e390b025880e1c62c9f0dbea60b683487ebbdca6`
- [ADR-0040](../docs/adr/0040-composable-typed-boolean-predicates-and-article-search.md)의 one-tree/nullable-NOT 경계와
  [ADR-0039](../docs/adr/0039-typed-projection-scalar-aggregate-and-stable-pagination.md)의 source/result 경계를 보존합니다.
- [ADR-0041](../docs/adr/0041-typed-scalar-comparisons-and-field-references.md)은 Phase A compile/reference proof로 Accepted입니다.
- Dirty 사용자 파일은 없고 Draft PR #1은 merge/release하지 않습니다.

## Django Reference / Contract

Exact profile은 Django 6.1, SQLite 3.50.4, pinned Python/UTC/C locale를 유지합니다. QRY-044..053은 range boundary,
same-row field equality/order, nullable negation, Boolean reuse, stable projection과 filtered Count/Max 결과·DB-state·metrics를
관찰합니다. Typed/dynamic literal AST convergence는 Go-native unit gate입니다. Phase A에서 scenario별 exact
payload/provenance를 고정하기 전에는 모두
`oracle_locked`/unregistered입니다.

PostgreSQL placeholder/identifier shape, compile-time cross-model rejection, RHS absent from `Plan.SourceFields`, mismatched-kind/relation RHS,
rows/context/cache lifetime과 Article pre-I/O 400/query count는 Go-native gates이며 Django parity claim에 포함하지 않습니다.

## 설계와 가설

- `query.Condition`은 one-of literal scalar, immutable value list, source-bound `FieldRef` RHS를 명시적으로 표현합니다.
- `gt`/`gte`/`lt`/`lte`는 Integer/String에만 허용하며 Boolean, `isnull`, `icontains`, `in`과 field RHS의 잘못된 조합은 거부합니다.
- 공개 후보는 literal 비교 메서드와 sealed `orm.F` reference입니다. 정확한 method spelling은 Phase A external compile
  usability에서 고정하고 parallel compatibility alias는 만들지 않습니다.
- `orm.F`의 model type parameter가 cross-model reference를 compile time에 닫고, AST/source validation은 low-level
  RHS가 exact `Plan.SourceFields` inventory에 없으면 runtime I/O 전에 닫습니다. `FieldRef` 자체에는 model/table
  provenance가 없으므로 structurally identical low-level reference를 별도 source로 식별한다고 주장하지 않습니다.
- Nullable guard는 LHS뿐 아니라 field RHS의 nullability도 고려해야 합니다. 정확한 guard shape는 pinned Django result와
  두 backend actual로 결정합니다.
- Relation condition은 이번 field RHS를 받을 수 없습니다. Scalar leaf와 relation leaf의 기존 root-conjunction 규칙은 유지합니다.
- Article `min_id`/`max_id`는 non-negative signed 64-bit decimal 하나씩이며 `min_id > max_id`는 DB I/O 0의 400입니다.
  `title_matches_summary`는 duplicate 없이 `true`/`false`만 허용합니다. `true`는 `title = F(summary)`, `false`는
  `NOT(title = F(summary))`이며 pinned Django complement에 따라 NULL summary row도 포함합니다. 미지정은 filter 없음입니다.

## 구현 단계

### Phase A — reference and public boundary

- [x] QRY-044..053 independent Django scenarios/tests와 exact payload/provenance
- [x] manifest/oracle/static fixture/checksum과 no-artifact-read safety
- [x] typed literal/`orm.F` external compile usability와 ADR-0041 acceptance decision

### Phase B — one typed AST/API

- [x] lookup/RHS union, validation, equality/clone/source binding
- [x] typed Integer/String literal comparisons, same-model F references와 dynamic literal lookup
- [x] invalid/cross-model/source-inventory/mismatched-kind/relation pre-I/O tests

### Phase C — parallel backend and user flow

- [x] SQLite identifier RHS compiler, nullable NOT and actual integration
- [x] PostgreSQL identifier RHS compiler, placeholder order and actual integration
- [x] Article bounded range/F filtering, pre-I/O 400, exactly-two-query rendered parity
- [x] oracle-blind GoDj/SQLite QRY-044..053 actual adapter

### Phase D — hardening

- [x] affected normal/race/CGO0/vet/generated drift
- [x] final full/386/repository-external source-clean-copy once
- [x] independent frozen-byte audit
- [x] non-force push, exact-head hosted result와 terminal mirror

## 완료 조건

- [x] Article advanced filter가 SQLite/PostgreSQL에서 같은 bounded response를 정확히 두 query로 냅니다.
- [x] QRY-044..053 oracle-blind actual 10/10이 locked Django observable result와 일치합니다.
- [x] Literal/reference RHS가 같은 immutable Boolean AST와 projection/aggregate source를 공유합니다.
- [x] Nullable RHS NOT, placeholder/argument order와 identifier escaping이 두 backend에서 검증됩니다.
- [x] Invalid HTTP, forged AST, cross-model/source/kind/relation 입력이 terminal I/O 전에 fail-closed합니다.
- [x] Final local/hosted evidence와 status/matrix/handoff가 같은 frozen bytes를 가리킵니다.

## 검증 cadence

- Phase A는 Python/reference/artifact/protocol과 compile spike만 실행합니다.
- Phase B는 `query`, `orm`, compiletest affected normal/race/CGO0/vet을 실행합니다.
- Phase C는 backend/Article/conformance affected gates와 actual PostgreSQL을 병렬 실행합니다.
- Phase D frozen source에서 full/386/source-clean-copy/independent audit을 한 번 실행합니다.
- Docs-only activation/terminal append는 link/frontmatter/status/`git diff --check`만 사용합니다.

## 현재 체크포인트와 다음 정확한 작업

Phase A reference commit `609609711cb542d4532e5962d0d15ed5123ebca6`은 exact Django 239/239,
QRY-034..043 observation-prefix 동일성과 typed `orm.F` external compile/cross-model·kind·Boolean·relation
compile-fail을 고정했습니다. Product source `05042276baf10a758897d88764b2952afdb8919d` 뒤 공개 `orm.F`의
nil/typed-nil panic은 `8d6b3e9d8d4bc7a6614496b6592a0d35172d5712`, unsupported-kind diagnostic 회귀는
`839516910cbea36fd576cb1926f79388e0a9e29d`에서 각각 fail-closed로 수정했습니다.

Actual/source-final commit `7f2bb2232afa7d71bea56d8910a52a045ec11faa`, tree
`221467b95b712dfed199b12f5a14ed17d987a7ac`는 QRY-044..053을 `passing`으로 전환하고 20/20 query-expression
zero-diff를 냅니다. 두 actual은 87,592 bytes/SHA-256
`c8762a8a728440e8b7c42c705aad9635f902100041c0171cdb121880b3813a7c`로 byte-identical했습니다. Affected
normal/race/CGO0/vet/generated drift, full `make ci`, Linux/386 82-package compile, 788-file repository-external
source-clean-copy와 두 독립 감사가 모두 통과했습니다.

Submitted documentation head `e97a4e319047bc156a78fac94e5c2d021e4dcdfe`, tree
`bcba40b731a5ed3e6554174e40cad62938e4b710`의 [EVID-118](../docs/status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion) /
CI #115 run `32647746430`은 exact 27/27 jobs·341/341 steps, 네 플랫폼 968/968/0 inventory, Python 네 좌표와
PostgreSQL 17.10 required 12/12·restart를 모두 성공했습니다. QRY-044..053은 `Verified`이고 이 packet은
completed입니다. 다음 제품 작업은 별도 work/ADR activation에서 새 경계를 고정합니다.

## 위험과 rollback

- RHS nullability를 무시한 raw `NOT (lhs = rhs)`는 NULL row를 잃을 수 있습니다.
- Field RHS를 bind parameter로 내리면 같은-row column comparison 의미가 사라집니다.
- Source membership을 LHS만 검사하면 `Plan.SourceFields`에 없는 forged RHS column을 참조할 수 있습니다.
- General expression interface를 성급히 열면 arithmetic/function/annotation ABI까지 잠길 수 있습니다.
- 문제가 생기면 GDJ-0040 hosted baseline의 literal four-lookup Boolean tree로 source commit을 되돌릴 수 있지만,
  legacy comparison path나 runtime flag는 남기지 않습니다.

## 미결정/Blocker

- Blocker 없음.
- 로컬 `GODJ_TEST_POSTGRES_URL`은 미설정입니다. 이는 진행 blocker가 아니며 PostgreSQL 실제 DB 완료 판정은 기존
  hosted PostgreSQL 17.10 job이 새 exact submitted head에서 통과한 뒤에만 합니다.
