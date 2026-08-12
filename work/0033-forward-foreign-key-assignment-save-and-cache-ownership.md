---
id: GDJ-0033
status: completed
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

이 work의 단일 결과는 production project facade에서 forward ForeignKey를 관계 객체 또는 explicit scalar로 설정하고
source를 저장하는 API를 제공하여 REL-002를 실제 product `passing`으로 만드는 것입니다. Django 6.1의 관찰 의미를
다시 결정하지 않고, 그것을 Go의 explicit context/error, generated wrapper와 immutable source derivation으로 옮깁니다.

Phase A/B/C가 끝난 지금 exact public surface는 다음과 같습니다.

```go
models, err := project.Using(backend)
author, err := models.AuthorsAuthor.New(authors.Author{Name: "Ada"})
post, err := models.BlogPost.New(blog.Post{Title: "draft"})

post, err = post.WithAuthor(author) // fresh source; original은 그대로
err = post.Save(ctx)                // no-PK target이면 pre-I/O REL-002

err = author.Save(ctx)              // same target wrapper에 generated PK 게시
err = post.Save(ctx)                // pending assignment만 key를 reconcile
```

Nullable relation은 `WithReviewer`, `WithReviewerID`와 `ClearReviewer`를 사용합니다. Required relation에는 clear
method가 없고 nil target은 pre-I/O invalid-plan입니다.

## 기준과 현재 상태

- Clean activation baseline은 GDJ-0032 terminal head
  `8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed`과 EVID-071/run `31563615648`입니다.
- GDJ-0033 activation head `a4a627a5702ac9db4ee8c39706ff098783a9c5e6`은 EVID-072/run
  `31566524953`의 unique exact 26/26 jobs·326/326 steps를 성공했습니다.
- Decision-documentation head `9d728610acbe037bab73fde8910cc80ae8411691`은 EVID-074/run `31574653183`의
  unique exact 26/26 jobs·326/326 steps와 independent audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다.
- Exact implementation head `be6f3d4e0838929fe96ec156ec0647845d905ea6`은 EVID-076/run `31586910749`의
  unique exact 26/26 jobs·326/326 steps와 independent audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. Bounded product는
  exact 12 adapters/127 contracts=`122 passing + 5 deviation + 0 oracle_locked`, relation actual 12/12이며 REL-002가
  `passing`입니다.
- Phase-B no-product prototype exact patch SHA-256은
  `8329bb0ae76dc3297ad692cd447d11f11cc6578b574202c72dc7c0d754b6c566`이고 independent audit
  P0/P1/P2/P3=`0/0/0/0`입니다.
- [ADR-0033](../docs/adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md)은 위 결정을 근거로
  `Accepted`, bounded code는 `Implemented`, EVID-076에 기록된 환경에서는 `Verified`입니다. ADR 상태 enum을
  `Verified`로 바꾸지는 않습니다.

## Django 6.1 관찰 의미

다음은 더 이상 Go-only open question이 아닙니다.

1. Object assignment는 target PK를 source FK에 복사하고 exact target cache를 warm합니다.
2. Raw scalar가 다를 때만 selected cache를 invalidate하고 같은 scalar는 cache를 유지합니다.
3. Assigned target PK presence가 없으면 required/nullable source Save와 pending `Unwrap`은
   `model_state_error/unsaved_related_object`, query/mutation 0입니다.
4. Manual PK-present target은 key `0`도 preflight를 통과하고 DB FK가 row 존재를 판단합니다.
5. Assignment 당시 no-PK였고 source scalar가 계속 empty인 exact target wrapper만 later Save 뒤 source key를
   reconcile합니다. Caller가 scalar를 바꾸면 그 선택이 이깁니다.
6. Assignment 당시 이미 key-present였던 target key가 뒤에 달라지면 old source scalar를 유지하고 stale selected cache를
   지웁니다.
7. Nullable clear는 raw NULL/cache absent를 함께 게시합니다.
8. Outer rollback은 DB만 복구하고 wrapper memory는 자동 rewind하지 않습니다.

Existing REL-002 oracle는 3번의 new-source/no-PK negative만 비교합니다. 1/2/4/5/6/7/8은 supplemental Go tests이며
reference oracle/runner/checksum을 바꾸지 않습니다.

