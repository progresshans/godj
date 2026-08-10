---
id: GDJ-0025
status: completed
updated: 2026-08-10
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "5bf143575e9b703117a328c1fc5b7eb5823fbfd6"
depends_on: ["GDJ-0024"]
contracts: ["REL-004", "Q-013"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "query/error.go"
  - "query/plan.go"
  - "query/plan_test.go"
  - "query/relation.go"
  - "query/relation_test.go"
  - "orm/manager.go"
  - "orm/relation.go"
  - "orm/relation_test.go"
  - "orm/relation_external_test.go"
  - "orm/relation_query.go"
  - "orm/relation_query_test.go"
  - "orm/relation_query_external_test.go"
  - "orm/dynamic.go"
  - "orm/orm_test.go"
  - "orm/dynamic_relation.go"
  - "orm/dynamic_relation_test.go"
  - "codegen/relation_query.go"
  - "codegen/relation_query_test.go"
  - "codegen/project_relation_query.go"
  - "codegen/project_relation_query_test.go"
  - "codegen/testdata/relation_query/**"
  - "db/sqlite/compiler.go"
  - "db/sqlite/compiler_test.go"
  - "db/sqlite/integration_test.go"
  - "conformance/README.md"
  - "conformance/contracts/relation-manifest.json"
  - "conformance/relationqueryproduct/**"
  - "conformance/runners/godj/relation_scenarios.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/runners/django/tests/test_relation_scenarios.py"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/internal/protocol/product_compare_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_query/**"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0024-autofield-foreign-key-schema-ir-vnext-and-rel001-product-metadata.md"
  - "work/0025-forward-foreign-key-predicate-product-slice.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Forward ForeignKey Predicate and SQLite INNER JOIN Product Slice

## 결과 목표

GDJ-0025는 locked Django 6.1 `REL-004`를 제품의 두 번째 relation contract로 구현합니다. 이 packet은
required AutoField-target ForeignKey의 one-hop target predicate를 Schema IR v3에서 generated query
companion, immutable project-bound Query AST, SQLite compiler와 실제 database result까지 연결합니다.

완료 시 다음 두 입력은 동일한 canonical relation edge를 사용해야 합니다.

```go
relations, err := project.BindRelations()
if err != nil {
	return err
}

typed := blog.PostObjects.Using(backend).
	Filter(relations.BlogPost.Author.Name.Exact("Ada")).
	OrderBy(blog.PostFields.ID.Asc())

dynamicPredicates, err := relations.BlogPost.ParseDynamic(nil, []orm.LookupInput{
	{Key: "author__name", Value: "Ada"},
})
```

제품 결과는 `REL-001`과 `REL-004`만 `observed`이고, REL-002/003/005..012는 manifest 순서의
payload-free `not_implemented`를 유지합니다. 완료 aggregate는 exact 12 adapter sets/127 contracts =
`112 passing + 5 deviation + 10 oracle_locked`, relation actual 2/12입니다.

## 기준 상태와 선행 조건

- Baseline은 `codex/revision-fenced-migration-lifecycle@5bf143575e9b703117a328c1fc5b7eb5823fbfd6`입니다.
- Baseline은 GDJ-0024 final evidence/status commit이며 Draft PR #1 run
  [31351169780](https://github.com/progresshans/godj/actions/runs/31351169780)의 exact 26/26 jobs와
  326/326 recorded steps를 통과했습니다. [EVID-038](../docs/status/TEST_EVIDENCE.md#evid-20260810-038--gdj-0024-final-exact-head-ci-and-gdj-0025-activation-baseline)에 기록합니다.
- [ADR-0023](../docs/adr/0023-symbolic-relation-binding-and-shared-relation-ast.md)은 symbolic relation,
  project-owned cross-app bridge와 typed/dynamic shared AST 방향을 Accepted했습니다.
- [ADR-0024](../docs/adr/0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md)은 exact IR v3,
  plain v3 main, additive metadata companion/bridge와 immutable binder를 Accepted했습니다.
- 현재 product relation은 REL-001 metadata만 actual이고 query/load/cache/write/delete/DDL/migration codec은
  미구현입니다. 이 work가 REL-004 외 계약을 우회 구현하지 않습니다.

## 왜 REL-004만 먼저 구현하는가

REL-004는 single-table 제품을 관계-aware product로 확장하는 가장 작은 정직한 수직 단면입니다. Schema IR,
codegen, runtime binding, typed/dynamic query API, immutable AST, SQLite compiler와 actual result를 모두 통과합니다.
반면 REL-003/006을 함께 구현하려면 plain v3 model instance의 loader/cache 소유권, nil relation access와
join trimming을 동시에 고정해야 합니다. 현재 정보로 model hidden state나 wrapper ABI를 선택하면 ADR-0024의
의도적 open boundary를 성급히 닫습니다.

따라서 이번 work는 instance loader/cache를 만들지 않고 relation predicate selector만 제품화합니다.
REL-003 cache 1→0, REL-006 nil access/isnull, REL-009/010 eager hydration은 별도 bounded work가 소유합니다.

## Locked REL-004 외부 동작

Fixture는 authors와 blog 두 앱을 사용합니다.

- `Author`: `(id, name)`
- `Post`: `(id, title, author_id required, reviewer_id nullable)`
- authors: `(1, Ada)`, `(2, Bob)`, `(3, Cleo)`
- posts: `(10, Alpha, 1, 2)`, `(11, Beta, 1, NULL)`, `(12, Gamma, 3, 2)`

Case A `author__name="Ada"`:

- construction query count 0
- evaluation SELECT count 1
- result Post IDs exact `[10, 11]` in explicit ID order
- `INNER JOIN` count 1, `LEFT OUTER JOIN` count 0

Case B `author__name="Ada"` + `author__id=1`:

- construction query count 0
- evaluation SELECT count 1
- result Post IDs exact `[10, 11]`
- both target predicates reuse the same canonical relation edge, so `INNER JOIN` count remains 1

Both cases leave database rows unchanged and perform no transaction or mutation. Exact Django SQL text and alias spelling
are not compatibility targets; result, query count, join kind/count and edge reuse are.

## Public and generated API target

The exact candidate is frozen in
[ADR-0025](../docs/adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md). Any required change to
these signatures or exported names must update the Accepted ADR through a follow-up decision before product code diverges.

### Additive code generators

```go
const RelationQueryGeneratorVersion = "godj-codegen-rel-query-v1"
const ProjectRelationQueryGeneratorVersion = "godj-codegen-rel-query-project-v1"

func GenerateRelationQuery(packageName string, input ir.Schema) ([]byte, error)

type RelationQueryPackage struct {
	Alias      string
	ImportPath string
	Schema     ir.Schema
}

func GenerateProjectRelationQuery(packageName string, packages []RelationQueryPackage) ([]byte, error)
```

`GenerateRelationQuery` accepts exact relation IR v3 only and returns a pure additive query companion. It does not
rewrite the existing v3 main or relation metadata companion. Generated exports are exact
`GoDjRelationQueryGeneratorVersion`, `GoDjRelationQuerySchemaSHA256`, model descriptor/field set/manager symbols.
For the fixture source app they are `PostDescriptor`, `PostFieldSet`, `PostFields`, `PostObjects`.

Before rendering, it rejects any exact identifier collision across existing main model/provenance symbols, relation
metadata symbols, the two query provenance symbols and every derived `<Model>Descriptor`, `<Model>FieldSet`,
`<Model>Fields`, `<Model>Objects` symbol.

The generated field set exposes only non-relation scalar fields (`ID`, `Title`). The descriptor still scans the complete
row including required and nullable FK scalar storage. Nullable `*int64` is scanned with `sql.NullInt64`, and
`CloneModel` deep-copies it. No WriteDescriptor, create/update/save/delete or model cache symbol is emitted.

`GenerateProjectRelationQuery` snapshots and normalizes every `RelationQueryPackage.Schema`, canonicalizes by app label,
alias and import path, and rejects duplicate/colliding inputs. Alias is an ASCII lower-camel Go identifier matching
`[a-z][A-Za-z0-9]*`, excluding keywords, `init`, generated-file predeclared identifiers `error`/`nil`, and reserved
imports `orm`/`ir`; its exported prefix uppercases exactly the first ASCII byte and preserves the remainder (`blog` →
`Blog`). It rejects duplicate prefixes and any collision among import aliases,
existing `Bind`/project-binding provenance, project-query provenance, `Relations`, `BindRelations`, derived
`<Prefix><Model>Relations`/`<Prefix><Model><Relation>Relation` types, unique `<Prefix><Model>` aggregate fields and each
model's unique relation fields. A generated model relation named `ParseDynamic` is rejected because it would collide
with the exact method. Generated code imports reserved `orm` and `ir`; it emits only normalized symbolic
`ir.ModelIdentity` literals required by `BindModel`, not field/table/schema replay. It calls the existing same-package
`Bind()`, then the runtime binder, and imports concrete app types only in the project bridge.

The project input intentionally mixes v2 and v3 schemas. Scalar v2 targets use their unchanged main descriptor; v3
models use the additive query companion descriptor. Only source models with at least one supported required forward
relation appear in `Relations`; target-only models are bound for validation but do not receive empty aggregate entries.

The project generator derives relation selector spelling from normalized FK storage `Field.GoName` by removing one exact
terminal `ID`: `AuthorID` becomes `Author`. It rejects a missing suffix, an empty/non-exported result, reserved
`ParseDynamic`, or duplicate selector spelling inside one source model. The symbolic binder key remains `Field.Name`.
The app query companion does not create relation selector names and does not apply this project-surface rule.

For aliases `authors` and `blog`, generated exports include exact `GoDjProjectRelationQueryGeneratorVersion`,
`Relations`, `BlogPostRelations`, `BlogPostAuthorRelation` and `BindRelations() (Relations, error)`. Prefixing model surfaces with the package alias avoids
cross-app model-name collisions. `BlogPostRelations.ParseDynamic` delegates to the same bound runtime path used by typed
fields. Generation and binding failures never call `panic` or publish a partial result.

### Immutable bound runtime

```go
func (b ProjectBinding) Model(identity ir.ModelIdentity) (ir.Model, bool)

type BoundModel[M any] struct { /* private immutable snapshot */ }

