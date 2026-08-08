# ADR-0016: Historical ProjectState는 loaded migration definition을 dependency order로 replay해 재구성한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0015, GDJ-0016, MIG-037..MIG-046, Q-012
- 대체하는 ADR: 없음

## 맥락

[ADR-0010](0010-m2-migration-state-and-executor-boundary.md)은 normalized Schema IR로
구성된 immutable `ProjectState`와 typed migration operation을 채택했고,
[ADR-0013](0013-immutable-migration-planner.md)은 historical schema state와 applied history를
분리했습니다. [ADR-0015](0015-recorder-backed-applied-state.md)는 durable recorder를
`AppliedState`로 읽고 known history를 검증하는 경계를 추가했습니다.

그러나 restart 후 `AppliedState`와 남은 plan만으로는 `Executor.ExecutePlan`에
필요한 `before ProjectState`를 만들 수 없습니다. Recorder의 app/name key는 어떤
모델·필드 변경이 있었는지 담지 않습니다. Live database schema나 현재 generated
model을 읽어 과거 state로 삼으면, migration operation이 표현한 logical history와
다른 schema를 정상으로 오인하게 됩니다.

Django의 `MigrationGraph.make_state()`는 선택한 node의 dependency closure를 구하고
loaded migration operation의 state transition을 empty `ProjectState`에 forward replay합니다.
`MigrationExecutor` startup state도 graph의 full forward order 안에서 known applied migration의
state transition을 replay하며, unrelated applied branch를 포함하고 graph에 없는 recorder
identity는 schema state로 만들지 않습니다. GoDj는 Python object 구조는
복제하지 않되 이 외부 의미를 별도 제품 경계로 만들어야 합니다.

## 결정 기준

- Historical state의 의미 소스는 loaded migration definition과 normalized Schema IR이어야 함
- 현재 generated model package나 live database introspection에 의존하지 않아야 함
- Planner의 identity-only/zero-I/O 경계와 `AppliedState` 분리를 보존해야 함
- Target before/after, multiple leaf, cross-app dependency와 applied unrelated branch를 구분해야 함
- Unknown legacy recorder identity는 보존하되 가상 schema로 materialize하지 않아야 함
- Replay는 backend I/O 없이 deterministic하고 concurrent read에 안전해야 함
- Definition/operation을 deep-copy해 caller mutation이 재구성 결과를 바꾸지 않아야 함
- Graph/history/state 오류는 execution 전에 fail-closed해야 함
- Public migration file/loader ABI와 CLI를 이 결정이 성급하게 freeze하지 않아야 함

## 고려한 선택지

### Live database schema를 introspection해 `ProjectState`로 변환

실제 테이블을 빠르게 복구할 수 있지만 logical model/field metadata, 삭제된 과거
state와 database에 반영되지 않는 state operation을 재구성할 수 없습니다.
Schema/recorder divergence를 history로 정상화하여 crash repair와 reconstruction의 역할도
뒤섞습니다.

### 현재 generated model descriptor를 historical state로 사용

코드 생성 결과를 재사용하기 쉽지만 오래된 migration target이나 partial applied
history를 표현하지 못합니다. Historical model이 현재 Go type에 결합되어
rename/delete와 generated package import graph이 migration replay를 깨뜨립니다.

### 기존 `Planner`가 full migration operation과 state replay까지 소유

하나의 graph를 재사용할 수 있지만 Planner의 현재 계약은 operation/backend를
보관하지 않는 identity-only pure plan입니다. Full definition을 넣으면 direct value
copy 비용, operation mutation과 state error가 plan API에 새게 유출됩니다.

### Full definition을 소유하는 별도 immutable state reconstructor

Graph validation/order 규칙은 Planner와 공유하되, deep-copied typed operation과 state replay는
별도 component가 소유합니다. Target projection과 recorder-applied projection을 같은
replay kernel로 만들 수 있고 Planner와 backend 경계를 바꾸지 않습니다.