## Phase A — semantics audit

- [x] Pinned Django 6.1 exact object의 `ForwardManyToOneDescriptor.__set__`,
  `ForeignKeyDeferredAttribute.__set__`, `Model._prepare_related_fields_for_save`와 regression provenance를 고정했습니다.
- [x] REL-002 oracle payload/phase/category/code/metrics/DB state를 재확인했고 oracle/checksum bytes는 그대로입니다.
- [x] No-PK, manual-PK including zero, pending+source-empty-only reconcile, key-present-later-change와 full
  `(presence,value)` tuple의 same/different scalar,
  nullable clear와 rollback memory non-rewind를 observable matrix로 분리했습니다.
- [x] Typed generated `select_related`가 Resolve/Bind original cause를 zero query로 잃는 P2를 재현했습니다. Dynamic path는
  cause를 보존하며, exact13 rewrite가 필요한 독립 remediation이므로 GDJ-0033에 섞지 않습니다.

## Phase B — no-product feasibility

Detached temp worktree에서 primary product file을 게시하지 않고 다음 seam을 증명했습니다.

### State와 write seam

- Project companion private `blogPostWriteModel{model blog.Post; primaryKeyPresent bool}`와 private descriptor가 app
  descriptor metadata/scan/clone을 재사용하고 generic `orm.Manager.Save`에 연결됩니다.
- Existing app-generated exact 13을 고치지 않습니다.
- Core seam은 `query.CodeUnsavedRelatedObject`와 `orm.mutationValueMatches`의 ForeignKey integer acceptance뿐입니다.
- SQLite compiler/schema/IR/migration/db package 변경은 필요하지 않습니다.
- Required scalar presence, relation cache state와 assignment pending은 서로 별도 state입니다.

### Exact cache/ownership semantics

- `With*`/clear는 fresh source wrapper를 만들고 original source를 바꾸지 않습니다.
- Selected edge는 새 cache cell을 얻습니다. Unrelated ready/absent snapshot은 독립 cell로 복제하고 cold/flight는
  derived wrapper에서 independent cold입니다.
- Original/derived가 mutex, evaluation, flight 또는 cache cell을 공유하지 않습니다.
- Eager result는 publication 전에 selected project cache를 hydrate합니다.
- Exact assigned target pointer 값만 local wrapper state로 보존합니다. Cross-materialization/global identity는 없습니다.

### Presence, preflight와 publication

```text
New(raw required FK 0) -> unset
New(raw required nonzero) -> present
WithAuthorID(0) -> explicit present
loaded FK 0 -> present
PK-present target key 0 -> present
no-PK assignment -> pending
nullable nil / ClearReviewer -> absent
```

- Receiver/self/context 같은 structural validation 뒤 relation preflight는 세 phase를 모두 canonical normalized
  source-model identity + relation field `Name` 순서로 수행합니다. Phase 1은 모든 relation-cache tuple을 검증·snapshot하고,
  Phase 2는 모든 assigned target origin을 검증하면서 target PK를 edge별 정확히 한 번 snapshot하며, Phase 3은 같은
  canonical 순서의 첫 no-PK target을 `unsaved_related_object`로 반환합니다.
- 세 phase가 모두 끝난 뒤에만 required-unset, candidate build, all-success publication과 Manager I/O로 진행합니다.
  앞선 Author no-PK가 뒤 Reviewer corrupt cache/self/origin을 가리거나 그 반대가 되는 adversarial case도 I/O 0/source
  unchanged로 거부합니다.
- 검증된 key snapshot으로 raw/write/object/cache candidate rebuild가 모두 성공하기 전 어떤 source state도 게시하지
  않습니다.
- Candidate rebuild 실패와 corrupt cache tuple은 source unchanged + I/O 0입니다.
- Manual-PK missing row는 REL-002가 아니라 backend insert exact +1까지 가고 DB unchanged/cause-preserved로 실패합니다.

### Phase-B validation

