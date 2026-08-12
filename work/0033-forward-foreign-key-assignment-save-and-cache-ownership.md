---
id: GDJ-0033
status: active
updated: 2026-08-12
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed"
depends_on: ["GDJ-0032"]
contracts: ["REL-002", "Q-013", "Q-017"]
allowed_paths:
  - "query/error.go"
  - "query/relation_mutation_test.go"
  - "orm/write.go"
  - "orm/write_test.go"
  - "orm/save_test.go"
  - "codegen/project_relation_facade.go"
  - "codegen/project_relation_facade_test.go"
  - "codegen/testdata/relation_facade/project.golden"
  - "conformance/relationdeleteproduct/project/zz_godj_relation_facade.go"
  - "conformance/relationdeleteproduct/product_test.go"
  - "conformance/relationdeleteproduct/observer.go"
  - "conformance/runners/godj/relation_scenarios.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/contracts/relation-manifest.json"
  - "conformance/README.md"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/runners/django/tests/test_relation_scenarios.py"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_facade/external_consumer.go.txt"
  - ".github/workflows/ci.yml"
  - "docs/COMPATIBILITY.md"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/CONCURRENCY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
  - "docs/adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0032-production-forward-project-facade-and-additive-first-publication.md"
  - "work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Forward ForeignKey Assignment, Save, and Cache Ownership

## 사용자에게 보이는 결과

이 work의 단일 결과는 current production project facade에서 forward ForeignKey를 관계 객체로 설정하고 source를
저장할 때 Django 6.1의 REL-002 observable semantics를 Go에 맞는 명시적 API로 제공하는 것입니다. 성공하면 현재
유일한 relation `oracle_locked`인 REL-002를 `passing`으로 전환합니다. Reverse manager, delete facade, migration
ForeignKey DDL, general generated upgrade는 이 결과에 포함하지 않습니다.

아래 이름은 Phase C의 compile/runtime decision gate 전까지 **비정본 후보**입니다.

```go
models, err := project.Using(backend)
author, err := models.AuthorsAuthor.New(authors.Author{Name: "unsaved"}) // candidate
post, err := models.BlogPost.New(blog.Post{Title: "draft"})              // candidate

derived, err := post.WithAuthor(author) // candidate: fresh source wrapper
saved, err := models.BlogPost.Save(ctx, derived)
```

Assignment 뒤 같은 derived source의 `Author(ctx)`는 database query 없이 exact assigned target wrapper를 반환해야
합니다. Raw `AuthorID`에서 다시 source를 파생하면 assigned-object override를 버리고 scalar와 일치하는 cold relation
state로 돌아갑니다. Nullable Reviewer clear는 explicit absent state와 SQL NULL을 함께 표현해야 합니다.

## 기준과 현재 상태

- Clean baseline은 GDJ-0032 terminal head
  `8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed`과 EVID-071/run `31563615648`입니다.
- Baseline product는 exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation
  actual 11/12이며 REL-002만 `oracle_locked`입니다.
- Existing generated exact 13과 additive exact 14/physical exact 17은 baseline product facts입니다. 이 activation
  tree는 source/generated/manifest를 바꾸지 않으며 자체 exact-head CI는 `not run/pending`입니다.
- [ADR-0032](../docs/adr/0032-production-forward-project-facade-and-additive-first-publication.md)의 bounded Gate 0
  facade, backend composite, wrapper namespace와 forward read surface는 재개방하지 않습니다.

## Django가 정한 observable semantics

Phase A는 pinned Django 6.1 source와 existing REL-002 oracle을 다음 동작 기준으로 고정합니다.

1. 관계 객체 assignment는 target key를 source raw FK에 반영하고 같은 관계 accessor cache를 warm합니다.
2. raw FK scalar가 바뀌면 이전 관계 cache는 무효화됩니다.
3. assigned target에 primary key가 없으면 source save는 database mutation 전에 unsaved-related-object 오류로
   실패합니다. Nullable FK도 silent data loss를 막기 위해 같습니다.
4. primary key가 수동으로 존재하지만 target row가 아직 저장되지 않은 객체는 preflight를 통과하고 database FK
   constraint가 존재 여부를 판단합니다.
5. assignment 뒤 같은 target object가 저장돼 key를 얻으면 source save 직전에 그 key를 다시 읽어 source FK를
   reconcile합니다.
