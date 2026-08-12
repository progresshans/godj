---
id: GDJ-0034
status: active
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
- EVID-078은 GDJ-0033 terminal closure와 이 work의 clean baseline만 증명합니다. EVID-078 append 및 이 activation
  문서 tree는 `not run/pending`이며 자체 고유 exact-head hosted CI가 필요합니다.
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

- [ ] Typed required resolve failure가 generic invalid-plan으로 축약되는 현재 경로를 focused test로 재현합니다.
- [ ] Typed required 및 nullable bind failure를 각각 재현하고 terminal 전 backend I/O가 0임을 잠급니다.
- [ ] Stored resolve/bind error와 nil context, typed-nil context, cancelled context를 각각 조합해 기존 context
  precedence 뒤 configuration cause가 반환되는 exact 순서를 잠급니다.
- [ ] 같은 입력의 dynamic path가 이미 exact cause를 보존한다는 control을 유지합니다.
- [ ] Zero/corrupt query와 configuration failure를 서로 다른 test case로 고정합니다.

### B. 최소 generated remediation

- [ ] Typed generated query에 private configuration error를 추가하고 resolve/bind 두 실패를 저장합니다.
- [ ] `All(ctx)`이 기존 context precedence 뒤, backend validation/I/O 전에 stored error를 반환하도록 합니다.
- [ ] Generator version을 v2로 올리고 golden, `relationselectproduct`, `relationdeleteproduct` companion을
  결정적으로 다시 생성합니다.
- [ ] Facade generator/source 변경 없이 exact cause가 public eager terminal까지 도달하는지 먼저 검증합니다.
  불가능하다는 focused proof가 생기면 scope를 재문서화하기 전에는 facade 경계를 수정하지 않습니다.

### C. 검증과 완료 전환

- [ ] Focused generator/golden/product tests와 exact error/I/O assertions PASS
- [ ] Generated deterministic/no-rewrite/last-good 및 physical no-overlay compile PASS
- [ ] Normal, race, CGO-disabled, vet와 bounded Linux/386 gate PASS
- [ ] Full `go test ./...`와 final measured workflow/artifact inventory PASS
- [ ] Exact implementation head의 고유 hosted 26-job/326-step CI와 independent P0..P3 audit PASS
- [ ] Completion documentation과 terminal baseline을 각각 별도 exact-head CI로 검증

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

- EVID-078/run `31593500615`은 baseline 전용입니다. Activation, implementation, completion 또는 terminal proof로
  재사용하지 않습니다.
- 이 exact activation documentation tree는 자체 hosted CI 전까지 `not run/pending`입니다. 이후 source implementation,
  completion documentation과 terminal evidence tree도 각각 고유 exact-head run을 사용합니다.
- 이 work가 completed이고 terminal clean baseline까지 닫히기 전에는 다음 work를 active/ready로 만들지 않습니다.
- 그다음 후보는 별도 contract-first `GDJ-0035` relation-capable migration packet뿐입니다. 이 문서가 그 migration의
  tuple, state, DDL 또는 ADR을 미리 Accepted하지 않습니다.
- Q-013은 `Partial`, Q-017과 Q-019는 P1/open을 유지합니다. Draft PR #1은 open/draft/unmerged 상태를 유지합니다.
