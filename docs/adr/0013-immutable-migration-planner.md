# ADR-0013: Migration planning은 불변 identity graph와 분리된 applied state를 사용한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0009, GDJ-0010, MIG-005..MIG-016, Q-012
- 대체하는 ADR: 없음

## 맥락

ADR-0010은 historical `ProjectState`, typed migration operation, one-migration atomic
executor와 backend editor/recorder 경계를 정했습니다. 이 경계에는 여러 migration의
dependency graph, 이미 적용된 migration 기록과 target별 forward/backward plan이 없습니다.

GDJ-0009는 Django 6.1의 planning 외부 동작을 MIG-005..016의 다섯 번째 reference set으로
고정했습니다. Linear/cross-app dependency, applied prefix pruning, prior/zero target rollback,
ordered multi-target와 shared dependency deduplication, missing target/dependency, inconsistent
history와 cycle 오류가 포함됩니다. Planning은 schema/recorder를 바꾸지 않고 DDL, write와
기타 non-SELECT statement를 실행하지 않아야 합니다.

GoDj는 Django의 mutable `MigrationGraph` Python 객체를 복제할 필요가 없습니다. 반면
`ProjectState`에 applied record까지 섞으면 historical schema와 실행 이력이 한 타입에
결합되고, backend recorder read API를 먼저 고정하면 file/CLI/orchestrator 범위를 이번
단면에 끌어옵니다. 따라서 planner가 필요로 하는 최소 identity/dependency와 caller가
제공하는 applied state를 분리해야 합니다.

## 결정 기준

- migration identity와 dependency 방향이 public type에서 명확해야 함
- historical schema state와 applied migration history를 다른 타입으로 유지해야 함
- graph/input slice와 Go map 삽입 순서가 plan을 흔들지 않아야 함
- caller가 전달한 target 순서는 multi-target plan의 의미 있는 입력으로 보존해야 함
- dependency-required precedence와 shared dependency deduplication을 보장해야 함
- graph/history/target 오류를 실행·rollback 오류와 다른 structured error로 표현해야 함
- planner는 zero-I/O pure computation이고 concurrent read에 안전해야 함
- 기존 one-migration `Executor`와 backend interface를 이번 결정에서 바꾸지 않아야 함

## 검토한 선택지

### `ProjectState`에 applied history와 graph를 함께 저장

한 객체로 모든 migration 상태를 전달할 수 있지만 schema evolution snapshot과 recorder
history의 수명주기가 결합됩니다. Planner가 Schema IR operation까지 보관하게 되어
identity-only 계산보다 alias와 dependency가 커집니다.

### Backend recorder가 graph와 applied history를 직접 공급

실제 CLI orchestration에는 필요하지만 이번 계약은 planning의 zero-mutation 의미만
다룹니다. Recorder read/list API, context/I/O failure, connection lifetime과 public file
loader를 먼저 고정하게 됩니다.

### 불변 identity graph와 caller-supplied applied state를 분리

Migration definition에서 key/dependency만 복사해 planner 내부 graph를 만들고 applied
record는 별도 immutable value로 전달합니다. Planner는 database를 모르며 local working
set에서 target을 순차 처리합니다. Loader/CLI/executor orchestration은 후속 단면으로 남길
수 있습니다.

## 결정

Migration identity와 planning 입력을 다음 최소 public surface로 둡니다.

```go
type MigrationKey struct {
    App  string
    Name string
}

type Migration struct {
    App          string
    Name         string
    Dependencies []MigrationKey
    Operations   []Operation
}

func (m Migration) Key() MigrationKey

type AppliedState struct { /* immutable */ }
func NewAppliedState(keys ...MigrationKey) (AppliedState, error)

type Target struct { /* tagged immutable value */ }
func NamedTarget(key MigrationKey) Target
func ZeroTarget(app string) Target

type PlanStep struct {
    Key       MigrationKey
    Direction Direction
}

type Planner struct { /* immutable graph */ }
func NewPlanner(migrations ...Migration) (Planner, error)
func (p Planner) Plan(applied AppliedState, targets ...Target) ([]PlanStep, error)
```

`Dependencies`의 각 key는 해당 migration보다 먼저 충족되어야 하는 prerequisite/parent입니다.
`NewPlanner`는 migration operation을 보관하지 않고 key와 dependency만 deep-copy합니다.
Adjacency는 `(App, Name)`의 안정된 순서로 만들고 duplicate node/edge, missing dependency와
cycle을 결정적으로 거부합니다. Planner와 AppliedState의 값 복사가 내부 read-only map을
공유하는 것은 허용하지만 `Plan`은 매번 local working set과 새 result를 사용합니다.

Graph construction validation 순서는 다음처럼 고정합니다.

1. 잘못된 node key
2. duplicate node
3. 잘못되거나 duplicate인 dependency edge
4. missing dependency
5. dependency cycle

같은 종류의 오류가 여러 개면 node는 key 순, edge는 `(child, parent)` 순으로 가장 앞선
항목을 선택합니다. Cycle은 strongly connected component별 member를 정렬하고 첫 member가
가장 작은 component를 선택해 그 member 전체를 정렬된 diagnostics로 반환합니다. 따라서
migration/dependency 입력 permutation이 오류 category/code와 diagnostics를 바꾸지 않습니다.