6. nullable 관계 clear는 raw FK NULL과 cached absent를 함께 설정합니다. Required relation의 nil-like assignment는
   memory에서 표현될 수 있어도 save의 database constraint 결과까지 성공으로 간주하지 않습니다.
7. transaction rollback은 Python/Go heap object를 자동으로 이전 값으로 되돌리지 않습니다. DB rollback과 memory
   state rewind를 혼동하지 않습니다.

GoDj는 Python descriptor, mutable model identity와 exception type을 복제하지 않습니다. 위 결과·부작용·오류 시점만
정본이고 `context.Context`, structured `error`, generated wrapper와 explicit origin으로 번역합니다.
특히 required nil-like assignment를 Go public API가 허용할지, construction에서 거부할지와 exact error timing은
Phase C의 Go-only decision입니다. Django가 memory assignment를 허용한다는 관찰만으로 후보 API를 미리 고정하지
않습니다. Nullable clear의 observable result와 required clear API 존재 여부를 분리합니다.

## Phase A — semantics audit

- [ ] Pinned Django 6.1 `ForwardManyToOneDescriptor.__set__`, `ForeignKeyDeferredAttribute.__set__`,
  `Model._prepare_related_fields_for_save`와 `many_to_one` regression을 source/line provenance와 함께 기록합니다.
- [ ] Existing REL-002 oracle payload/phase/category/code/metrics/DB-state를 재확인하되 oracle, Django runner와 checksum은
  수정하지 않습니다.
- [ ] No-PK assigned target, manually key-present but unpersisted target, target-saved-after-assignment, nullable clear,
  raw FK invalidation과 outer rollback memory non-rewind를 각각 observable matrix로 고정합니다.
- [ ] `select_related` Resolve/Bind cause가 facade에서 generic invalid-plan으로 축약되는 current gap을 재현하고, REL-002
  작업과 독립적인 narrow remediation인지 판정합니다. 새 Q/ADR로 확장하지 않습니다.

## Phase B — no-product feasibility

Phase B는 checked-in product companion이나 manifest를 바꾸기 전에 temp/golden/compile/runtime seam에서 아래 후보를
검증합니다.

### Leading ownership candidate

- Source relation assignment는 **fresh `*BlogPost` derived wrapper**를 반환하고 original source wrapper는 그대로 둡니다.
- Derived source는 exact assigned `*AuthorsAuthor` pointer와 assigned/present/cleared tri-state를 보존합니다.
- Required `blog.Post.AuthorID int64`는 Django의 pending `None`을 직접 표현하지 못하므로, no-PK assignment 동안
  assigned/pending tri-state가 authoritative입니다. Raw `AuthorID == 0`은 이 상태에서 savedness나 valid FK가 아니며
  private write descriptor/plan으로 전달되기 전에 반드시 REL-002 preflight에서 차단합니다.
- Accepted Gate 0 `Unwrap() blog.Post`도 pending `None`을 충실히 표현할 수 없습니다. Pending 동안 raw scalar를
  authoritative하게 노출하지 않고 accessor는 exact assigned target/query 0을 유지하며, pending `Unwrap`/scalar
  exposure의 exact representation은 Phase C에서 고정합니다. Loaded source의 이전 nonzero FK가 새 no-PK assignment의
  authoritative key처럼 새면 안 됩니다.
- Target wrapper Save는 existing `authors.AuthorObjects.Save(..., &target.model)`을 통해 same wrapper를 in-place
  갱신하는 후보입니다. Source Save는 mutation plan 직전에 그 exact target wrapper descriptor의 PK presence를 다시
  읽어 assignment 뒤 target Save로 생긴 key를 reconcile합니다.
- 이는 별도 materialization 사이 global identity map이나 pointer identity 계약이 아닙니다. Exact assigned pointer
  한 개의 local ownership만 사용합니다.
- Same wrapper의 concurrent Save/access는 caller synchronization 없이는 지원하지 않으며 data race가 될 수 있습니다.
  Nil/zero/dereference-copy/cross-origin wrapper는 pre-I/O structured error로 거부해야 합니다.
- Source wrapper self sentinel, facade state pointer와 exact origin equality를 검증합니다. Session-origin 사용은 callback
  안에서만 지원하고 callback 이후 behavior는 계속 noncontractual입니다.