func BindModel[M any](
	binding ProjectBinding,
	identity ir.ModelIdentity,
	descriptor ModelDescriptor[M],
) (BoundModel[M], error)

type ForwardRelation[S, T any] struct { /* private immutable path source */ }

func BindForward[S, T any](
	source BoundModel[S],
	field string,
	target BoundModel[T],
) (ForwardRelation[S, T], error)

type RelatedIntegerField[M any] struct { /* private sealed state */ }
type RelatedStringField[M any] struct { /* private sealed state */ }

func (r ForwardRelation[S, T]) Integer(field IntegerField[T]) (RelatedIntegerField[S], error)
func (r ForwardRelation[S, T]) String(field StringField[T]) (RelatedStringField[S], error)
func (f RelatedIntegerField[M]) Exact(value int64) Predicate[M]
func (f RelatedStringField[M]) Exact(value string) Predicate[M]

func ParseDynamicRelations[M any](
	model BoundModel[M],
	policy LookupPolicy,
	inputs []LookupInput,
) ([]Predicate[M], error)
```

`ProjectBinding` retains normalized model metadata in the same private immutable snapshot as its relation metadata.
`Model` returns `ir.Model.Clone()`. `BindModel` compares the bound model and generated descriptor metadata exactly;
`BindForward` requires source and target from the same snapshot, a required many-to-one FK, and matching target identity.
Nullable relations are deliberately rejected in this packet.

`ForwardRelation.String/Integer` validate target field identity and construct a canonical relation path. The returned
related field carries only the source model type because the target model type has already been checked by the method
receiver and target field argument. `errors.As` must expose stable `*query.Error` values; dynamic exact error codes are
`unknown_relation` and `unknown_related_field`, while extra path segments or lookup suffixes use existing
`unsupported_lookup`. All are `CategoryField` and construction I/O remains zero.

`ParseDynamicRelations` accepts only exact two-segment required forward paths such as `author__name` and `author__id`.
It rejects nullable `reviewer`, reverse paths, multiple hops and non-exact lookups. Existing scalar `ParseDynamic`
preserves every scalar-field behavior, but must fail closed with `CategoryField` / `CodeUnsupportedLookup` when a public
v3 descriptor exposes `FieldForeignKey` or non-nil relation metadata. This prevents `author__isnull` or
`reviewer__isnull` from bypassing the project-bound relation path as a raw integer lookup before REL-006 is implemented.

### Immutable Query AST

`query.RelationHop` and `query.RelationPath` use private state and copy accessors. A one-hop path records source and target
symbolic identity/table, source field/FK column, target PK column, exact direction `forward`, cardinality `many_to_one`,
nullability and terminal `FieldRef`.

```go
func NewForwardRelationPath(
	source ir.ModelIdentity,
	sourceTable, field, sourceColumn string,
	target ir.ModelIdentity,
	targetTable, targetPKColumn string,
	nullable bool,
	terminal FieldRef,
) (RelationPath, error)