`ProjectState`는 historical schema state로 유지하고 `AppliedState`와 합치지 않습니다.
`AppliedState` accessor는 실제 외부 소비자가 필요로 할 때 추가하며 첫 단면에는 constructor만
공개합니다. Empty plan은 `len(plan) == 0`만 보장하고 nil/non-nil slice identity를 계약하지
않습니다.

두 exported value type의 zero value는 유효합니다. Zero `Planner`는 `NewPlanner()`의 empty
graph, zero `AppliedState`는 `NewAppliedState()`의 empty set과 같은 의미입니다. Constructor를
우회해도 panic이나 hidden invalid state가 생기지 않습니다.

`Plan`은 target representation을 먼저 검증하고, 알려진 applied node의 history
consistency를 traversal/target 존재 검사 전에 검증한 뒤 target을 caller 입력 순서대로
처리합니다. Graph에 없는 applied record는 보존하되 consistency 검사에서 건너뜁니다.
여러 known history violation은 `(applied child, missing parent)` 순으로 가장 앞선 항목을
반환합니다.

- 아직 적용되지 않은 named target은 미적용 prerequisite를 dependency-first forward
  순서로 추가합니다.
- 이미 적용된 named target은 immediate same-app child에서 시작해 적용된 descendant를
  dependent-first backward 순서로 제거하고 target 자체는 남깁니다.
- app zero target은 해당 app의 same-app parent가 없는 root에서 시작해 적용된 cross-app
  dependent까지 dependency보다 먼저 backward합니다.
- 각 target 처리 뒤 local applied set을 갱신해 다음 target과 공유하는 migration을 중복
  계획하지 않습니다.

각 seed closure에서 동시에 실행 가능한 여러 `PlanStep`이 있으면 가장 작은
`MigrationKey`를 먼저 emit합니다. Forward closure는 모든 parent가 working applied set에
들어온 node 중 ascending key, backward closure는 모든 적용된 dependent가 제거된 node 중
ascending key를 선택합니다. Named-applied target의 immediate same-app child와 zero target의
same-app root는 seed 자체를 ascending key로 정렬한 뒤 **각 seed의 현재 closure를
순차 처리**합니다. 여러 seed closure를 하나의 union으로 합쳐 global sort하지 않습니다.
앞 seed 처리로 바뀐 working applied set은 다음 seed의 closure/dedup에 반영합니다. 이것이
GoDj의 canonical emitted-order 규칙입니다.

Django private `iterative_dfs` stack/set 순서는 복제하거나 호환 계약으로 삼지 않습니다.
MIG-012가 잠그는 것은 dependency precedence, caller target order와 shared dependency
1회뿐입니다. 따라서 위 ascending policy가 Django의 incomparable sibling 순서와 다르더라도
deviation이 아니며, GoDj product test가 그 자체의 deterministic API를 고정합니다.

`Plan`의 경계 case는 다음과 같습니다.

- target이 없으면 empty plan을 반환합니다.
- zero-value `Target`, empty app/name은 `invalid_target`입니다.
- 존재하지 않는 named target은 `target_not_found`입니다.
- syntactically valid하지만 graph에 node가 없는 `ZeroTarget(app)`은 Django planner처럼
  empty plan입니다.
- duplicate target은 caller order대로 처리하며 두 번째 이후에 새 step이 필요하지 않으면
  no-op입니다.
- caller target 조합이 forward와 backward step을 모두 만들 수 있으며 Planner는 그 mixed
  plan을 반환합니다. 실행 단계의 mixed-plan 거부/분할은 `ExecutePlan`과 함께 후속 범위입니다.

Planning 오류는 기존 execution `*migrations.Error`를 재사용하지 않습니다. 그 타입은
direction, operation index, rollback cause를 전제로 하기 때문입니다. 별도
`*PlanningError`가 `ErrorCategory`/`ErrorCode`, 관련 `Node`/`Related` key와 cycle인 경우
정렬된 member를 보존합니다. Member slice는 unexported이고 `Members()`가 매번 clone을
반환해 caller mutation이 error나 Planner의 internal graph/future error를 바꾸지 못합니다.

```go
type PlanningError struct {
    Category ErrorCategory
    Code     ErrorCode
    Node     MigrationKey
    Related  MigrationKey
    members  []MigrationKey
}

func (e *PlanningError) Members() []MigrationKey
```

Locked error taxonomy는 다음과 같습니다.

| Category | Code | 의미 |
|---|---|---|
| `migration_plan_error` | `target_not_found` | named target이 graph에 없음 |
| `migration_history_error` | `inconsistent_applied_history` | applied child의 dependency가 미적용 |
| `migration_graph_error` | `dependency_not_found` | graph construction의 parent 누락 |
| `migration_graph_error` | `dependency_cycle` | dependency cycle |

Go-native validation taxonomy와 diagnostic field 의미는 다음과 같습니다.

