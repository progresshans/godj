---
id: GDJ-0034
status: completed
updated: 2026-08-12
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "db5c11f6fb5b2d165e0d85538bf255f4258e47dc"
depends_on: ["GDJ-0033"]
contracts: ["REL-009", "REL-010", "REL-011", "Q-013", "Q-017"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "codegen/project_relation_select_related.go"
  - "codegen/project_relation_select_related_test.go"
  - "codegen/testdata/relation_select_related/project.golden"
  - "conformance/relationselectproduct/project/zz_godj_relation_select_related.go"
  - "conformance/relationselectproduct/product_test.go"
  - "conformance/relationdeleteproduct/project/zz_godj_relation_select_related.go"
  - "conformance/relationdeleteproduct/product_test.go"
  - "conformance/README.md"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "conformance/cmd/godjcheck/main_test.go"
  - "internal/compiletest/compile_test.go"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md"
  - "work/0034-typed-generated-select-related-cause-preservation.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Typed Generated `select_related` Cause Preservation

## 사용자에게 보이는 단일 결과

Generated typed `select_related`를 구성할 때 relation path resolve 또는 required/nullable bind가 실패하면,
terminal `All(ctx)`이 그 원래 structured error를 backend I/O 전에 그대로 반환합니다. 현재처럼 zero query로
축약한 뒤 typed terminal의 generic `backend_error/invalid_plan`으로 바꾸지 않습니다. Dynamic zero/corrupt
query의 기존 `query_error/invalid_plan` 의미는 별도로 유지합니다.

```go
posts, err := models.BlogPost.
    SelectRelated(models.BlogPost.Related.Author).
    All(ctx)
```

정상 schema/binding에서는 결과와 query 수가 달라지지 않습니다. 개선되는 것은 stale generated companion,
schema/binding 불일치 또는 잘못 조합된 project package에서의 fail-closed 진단 정확도뿐입니다.

## 기준과 활성화 상태

- Clean baseline은 GDJ-0033 terminal head
  `db5c11f6fb5b2d165e0d85538bf255f4258e47dc`입니다.
- [EVID-078](../docs/status/TEST_EVIDENCE.md#evid-20260812-078--gdj-0033-terminal-exact-head-ci-and-clean-baseline) /
  [run 31593500615](https://github.com/progresshans/godj/actions/runs/31593500615)은 그 exact head를 별도
  26/26 jobs·326/326 steps와 audit P0/P1/P2/P3=`0/0/0/0`으로 검증했습니다.
- EVID-078은 GDJ-0033 terminal closure와 이 work의 clean baseline만 증명합니다. Exact activation head
  `e2e0a4e3750e0f38f8bbe06ddbf9e1f8b607a9ef`은
  [EVID-079](../docs/status/TEST_EVIDENCE.md#evid-20260812-079--gdj-0034-activation-documentation-head-exact-26-job-ci) /
  [run 31599273044](https://github.com/progresshans/godj/actions/runs/31599273044)의 고유
  26/26 jobs·326/326 steps와 independent audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다.
- Exact 12-path source/product/workflow implementation은
  [EVID-080](../docs/status/TEST_EVIDENCE.md#evid-20260812-080--gdj-0034-typed-generated-select_related-cause-preservation-pre-hosted-local-validation)의
  local normal/race/CGO-disabled/vet/Linux386, final inventory와 independent audit를 통과했습니다. Exact implementation
  head `3099bd62d6936eb35edf31ebfa62329ed0eca718`은
  [EVID-081](../docs/status/TEST_EVIDENCE.md#evid-20260812-081--gdj-0034-github-hosted-typed-generated-select_related-cause-preservation-implementation-head-exact-26-job-ci) /
  [run 31605477297](https://github.com/progresshans/godj/actions/runs/31605477297)의 고유 26/26 jobs·326/326 steps와
  independent audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. Code는 Implemented이고 그 명시된 hosted 환경에서
  Verified입니다.
- 제품 분류는 exact 12 adapters/127 contracts=`122 passing + 5 deviation + 0 oracle_locked`, relation 12/12로
  그대로입니다. REL-009/010/011 상태나 locked Django oracle/checksum을 바꾸지 않습니다.
- 이 work는 [ADR-0029](../docs/adr/0029-one-hop-forward-select-related.md)의 이미 Accepted된 bounded eager 경계에서
  발견된 좁은 오류 보존 결함을 고칩니다. 새 장기 결정, 새 Open Question 또는 새 ADR을 만들지 않습니다.

## 고정 불변 조건

1. Generated typed edge query는 private `configurationErr error`를 소유합니다.
2. Typed `Author()`/`Reviewer()` builder는 `orm.ResolveForwardSelectPath` 또는
   `orm.BindRequiredForwardSelect`/`orm.BindNullableForwardSelect`의 오류를 버리지 않고 저장합니다.
3. Typed terminal `All(ctx)`은 ADR-0029의 기존 nil/typed-nil context와 `ctx.Err()` 우선순위를 먼저 지킨 뒤,
   backend validation 또는 query/mutation I/O 전에 저장된 오류를 반환합니다. 오류의 category, code, detail과
   unwrap/cause chain을 generic invalid-plan으로 대체하지 않습니다.
4. 정상 typed query의 SQL, result order, eager cache publication과 query-count 의미는 byte 외 동작상 동일합니다.
5. `ParseDynamic`은 이미 원래 resolve/bind 오류를 직접 반환하므로 동작을 바꾸지 않습니다.
6. 실제 zero/corrupt typed 또는 dynamic query에 대한 기존 generic invalid-plan 의미는 유지합니다.
7. Production project facade는 low-level typed terminal을 통과해 같은 original cause를 반환합니다. Facade 공개
   API, selector namespace, wrapper와 backend capability는 바꾸지 않습니다.
8. Generated source 의미가 바뀌므로 `ProjectRelationSelectRelatedGeneratorVersion`과 generated provenance constant는
   v1에서 v2로 올립니다. Golden과 두 checked-in product companion은 같은 generator bytes로 결정적으로 갱신합니다.
9. Candidate compile/gofmt/determinism/last-good 보존, generated inventory/digest와 CI test roster는 final bytes에서
   다시 측정합니다. 숫자를 예상값으로 맞추지 않습니다.

## 실행 단계

### A. 실패 재현과 source boundary 고정

- [x] Typed required resolve failure가 generic invalid-plan으로 축약되는 현재 경로를 focused test로 재현했습니다.
- [x] Typed required 및 nullable bind failure를 각각 재현하고 terminal 전 backend I/O가 0임을 잠갔습니다.
- [x] Stored resolve/bind error와 nil context, typed-nil context, cancelled/deadline context를 각각 조합해 기존 context
  precedence 뒤 configuration cause가 반환되는 exact 순서를 잠갔습니다.
- [x] 같은 입력의 dynamic path가 이미 exact cause를 보존한다는 control을 유지했습니다.
- [x] Zero/corrupt query와 configuration failure를 서로 다른 test case로 고정했습니다.

### B. 최소 generated remediation

- [x] Typed generated query에 private configuration error를 추가하고 resolve/bind 두 실패를 저장했습니다.
- [x] `All(ctx)`이 기존 context precedence 뒤, backend validation/I/O 전에 stored error를 반환하도록 했습니다.
- [x] Generator version을 v2로 올리고 golden, `relationselectproduct`, `relationdeleteproduct` companion을
  결정적으로 다시 생성했습니다.
- [x] Facade generator/source 변경 없이 exact cause가 public eager terminal까지 도달함을 same-package facade와
  external stale-companion public proof로 검증했습니다.

### C. 검증과 완료 전환

- [x] Focused generator/golden/product tests와 exact error/I/O assertions PASS
- [x] Generated deterministic/no-rewrite/last-good 및 physical no-overlay compile PASS
- [x] Normal, race, CGO-disabled, vet와 bounded Linux/386 gate PASS
- [x] Full `go test ./...`와 final measured workflow/artifact inventory PASS
- [x] Exact implementation head의 고유 hosted 26-job/326-step CI와 independent P0..P3 audit PASS
- [x] Exact 13-document completion transition과 integrated current-document 상태 전이
- [x] Completion-documentation exact-head hosted verification
- [x] Exact six-document terminal evidence/status exact-head hosted verification

## 명시적 비목표

- REL-009/010/011의 SQL, projection, cache 또는 public selector API 확장
- QuerySet/eager/prefetch evaluation cache 통합
- Q-017의 reader/writer capability, raw model/wrapper, Fields/Relations/Select namespace 또는 project generation manifest
- Reverse manager, multi-edge/nested eager, custom Prefetch/filter/order
- Q-019 retained-connection poison/cap/recovery policy
- PROTECT protected-row bounded-memory/payload 변경
- Relation-capable migration tuple, ProjectState, codec, SQLite ForeignKey DDL, dependency, apply/unapply 또는 restart
- PostgreSQL/MySQL/Windows와 broader non-SQLite 지원
- Django oracle, relation manifest status, static fixture 또는 checksum 변경
- Draft PR merge

## 증거 비재사용과 다음 packet

- EVID-078/run `31593500615`은 baseline 전용이고 EVID-079/run `31599273044`는 activation 전용입니다. 둘 다
  implementation, completion 또는 terminal proof로 재사용하지 않습니다. EVID-080은 exact 12-path local source
  freeze 전용이고 EVID-081/run `31605477297`은 exact 19-path implementation head `3099bd62...` 전용입니다.
- EVID-082/run `31609500811`: EVID-081을 포함하는 exact 13-document completion head `45cfccd...`, exact
  26/26 jobs·326/326 steps success, unchanged four relation coordinates each 715/715/0·72,623 bytes·SHA-256
  `127fb3d8...3a17`, audit P0..P3=0. Implementation run을 completion proof로 재사용하지 않았습니다.
- Terminal evidence/status transition의 exact allowlist는 다음 six documents뿐입니다. Source, workflow, generated
  output, manifest, oracle, fixture와 checksum은 바꾸지 않습니다.

```text
docs/ROADMAP.md
docs/TESTING.md
docs/status/CURRENT.md
docs/status/TEST_EVIDENCE.md
work/0034-typed-generated-select-related-cause-preservation.md
work/README.md
```

- Exact six-document terminal evidence/status head는 EVID-083/run `31613170021`의 고유 exact-head hosted CI와
  terminal strict audit를 통과했습니다. EVID-082를 terminal proof로 재사용하지 않았습니다.
- 다음 단일 active packet은 별도 contract-first `GDJ-0035` relation-capable migration입니다. Proposed
  ADR-0034의 tuple/state/DDL candidate는 Phase A/B/C 전에 Accepted로 표현하지 않습니다.
- Q-013은 `Partial`, Q-017과 Q-019는 P1/open을 유지합니다. Draft PR #1은 open/draft/unmerged 상태를 유지합니다.

## Terminal closure and handoff

- Exact six-document terminal head `0bb8c969d0658f50f40d916996f027e7393bce14`, tree
  `341deb1da8d864f21252a6e3846745af36c1551e`는
  [EVID-083](../docs/status/TEST_EVIDENCE.md#evid-20260812-083--gdj-0034-terminal-exact-head-ci-and-clean-baseline) /
  [run 31613170021](https://github.com/progresshans/godj/actions/runs/31613170021)의 고유 26/26 jobs·326/326 steps
  success와 independent audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다.
- EVID-082 completion run을 terminal proof로 재사용하지 않았고, EVID-083은 이 work의 terminal clean
  baseline만 증명합니다. Later activation/decision/implementation tree를 재귀적으로 증명하지 않습니다.
- 이 work는 terminally closed입니다. 다음 단일 active packet은 별도 contract-first
  [GDJ-0035](0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md)이며, Proposed
  [ADR-0034](../docs/adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)의
  candidate를 activation했습니다. GDJ-0035 activation tree own CI는 `not run/pending`입니다.