func (p RelationPath) Hops() []RelationHop
func (p RelationPath) Terminal() FieldRef
func (p RelationPath) Equal(other RelationPath) bool
func NewRelatedCondition(path RelationPath, lookup Lookup, value Value) Condition
func (c Condition) RelationPath() (RelationPath, bool)
```

`Condition.Equal`, `Plan.Equal`, `Conditions`, `WithConditions` and plan cloning include a deep relation path copy.
Post-construction caller mutation cannot alter a plan, and concurrent reads are race-free. Typed and dynamic predicates
must produce `Plan.Equal` values.

### SQLite compilation

Plans without relation paths execute the existing scalar compiler branch without changing SQL, arguments or errors.
The relation branch supports exact one required forward hop only. It:

- canonicalizes and deduplicates joins by `(source app, model, field, target app, model)`;
- rejects inconsistent metadata for one key;
- sorts edges and assigns deterministic aliases `t0`, `t1`, ...;
- qualifies root SELECT/order/scalar conditions with `t0`;
- emits `INNER JOIN` and qualifies related terminal predicates with their target alias;
- supports `Exact` only and keeps every value parameterized;
- rejects structurally invalid/reverse/multi-hop/nullable paths, root/source-table mismatch and conflicting metadata for
  one repeated edge before backend `QueryContext`.

Target model/key correctness is validated while `BindForward` still owns the full immutable `ProjectBinding`; the
SQLite compiler has no project snapshot and does not independently re-resolve symbolic target identity from a path.

REL-004's exact shape is semantically:

```sql
FROM "blog_post" AS "t0"
INNER JOIN "authors_author" AS "t1"
  ON "t0"."author_id" = "t1"."id"