| Category | Code | `Node` | `Related` / `Members()` |
|---|---|---|---|
| `migration_graph_error` | `invalid_node` | 잘못된 migration key | zero / empty |
| `migration_graph_error` | `duplicate_node` | 중복 migration key | zero / empty |
| `migration_graph_error` | `invalid_dependency` | child key | 잘못된 parent / empty |
| `migration_graph_error` | `duplicate_dependency` | child key | 중복 parent / empty |
| `migration_graph_error` | `dependency_not_found` | child key | 누락 parent / empty |
| `migration_graph_error` | `dependency_cycle` | zero | zero / 선택한 sorted SCC |
| `migration_history_error` | `invalid_applied_state` | 잘못된 applied key | zero / empty |
| `migration_history_error` | `duplicate_applied` | 중복 applied key | zero / empty |
| `migration_history_error` | `inconsistent_applied_history` | applied child | 미적용 parent / empty |
| `migration_plan_error` | `invalid_target` | 잘못된 target key 또는 zero | zero / empty |
| `migration_plan_error` | `target_not_found` | unknown named target | zero / empty |

`NewAppliedState`는 모든 invalid key 중 lexicographically first를 duplicate보다 먼저
거부하고, 그 뒤 lexicographically first duplicate를 고릅니다. Target representation은 caller
순서가 의미 있으므로 첫 invalid target을 반환합니다. 오류 message와 cycle DFS path는
계약하지 않습니다. Canonical ascending Go emitted order는 제품 API 정책이지만 Django
incomparable sibling traversal과의 동일성은 계약하지 않습니다.

Self-dependency는 invalid/duplicate edge가 아니라 길이 1의 dependency cycle입니다. Cycle
검출은 self-loop가 있는 singleton SCC도 cyclic component로 포함하며 동일한 SCC selection과
sorted-member diagnostics 규칙을 적용합니다.

## Executor 경계

`PlanStep.Direction`은 기존 `Direction`을 재사용하고 `PlanStep.Key`는 실행 정의를 찾는
identity입니다. 그러나 GDJ-0010은 `ExecutePlan`을 추가하지 않습니다. 기존
`Executor.Apply/Unapply`는 한 migration의 atomic operation/recorder primitive로 남고
dependency를 자체 enforcement하지 않습니다.

이번 결정은 `migrations/backend`, SQLite backend, recorder read/list, 여러 migration의
transaction/partial-failure 의미와 applied history로부터 `ProjectState`를 재구성하는 방법을
바꾸지 않습니다.

## 결과

- Graph planning은 operation/Schema IR/backend lifetime과 분리된 pure computation이 됩니다.
- Input permutation과 map order가 결과를 흔들지 않고 direct value copy/concurrent `Plan`이
  mutable cache 없이 안전해집니다.
- Caller target order는 보존하면서 shared dependency는 local simulated state로 한 번만
  계획합니다.
- Error category/code와 관련 key는 안정되지만 Python exception class/message와 private graph
  객체는 공개 ABI가 되지 않습니다.

## 의도적으로 결정하지 않는 것

- migration file/source encoding과 generator
- CLI, loader, recorder read/list와 `ExecutePlan` orchestration
- data migration callback ABI
- autodetector, rename prompt, optimizer
- merge, squash, replacement migration과 `fake`/`fake-initial`
- multi-process lock, crash recovery와 여러 migration transaction 경계
- backend DDL 실행, multi-DB/router와 applied `ProjectState` reconstruction

이 항목들은 Q-012의 후속 범위이며 GDJ-0010 완료 뒤에도 Q-012는 `Partial`입니다.

## 검증 계획

GDJ-0010은 MIG-005..016 table test와 actual adapter 외에 graph/dependency/applied input
permutation, reversed target order, construction 뒤 caller slice mutation, deterministic
missing/duplicate/cycle/history error, random small DAG precedence, external consumer compile와
concurrent shared Planner race를 검증해야 합니다. 두 독립 Go actual은 서로 byte-identical해야
하고 Django oracle과 protocol 의미가 12-contract 0-diff여야 합니다. Static 12 mismatch와
다섯 set의 20 ordered cross-binding은 그대로 유지합니다.

Planner는 backend/recorder accessor를 받지 않으므로 GoDj adapter는 plan/error만 public API
실행 결과에서 normalize하고, contract의 logical before/after state는 constructor에 전달한
scenario applied-key snapshot으로 만듭니다. DDL/write/non-SELECT 0과 state unchanged metrics는
contract ID별 상수가 아니라 공통 scenario harness에서 이 snapshot과 zero-I/O planner
경계로 산출합니다. Durable gate는 planner source가 `database/sql`, `db`, migration backend를
import하지 않는지, repeated/concurrent `Plan`이 applied input을 바꾸지 않는지, fixture
mutation이 result/DB state/metrics에 전파되는지를 확인해야 합니다. 이는 실제 DB probe를
실행한 척하지 않고 pure planner의 구조적 zero-I/O를 검증하는 선택입니다.

Accepted 상태는 이 API가 이미 구현됐거나 MIG-005..016이 `passing`임을 뜻하지 않습니다.
제품 구현과 evidence는 GDJ-0010에서 별도로 검증합니다.
