---
id: GDJ-0016
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "594bd9c68b609ea8c6dfb0a3a5dcf9466a336972"
depends_on: ["GDJ-0015"]
contracts: ["MIG-037..MIG-046", "Q-012"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "conformance/contracts/migration-state-reconstruction-manifest.json", "conformance/runners/godj/migration_state_reconstruction_scenarios.go", "conformance/runners/godj/runner.go", "conformance/runners/godj/runner_test.go", "conformance/cmd/godjcheck/main_test.go", "conformance/internal/protocol/migration_state_reconstruction_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "migrations/reconstructor.go", "migrations/reconstructor_test.go", "migrations/planner_graph.go", "migrations/planner_test.go", "migrations/execution.go", "migrations/external_test.go", "internal/compiletest/compile_test.go", "internal/compiletest/testdata/migration_external_consumer.go.txt", "conformance/README.md", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Historical ProjectState Reconstruction Product Slice

## 사용자에게 보이는 결과

Loaded migration definition만으로 explicit empty, latest, migration 전·후와 durable applied
history 시점의 historical `ProjectState`를 Go에서 재구성합니다. Live database schema나 현재
generated model을 과거 state로 오인하지 않고, MIG-037..046 exact reference 10개를 실제
GoDj public API 결과로 검증합니다.

## 목표

- Full migration definition을 deep-copy하는 immutable `StateReconstructor` 제품 경계
- Explicit empty/latest/before/after/applied 의미를 nil·empty variadic과 분리한 tagged request
- 기존 `plannerGraph` validation/order kernel 재사용과 graph 알고리즘 중복 제거
- Dependency-ordered pure `Operation.stateForward` replay와 cloned `ProjectState` 반환
- Cross-app dependency, multiple target/shared dependency와 latest leaf union 구현
- Known applied history replay, unrelated applied branch 포함과 unknown legacy key 무시
- Invalid request/target, graph/history와 state replay error의 구조화된 fail-closed 경계
- Definition/request/result alias 방지와 repeated/concurrent reconstruction 검증
- MIG-037..046 live GoDj adapter 10개와 locked Django oracle 0-diff
- 기존 `73 passing + 4 deviation`을 `83 passing + 4 deviation`으로 전환하되 DEV-0001 보존

## 비목표

- Migration source/file encoding, Go module/directory discovery와 public loader ABI
- Public CLI, project binary, `showmigrations`/`migrate` command orchestration
- Recorder read와 reconstruct/plan/execute를 하나의 public lifecycle API로 합치기
- Read/reconstruct/execute 사이 revision token, process lock, TOCTOU 해결과 crash recovery
- Live schema introspection, drift repair와 schema/recorder reconciliation
- Data migration callback/plugin ABI와 historical apps registry/model mutation API
- Replacement/squash/merge/fake/fake-initial, optimizer와 conflict resolution
- Unmigrated app/`real_apps`, relation rendering, PostgreSQL 또는 multi-DB router
- `migrations/backend/**`, `db/sqlite/**`, Schema IR와 codegen 변경
- Locked Django oracle/static fixture 또는 기존 일곱 artifact meaning 변경
- Result cache; pure replay 비용을 먼저 측정하지 않고 cache ownership을 도입하지 않음

## 선행 조건과 기준 상태

- Baseline machine commit:
  `main@594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`
- Product baseline:
  `main@a9ce9597551840f1be8e1f27006d427842f38081`
- [GDJ-0015](0015-historical-project-state-reconstruction-compatibility-contracts.md)는
  MIG-037..046을 10 `oracle_locked`, Django 10 `observed`, static 10
  `not_implemented`로 고정했습니다.
- Reference는 8 set/87개이고 제품 분류는 `73 passing + 4 deviation + 10
  oracle_locked`입니다. 새 10개를 구현 전 passing으로 세지 않습니다.
- [ADR-0016](../docs/adr/0016-historical-project-state-reconstruction.md)은 loaded
  definition replay와 identity-only Planner 분리를 Proposed로 기록합니다. API spike와
  mutation audit 뒤 Accepted 여부를 같은 작업에서 결정합니다.

## 공개 API 가설

첫 implementation 전에 external-package compile spike로 다음 ownership을 검증합니다.
이름은 ADR Accepted 전까지 확정 API가 아닙니다.

```go
type StateRequest struct { /* unexported tagged value */ }

func EmptyStateRequest() StateRequest
func LatestStateRequest() StateRequest
func BeforeStateRequest(first MigrationKey, rest ...MigrationKey) StateRequest
func AfterStateRequest(first MigrationKey, rest ...MigrationKey) StateRequest
func AppliedStateRequest(AppliedState) StateRequest

type StateReconstructor struct { /* immutable graph + copied definitions */ }
func NewStateReconstructor(...Migration) (StateReconstructor, error)
func (StateReconstructor) Reconstruct(StateRequest) (ProjectState, error)
```

- Zero `StateRequest`는 invalid입니다. Explicit empty와 omitted/latest를 zero argument나
  nil/empty slice로 추론하지 않습니다.
- Zero `StateReconstructor`는 empty graph constructor와 같은 immutable value 후보입니다.
- Before/after request는 최소 한 target을 생성자에서 요구하고 caller order를 복사합니다.
- Applied request는 opaque `AppliedState` snapshot을 복사하고 unknown key는 보존하되 replay에서
  제외합니다.
- Returned `ProjectState`는 매 호출 fresh clone이며 caller mutation이 reconstructor나 다음
  결과를 바꾸지 않습니다.

## 알고리즘과 오류 경계

1. `NewStateReconstructor`는 기존 `newPlannerGraph`로 key/dependency를 검증하고, built-in
   `CreateModel`/`AddField` operation과 nested IR을 deep-copy합니다.
2. Request representation을 먼저 검증합니다. Named target 존재와 applied known-history
   consistency를 replay 전에 확인합니다.
3. Explicit empty는 empty state, latest는 Django `leaf_nodes()`처럼 같은 app의 child가 없는
   모든 leaf closure, before/after는 selected target closure union을 canonical dependency
   order로 replay합니다. Before는 closure를 만든 뒤 명시 target set 전체를 제외하는 Django
   `_generate_plan` 의미이며, shared dependency는 한 번만 적용합니다.
4. Applied request는 canonical full-forward order에서 known applied node만 replay합니다.
   Unrelated known branch를 포함하고 unknown applied identity는 state를 만들지 않습니다.
5. Replay는 `Operation.stateForward`만 호출하며 backend/recorder/SQL I/O가 없습니다.

Graph/history diagnostics는 기존 `PlanningError` taxonomy를 재사용합니다. Invalid request와
unknown target은 `CategoryPlan`의 structured code, state transition 실패는 기존
`CategoryState`/`CodeInvalidState` 의미를 재사용하는 방향을 spike에서 검증합니다. Exact
계약에 없는 새 reconstruction error category는 근거 없이 추가하지 않습니다.

## False-green 위험과 필수 gate

- **Definition alias**: constructor 뒤 caller가 dependency/operation/model/field/default slice를
  바꿔도 결과와 error가 변하지 않아야 합니다.
- **Result alias**: returned state의 schema/model/field/default를 바꾼 뒤 같은 request 결과가
  원래 값이어야 합니다.
- **Lexical replay**: child key가 parent보다 먼저 정렬되는 fixture에서 dependency order가
  아니면 state replay가 실패해야 합니다.
- **Current/latest reuse**: before/middle/applied-prefix가 unapplied descendant field를 포함하면
  mismatch여야 합니다.
- **Target-only replay**: cross-app prerequisite, shared dependency 또는 unrelated applied branch를
  빼면 contract mutation이 실패해야 합니다.
- **Global-leaf 오판**: cross-app child만 있는 node도 자기 app의 latest leaf입니다. Out-degree
  0만 leaf로 세는 구현은 Go-native focused gate에서 실패해야 합니다.
- **Unknown materialization**: unknown applied key가 dummy app/model을 만들면 실패해야 합니다.
- **Backend smuggling**: reconstructor source는 backend/SQLite/SQL import가 없어야 하고 fake
  backend call count가 아니라 dependency/source gate로 zero-I/O를 고정합니다.
- **Adapter synthesis**: GoDj adapter는 public API output과 live divergent DB before/after에서
  observation을 만들며 contract ID/oracle/static payload를 읽지 않습니다.
- **ID dispatch**: arbitrary contract ID와 target/dependency/applied/live-DB mutation이 public
  result/metrics/db_state에 전파되어야 합니다.
- **Concurrency**: 같은 immutable reconstructor의 repeated/concurrent request가 deterministic하고
  race-free여야 합니다.

## 구현 단계

1. Tagged request/zero value, operation deep-copy와 external package API spike를 먼저 실행합니다.
2. Reconstructor가 `Planner`를 보유하고 empty applied state의 `Planner.Plan(...NamedTarget)`
   결과를 projection order로 재사용합니다. Graph에는 same-app leaf accessor만 추가하고 별도
   closure/DFS 알고리즘을 만들지 않습니다.
3. Reconstructor constructor/request/replay와 structured error unit/property tests를 구현합니다.
4. Definition/input/output mutation, graph insertion permutation, repeated/concurrent race gate를
   추가합니다.
5. Scenario-driven GoDj adapter로 MIG-037..046을 public API와 divergent live DB에서 생성합니다.
6. Manifest status 10개만 `passing`으로 전환하고 locked oracle/static bytes를 보존합니다.
7. Two-process Go actual byte identity, semantic 10/0-diff와 static ordered 10 mismatch를 확인합니다.
8. Eight product set `83 passing + 4 deviation`, 87 unique IDs와 56 cross-binding을 검증합니다.
9. Full/race/CGO=0/vet, compile/source/mutation gate와 독립 product/conformance 감사를 통과합니다.
10. ADR-0016과 status/evidence를 같은 checkout에 완료 반영합니다.

## 완료 조건

- [ ] Tagged request와 reconstructor public API spike/ADR 결정
- [ ] Explicit empty/latest 및 zero request/reconstructor 의미 검증
- [ ] Existing planner graph validation/order kernel 재사용
- [ ] Full definition/operation/nested IR deep-copy와 alias mutation gate
- [ ] MIG-037..044 empty/before/after/middle/cross/shared/latest exact 결과
- [ ] MIG-045..046 applied prefix/unrelated known/unknown exact 결과
- [ ] Invalid request/target, graph/history/state failure structured error와 zero-I/O
- [ ] Repeated/concurrent race와 deterministic result clone
- [ ] GoDj live adapter 10개, two-process actual byte identity와 oracle 10/0-diff
- [ ] Migration-state manifest status 외 locked oracle/static payload 불변
- [ ] Static ordered 10 mismatch와 product unknown scenario gate 전환
- [ ] Existing `73 passing + 4 deviation` 회귀 없이 `83 passing + 4 deviation`
- [ ] Eight product adapter, 87 global unique IDs/scenarios와 56 cross-binding 유지
- [ ] Full Go/race/CGO=0/vet, portable/exact Python, compile/source/mutation gate
- [ ] 독립 P0–P3 product/conformance audit
- [ ] Work/CURRENT/matrix/evidence/ADR가 같은 checkout을 가리킴

## 진행 기록

- [x] GDJ-0015 exact Django oracle/static/fail-closed boundary 완료
- [x] Explicit empty와 omitted latest의 request 의미 분리
- [ ] Public API/deep-copy/graph-kernel spike와 ADR 승인
- [ ] Reconstructor core/unit/property/race 구현
- [ ] GoDj adapter/manifest status 전환과 full audit
- [ ] 최종 문서·evidence·handoff

## 수정 파일

활성화 시점에는 이 work와 status/ADR handoff만 추가합니다. Product 구현은 frontmatter
`allowed_paths`에 한정하고 완료 시 파일별 역할을 기록합니다.

## 결정된 사항

- 2026-08-08: Historical state 의미 소스는 loaded migration definition이며 live DB/current
  generated model introspection을 금지합니다.
- 2026-08-08: Explicit empty/latest는 nil·empty variadic 차이가 아니라 tagged request입니다.
- 2026-08-08: Planner public shape는 identity-only로 유지하고 immutable graph kernel만 내부
  공유합니다.
- 2026-08-08: Product slice에 loader/CLI/lifecycle lock을 포함하지 않습니다.

## 미결정/Blocker

외부 blocker는 없습니다. 첫 spike에서 다음을 결정합니다.

- Exact public constructor/method names와 zero reconstructor 정책
- Multiple before target의 set semantics와 request validation precedence
- Built-in operation pointer/value clone 지원과 typed-nil 진단
- State replay error가 기존 `*Error`를 재사용할지 별도 error type이 필요한지
- Graph kernel helper를 Planner와 reconstructor가 공유하는 최소 refactor

## 테스트 증거

- Contract baseline:
  [EVID-20260808-014](../docs/status/TEST_EVIDENCE.md#evid-20260808-014--gdj-0015-historical-projectstate-reconstruction-compatibility-contracts)
- Machine baseline: `594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`
- Product baseline: `a9ce9597551840f1be8e1f27006d427842f38081`
- Not run: GDJ-0016 product/API/adapter tests — active product work 시작 전
- Not run: GitHub-hosted CI — branch를 push하지 않음

## 다음 정확한 작업

Public tagged request와 immutable reconstructor를 repository 밖 spike로 먼저 검증합니다. 특히
zero request, first-required before/after, operation deep-copy, existing planner graph reuse와
external package compile shape를 고정한 뒤 ADR-0016을 Accepted로 전환하고 product test를
먼저 작성합니다.