- [x] Focused normal, race, CGO-disabled compile/runtime suite PASS
- [x] Full `go test -count=1 ./codegen ./orm ./query` PASS
- [x] Full `go test -race -count=1 ./codegen ./orm ./query` PASS
- [x] Full `CGO_ENABLED=0 go test -count=1 ./codegen ./orm ./query` PASS
- [x] `go vet ./codegen ./orm ./query` PASS
- [x] Final frozen bytes의 Linux/386 codegen compile PASS
- [x] Full `go test -count=1 ./...`의 sole expected failure는 checked-in product facade가 no-product candidate와 다른
  deterministic drift입니다. 그 외 package는 PASS했습니다.
- [x] Independent audit P0/P1/P2/P3=`0/0/0/0`

## Phase C — Accepted decision freeze

다음 exact API를 freeze했습니다.

```go
func (q AuthorsAuthorQuery) New(authors.Author) (*AuthorsAuthor, error)
func (q BlogPostQuery) New(blog.Post) (*BlogPost, error)
func (*AuthorsAuthor) Save(context.Context) error
func (*BlogPost) Save(context.Context) error
func (*BlogPost) WithAuthor(*AuthorsAuthor) (*BlogPost, error)
func (*BlogPost) WithReviewer(*AuthorsAuthor) (*BlogPost, error)
func (*BlogPost) WithAuthorID(int64) (*BlogPost, error)
func (*BlogPost) WithReviewerID(int64) (*BlogPost, error)
func (*BlogPost) ClearReviewer() (*BlogPost, error)
```

- [x] `New`는 query root의 filters/order/limit와 무관한 pure construction입니다.
- [x] Target/source `Save`는 같은 wrapper에 성공 state를 게시합니다.
- [x] Assignment/scalar/clear는 fresh source를 반환합니다.
- [x] Required clear와 nullable pointer overload는 제공하지 않습니다.
- [x] Pending `Unwrap`은 raw zero/old scalar를 authoritative하게 노출하지 않고 REL-002로 실패합니다.
- [x] Cache-tuple, assigned-target origin/PK snapshot, 첫 no-PK 판정의 corrected three-phase preflight는 required-unset보다
  우선하고 모든 phase의 relation order는 canonical name order입니다.
- [x] Session callback-local positive만 계약이며 callback 이후 success/failure assertion은 두지 않습니다.

## bounded implementation 경로

Decision-documentation head의 hosted CI가 clean임을 EVID-074로 확인한 뒤 frozen prototype을 primary implementation의
출발점으로 사용했습니다. Exact patch/hash를 재검증했고 구현은 아래 frontmatter allowlist의 exact 23 source/product
path에 한정됩니다.

Source/product paths:

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

Conditional measured integration locks:

- `.github/workflows/ci.yml`, `conformance/README.md`
- `conformance/internal/protocol/migration_project_check_artifacts_test.go`
- `conformance/internal/protocol/relation_artifacts_test.go`
- `conformance/internal/protocol/write_migration_artifacts_test.go`
- `conformance/cmd/godjcheck/main_test.go`
- `conformance/runners/django/tests/test_relation_scenarios.py`의 manifest status assertion only

Conditional completion docs are `docs/ARCHITECTURE.md`, `docs/CAPABILITY_CATALOG.md`, `docs/CONCURRENCY.md` plus this
work의 existing documentation allowlist입니다.

명시적 제외: other app-generated exact13, `schema/**`, `db/**`, Django scenario/oracle/checksum/static-NI fixture,
reverse/delete facade, migration/codec, `go.mod`, `go.sum`, `Makefile`.

## Product implementation 완료 조건

- [x] New source/no-PK target external consumer와 REL-002 actual이 exact failure/DB unchanged/I/O0
- [x] Target Save 뒤 same pending source Save reconcile success
- [x] Manual PK including zero, missing-row DB reach, required unset과 error precedence
- [x] Same/different scalar, nullable clear, original/derived/unrelated/eager COW cache matrix
- [x] Nil/zero/copy/cross-origin/context/session/corrupt tuple/no-partial-publication adversarial gates
- [x] Corrected canonical three-phase preflight와 양방향 masking adversarial gate
- [x] Project companion deterministic generation, last-good, external no-overlay compile
- [x] Existing app-generated exact13 byte identity
- [x] REL-002 manifest만 `passing`; reference oracle/checksum/static NI bytes unchanged
- [x] Aggregate hard locks and workflow inventory measured from final bytes
- [x] Local normal/race/CGO0/vet/bounded Linux386 PASS
- [x] Exact implementation-head hosted 26-job matrix PASS
- [x] Exact 15-document completion transition과 bounded ADR/work 상태 전이
- [x] Completion-documentation exact-head hosted verification
- [x] Exact seven-document terminal evidence/status exact-head hosted verification