### Write descriptor boundary

- App-level `blog.Post`는 PK-presence bit를 갖지 않고 companion이 다른 package type에 field를 추가할 수 없습니다.
  Existing app-generated exact 13을 다시 쓰는 relation `WriteDescriptor`는 이 packet에서 금지합니다.
- Leading candidate는 generated project-private `blogPostWriteModel{model blog.Post; primaryKeyPresent bool}`와 private
  descriptor를 만들고 generic `orm.Manager.Save`를 재사용합니다.
- Core 변경 후보는 `orm/write.go`의 ForeignKey integer/NULL acceptance와 stable
  `query.CodeUnsavedRelatedObject`뿐입니다. 일반 relation-specific save engine을 별도로 만들지 않습니다.
- Manually key-present target은 unsaved preflight를 통과해 실제 database constraint까지 가야 합니다.

### Unsaved wrapper construction and PK-presence ownership

REL-002 exact scenario는 loaded source/target이 아니라 **새 no-PK target과 새 source**를 관계로 묶은 뒤 source Save를
호출합니다. Phase B는 raw numeric ID 값으로 savedness를 추측하지 않고 다음 construction seam을 prototype해야 합니다.

- Unsaved `AuthorsAuthor` wrapper를 raw/generated input에서 만들되 `AuthorDescriptor`의 hidden PK-presence를 wrapper
  state로 정확히 보존합니다. ID가 0/비0인지가 아니라 descriptor presence가 authoritative입니다.
- Unsaved `BlogPost` wrapper는 source `primaryKeyPresent=false`로 시작합니다. Loaded query result만 source presence
  `true`로 materialize합니다.
- Required FK의 no-PK assignment는 raw `int64` zero를 key-present로 승격하지 않습니다. Assigned/pending tri-state를
  먼저 검사하고, target descriptor presence가 false이면 private write-model field derivation과 plan construction 전에
  exact REL-002로 실패해야 합니다.
- Manual key-present target constructor는 row가 DB에 없어도 descriptor presence `true`를 보존해 unsaved-related
  preflight를 통과시키고 database FK가 실패하도록 합니다.
- Manual key-present target은 numeric ID가 0이든 nonzero이든 descriptor presence가 authoritative하며 둘 다
  preflight를 통과한 뒤 database constraint가 실제 row 존재를 결정해야 합니다.
- Constructor/build/helper exact names와 raw vs generated input shape는 Phase C 전까지 noncanonical입니다.
- External compile gate는 새 source+새 no-PK target assignment→source Save REL-002 failure와, 같은 target wrapper를
  먼저 Save한 뒤 same derived source Save가 key를 reconcile하는 두 흐름을 모두 증명해야 합니다.

### Required feasibility gates

- [ ] Assignment warm cache: same exact target wrapper, accessor query 0.
- [ ] No-PK target: stable category/code, source mutation I/O 0, target/source DB state unchanged.
- [ ] Required raw-FK representation: pending tri-state가 authoritative이고 raw zero는 valid FK/savedness가 아니며
  private descriptor/plan 이전에 차단됨.
- [ ] Pending raw exposure: new-source zero와 loaded-source old-nonzero 두 경우 모두 `Unwrap` scalar가 authoritative
  FK처럼 사용되지 않고 accessor는 exact target/query 0, source Save는 REL-002/I/O0; target Save 뒤에만 raw key reconcile.
- [ ] Manual key-present but absent row: preflight 통과, SQLite FK error가 authoritative.
- [ ] Unsaved source/target constructor: descriptor PK-presence 보존, numeric ID 기반 savedness 추론 없음.
- [ ] Same assigned target later Save: target wrapper key in-place publication, source Save 직전 one-time key snapshot과
  reconciliation.
- [ ] Raw scalar derivation: assigned override 제거, cache cold, 새 scalar target만 load.
- [ ] Nullable clear: explicit absent override, raw FK NULL, accessor `nil,false,nil`, save parity.
- [ ] Original source immutability: assignment/save가 original source wrapper의 raw scalar/cache/state를 바꾸지 않음.
- [ ] Outer transaction rollback: database는 rollback되지만 target PK/source derived memory는 자동 rewind하지 않음.
- [ ] Nil/typed-nil/zero/dereference-copy/cross-model/cross-origin misuse가 I/O 전 실패. Callback-local session positive를
  검증하되 post-return에는 success/failure assertion을 두지 않고 noncontractual 경계를 유지.