## 결정

GDJ-0015의 MIG-037..046 exact contract는 loaded definition replay 방향을 지지했습니다.
`migrations` package에 full definition을 deep-copy하는 immutable historical-state
reconstructor를 두고 다음 public API를 사용합니다.

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

MIG-037의 explicit empty node set과 MIG-044의 omitted-node latest state는 서로 다른
의미입니다. Public API는 variadic zero argument나 nil/empty slice 차이로 이를 추론하지 않고
tagged request로 명시적으로 구분합니다. Zero `StateRequest`는 invalid이고 before/after는
first target을 필수로 받습니다. Zero `StateReconstructor`는 empty graph constructor와 같은
안전한 immutable value입니다.

제품 data flow는 다음과 같습니다.

```text
loaded []Migration
→ graph/definition/operation validation + deep copy
→ immutable StateReconstructor
   └─ tagged StateRequest
      ├─ explicit empty/latest/before/after → dependency closure
      └─ applied snapshot → known applied membership
→ canonical dependency-ordered stateForward replay
→ cloned normalized ProjectState
```

- Replay는 `Operation.stateForward`만 사용하고 `databaseForward`, recorder, SQL과
  backend를 호출하지 않습니다.
- Target `after`는 해당 node를 포함한 dependency closure를 의미합니다. `before`는 full
  forward closure에서 명시 target set 전체를 제외하는 Django `_generate_plan` 의미입니다.
  Multiple target은 union/dedup하고 shared dependency는 한 번만 replay합니다.
- `latest` leaf는 global out-degree 0이 아니라 Django `leaf_nodes()`처럼 같은 app의 child가
  없는 node입니다. Cross-app dependent만 있는 node도 자기 app의 latest leaf입니다.
- Applied projection은 known applied node만 canonical full-forward order에서 replay합니다.
  Known inconsistent history는 replay 전에 거부하고, unknown applied key는
  `AppliedState`에 남지만 `ProjectState`를 생성하지 않습니다.
- Applied projection은 target plan에 없는 unrelated known applied branch도 누락하지
  않습니다.
- Reconstructor는 identity-only `Planner`를 보유하고 empty applied state에 대한
  `Planner.Plan(...NamedTarget)` 결과를 projection order로 재사용합니다. Graph에는 same-app
  leaf accessor만 추가하고 별도 closure/DFS 알고리즘을 만들지 않습니다.
- Reconstructor와 returned `ProjectState`는 caller-owned definition/operation/result mutation에
  영향을 받지 않아야 하며 shared concurrent reconstruction을 지원해야 합니다.
- Historical state는 runtime Schema IR이며 generated concrete model type/generics를 입력으로
  사용하지 않습니다. Generics는 현재 모델의 typed API에 남고, codegen은
  historical replay의 의미 소스가 아닙니다.

## 오류 경계

- Invalid/duplicate node, dependency missing/cycle은 Planner와 같은 graph taxonomy여야 합니다.
- Applied known dependency 누락은 기존
  `migration_history_error/inconsistent_applied_history`입니다.
- Nil/typed-nil operation, app mismatch와 state transition 실패는 backend I/O 전
  structured state/reconstruction error로 거부합니다.
- Invalid/zero request는 기존 plan taxonomy의 `invalid_target`, unknown named target은
  `target_not_found`를 재사용합니다. Exact contract에 없는 새
  reconstruction category는 근거 없이 추가하지 않습니다.
- Error message, Python exception class, Django private DFS/replay object identity는 호환
  계약이 아닙니다.

## 결과와 비용

- Restart orchestrator가 durable applied identity에 맞는 `before ProjectState`를 DB I/O
  없이 구할 수 있습니다.
- Historical schema가 current generated code와 live database drift에서 분리됩니다.
- Full migration definition을 deep-copy/replay하므로 identity-only Planner보다 memory·CPU
  비용이 커집니다. Cache가 필요하다면 immutable result ownership과 version key를 별도
  ADR/benchmark으로 입증해야 합니다.