## 별도 추적 범위

- Q-017: Unwrap-only 장기 UX, read/write capability views, Fields/Relations/Select namespace, project generation manifest.
- Q-019: SQLite unknown-outcome retained connection의 poison/cap/recovery/observability policy.
- ROADMAP production-scale gate: PROTECT full protected-row materialization bounded memory.
- Separate P2 work: typed generated select-related resolve/bind original cause preservation.
- Separate relation-migration packet: vNext tuple/ProjectState/codec/reconstructor/SQLite FK DDL/dependency/apply-unapply/restart.

## 현재 증거와 다음 정확한 작업

- EVID-071/run `31563615648`: GDJ-0032 terminal exact head/clean activation baseline.
- EVID-072/run `31566524953`: GDJ-0033 activation head exact 26/26·326/326 success; four relation coordinates each
  697/697/0, 70,659 bytes, SHA-256
  `d017e9e848d4cf3e73b67075c0e271b7b31c1ed5a93416b1c78968d3d5904dde`.
- EVID-073: no-product Phase A/B local feasibility, patch `8329bb0...`, audit P0..P3=0.
- EVID-074/run `31574653183`: exact decision-documentation head `9d728610...`의 unique hosted 26/26·326/326 success와
  audit P0..P3=0. Decision-only proof이며 implementation proof로 재사용하지 않습니다.
- EVID-075: exact 23-path local bounded implementation, diff SHA-256 `b760d6d7...`, local `122 + 5 + 0`, relation
  12/12, corrected three-phase와 final normal/race/CGO0/vet/386 evidence.
- EVID-076/run `31586910749`: exact implementation head `be6f3d4e...`, exact 26/26·326/326 success, four relation
  coordinates each 715/715/0·72,621 bytes·SHA-256 `85575c84...e237`, audit P0..P3=0. EVID-072/074를 implementation
  proof로 재사용하지 않았습니다.
- EVID-077/run `31590911735`: EVID-076을 포함하는 exact 15-document completion head `81f4aacb...`, exact
  26/26·326/326 success, unchanged four relation coordinates each 715/715/0·72,621 bytes·SHA-256
  `85575c84...e237`, audit P0..P3=0. Implementation run을 completion proof로 재사용하지 않았습니다.
- EVID-078/run `31593500615`: EVID-077을 포함하는 exact seven-document terminal head `db5c11f6...`, exact
  26/26·326/326 success, unchanged four relation coordinates each 715/715/0·72,621 bytes·SHA-256
  `85575c84...e237`, audit P0..P3=0. Completion run을 terminal proof로 재사용하지 않았고 source, workflow와
  generated bytes는 바뀌지 않았습니다.
- Terminal evidence/status transition의 exact allowlist는 다음 seven documents뿐이었습니다.

```text
docs/ROADMAP.md
docs/TESTING.md
docs/adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md
docs/status/CURRENT.md
docs/status/TEST_EVIDENCE.md
work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md
work/README.md
```

- GDJ-0033은 exact terminal head/run까지 닫혔습니다. 그 뒤 별도
  [GDJ-0034](0034-typed-generated-select-related-cause-preservation.md)가 typed generated `select_related` cause-loss P2만
  수정했고 exact implementation head `3099bd62...`는 EVID-081/run `31605477297`의 고유 exact 26/26 jobs·326/326
  steps와 audit P0..P3=0을 통과했습니다. GDJ-0034는 completed이고 현재 active/ready work는 없지만 EVID-081을
  포함하는 completion-documentation head와 그 뒤 terminal head는 각각 own exact-head CI가 필요합니다.
  Relation-capable migration은 GDJ-0034 terminal 뒤 별도 GDJ-0035 contract-first packet으로만 열고,
  reverse/general facade와 non-SQLite backend도 섞지 않습니다. Q-013은 `Partial`, Q-017은 P1/open이며 Draft PR도
  merge하지 않습니다.