- [ ] Save가 exact target key를 plan 전 한 번 snapshot하고 validation/REL-002 오류 precedence를 보존.
- [ ] Minimal capability fake와 callback-local session이 필요한 exact method set만 요구하는지 확인.

Phase B가 이 후보를 compile/runtime로 증명하지 못하면 Phase C나 product publication으로 진행하지 않고 work를
blocked 처리하거나 새 activation으로 scope를 다시 고정합니다.

## Phase C — decision freeze와 bounded implementation

Phase B가 clean일 때만 [ADR-0033](../docs/adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md)을
Accepted로 전환하고 exact public method names, return shapes, wrapper mutation/derivation, error category/code와 generated
bytes를 고정합니다. 지금 문서의 `WithAuthor`, `Save`, clear/scalar helper 이름은 모두 비정본입니다.

같은 work에서 implementation으로 이어갈 수 있는 범위는 frontmatter의 exact allowlist에 한정합니다. 그 밖의 core,
generated app file, DB backend, migration/schema 또는 conformance artifact가 필요하면 즉시 멈추고 별도 activation을
거칩니다. Integration files `.github/workflows/ci.yml`, `conformance/README.md`,
`conformance/internal/protocol/migration_project_check_artifacts_test.go`,
`conformance/internal/protocol/relation_artifacts_test.go`,
`conformance/internal/protocol/write_migration_artifacts_test.go`,
`conformance/cmd/godjcheck/main_test.go`,
`conformance/runners/django/tests/test_relation_scenarios.py`는 REL-002 manifest/status와 measured inventory가 실제로
변한 뒤 integration owner가 exact lock을 함께 갱신할 때만 수정합니다. Python path는 product manifest status-vector
assertion만 갱신할 수 있고 Django scenario execution, oracle payload와 checksum은 계속 byte-frozen입니다.

Phase C에서 bounded implementation/status가 실제로 freeze된 뒤 `docs/ARCHITECTURE.md`와
`docs/CAPABILITY_CATALOG.md`의 product aggregate/REL-002 overview, `docs/CONCURRENCY.md`의 same-target in-place Save,
caller synchronization과 rollback memory boundary만 conditional completion update로 맞춥니다. 이 세 문서는 activation
exact14 diff에는 포함하지 않습니다.

## 오류와 cache 계약 후보

- Unsaved related target은 frozen REL-002와 같은 stable `model_state_error/unsaved_related_object` 후보이며 detail
  message는 noncontractual입니다.
- Original binder/invalid-wrapper/backend validation과 REL-002 preflight의 exact precedence를 테스트로 고정합니다.
- Assignment cache는 source derived wrapper에만 속합니다. 별도 materialization이나 downstream target cache를
공유하지 않습니다.
- Raw FK scalar가 authoritative하게 파생되면 assigned override를 폐기합니다. Assigned target에서 source key를
가져올 때는 save plan 직전 한 번 snapshot해 plan 안에서 바뀌지 않게 합니다.
- Failed save와 outer rollback은 wrapper memory를 자동 복원하지 않습니다. Caller는 returned error와 transaction
결과를 기준으로 fresh load/explicit re-derivation을 선택합니다.

## 별도 추적하지만 이 work의 결과가 아닌 항목

- Q-019는 SQLite unknown-outcome retained connection이 `Backend.Close`까지 누적될 수 있는 resource policy를 추적합니다.
  GDJ-0033은 `db/**`를 바꾸지 않습니다. Behavior를 바꿀 때는 별도 work/ADR로 ADR-0030을 명시적으로
  amend/supersede합니다.
- Q-017의 facade prerequisite generator-version/output-digest provenance는 coordinated multi-file upgrade/general
  `--check` 전에 필수입니다. REL-002 한 파일 교체를 막는 선행 조건으로 과장하지 않습니다.
- PROTECT가 모든 protected-row identity를 materialize하는 현재 구현은 production-scale bounded-memory gate입니다.
  Public `ProtectedError` payload 의미를 바꾸지 않는 최적화라면 새 ADR 없이 별도 work로 다룹니다.