```

One or two author predicates must still emit exactly one join. Existing no-relation SQLite SQL goldens remain exact.

## Product fixture and adapter

New `conformance/relationqueryproduct/**` owns a separate checked-in generated project. It does not modify existing
`conformance/relationproduct/**` bytes. The actual handler imports only product/generated code and SQLite; it does not
read relation oracle/static fixtures or expected payload constants.

The adapter provisions manual test tables only as a conformance fixture. This is not relation DDL/migration support.
It enables SQLite FK enforcement and proves an orphan source row is rejected, then executes both oracle cases through
generated project bindings, ORM QuerySet, relation AST and SQLite compiler. Metrics derive from actual backend query
count and compiled plan/SQL classification rather than replayed constants.

Mutation gates change author name, FK identity, terminal target field and expected row order independently. Any such
mutation must change the observation or fail structurally. Setup/teardown SQL is outside the metric window.

## CI and false-green gates

- Preserve exact top-level 26-job topology: existing 2 + six four-coordinate matrices. Do not add service-only jobs.
- Extend the existing relation-product four-coordinate package set with query/ORM/codegen/SQLite/relationqueryproduct
  paths; assert exact test inventory, equal run/pass sets, skip 0, encoded bytes and SHA after implementation.
- Run normal, race, CGO-disabled and vet on linux/amd64, linux/arm64, darwin/amd64 and darwin/arm64.
- Run actual REL-004 path with `GOARCH=386`, `CGO_ENABLED=0` in the full Ubuntu job.
- Lock existing v2 output, existing v3 main/metadata/`Bind()` bytes and old scalar SQLite SQL.
- Lock new companion/bridge deterministic bytes, gofmt, schema hash, generator version and checked-in no-rewrite.
- External temp module must pass `GOWORK=off`, `GOPROXY=off`, `GOTOOLCHAIN=local` `go list` and `go test`;
  app-to-app Imports/Deps remain exact 0 and project bridge owns both app imports.
- Positive external public API compile and negative cross-model/wrong-target/value compile fixtures are mandatory.
- Construction I/O 0; each case evaluation exactly one SELECT; one/two predicates exactly one INNER and zero LEFT.
- Qualified root/target duplicate `id` columns and parameterized values must be asserted.
- Typed/dynamic `Plan.Equal`, input/output mutation isolation and concurrent-read race gates are mandatory.
- Invalid/reverse/multi-hop/nullable/unsupported paths fail before compiler/backend I/O.
- Product result `[10,11]` must come from actual SQLite rows; DB state is byte/semantic unchanged after both cases.
- Manifest diff is status-only for REL-004. Oracle, not-implemented static fixture and SHA256SUMS bytes are immutable.
- Product sequence is exact observed REL-001/004 and ordered payload-free NI for the other ten contracts.
- Update all aggregate/workflow Python self-pins from measured values. Keep four exact Python versions CI-only;
  routine local remains CPython 3.14.3 + uv 0.12.3. Historical exact darwin keeps uv 0.10.12.
- `continue-on-error`, green skips, PostgreSQL/MySQL services and Windows jobs remain absent.

## 완료 조건

- [x] ADR-0025 exact API/AST/compiler boundary is independently reviewed while Proposed.
- [x] Existing main/metadata/project binding generator bytes and scalar SQLite SQL are byte-preserved.
- [x] Additive relation query companion and project query bridge compile without app-to-app imports.
- [x] ProjectBinding model snapshot, BoundModel and required ForwardRelation are immutable and deterministic.
- [x] Typed and dynamic `author__name`/`author__id` build equal immutable plans with construction I/O 0.
- [x] SQLite emits one reusable required INNER JOIN and actual result `[10,11]` for both locked cases.
- [x] Invalid/reverse/multi-hop/nullable paths, root/source-table mismatch and repeated-edge metadata conflicts fail
  pre-I/O with structured errors; wrong source/target bindings fail earlier in `BindModel`/`BindForward`.
- [x] Existing scalar `ParseDynamic` preserves scalar behavior but rejects relation/FK fields, and
  `ParseDynamicRelations` classifies relation-level `__isnull` as `unsupported_lookup` without a plan or I/O.
- [x] REL-004 actual adapter is oracle-blind and mutation-sensitive; ten other REL contracts stay payload-free NI.
- [x] Product aggregate is exact `112 passing + 5 deviation + 10 oracle_locked = 127`.
- [x] Local normal/race/CGO-disabled/vet/repetition/386 compile/no-rewrite/diff-clean gates pass; actual Linux/386
  execution remains hosted-only.
- [x] Existing exact 26 hosted executions pass on the exact implementation head with skip 0.
- [x] Independent query/API, codegen/import, SQLite/conformance and final integration audits report P0..P3 = 0.
- [x] Work/status/matrix/evidence and ADR are synchronized; ADR is Accepted only for this bounded slice.

## 비목표

- REL-002 unsaved assignment/save and any relation write API
- REL-003 forward object access, loader, instance cache, singleflight or cache invalidation
- REL-005 reverse accessor/query path and any reverse manager
- REL-006 nullable object access, relation `isnull`, join trimming or LEFT join
- REL-007/008 delete collector, PROTECT/SET_NULL mutation execution or transaction semantics
- REL-009/010/011 `select_related`, eager hydration and invalid reverse eager path
- REL-012 prefetch, IN batching and reverse cache
- nullable forward target predicate, multi-hop/reverse predicate, OR/Q, relation ordering/aggregation
- OneToOne/ManyToMany, non-AutoField/to_field/composite target, relation DDL/migration/history
- model hidden relation state, public cache/loader/Get/Assign/Clear/Save APIs
- generated batch publication/generate CLI and existing generated-file replacement guarantees
- PostgreSQL/MySQL/Windows/multi-DB support or service-only CI

## 진행 기록

- 2026-08-10: GDJ-0024 final exact-head `5bf14357...` run `31351169780`의 26/26 jobs,
  326/326 recorded steps를 EVID-038로 확인했습니다.
- 2026-08-10: 세 독립 read-only gap/priority/API 검토가 REL-004-only를 최소 정직한 다음 수직 단면으로
  합의했습니다. REL-003/006 cache/nullable ABI는 후속으로 유지합니다.
- 2026-08-10: ADR-0025를 Proposed, 이 work를 active로 활성화했습니다. Activation patch 자체 exact-head CI는
  activation commit `cf8cb589575836cb1393079ce04ff06fc549800a`의
  [run 31354040515](https://github.com/progresshans/godj/actions/runs/31354040515)에서 exact 26/26 jobs와
  326/326 recorded steps를 통과했습니다. 이 run은 activation 문서와 기존 제품만 증명합니다.
- 2026-08-10: REL-004 implementation은 local normal/race/CGO-disabled/vet, count-20/shuffle-10, bounded
  Linux/386 compile, Python relation 11/11, twelve-adapter conformance와 exact workflow inventory
  492 run/492 pass/0 skip·49,902 bytes·SHA-256 `05064a7f...82eb`을 통과했습니다. Query/ORM,
  codegen/import, SQLite/conformance, final integration/security audits는 모두 P0/P1/P2/P3=0입니다.
  [EVID-039](../docs/status/TEST_EVIDENCE.md#evid-20260810-039--gdj-0025-rel-004-forward-predicate-pre-hosted-local-validation)에
  pre-hosted 증거를 기록했고 그 evidence 시점의 implementation-head hosted CI는 `not run/pending`이었습니다.
- 2026-08-10: Implementation commit `98db55a30ff71a2f2f70722cb569a046208a5403`의 Draft PR #1
  [run 31357283530](https://github.com/progresshans/godj/actions/runs/31357283530)이 exact 26/26 jobs와
  326/326 recorded steps를 성공했습니다. Four relation-product coordinates는 각각 exact
  492 run/492 pass/0 skip·49,902 bytes·SHA-256 `05064a7f...82eb`을 재현했고 actual Ubuntu Linux/386와
  four exact Python compatibility legs도 통과했습니다. Independent hosted raw-log audit는
  P0/P1/P2/P3=0이었습니다. [EVID-040](../docs/status/TEST_EVIDENCE.md#evid-20260810-040--gdj-0025-github-hosted-exact-26-job-implementation-head-ci)에
  기록하고 bounded ADR-0025를 Accepted, 이 work를 completed로 전환했습니다.
- 2026-08-10: Completion-documentation commit `7b5cebda7410ae8c096a8c30bd60daad1295bbf2`의 Draft PR #1
  [run 31358640776](https://github.com/progresshans/godj/actions/runs/31358640776)도 exact 26/26 jobs와
  326/326 recorded steps를 성공했습니다. Four relation-product coordinates는 각각 exact
  492 run/492 pass/0 skip·49,902 bytes·SHA-256 `05064a7f...82eb`을 재현했고 actual Ubuntu Linux/386,
  exact darwin 193/193과 four exact Python compatibility legs도 통과했습니다. Independent hosted
  raw-log audit는 P0/P1/P2/P3=0이었으며
  [EVID-041](../docs/status/TEST_EVIDENCE.md#evid-20260810-041--gdj-0025-github-hosted-completion-documentation-head-exact-26-job-ci)에
  기록했습니다.
- 2026-08-10: Final evidence/status commit `bffc52844de87a2791959ea1e8f99c60dd13d1aa`의 별도
  [run 31359958949](https://github.com/progresshans/godj/actions/runs/31359958949)도 exact 26/26 jobs와
  326/326 recorded steps를 성공했습니다. EVID-041의 recursive pending을
  [EVID-042](../docs/status/TEST_EVIDENCE.md#evid-20260810-042--gdj-0025-final-exact-head-ci-and-gdj-0026-activation-baseline)로
  닫았고 이 tested head는 GDJ-0026 activation baseline일 뿐 REL-003/006 증거로 재사용하지 않습니다.

## 현재 blocker

외부 blocker는 없습니다. Activation, local implementation/audit, exact implementation-head,
completion-documentation-head와 final evidence/status head의 hosted acceptance가 모두 통과했습니다.
GDJ-0025의 recursive pending은 EVID-042로 닫혔습니다.

## 다음 정확한 작업

1. Tested final head `bffc5284...`와 EVID-042를 다음 packet의 baseline으로만 사용합니다.
2. REL-003/006은 별도 [GDJ-0026](0026-forward-foreign-key-object-cache-and-nullability-product-slice.md)과
   Proposed ADR-0026의 exact allowlist/API 안에서만 구현합니다.
3. GDJ-0026 activation/implementation diff는 각 exact head에서 same 26을 새로 검증합니다.
4. Draft PR은 사용자 요청 전 merge하지 않습니다.

## 인수인계

GDJ-0025는 completed required-one-hop predicate boundary입니다. Activation exact-head, local implementation/
four independent audits, implementation exact-head와 completion-documentation exact-head exact 26/26 hosted
acceptance와 final evidence/status exact-head acceptance까지 통과했고 ADR-0025의 additive query
companion/project bridge, immutable shared relation path와 SQLite reusable required INNER JOIN을 Accepted했습니다. 제품은 exact
`112 passing + 5 deviation + 10 oracle_locked`, relation actual REL-001/004 2/12입니다. Loader/cache,
nullable/reverse/eager/write/delete/DDL/migration, broader target와 PostgreSQL/MySQL/Windows는 완료 범위가
아닙니다. Final baseline은 `bffc5284...`/EVID-042이고 후속 GDJ-0026 activation diff 자체는 pending입니다.
Draft PR #1은 사용자 요청 전 merge하지 않습니다.