- Existing `Operation` interface가 package-sealed이므로 현재 built-in은 deep-copy할 수
  있지만 data migration callback/plugin ABI는 아직 표현하지 못합니다.
- Same-app leaf accessor를 포함한 graph kernel을 Planner와 공유하고 Planner의 public
  behavior/order는 바꾸지 않았습니다.
- GDJ-0016은 `StateReconstructor`와 tagged request를 구현하고 MIG-037..046을 10
  `passing`으로 전환했습니다. 여덟 product set의 현재 분류는 `83 passing + 4 deviation`입니다.
- Applied adapter는 real SQLite recorder를 read-only로 열어 `LoadAppliedState`를 거치며,
  capture/request 경로의 exact call allowlist와 exact-one-SELECT driver gate가 임의 helper I/O,
  `Exec`/transaction/`PRAGMA`를 거부합니다. Pure reconstructor는 DB handle을 받지 않고
  backend/SQLite/SQL package를 import하지 않습니다.
- Full definition, built-in operation과 nested IR은 constructor에서 deep-copy되고 반환 state도
  fresh clone입니다. Caller input/result mutation과 repeated/concurrent call이 이후 결과를
  바꾸지 않습니다.

## 의도적으로 결정하지 않은 것

- Migration file encoding, module/directory discovery와 loader version protocol
- Public `godj migrate`, `showmigrations`, `makemigrations`와 listing API
- `LoadAppliedState → reconstruct → plan → ExecutePlan`을 하나로 묶는 lifecycle API
- Read/reconstruct/execute 사이 multi-process lock, revision token과 session binding
- Crash repair, live schema/recorder reconciliation와 schema introspection
- Data migration callback/historical app registry·model mutation ABI
- Replacement/squash/merge/fake/fake-initial, optimizer와 conflict resolution
- Unmigrated app/`real_apps`, cross-app relation rendering과 historical model registry
- PostgreSQL/MySQL/MariaDB/Oracle, multi-DB router와 alias registry

## 검증

- MIG-037..046 exact Django observation으로 empty/before/after/intermediate/latest state 잠금
- Cross-app dependency, multiple target/shared dependency와 unrelated applied branch 검증
- Unknown applied identity가 result applied observation에는 남고 ProjectState에는 생성되지
  않는지 검증
- Live database를 의도적으로 비우거나 다르게 두고 state replay 결과와 DB
  before/after 불변을 동시 검증
- Definition/operation/input/output mutation, permutation과 repeated/concurrent reconstruction gate
- State replay 실패에서 backend/recorder call 0인 fault-injection gate
- Planner/reconstructor graph validation/order 동치와 기존 MIG-005..036 회귀
- Two-process random-hashseed oracle/actual byte identity, eighth-set global uniqueness와 56
  ordered cross-binding 거부
- Static fixture ordered 10 mismatch, unknown scenario exit 2/no output과 payload semantic mutation gate
- Full/race/CGO=0/vet, portable/exact Python과 Markdown/link validation

GDJ-0016은 tagged request, deep-copy ownership, Planner graph 재사용, zero-value/error 경계와
external package compile gate를 통과했습니다. 두 독립 Go actual은 각각 89,867 bytes,
SHA-256 `a307d185e5a3c67a679f62bfa4575f6f43ef8ad41e55c78fdf34d5acb5866e44`로
byte-identical하고 locked Django oracle과 protocol 의미상 10개 0-diff입니다. Locked
oracle/static/SHA256SUMS는 변경하지 않았고 static fixture는 ordered 10 mismatch를
유지했습니다. `make check`, full/race/CGO=0/vet와 독립 product/conformance 감사에서
P0–P3 finding은 없었습니다. 상세 실행 증거는
[EVID-20260808-015](../status/TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
기록합니다.
