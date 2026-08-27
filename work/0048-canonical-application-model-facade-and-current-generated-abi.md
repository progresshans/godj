---
id: GDJ-0048
status: active
updated: 2026-08-27
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "38829025670581e2c759d8e2cf191e6049b7c1e0"
depends_on: ["GDJ-0033", "GDJ-0036", "GDJ-0037", "GDJ-0047"]
contracts: ["GEN-M1-001", "REL-002", "Q-013", "Q-017"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "cmd/godj/**"
  - "codegen/**"
  - "internal/compiletest/**"
  - "internal/projectcheck/**"
  - "internal/projectgenerate/**"
  - "internal/projectspec/**"
  - "examples/article/**"
  - "conformance/postgresproduct/**"
  - "conformance/README.md"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/system_state_artifacts_test.go"
  - "conformance/relationdeleteproduct/**"
  - "conformance/runserverproduct/**"
  - "conformance/systemstate/attestations/**"
  - "db/postgres/coordinated_transaction_test.go"
  - "docs/adr/0050-canonical-embedded-application-model-facade.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
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
  - "work/0048-canonical-application-model-facade-and-current-generated-abi.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0048 — Canonical Application Model Facade and Current Generated ABI

## 사용자에게 보이는 결과

Generated project model 하나에서 scalar field, app-owned method, query, relation accessor와 write lifecycle을 함께 사용합니다.

```go
models, err := project.Using(backend)
if err != nil {
    // handle binding error
}
post, found, err := models.BlogPost.Filter(blog.PostFields.Title.Exact("draft")).First(ctx)
if err != nil || !found {
    // handle result
}

post.Title = "current title"
post.NormalizeTitle() // handwritten app-owned method

author, err := post.Author(ctx)
post, err = post.WithReviewerID(7)
err = post.Save(ctx)

raw, err := post.Unwrap() // detached deep-cloned raw-model escape hatch
```

현재 private `model` field와 `Unwrap`만으로 scalar/user method를 우회하게 하는 Gate 0 UX를 application-facing current ABI로
넓히되, existing relation state/cache, explicit I/O, pointer-only operational safety와 recoverable whole-project publication을
약화하지 않습니다.

## 목표

- App raw model을 private alias로 project wrapper에 embed해 exported scalar field와 app-owned method를 promotion합니다.
- 기존 canonical `Save`, `With*`, `Clear*`, forward accessor와 `Unwrap` signature를 유지합니다.
- Source wrapper의 직접 scalar/FK mutation과 그 edge의 project-owned object/cache/pending state를 관련 operational
  boundary 전에 일관되게 reconcile합니다.
- Required zero의 unset/explicit-zero 구분과 nullable `(present, value)` 의미를 보존합니다.
- Generated framework method와 promoted field/handwritten method 충돌을 target mutation 전에 fail-closed합니다.
- Wrapper direct JSON marshal/unmarshal을 fail-closed합니다. `Unwrap`은 DTO 구축용 raw typed/deep-clone escape이고
  Web JSON/template의 supported representation authority는 app-owned DTO만 유지합니다.
- Facade renderer를 current v3로 올리고 기존 13-role roster 전체를 한 snapshot/manifest로 재생성·검증합니다.
- Article과 strong relation fixture에서 direct field/user method/query/relation/write/restart 흐름을 SQLite/PostgreSQL로 검증합니다.

## 이번 packet에서 결정하는 경계

- Canonical application type은 app alias + model 이름의 project-owned wrapper입니다. Raw app type은 embedded private alias와
  `Unwrap` result type으로만 명시적으로 노출됩니다.
- Embedding은 scalar field와 app-owned ordinary method를 promotion하지만 relation state를 raw model에 넣거나 global sidecar
  map을 만들지 않습니다.
- Framework-owned `Save`, `With*`, `Clear*`, relation accessor, `Unwrap` 이름은 reserved namespace입니다. Schema-derived field와
  non-generated production Go source의 raw-model exported method가 이 namespace와 충돌하면 generation/publication 전에 오류입니다.
- Whole-candidate compile alone은 outer method가 embedded method를 조용히 가리는 경우를 잡지 못하므로 bounded Go AST namespace
  audit을 `generate --check`와 write candidate verification의 일부로 둡니다. Pure generator는 schema-known collision,
  sealed-root audit은 raw receiver promotion, whole-candidate compile은 outer receiver/package symbol collision을 소유합니다. 자동
  rename/mangling이나 compatibility shim은 만들지 않습니다.
- Direct FK scalar mutation은 source wrapper의 selected edge만 cold로 만들고 object/source snapshot을 current scalar에서 다시 구성합니다.
  Relation accessor, `With*`/`Clear*`, `Unwrap`과 `Save`가 모두 이 reconciliation을 먼저 수행합니다. Unchanged edge cache는
  유지하며 pending target 뒤 scalar mutation은 scalar가 이깁니다.
- Required new raw zero는 계속 unset입니다. `WithAuthorID(0)`와 loaded zero만 explicit presence를 가지므로 value-only heuristic으로
  presence를 재정의하지 않습니다.
- Promoted primary key는 ordinary editable scalar가 아닙니다. Wrapper construction/save-success 시 `(present, value)` snapshot을
  보존하고 이후 direct PK change는 모든 stateful boundary에서 `model_state_error/primary_key_update_field`로 I/O 전에 거부합니다.
  Manual PK는 raw `New<Model>WithID`를 wrapper `New` 전에 사용합니다.
- Nil/zero/shallow-copied wrapper는 `Save`, `With*`, relation accessor와 `Unwrap`에서 기존처럼 pre-I/O fail-closed합니다.
  Ordinary promoted field/method access 자체는 Go value semantics이며 framework가 concurrency safety를 합성하지 않습니다.
- `encoding/json`이 embedded raw fields 또는 raw `MarshalJSON`을 암묵적으로 게시하지 못하도록 wrapper direct marshal/unmarshal을
  명시적으로 거부합니다. Non-nil wrapper value/pointer marshal과 pointer unmarshal은 fixed error이고 unmarshal receiver는
  unchanged입니다. `encoding/json`의 nil-pointer special case가 `null`을 내는 것은 no-I/O harmless exception입니다. `Unwrap`은
  raw typed/deep-clone escape로 보존하고 Web JSON/template는 그 clone에서 구성한 app DTO만 authority입니다. Go
  `html/template`가 exported promoted field 접근을 type-level로 차단한다고 주장하지 않으며 wrapper 직접 전달은 unsupported/
  noncontractual이고 ADR-0038 Web path는 DTO-only입니다.
- Current roster는 app 4 + project 8 + bundle/seal 1 role을 유지합니다. Facade v3 변화는 snapshot preimage를 바꾸므로 generated
  source와 manifest는 exact whole bundle로 함께 갱신됩니다.

## 비목표

- Schema IR, Field/Relation 종류, Migration Definition/State, migration writer/autodetector와 backend compiler 변경
- Reverse assignment/general relation manager, recursive/CASCADE delete, identity map과 application-global cache invalidation
- Arbitrary reflection serializer, supported wrapper template/JSON representation, DTO 제거
- Installed-version negotiation, renderer rename/deprecation, first-alpha 이후 semver/upgrader policy
- JWT/opaque token issuance, OpenAPI, Realtime, GIS, multi-DB router와 production deployment
- Existing low-level relation/query API 제거 또는 public package 대규모 재배치
- 새 Django/DRF conformance manifest/adapter 또는 current 22/21 set·249/237 contract aggregate 변경
- Final source-fingerprint 확인 뒤 non-generated app source를 동시에 바꾸는 비협조적 editor/writer; caller가 serialize해야 함

## Acceptance invariants

이 packet은 기존 internal codegen capability `GEN-M1-001`, preserved relation behavior `REL-002`와 Q-013/Q-017을 좁혀
검증합니다. 새 Django/DRF conformance set이나 global reference/product aggregate를 만들지 않습니다. Exact invariant는 이름 있는
Go compile/runtime/publication tests와 work evidence에서 관리합니다.

- Private raw-model alias embedding과 exported non-PK scalar promotion
- App-owned value/pointer receiver method promotion
- Direct non-PK scalar mutation → Save/reload와 PK snapshot/direct-mutation rejection
- Direct required/nullable FK mutation의 object/cache/pending reconciliation
- Lazy/eager/`With*`/`Clear*`의 canonical wrapper와 per-edge COW state
- `Unwrap` deep clone과 nil/zero/shallow-copy receiver 경계
- Wrapper direct JSON marshal/unmarshal fail-closed와 DTO-only supported Web representation
- Schema/handwritten generated namespace audit, check target mutation 0와 mandatory recovery 뒤 write의 새 candidate target mutation 0
- Deterministic facade renderer current-v3와 existing 13-role whole bundle
- Stale v2 detection, v3 recoverable publication과 exact old-or-new recovery

## 기준 상태와 CI #156 관찰

- GDJ-0047 terminal behavioral source `14e47c9b...`는 EVID-138/CI #155에서 exact 27/27 jobs·360/360 steps를 통과했습니다.
- Terminal docs descendant `31fee59...`의 CI #156/run `33053749701`은 26 success/1 failure였습니다. 유일한 실패는
  `TestCoordinatedAtomicRealMutationErrorRollsBackBeforeOtherBackendAcquires`가 `mutationRan`과 expected `callbackErr`
  result를 동시에 ready로 만든 test-only select race였습니다. 실제 rollback/product failure 증거가 아닙니다.
- Baseline commit `3882902...`는 callback이 main barrier observation 전 반환하지 않도록 handshake를 추가했습니다. Focused normal
  count=2000, race count=1000, CGO0 count=500, full `./db/sqlite` normal/race/CGO0, vet와 current source-binding test가
  통과했습니다. Activation head `1070ec3...`의 CI #157/run `33063990270`은 exact 27/27 jobs·360/360 steps,
  failure/cancel/skip/annotation 0으로 corrected descendant를 확인했습니다.
- Worktree는 activation 시작 시 clean이고 Draft PR #1은 OPEN/DRAFT/unmerged입니다.

## 설계 및 구현 단계

### Phase A — current ABI decision and compile boundary

- [x] embedding/explicit unwrap/sidecar를 repository-external compile prototype으로 비교
- [x] raw `Save`가 outer generated `Save`에 조용히 가려지는 Go method-promotion negative를 확인
- [x] Proposed ADR-0050의 핵심 namespace/copy/JSON/reconciliation decision을 activation head `1070ec3...`에 고정하고,
  구현 중 확인한 bounded source budget과 mandatory-recovery ordering clarification을 current checkpoint에 반영
- [x] positive/negative external compile fixture와 target-write-zero namespace audit을 먼저 추가

### Phase B — facade renderer v3

- [x] private alias embedding과 promoted scalar/user method surface를 생성
- [x] raw clone `Unwrap`, pointer/self validation과 existing `Save`/`With*` signatures를 보존
- [x] non-nil wrapper value/pointer JSON marshal과 pointer unmarshal을 deterministic pre-I/O error로 닫고 nil-pointer `null`
  special case/no-mutation을 exact 검증
- [x] Schema-derived 및 handwritten production method namespace collision을 generate/check/publication 전 거부

### Phase C — relation state reconciliation

- [x] direct required/nullable FK mutation을 canonical snapshot → full validation/rebuild → all-success publication 순서로
  accessor/With*/Clear*/Unwrap/Save 전에 edge별 reconcile; failure state unchanged/I/O 0
- [x] promoted PK direct mutation의 pre-I/O rejection과 manual-PK-before-New path를 검증
- [x] warm present/absent cache invalidation, pending-target scalar override와 unchanged-edge preservation 검증
- [x] explicit-zero/presence, per-edge COW, eager/lazy same-wrapper와 copy failure 회귀 검증

### Phase D — whole-bundle product adoption

- [x] facade ABI v3와 existing 13-role snapshot preimage를 갱신
- [x] Article exact 12와 relation-delete exact 16 source/manifest를 current bundle로 재생성
- [x] current-v3 generated drift, ordinary failure old exact와 crash recovery old-or-new exact 검증
- [x] checked-in v2 → v3 stale detection과 mixed-generation compile-success 0 검증
- [x] Article user method와 relation fixture namespace collision/rollback fixture를 실제 package에서 검증

### Phase E — vertical integration and publication

- [x] SQLite/PostgreSQL에서 query → direct scalar/user method → relation access/assignment → Save → fresh reload 및 별도 OS process
  reopen 흐름 검증
- [x] affected normal/race/CGO0/vet, external compile와 generate/check 통과
- [x] clean source checkpoint에서 독립 2회 source-bound PostgreSQL canary/attestation 통과
- [x] frozen source에서 full `make ci`, Linux/386, repository-external clean archive와 independent audit 한 번 수행
- [ ] exact submitted head hosted matrix를 통과한 뒤 ADR/상태/증거를 terminal bytes에 맞게 갱신

## 완료 조건

- Direct scalar와 app-owned methods가 canonical wrapper에서 compile/runtime 동작하며 기존 relation/write API와 공존합니다.
- Direct FK mutation 뒤 stale relation object/cache가 관찰되거나 잘못 저장되지 않습니다.
- Generated/promoted namespace collision은 `generate --check`에서 target mutation 0, write에서는 mandatory interrupted-publication
  recovery 뒤 새 candidate target mutation/application DB I/O 0으로 deterministic하게 실패합니다. Handwritten method change만으로
  manifest bytes가 같아도 false-clean이 되지 않습니다.
- Wrapper direct JSON serialization은 implicit I/O/field leakage 없이 거부되고 `Unwrap` raw clone/DTO-only Web path는 기존
  의미를 보존합니다. Generic template access를 type-level로 거부한다고 주장하지 않습니다.
- Article/relation bundles가 deterministic current ABI v3 exact set으로 게시되고 check/recovery gate를 통과합니다.
- 완료 문서는 실제 실행 명령, checkout/tree/artifact identity, 실행하지 못한 gate와 남은 Q-017 범위를 정확히 기록합니다.

## 실행 증거 — pre-freeze affected checkpoint

2026-08-27 activation head `1070ec323f0d91b3ade54e1ec0cc6ac9e6d96175`의 descendant working snapshot에서 다음
affected gate가 모두 exit 0으로 통과했습니다. 이 실행 뒤 source product/generated/test bytes는 `e0d4b94...`에 고정했고
정식 source/attestation identity는 아래와 EVID-139에 별도로 기록했습니다.

```bash
go test -count=1 ./codegen ./internal/projectgenerate ./internal/compiletest ./examples/article/... \
  ./conformance/relationdeleteproduct ./conformance/postgresproduct ./conformance/internal/protocol
go test -race -count=1 ./codegen ./internal/projectgenerate ./internal/compiletest ./examples/article/... \
  ./conformance/relationdeleteproduct ./conformance/postgresproduct ./conformance/internal/protocol
CGO_ENABLED=0 go test -count=1 ./codegen ./internal/projectgenerate ./internal/compiletest ./examples/article/... \
  ./conformance/relationdeleteproduct ./conformance/postgresproduct ./conformance/internal/protocol
go vet ./codegen ./internal/projectgenerate ./internal/compiletest ./examples/article/... \
  ./conformance/relationdeleteproduct ./conformance/postgresproduct ./conformance/internal/protocol
make generate-check
go test -count=100 ./examples/article/webapp
git diff --check
```

Digest-pinned `golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`
Linux/amd64와 `postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`에서
다음 focused command도 exit 0으로 통과해 별도 OS process reopen marker를 확인했습니다.

```bash
GODJ_TEST_POSTGRES_URL=<redacted> \
  go test -count=1 -run '^TestGeneratedRelationPostgresE2E$' ./conformance/postgresproduct
```

Dirty shared source에서 만든
두 preliminary attestation capture는 byte-identical이지만 정식 source-bound proof로 사용하지 않았고, clean checkpoint의 서로
독립된 PostgreSQL instance에서 아래 정식 proof로 다시 수집해 대체했습니다.

Clean behavioral source `e0d4b941927d3661a7907f46b50a569b736dc1f1`, tree
`e09c8d4f043656665d3a817e521b7732eac978d1`에서 서로 다른 PostgreSQL container/network와 capture path를 사용한 두 정식
capture는 각각 1,134 bytes/SHA-256 `5b2055445b787acdd018771a4f8c1395b19e96ae7ce3efce8d9efe85b02c004e`로
byte-identical했습니다. Source binding은 257 files/2,942,402 bytes/SHA-256
`07290ae1efd74782a4cc97ab50f4688933bd896c2031bfa1f7523f24a97f1f29`입니다. 두 instance 모두 SYS-020
two-process와 relation separate-process sentinel pass/skip 0을 확인했고 첫 instance에서 Article Bearer normal/race/CGO0도
통과했습니다. Evidence publication commit은 `ab80217bedcc486d302652a8aae1ce9e6b492ed0`, tree
`0d83ead4c2c4ad91bd1edd8db7b7a7f39b64e40f`이며 [EVID-139](../docs/status/TEST_EVIDENCE.md#evid-20260827-139--gdj-0048-canonical-facade-source-checkpoint-and-postgresql-attestation)에
명령·제외된 시도와 checksum lock을 기록합니다.

## 실행 증거 — frozen local final

Test-only correction commit `b2f6bc50ea25cd433f75c156abb36b9f3e8054a4`, tree
`5b6eceb7f90a05b31cc69990d46c069278c2be28`에서 required PostgreSQL 17.10 lane, full `make ci`, 107-package
Linux/386 compile-only, 1,088-file repository-external archive와 independent audit가 모두 통과했습니다.
[EVID-140](../docs/status/TEST_EVIDENCE.md#evid-20260827-140--gdj-0048-frozen-local-final-gates-and-postgresql-test-correction)은
정확한 명령·환경·checksum과 excluded orchestration attempts를 기록합니다. `b2f6bc5...`는 ordinary PostgreSQL `_test.go`와
work scope만 바꾼 test-only descendant이므로 source binding 257 files/2,942,402 bytes/SHA-256
`07290ae1efd74782a4cc97ab50f4688933bd896c2031bfa1f7523f24a97f1f29` 및 checked attestation은 unchanged/current입니다.

Required PostgreSQL lane은 exact 18/18 named pass·skip 0, normal/race/CGO0, checked capture byte comparison,
actual service restart `prepare→probe→resume→verify→cleanup`, vet와 credential-clean retained logs를 통과했습니다. Full local
matrix와 외부 archive의 Article/relation bundle은 각각 exact 12/SHA-256 `f0043e499...`와 16/SHA-256 `81534d390...`로
clean했고, archive 전후 exact path/mode/blob roster는 1,088 entries/SHA-256 `f3a502811...160ab`로 같았습니다.
Independent implementation/source/status audit의 blocker는 0이며 발견된 stale Gate-0/future-tense 문서만 이
documentation descendant에서 current-v3/local-final 상태로 교정합니다. Exact submitted-head hosted matrix 전에는 ADR-0050을
Accepted로 올리거나 GDJ-0048을 completed로 닫지 않습니다.

## 인수인계

- 현재 정확한 다음 작업: local-final documentation descendant를 non-force push하고 Draft PR #1을 갱신한 뒤 exact submitted-head
  hosted matrix 하나를 통과합니다. 성공한 exact head/run을 별도 terminal evidence에 기록한 뒤에만 ADR-0050/GDJ-0048 상태를 닫습니다.
- 같은 공개 facade generator, ProjectSpec ABI와 CURRENT 문서는 통합 담당 한 명만 수정합니다.
- Corrected descendant CI가 실행 중이면 로컬 구현은 계속하되 완료 전 추가 push로 run을 취소하지 않습니다.
