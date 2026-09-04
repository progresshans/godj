---
id: GDJ-0023
status: completed
updated: 2026-08-10
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "1f161f311daa775e6a386ec0df568ff85d681f15"
depends_on: ["GDJ-0022"]
contracts: ["REL-001..REL-012", "Q-013"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "NOTICE.md", "conformance/README.md", "conformance/contracts/relation-manifest.json", "conformance/fixtures/godj-relation-not-implemented.json", "conformance/runners/django/runner.py", "conformance/runners/django/normalizer.py", "conformance/runners/django/relation_scenarios.py", "conformance/runners/django/relation_fixture/**", "conformance/runners/django/tests/test_normalizer.py", "conformance/runners/django/tests/test_relation_scenarios.py", "conformance/runners/django/tests/test_runner_safety.py", "conformance/runners/django/tests/test_scenarios.py", "conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json", "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS", "conformance/internal/protocol/relation_artifacts_test.go", "conformance/internal/protocol/migration_definition_source_artifacts_test.go", "conformance/internal/protocol/migration_project_check_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "conformance/internal/protocol/protocol_test.go", "conformance/cmd/contractcheck/main_test.go", "conformance/cmd/godjcheck/main_test.go", "conformance/relationbinding/**", "docs/ARCHITECTURE.md", "docs/CAPABILITY_CATALOG.md", "docs/COMPATIBILITY.md", "docs/LICENSING.md", "docs/OPEN_QUESTIONS.md", "docs/ROADMAP.md", "docs/SOURCES.md", "docs/TESTING.md", "docs/adr/0023-symbolic-relation-binding-and-shared-relation-ast.md", "docs/adr/README.md", "docs/status/CURRENT.md", "docs/status/IMPLEMENTATION_MATRIX.md", "docs/status/TEST_EVIDENCE.md", "work/0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md", "work/README.md"]
integration_owner: "one primary agent"
---

# ForeignKey Relation Compatibility Contracts and Binding Feasibility

## 사용자에게 보이는 결과

GoDj의 첫 관계 단면을 제품 API보다 먼저 고정합니다. Cross-app `ForeignKey`의 symbolic target,
forward/reverse lookup, nullable relation, `PROTECT`/`SET_NULL`, `select_related`와 reverse
`prefetch_related`의 외부 의미를 REL-001..012 exact Django/SQLite observation으로 잠급니다.

이번 작업은 **contract/reference + test-only feasibility**입니다. `schema`, `schema/ir`, `codegen`,
`orm`, `query`, `db/sqlite` 같은 제품 package와 GoDj relation actual adapter를 수정하지 않습니다.
완료해도 사용자가 GoDj 제품에서 `ForeignKey`, relation lookup 또는 eager loading을 사용할 수 있다고
표현하지 않습니다. Accepted ADR-0023을 입력으로 후속 [GDJ-0024](#다음-정확한-작업)가 첫 exact 제품
subset을 별도 work에서 정하고, 나머지 relation breadth는 별도 bounded product packet이 소유합니다.

## 목표

- REL-001..012 exact Django 6.1/SQLite 관계 fixture, manifest, oracle와 provenance 구축
- Cross-app lazy target metadata, forward cache와 reverse manager 외부 의미 고정
- Forward/reverse relation lookup의 result, join 종류·개수와 construction/evaluation I/O 고정
- Nullable FK의 zero-query null access와 `isnull` 의미 고정
- `PROTECT` 실패의 mutation 0과 `SET_NULL` delete transaction의 mutation 순서·row 수 고정
- `select_related`의 non-null `INNER JOIN`, nullable `LEFT OUTER JOIN`, invalid reverse path 오류 고정
- Reverse `prefetch_related`의 exact two-query batch와 related-cache 소비 의미 고정
- Twelfth ordered set을 추가해 12 set/127 contract와 132 ordered cross-binding을 검증
- 기존 11 product adapter/115 product contract의 `110 passing + 5 deviation`을 byte/semantic 회귀로 보존
- 새 관계 set 12개는 `oracle_locked`, static fixture는 12개 `not_implemented`, 제품 actual은 없음으로 분리
- `conformance/relationbinding/**`에서 symbolic relation identity, project-level binder,
  cross-app import-cycle 회피, typed/dynamic shared relation AST와 Schema IR vNext 후보를 검증
- [Accepted ADR-0023](../docs/adr/0023-symbolic-relation-binding-and-shared-relation-ast.md)의
  architecture decision을 compile/runtime/mutation proof로 확정하고 GDJ-0024에 인계

## 비목표와 금지 경계

- `schema/**`, `codegen/**`, `orm/**`, `query/**`, `db/**`, `migrations/**`, `project/**`,
  `cmd/**`, `internal/**` 제품 source 변경
- `conformance/runners/godj/**` relation actual adapter, expected oracle replay 또는 제품 status `passing`
- Public relation type, method, generated package name, binder API나 error type의 최종 freeze
- Current Schema IR v2를 관계를 포함한 것으로 재해석하거나 format version을 조용히 변경
- Existing migration definition tuple `(definition format 1, loader ABI 1, operation codec 1,
  Schema IR 2)` 또는 codec v1에 relation payload 추가
- Migration autodetector/writer, relation schema editor, FK DDL, data backfill 또는 historical relation state
- `OneToOneField`, many-to-many, generic relation, multi-table inheritance와 parent link
- Non-PK/custom primary-key target과 explicit `to_field`
- `CASCADE`, `RESTRICT`, `DO_NOTHING`, `SET_DEFAULT`, database-level cascade 전체 제품 의미
- Nested/multi-hop eager loading, `Prefetch` object/custom queryset/`to_attr`, filtered relation과 aggregation
- PostgreSQL/MySQL backend, service-only CI 또는 SQLite 결과를 non-SQLite 지원으로 표현
- Relation assignment/save/delete/eager-loading 제품 API 구현, benchmark 또는 performance claim
- Existing 11 manifest/oracle/static/deviation payload의 수정·재생성 또는 checksum 교체
- 로컬에서 CPython 3.12/3.13/3.14 최신 micro 전체를 설치·순회하는 검증

## 선행 조건과 기준 상태

- 활성 baseline은
  `codex/revision-fenced-migration-lifecycle@1f161f311daa775e6a386ec0df568ff85d681f15`
  (`docs: record project process stabilization`)입니다.
- [GDJ-0022](0022-migration-project-check-product-slice.md)는 exact 18 hosted execution을 통과한
  project-linked migration check 제품 단면으로 완료됐습니다.
- Baseline protocol v2는 11 ordered reference/product set, 115 contract, 110 ordered
  cross-binding이고 제품 분류는 `110 passing + 5 deviation`입니다.
- Current product Schema DSL/IR v2는 `auto`, `char`, `boolean` scalar field만 표현합니다.
  Query AST와 SQLite compiler는 single-table scalar plan만 소유하고 codegen은 한 schema/package의
  scalar model만 생성합니다. 관계 의미를 넣을 제품 slot이 아직 없습니다.
- ADR-0001은 Schema IR을 canonical source로, ADR-0003은 typed/dynamic query를 같은 AST로,
  ADR-0006은 declaration/generated target 분리와 last-good codegen 보존을 요구합니다.
- Activation 당시 Q-013은 M3 전에 cross-app relation source/target type, import, reverse path와 loader를
  결정해야 하는 P1 질문이었습니다.
- Activation 당시 기준 commit은 clean이었습니다. Shared worktree에서 범위 밖 파일의 이후 변경은
  다른 agent/사용자 소유이므로 보존합니다.

## Exact Django Reference / Provenance

정본은 로컬 `/Users/hanhyeonjin/Documents/django`의 현재 6.2 alpha HEAD가 아닙니다. 모든 source와
test provenance는 exact Django 6.1 commit
`fe0a859f537d4238cf49fca39073513206f83122`에 고정합니다.

- Exact semantic profile: Django 6.1 exact commit, CPython 3.14.3, SQLite 3.50.4,
  `django.db.backends.sqlite3`, UTC, `en-us`, C locale, darwin/arm64
- 기존 locked profile과 `uv.lock` identity는 이전 11 artifact를 위해 불변으로 보존합니다.
  Routine local validation은 installed CPython 3.14.3과 uv 0.12.3만 사용하며 profile을 재작성하지
  않습니다.
- P1: `docs/ref/models/fields.txt`의 `ForeignKey`, `related_name`, `lazy-relationships`,
  `absolute-relationships`
- P2: `docs/topics/db/examples/many_to_one.txt`;
  `tests/many_to_one/tests.py::ManyToOneTests.test_fk_assignment_and_related_object_cache`
- P3: `docs/topics/db/queries.txt#lookups-that-span-relationships`;
  `ManyToOneTests.test_selects`, `test_joined_sql`, `test_reverse_selects`
- P4: `docs/ref/models/fields.txt#ForeignKey.on_delete`;
  `tests/delete/tests.py::OnDeleteTests.test_protect`, `test_setnull`
- P5: `docs/ref/models/querysets.txt#select-related`;
  `SelectRelatedTests.test_access_fks_without_select_related`,
  `test_access_fks_with_select_related`;
  `SelectRelatedValidationTests.test_reverse_relational_field`
- P6: `docs/ref/models/querysets.txt#prefetch-related`;
  `PrefetchRelatedTests.test_foreignkey_reverse`

Manifest의 각 reference는
`django@fe0a859f537d4238cf49fca39073513206f83122:<path-or-symbol>`,
`license=BSD-3-Clause`로 기록합니다. GoDj가 외부 동작을 독립적으로 재작성한 scenario는
`derived=false`입니다. Upstream assertion/code를 번역한 경우에만 `derived=true`와 수정 내용을
가까이 기록합니다. 기존 `NOTICE.md`와 `LICENSE.django`를 사용하고 라이선스 고지를 축소하지 않습니다.

## 고정 fixture

각 contract는 pinned in-memory SQLite connection 위에 fresh disposable table/row/transaction state와
fresh Django model/query state를 다시 만듭니다. 같은 process의 connection 객체 재사용 자체는
비교 의미가 아니지만 table, row, rollback/autocommit, relation cache는 contract 사이에 공유하지
않습니다. REL-007/008처럼 delete가 있는 contract도 다른 contract의 fixture state를 재사용하지 않습니다.

- `authors.Author(id, name)`
- `blog.Post(id, title, author, reviewer)`
- `author`: `"authors.Author"` lazy absolute target, `null=False`, `PROTECT`, reverse `posts`
- `reviewer`: 같은 target, `null=True`, `SET_NULL`, reverse `reviewed_posts`
- 양쪽 모델 모두 명시적 `ordering=["id"]`
- Authors: `(1,Ada)`, `(2,Bob)`, `(3,Cleo)`
- Posts: `(10,Alpha,author=1,reviewer=2)`, `(11,Beta,author=1,reviewer=NULL)`,
  `(12,Gamma,author=3,reviewer=2)`

## REL-001..012 External Contract

아래 ID, 순서, phase/comparison, exact result와 metric은 이 work의 정본입니다.

| ID | Phase / comparison | 연산과 exact 결과 | 잠글 metrics |
|---|---|---|---|
| REL-001 | `metadata` / result | Cross-app lazy binding. Forward metadata: `author → {column:author_id,target:authors.author,nullable:false,reverse:posts}`, `reviewer → {column:reviewer_id,target:authors.author,nullable:true,reverse:reviewed_posts}`. Reverse metadata 두 개는 `one_to_many=true,target=blog.post`. | 없음 |
| REL-002 | `evaluation` / error, db_state, metrics | `Post(title="Unsaved", author=Author(name="Unsaved"))` 저장 시 `ValueError`. Normalize: `model_state_error/unsaved_related_object`, message 비계약. Baseline DB 그대로. | `query_count=0`, `statement_kinds=[]`, row delta `0/0` |
| REL-003 | `evaluation` / result, db_state, metrics | DB에서 새로 읽은 Post 10의 `author`를 두 번 접근. 두 결과 모두 `{id:1,name:Ada}`. | cold `1 SELECT`, warm `0`; 동일 인스턴스 내 cache |
| REL-004 | `evaluation` / result, db_state, metrics | `author__name=Ada`와 `author__name=Ada AND author__id=1` 모두 Post IDs `[10,11]`. | 각 construction I/O `0`, evaluation `1 SELECT`, `INNER JOIN=1`, `LEFT JOIN=0`; 두 predicate도 join 재사용 |
| REL-005 | `evaluation` / result, db_state, metrics | `ada.posts.all()` → `[10,11]`; `Author.filter(posts__title="Alpha")` → `[1]`. | reverse accessor: `1 SELECT`, join `0`; reverse lookup: `1 SELECT`, `INNER JOIN=1` |
| REL-006 | `evaluation` / result, db_state, metrics | 새로 읽은 Post 11의 `reviewer` → `NULL`; `reviewer__isnull=true` → `[11]`. | null forward access `0` queries; isnull query `1 SELECT`, join `0` |
| REL-007 | `evaluation` / error, db_state, metrics | Author 1 삭제 → `ProtectedError`. Normalize: `integrity_error/protected_foreign_key`, message 비계약. `protected_objects=2`; 모든 Author/Post와 FK 그대로. | 잠글 값은 `protected_source_rows=2`, mutation `UPDATE=0,DELETE=0`; Django collector의 선행 SELECT/query shape는 비교 payload에서 제외 |
| REL-008 | `commit` / result, db_state, metrics | Author 2 삭제 → `{deleted_total:1,target_deleted:1}`. Authors `[1,3]`, Posts `[10,11,12]` 유지, 세 reviewer 모두 NULL. | 한 transaction, `UPDATE statements=1`, affected source rows `2`, `DELETE statements=1`, deleted target rows `1`, mutation order `[UPDATE,DELETE]` |
| REL-009 | `evaluation` / result, db_state, metrics | 일반 Post 조회 후 author 접근과 `select_related("author")` 결과가 모두 `[(10,Ada),(11,Ada),(12,Cleo)]`. | plain `4 SELECT`; eager `1 SELECT`, `INNER JOIN=1`, access-extra `0` |
| REL-010 | `evaluation` / result, db_state, metrics | `select_related("reviewer")` → `[(10,Bob),(11,NULL),(12,Bob)]`. | `1 SELECT`, `LEFT OUTER JOIN=1`, `INNER JOIN=0`, access-extra `0` |
| REL-011 | `evaluation` / error, db_state, metrics | `Author.select_related("posts")` 평가 → `FieldError`. Normalize: `field_error/invalid_related_path`, message 비계약. Reverse FK는 multi-valued라 select_related 불가. | `query_count=0`, mutation `0` |
| REL-012 | `evaluation` / result, db_state, metrics | `Author.prefetch_related("posts")`를 `.posts.all()`로 소비 → `[(1,[10,11]),(2,[]),(3,[12])]`. | 정확히 `2 SELECT` = primary 1 + `author_id IN (1,2,3)` batch 1; join `0`, related-access extra `0`, batch key count `3` |

REL-012는 prefetched related manager에서 다시 `order_by()` 또는 `filter()`하지 않습니다. Django는
그 경우 prefetch cache를 사용하지 않고 parent별 새 query를 실행할 수 있으므로 이 contract의
소비 형태와 다른 의미입니다. REL-007/008의 Django collector SELECT 수와 statement shape는
implementation choreography이므로 comparison payload에 넣지 않고, product equality에는
transaction/mutation, protected/affected row와 DB state만 사용합니다.

## Observation과 비교 경계

- Raw SQL 전체 문자열, alias 이름, quoting, whitespace와 predicate serialization 순서는 비교하지
  않습니다.
- Stable comparison은 result/ordered IDs, structured category/code, before/after DB state,
  statement kind, join 종류·개수, query 수, mutation 순서와 affected row 수입니다.
- Query capture는 QuerySet construction뿐 아니라 forward/reverse attribute access와 prefetched manager
  consumption이 끝날 때까지 활성 상태여야 합니다.
- Ordered result는 model `ordering=["id"]` 또는 explicit total ordering을 live assertion하고,
  implicit database row order를 결과로 사용하지 않습니다.
- 각 top-level observation은 result 또는 error 중 하나만 가집니다. 오류 message와 Python private
  exception object identity는 계약하지 않습니다.
- REL-001은 runtime metadata observation이며 table creation/fixture insert를 metrics에 섞지 않습니다.
- Setup/teardown SQL과 scenario operation SQL은 capture window와 connection marker로 분리합니다.
- Relation query가 실제 SQLite connection에서 실행됐는지, foreign key constraint가 활성인지와 orphan
  insert가 거부되는지를 runner safety gate로 검증합니다. Python list/fixture replay는 observation으로
  허용하지 않습니다.
- REL-008은 one transaction의 abstract mutation order와 final DB state를 비교합니다. Django private
  collector object/traversal과 raw `BEGIN`/`COMMIT` SQL text는 비교하지 않습니다.

## Machine artifact와 분류

새 artifact는 protocol v2를 유지합니다.

```text
conformance/contracts/relation-manifest.json
conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json
conformance/fixtures/godj-relation-not-implemented.json
```

- Manifest는 REL-001..012 exact order, declared phase, provenance, comparison dimension과
  `oracle_locked` status를 가집니다.
- Oracle은 12개 `observed`, static fixture는 같은 순서의 12개 `not_implemented`입니다.
- Static comparison은 exit 1과 ordered mismatch 12를 요구합니다.
- Product `godjcheck`는 relation set에 등록된 actual adapter가 없으므로 exit 2/no actual output으로
  fail-closed해야 합니다. `make godj-conformance`의 제품 대상은 기존 11 set으로 유지합니다.
- Reference aggregate는 12 set/127 contract입니다. 모든 set pair의 양방향 ordered mismatch인
  `12 × 11 = 132` ordered cross-binding을 거부합니다.
- 제품 aggregate는 변경 없이 11 adapter/115 contract,
  `110 passing + 5 deviation`입니다. 완료 표현은
  `110 passing + 5 deviation + 12 oracle_locked`이고 127개 전부를 product passing으로 합치지 않습니다.

## False-green 위험과 필수 gate

- **Registry omission/duplicate**: exact 12 ID/order와 scenario registry의 uniqueness를 manifest,
  runner import와 artifact test에서 각각 검증합니다.
- **Precomputed observation**: fixture name/ID, target, nullability, related name, predicate 또는 delete
  policy mutation이 result/error/DB state/metrics 중 기대한 dimension의 mismatch를 만들어야 합니다.
- **Expected replay**: Django runner는 oracle/static JSON을 읽지 않고 live ORM/SQLite를 실행합니다.
  `conformance/relationbinding`도 oracle/manifest/static fixture를 read/import하지 않습니다.
- **Non-deterministic artifact**: 다른 `PYTHONHASHSEED`의 두 독립 process가 relation oracle을 만들고
  bytes와 SHA-256이 같아야 합니다.
- **Static false product**: ordered 12 `not_implemented` mismatch를 보존하고 unknown/unregistered set은
  exit 2/no actual입니다.
- **Status-only product claim**: relation manifest를 `passing`으로 바꾸려면
  `conformance/runners/godj`의 실제 제품 adapter가 등록되어 product packages를 관찰해야 합니다.
  Expected constant/locked oracle replay로 status를 올릴 수 없습니다. 이번 work에서는 이 adapter
  자체를 금지합니다.
- **Query capture gap**: construction/evaluation/access/prefetch consumption 전체를 capture하며 join
  kind를 normalized parser와 independent sentinel로 검증합니다.
- **Implicit ordering**: 모든 multi-row observation은 total ordering을 assertion하고 hash/map iteration
  order를 canonical output에 노출하지 않습니다.
- **Fake relation DB**: actual SQLite FK constraint와 orphan rejection safety test가 없으면 relation
  oracle을 잠그지 않습니다.
- **Delete partial success**: REL-007은 UPDATE/DELETE 0과 full state 불변, REL-008은 one transaction,
  `[UPDATE,DELETE]`, affected rows와 final state를 함께 요구합니다.
- **Product rollback gap**: test-only Go binding spike는 SET_NULL update 뒤 target delete fault를 주입해
  두 mutation이 모두 rollback되는 안전성을 별도 gate로 검증합니다. 이는 REL-008 Django equality나
  제품 지원이 아닙니다.
- **Cross-app import cycle**: external temporary module에서 two app packages와 project binder를 실제
  `go list`/`go test`하고 app-to-app import edge가 0인지 검증합니다.
- **Partial binding publication**: unresolved symbolic target, duplicate model identity와 reverse-name
  collision에서 binding output과 generated candidate가 publish되지 않고 last-good bytes가 보존되어야
  합니다.
- **Typed/dynamic divergence**: equivalent typed selector와 dynamic `author__name`/reverse path는 같은
  canonical shared AST bytes를 만들고 invalid path는 query/compiler I/O 전에 실패해야 합니다.
- **IR silent reinterpretation**: Schema IR v2 payload에 relation을 넣거나 v2 decoder가 candidate vNext
  relation을 받아들이는 mutation은 실패해야 합니다.
- **Prior artifact drift**: 기존 11 manifest/oracle/static/deviation payload와 기존 SHA256SUMS prefix는
  byte-for-byte 불변이어야 합니다.

## `conformance/relationbinding` Test-only Feasibility

Exact REL artifact와 별개로 `conformance/relationbinding/**`에만 작은 candidate model을 둡니다. 이
directory는 제품 package가 아니며 public import/API, generated source contract 또는 relation actual
adapter가 아닙니다.

### 검증할 symbolic identity와 binding 가설

```text
source schema/app descriptors
→ canonical model key (app label, model name)
→ field-owned symbolic target key + column/nullability/delete/reverse declaration
→ project-wide all-model registry validation
→ atomic forward/reverse relation binding publication
```

- Per-app declaration/IR은 target Go type이나 target generated package를 import하지 않고 stable symbolic
  model key를 보유합니다.
- Project binder는 모든 app model descriptor를 수집한 뒤 resolution을 시작합니다. Target lookup,
  source field uniqueness와 reverse namespace conflict 전체가 성공한 뒤에만 immutable binding set을
  publish합니다.
- Missing target, duplicate model identity, invalid source field와 duplicate reverse name은 structured
  candidate error가 되며 partial forward/reverse binding을 남기지 않습니다.
- Generated app model candidate는 FK scalar identity storage를 소유하고 app-to-app import를 만들지
  않습니다. Target type을 함께 요구하는 typed selector/loader는 project binding/bridge candidate가
  조합합니다.
- Two app이 서로를 참조하는 fixture와 same app self relation을 external module에서 compile해 import
  cycle이 없는지 검증합니다. Exact public generated shape는 이 spike로 확정하지 않습니다.

### Shared relation AST 가설

- Typed relation selector와 dynamic string lookup은 metadata/binder validation 뒤 동일한 immutable
  relation path node로 수렴합니다.
- Node는 source/target descriptor identity, ordered hop, direction, cardinality, nullability와 requested
  lookup을 canonical하게 보존하고 raw SQL, package pointer 또는 runtime map identity를 담지 않습니다.
- Equivalent forward/reverse typed/dynamic input은 deep-equal/canonical-byte-equal AST이고 compiler
  candidate가 보는 join graph도 하나입니다.
- Unknown field/target, reverse multi-valued path의 `select_related`, unsupported lookup은 compiler/DB
  I/O 전에 structured error가 됩니다.
- Join은 nullability 하나가 아니라 lookup/eager operation context와 edge nullability가 함께 결정합니다.
  REL-004 target predicate `INNER`/join reuse, REL-006 `isnull` join 0, REL-009 non-null eager `INNER`,
  REL-010 nullable eager `LEFT OUTER`와 prefetch batch plan을 AST/plan meaning으로 검증하되 현재 product
  `query.Plan`이나 SQLite compiler를 수정하지 않습니다.

### Schema IR vNext 결정 gate

Current IR v2는 scalar field 의미로 고정되어 있으므로 relation을 v2 zero field나 side metadata로
끼워 넣지 않습니다. Spike는 다음 후보를 비교합니다.

1. Field union을 relation arm으로 확장한 explicit Schema IR vNext
2. Scalar FK storage field와 별도 ordered relation declaration을 갖는 explicit Schema IR vNext

두 후보 모두 symbolic target, physical column, nullability, reverse name, delete policy를 lossless하게
정규화/serialize/hash할 수 있어야 합니다. Cross-app normalize order, duplicate/reverse conflict error,
old v2 rejection과 deterministic round-trip을 검증합니다. Spike 완료 때 하나를 선택하고 새 explicit
format version을 ADR-0023에 기록하거나 둘 다 부적합하면 Proposed 상태와 Q-013을 유지합니다.

IR vNext 선택은 migration definition format/operation codec upgrade가 아닙니다. Existing tuple
`(1,1,1,2)`는 그대로 유지하고 relation-bearing migration source/upgrade는 별도 work/ADR 없이는
decode하지 않습니다.

## 단계별 구현

### Phase A — Reference contract lock

1. P1..P6 pinned provenance와 fixture schema를 manifest REL-001..012에 연결합니다.
2. Django runner에 isolated two-app fixture, SQL/query/join/mutation normalizer와 live safety assertion을
   구현합니다.
3. Contract별 result/error/DB state/metrics와 mutation tests를 먼저 실행합니다.
4. Two-process byte-identical oracle, 12-mismatch static fixture와 additive SHA256SUMS entry를 만듭니다.
5. 12 set/127 contract uniqueness와 132 ordered cross-binding을 모두 거부합니다.
6. Product relation adapter 부재, 11 adapter/115 contract와 기존 artifact 불변을 검증합니다.

### Phase B — Independent binding feasibility

1. `conformance/relationbinding/**`에 product import 없이 candidate identity/IR/binder/AST를 작성합니다.
2. Cross-app mutual/self relation external module을 실제 compile하고 import graph를 검사합니다.
3. Atomic binder success, missing target/duplicate/reverse collision과 last-good output 보존을 검증합니다.
4. Typed/dynamic forward/reverse path의 shared AST equality와 pre-I/O error를 검증합니다.
5. Two IR vNext 후보의 deterministic normalize/round-trip/hash와 v2 fail-closed를 비교합니다.
6. SET_NULL update 뒤 delete failure fault로 transaction rollback safety를 test-only로 검증합니다.

### Phase C — Review, CI와 handoff

1. Independent contract/provenance/metric false-green audit와 binding/import/IR design audit를 받습니다.
2. Local은 installed CPython 3.14.3 + uv 0.12.3으로 routine portable Python만 검증하고 다른 Python
   version을 local에 설치하지 않습니다.
3. Existing exact 18 required execution을 보존하고 relation proof matrix 4를 추가해 exact 22 required
   executions을 구성합니다.
4. ADR-0023은 proof가 모든 gate를 만족할 때만 Accepted로 승격하고 Q-013의 해결/남은 항목을 명시합니다.
5. CURRENT/matrix/evidence/roadmap/work index를 같은 checkout으로 갱신합니다. ADR-0023을 Accepted하면
   GDJ-0024 첫 product subset의 exact allowed paths와 red-to-passing 범위를 열고, 기각하면
   alternative-design work를 엽니다.

## CI 계획 — existing 18 + relation proof 4 = exact 22

기존 required job을 줄이거나 합치지 않습니다.

- Existing full/exact 2
- Existing project-check independent proof 4
- Existing actual SQLite 4
- Existing product 4
- Existing Python compatibility 4
- New relation binding/external-compile proof 4

New proof 4는 official hosted 좌표 `ubuntu-22.04` amd64, `ubuntu-24.04-arm` arm64,
`macos-15-intel` amd64, `macos-26` arm64를 사용합니다. 각 leg는 exact GOOS/GOARCH assertion,
`conformance/relationbinding` normal/race/CGO-disabled/vet, external two-app compile/import graph,
artifact no-rewrite와 final clean-worktree를 실행하고 expected execution count와 skipped 0을 잠급니다.
Windows green skip은 만들지 않습니다.

Python compatibility job은 uv 0.12.3과 exact pin
`3.12.13`, `3.13.15`, `3.14.3`, `3.14.7` 네 개를 유지합니다. 각 job은 해당 exact runtime과
Django/asgiref/sqlparse dependency를 검증하고 portable relation test를 포함한 전체 expected count,
intentional exact-profile skip count와 clean tree를 검사합니다. Floating `3.14`나 local multi-version
loop를 사용하지 않습니다. Existing exact profile artifact job은 기존 immutable profile lock을
계속 소유하며 routine uv 0.12.3 job과 섞지 않습니다.

PostgreSQL/MySQL service는 실제 backend/compiler/schema/write/transaction/relation adapter와 그
contract가 없으므로 이번 exact 22에 넣지 않습니다. Service 기동만 성공하는 job을 DB 지원으로
세지 않습니다.

## 완료 조건

- [x] REL-001..012 exact ID/order/phase/comparison과 P1..P6 provenance가 manifest에 잠김
- [x] Cross-app fixture, actual SQLite FK/orphan safety와 total ordering이 live assertion으로 검증됨
- [x] Query access까지 포함한 query count/join kind와 delete mutation metrics가 exact observation에 잠김
- [x] Two-process oracle가 byte-identical이고 additive SHA256SUMS가 재현됨
- [x] Static fixture가 ordered 12 `not_implemented`, comparison exit 1/mismatch 12를 냄
- [x] Product relation actual adapter가 없고 unknown relation set은 exit 2/no actual로 fail-closed함
- [x] 12 set/127 contract global uniqueness와 132 ordered cross-binding이 전부 거부됨
- [x] Existing 11 adapter/115 product contract와 `110 passing + 5 deviation`이 변하지 않음
- [x] 완료 aggregate가 `110 passing + 5 deviation + 12 oracle_locked`로 정확히 기록됨
- [x] Prior 11 artifact/deviation/SHA256SUMS prefix가 byte-for-byte 보존됨
- [x] Symbolic target/project binder 후보가 success+atomic failure를 검증하거나 재현 가능한 실패 증거로 기각됨
- [x] Cross-app mutual/self external compile/import graph 후보가 성공하거나 재현 가능한 cycle/shape 실패로 기각됨
- [x] Typed/dynamic shared-AST 후보가 convergence/pre-I/O failure를 검증하거나 divergence 증거로 기각됨
- [x] Schema IR v2는 불변이며 vNext 한 후보가 deterministic round-trip/hash/old-version rejection으로
  선택되거나 두 후보 모두 부적합이라는 결정적 evidence가 기록됨
- [x] SET_NULL update 뒤 delete failure가 test-only transaction 전체 rollback을 검증함
- [x] Product package와 `conformance/runners/godj/**` 변경이 0임
- [x] Local CPython 3.14.3/uv 0.12.3 routine test와 full Go/race/CGO=0/vet가 통과함
- [x] GitHub exact 22/22, Python 3.12.13/3.13.15/3.14.3/3.14.7와 four-OS/arch proof가 통과함
- [x] 두 independent local final audit가 P0/P1/P2/P3 finding 0임
- [x] Implementation commit 뒤 ADR/work/CURRENT/matrix/evidence가 같은 exact head를 가리킴
- [x] ADR-0023의 Accepted/Proposed 결과와 Q-013 남은 범위가 증거에 맞게 갱신됨

Feasibility 후보가 기각된 경우 위 후보 체크는 failure를 숨겨 통과로 바꾸는 뜻이 아닙니다. Exact 재현
증거, 제품 publication 0, ADR-0023 Proposed 유지와 alternative-design handoff를 기록하면 evaluation
work를 닫을 수 있습니다. Reference/artifact/CI/audit gate는 후보 기각 여부와 무관하게 생략하지 않습니다.

## 진행 기록

- [x] GDJ-0022 completion baseline과 existing exact 18 확인
- [x] M3 첫 bounded slice를 ForeignKey relation contract/binding feasibility로 선택
- [x] REL-001..012 fixture, phase/comparison/result/metrics와 pinned provenance 조사
- [x] Product-free symbolic binding/shared AST/IR vNext spike 경계 작성
- [x] Proposed ADR-0023 작성
- [x] Reference manifest/runner/oracle/static artifact 구현
- [x] Global identity/cross-binding/false-green gate 구현
- [x] `conformance/relationbinding` feasibility 구현
- [x] Local routine/exact reference verification와 independent audit
- [x] Implementation commit/push와 exact 22 hosted verification
- [x] Completion status/handoff와 Accepted 결과에 따른 GDJ-0024 다음 bounded packet 지정

## 수정 파일

- Activation에서 이 work와 Proposed ADR-0023을 추가합니다.
- 이후 변경은 frontmatter `allowed_paths`의 exact 목록과 glob 아래에서만 허용합니다.
- 특히 product packages와 `conformance/runners/godj/**`는 허용 목록에 없으며 변경하면 이 work의
  범위 위반입니다.

## 결정된 사항

- 2026-08-10: M3 전체 관계/PostgreSQL을 한 번에 열지 않고 ForeignKey external contract와
  product-free binding feasibility를 먼저 잠급니다.
- 2026-08-10: Relation reference는 local Django 6.2 alpha HEAD가 아니라 Django 6.1 exact commit
  `fe0a859f537d4238cf49fca39073513206f83122`를 사용합니다.
- 2026-08-10: 새 relation set은 `oracle_locked`이고 제품 aggregate/adapter는 변하지 않습니다.
- 2026-08-10: Current Schema IR v2와 migration source tuple을 재해석하지 않고 explicit vNext 후보를
  spike에서 검증합니다.
- 2026-08-10: Local Python은 3.14.3/uv 0.12.3 한 환경, multi-Python과 OS/architecture 차이는 CI가
  소유합니다.
- 2026-08-10: Phase A는 REL-001..012를 12 `oracle_locked`로 고정했고 reference aggregate를
  12 set/127 contract/132 ordered cross-binding으로 확장했습니다. Product adapter는 0개 추가되어
  제품 aggregate는 11 adapter/115 contract=`110 passing + 5 deviation`로 그대로입니다.
- 2026-08-10: Phase B test-only proof는 symbolic identity, atomic binder, mutual/self cross-app
  external compile, shared immutable relation AST, SET_NULL fault rollback과 Schema IR v2 fail-closed를
  통과했습니다. 두 vNext 후보 중 field union relation arm이 test-only 후보로 선택됐지만 ADR-0023은
  exact 22 hosted acceptance 전까지 Proposed로 유지합니다.
- 2026-08-10: Implementation commit `b56ccf52d71a09e2f4db42ce30fb5eaf58ffba99`의 exact
  [run 31338151743](https://github.com/progresshans/godj/actions/runs/31338151743)이 existing 18 +
  relation-binding 4의 22/22를 통과했습니다. Field-union relation arm과 no-app-import project bridge
  ownership을 architecture로 채택해 ADR-0023을 Accepted하고 Q-013을 `Partial`로 전환합니다. Exact
  public/wire/ABI와 제품 relation 동작은 GDJ-0024 이후 범위입니다.

## 미결정/알려진 제한

- Generated project bridge의 exact package/API와 typed relation selector public shape
- Reverse manager/cache ownership과 result collection의 concrete product type
- Product error category/type와 nullable FK scalar representation
- Relation migration writer/loader/codec version, existing database upgrade와 schema editor
- PostgreSQL relation compiler/transaction behavior와 backend conformance

외부 blocker는 없습니다. Test-only Phase B와 exact 22 hosted evidence는 field union relation arm과
project bridge ownership을 Accepted architecture로 좁혔지만 public/wire freeze나 product support는
아닙니다. 나머지 항목은 후속 GDJ-0024/별도 migration/backend work 없이 추측으로 해결하지 않습니다.

## 테스트 증거

- Evidence ID:
  [EVID-20260810-031](../docs/status/TEST_EVIDENCE.md#evid-20260810-031--gdj-0023-foreignkey-reference-and-binding-pre-hosted-local-validation)
- Hosted evidence ID:
  [EVID-20260810-032](../docs/status/TEST_EVIDENCE.md#evid-20260810-032--gdj-0023-github-hosted-exact-22-job-implementation-head-ci)
- Completion-documentation hosted evidence ID:
  [EVID-20260810-033](../docs/status/TEST_EVIDENCE.md#evid-20260810-033--gdj-0023-github-hosted-completion-documentation-head-exact-22-job-ci)
- Activation commit: `d5d00d9e803c637a78961ed6f7dac0b415ce7901`; provided verified
  [run 31335315454](https://github.com/progresshans/godj/actions/runs/31335315454) exact 18/18 success
- Exact tested implementation checkout:
  `codex/revision-fenced-migration-lifecycle@b56ccf52d71a09e2f4db42ce30fb5eaf58ffba99`
  (`test: lock foreign key relation contracts`)
- Exact tested completion-documentation checkout:
  `codex/revision-fenced-migration-lifecycle@31784ae1e8261ad0698921b93803aa35e9b63f93`
  (`docs: accept relation binding architecture`)
- Local PASS: CPython 3.14.3 + uv 0.12.3 `make ci`와 portable Python 193/17 intentional skips;
  profile-owned uv 0.10.12 exact Python 193/0와 12 oracle checks; relationbinding
  normal/race/CGO-disabled/vet/race count-20; two independent final audits P0/P1/P2/P3 0
- Final pins: manifest 10,842 bytes/
  `08124b420e6313e4c2c1a5be32a3bdd29d831f02f1479bc3591af6f8f7da1522`; oracle 33,792 bytes/
  `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`; static 1,859 bytes/
  `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`; 12-line `SHA256SUMS`
  1,148 bytes/`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`;
  127-scenario payload 498,051 bytes/
  `2e1c34f3604a324f40cb19bf255086cf71672712409321fc54f6d02216c9a995`
- Hosted PASS: Draft PR #1 run `31338151743`, attempt 1, exact 22/22 and non-success required step 0;
  Python 3.12.13/3.13.15/3.14.3/3.14.7 portable 193/17와 four relation proof coordinates
- Completion-documentation hosted PASS: Draft PR #1 run `31339409336`, attempt 1, exact 22/22와
  273/273 successful steps
- Not run/pending: 현재 EVID-033/status patch 자체의 commit/push와 exact-head hosted CI. Product relation
  adapter, Windows, PostgreSQL/MySQL 지원은 없음

## 위험과 rollback

- Public API freeze: test-only candidate 이름을 product API로 복사하지 않습니다.
- Import cycle: generated app-to-app import가 생기면 candidate를 채택하지 않습니다.
- Generated drift: failure 때 last-good bytes가 달라지면 binder/IR 후보를 reject합니다.
- IR compatibility: v2를 재해석하거나 existing tuple/artifact를 변경하면 patch를 되돌립니다.
- False product support: adapter/status를 추가하지 않고 static `not_implemented`를 유지합니다.
- Delete data loss: REL-007/008 DB state와 fault rollback proof 없이는 다음 제품 work를 열지 않습니다.
- CI cost: relation proof 4만 additive하고 expected exact 22를 job inventory gate로 고정합니다.
- Backend overclaim: PostgreSQL/MySQL service-only green job을 추가하지 않습니다.

## 다음 정확한 작업

1. 이 EVID-033/status patch를 exact allowed scope로 commit/push하고 그 exact head의 22-job hosted CI를
   별도로 수집합니다. Run `31339409336`을 뒤의 evidence/status head 증거로 재사용하지 않습니다.
2. 그 exact-head 결과를 확인한 뒤 Accepted ADR-0023을 입력으로 별도 GDJ-0024 work/allowed paths를
   작성해 AutoField-target required/nullable ForeignKey의 첫 SQLite product slice만 활성화합니다.

## 결과와 인수인계

Phase A reference와 Phase B test-only binding proof는 local에서 완료됐고 두 independent final audit도
P0/P1/P2/P3 finding 0입니다. Exact implementation commit `b56ccf5`의 hosted 22/22까지 통과해 work는
completed이고 ADR-0023은 Accepted입니다. Completion-documentation commit `31784ae1`도 별도 run
`31339409336`의 exact 22/22를 통과했습니다. Reference는 12/127/132, 제품은 11 adapters/115 contracts=
`110 passing + 5 deviation`, relation actual adapter/지원은 0입니다. 현재 EVID-033/status patch 자체의
commit/push와 exact-head CI는 `not run/pending`이며, 그 결과를 확인한 뒤에만 GDJ-0024를 활성화합니다.
Windows와 PostgreSQL/MySQL 지원은 시작하지 않았습니다.