## 비목표

- Reverse manager/chaining, reverse assignment, OneToOne/ManyToMany 또는 multi-hop eager/write
- Target wrapper의 global identity map, 별도 query materialization 사이 stable pointer identity 또는 downstream cache
- Delete facade, CASCADE/recursive/bulk delete와 PROTECT payload redesign
- Relation migration ProjectState/CreateModel/AddField/constraint codec/rollback/restart
- Existing app-generated exact 13 재작성, general coordinated generated upgrade/repair/CLI
- SQLite retained-connection policy 변경, callback-after-return deterministic lifetime enforcement
- PostgreSQL/MySQL/Windows 또는 broad non-SQLite support

## 정확한 구현 경로 경계

Phase B/C와 clean feasibility 뒤 bounded implementation이 사용할 수 있는 source/product path는 다음 exact 목록뿐입니다.

- `query/error.go`, `query/relation_mutation_test.go`
- `orm/write.go`, `orm/write_test.go`, `orm/save_test.go`
- `codegen/project_relation_facade.go`, `codegen/project_relation_facade_test.go`,
  `codegen/testdata/relation_facade/project.golden`
- `conformance/relationdeleteproduct/project/zz_godj_relation_facade.go`,
  `conformance/relationdeleteproduct/product_test.go`, `conformance/relationdeleteproduct/observer.go`
- `conformance/runners/godj/relation_scenarios.go`, `conformance/runners/godj/runner_test.go`
- `conformance/contracts/relation-manifest.json`
- `internal/compiletest/compile_test.go`,
  `internal/compiletest/testdata/relation_facade/external_consumer.go.txt`
- Conditional measured integration lock: `.github/workflows/ci.yml`, `conformance/README.md`,
  `conformance/internal/protocol/migration_project_check_artifacts_test.go`,
  `conformance/internal/protocol/relation_artifacts_test.go`,
  `conformance/internal/protocol/write_migration_artifacts_test.go`, `conformance/cmd/godjcheck/main_test.go`,
  `conformance/runners/django/tests/test_relation_scenarios.py`의 manifest status assertion only
- Conditional completion docs: `docs/ARCHITECTURE.md`, `docs/CAPABILITY_CATALOG.md`, `docs/CONCURRENCY.md`

기존 app-generated exact 13의 다른 파일, `schema/**`, `db/**`, 위 status assertion을 제외한 Django runner/scenario,
oracle/SHA/static not-implemented fixture, reverse/delete facade, migration/codec, `go.mod`, `go.sum`, `Makefile`은 명시적
제외입니다.

## 완료 조건

- [ ] Phase A observable matrix와 source provenance가 문서/테스트에 고정됨
- [ ] Phase B leading candidate가 no-product compile/runtime gate를 통과함
- [ ] New source/no-PK target construction과 target-save-then-source reconciliation external compile gate 통과
- [ ] Phase C에서 public 이름과 ownership/error/cache 결정을 ADR에 명시적으로 freeze함
- [ ] REL-002 reference bytes는 변하지 않고 GoDj actual만 exact manifest row로 전환됨
- [ ] Product aggregate가 실제 결과에 맞게 재계산되고 false green 없이 relation inventory가 증가함
- [ ] Existing generated exact 13 byte identity와 product companion determinism/last-good가 유지됨
- [ ] Normal/race/CGO0/vet/bounded Linux386, exact Darwin/four Python/four relation-product hosted gate가 통과함
- [ ] Completion documentation과 terminal evidence는 각각 별도 exact-head CI를 사용함

## 현재 증거와 다음 작업

- EVID-071/run `31563615648`: baseline head `8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed`, exact
  26/26 jobs·326/326 steps success, each relation coordinate 697/697/0, 70,659 bytes, SHA-256
  `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`, independent audit P0..P3=0.
- 이 EVID-071 append와 GDJ-0033 activation exact 14-path tree 자체 CI는 `not run/pending`입니다. Run
  `31563615648`을 activation proof로 재사용하지 않고 Draft PR은 merge하지 않습니다.
- 다음 작업은 Phase A source/oracle matrix, Phase B temp/golden feasibility를 순서대로 실행하는 것입니다. Product
  source/publication은 Phase B clean과 Phase C decision freeze 전에는 수정하지 않습니다.
