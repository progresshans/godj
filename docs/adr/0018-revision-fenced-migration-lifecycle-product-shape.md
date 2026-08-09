# ADR-0018: Revision-fenced lifecycle은 Executor와 backend-owned session으로 조립한다

- 상태: Accepted
- 날짜: 2026-08-09
- 관련 work/contract: GDJ-0018, MIG-047..MIG-056, Q-012, DEV-0002
- 대체하는 ADR: 없음

## 맥락

[Accepted ADR-0017](0017-revision-fenced-migration-lifecycle.md)은 recorder identities와 opaque
freshness revision을 같은 snapshot에서 읽고, 각 migration transaction의 첫 DDL/recorder
write 전에 expected revision을 검증하며, schema/recorder/successor revision을 원자적으로
commit하는 안전성 방향을 채택했습니다. 그러나 GDJ-0017은 제품 source를 바꾸지 않았고
public coordinator, optional backend port, SQLite metadata와 final error taxonomy를 의도적으로
열어 두었습니다.

현재 제품에는 이미 다음 조각이 있습니다.

```text
AppliedMigrationReader → LoadAppliedState
Planner.CheckHistory / Planner.Plan
StateReconstructor.Reconstruct(AppliedStateRequest(...))
Executor.ExecutePlan
```

이를 caller가 순서대로 호출하면 snapshot 뒤 competing writer가 commit할 수 있습니다.
Convenience function 하나로 감싸도 execution transaction 안의 fence가 없으면 TOCTOU는
해결되지 않습니다. 반대로 full source loader나 CLI를 먼저 설계하면 file encoding과 project
handshake를 migration safety API에 불필요하게 결속합니다.

실제 `Executor`는 이미 `Backend backend.AtomicBackend` field를 소유합니다. 따라서 lifecycle
method에 reader/backend를 별도 인자로 다시 받으면 서로 다른 database를 결속하거나 ownership을
중복 표현할 수 있습니다. GDJ-0018은 already-loaded definition과 Executor-owned backend만으로
MIG-047..056을 충족하는 최소 제품 shape를 검증해야 합니다.

또한 exact fixture의 A2는 empty table에 logical `BooleanField(default=false)`를 추가합니다.
현재 SQLite backend는 default가 있는 모든 `AddField`를 거부합니다. Django의 migration-time
default는 persistent database default가 아니므로, empty table에서는 backfill 없이 logical
default를 state에 보존하고 physical default 없는 column을 안전하게 추가할 수 있습니다.

## 결정 기준

- MIG-047..056의 latest/named/zero/history/failure/reopen 외부 의미
- ADR-0014의 migration별 commit과 last durable `ProjectState`
- ADR-0017의 same-snapshot identities/revision, first-write fence와 atomic successor
- Snapshot reader와 executor가 같은 backend/database에 결속됨
- Existing pure Planner/Reconstructor와 transaction execution kernel 재사용
- Existing `AppliedMigrationReader`, `AtomicBackend`, `Transaction`과 external fake source compatibility
- Zero value와 empty/latest/target request가 모호하지 않음
- Unsupported/adoption/stale/contention/integrity가 구조화되고 silent fallback/auto retry가 없음
- SQLite fresh bootstrap과 existing database cutover를 구분함
- Public API가 file/source loader, operation codec와 CLI handshake를 freeze하지 않음
- Fault/cancellation/concurrent process에서 resource leak과 false durable state가 없음

## 고려한 선택지

### Package-level `Migrate(ctx, reader, backend, definitions, targets...)`

의존성이 명시적이지만 reader와 backend가 다른 database를 가리켜도 type system이 막지
못합니다. Existing `Executor` backend ownership을 중복하고, empty target을 latest로 해석하면
zero/nil 의미도 모호해집니다.

### Definition을 보유하는 새 long-lived `Migrator` type

Definition deep-copy와 repeated call에는 편리하지만 source version/lifetime, result cache와
backend session lifetime까지 지금 결정하게 됩니다. 현재 exact set은 한 번의 already-loaded
definition lifecycle만 요구하며 existing `Executor`의 execution/error semantics를 다시
감싸는 type을 추가할 근거가 부족합니다.

### Existing `AtomicBackend`/`Transaction`에 revision method 추가

모든 external fake와 backend 구현을 즉시 깨뜨리고 unfenced `Apply`/`ExecutePlan`의 의미까지
변경합니다. Optional capability가 필요한 backend만 별도 port를 구현하는 방식보다 migration
비용이 큽니다.

### Public revision token을 snapshot/result에 노출

Coordinator와 tests가 token을 전달하기는 쉽지만 caller가 epoch/revision을 해석하거나 stale
successor를 조립할 수 있습니다. Token representation을 storage schema와 public API에
결속하고 validation/commit 뒤 session advance ownership도 분산합니다.

### Backend-owned opaque revision session

Backend가 identities와 private token을 같은 atomic snapshot에서 읽고, 각 step의 declared
history transition을 받아 fenced transaction을 엽니다. Successful commit만 session의 next
token을 advance하므로 token이 caller에게 노출되지 않습니다. Existing ports는 그대로 두고
optional capability로 fail-closed할 수 있습니다.

### MIG-052에서 Django의 incomparable sibling traversal order를 복제

Django oracle의 alpha zero plan은 B1←A3←A2←A1이고 existing GoDj Planner의 canonical plan은
A3←A2←B1←A1입니다. B1과 A3은 서로 dependency가 없는 valid reverse sibling이라 두 순서의
final state/schema/history는 같습니다. Django private traversal에 맞추기 위해 Accepted
[ADR-0013](0013-immutable-migration-planner.md)의 canonical ascending policy를 바꾸면 기존
planner deterministic contract와 더 넓은 결과가 흔들립니다. 따라서 순서를 복제하지 않고
MIG-052의 ordered plan/step payload만 별도 reviewed deviation으로 분리합니다.

## 결정

GDJ-0018의 compile/fault/concurrency/differential 검증을 근거로 다음 product shape를
**Accepted**로 채택합니다.

### Lifecycle request와 Executor method

```go
type LifecycleRequest struct { /* unexported tagged value */ }

func LatestLifecycleRequest() LifecycleRequest
func TargetedLifecycleRequest(first Target, rest ...Target) LifecycleRequest

func (e Executor) Migrate(
    ctx context.Context,
    definitions []Migration,
    request LifecycleRequest,
) (ProjectState, error)
```

- `Executor`의 existing `Backend` field가 snapshot과 write backend를 함께 소유합니다. Method에
  backend/reader를 추가로 받지 않습니다.
- Zero request는 invalid입니다. Explicit latest는 tagged constructor로 만들고 targeted는 최소
  한 existing immutable `Target`을 요구합니다.
- Latest는 existing planner graph의 same-app leaf 전체를 canonical dependency order로
  확장합니다. Cross-app child만 있는 node도 자기 app의 leaf라는 ADR-0016 의미를 재사용하고,
  targeted request는 caller target order와 existing Planner semantics를 보존합니다.
- Caller-owned definitions, dependency/operation/nested IR와 target slice는 lifecycle 시작 전에
  copy합니다. Reconstructor/Planner의 existing validation/deep-copy kernel을 중복하지 않습니다.
- Definitions는 이미 load되고 version-compatible한 값입니다. 이 method는 file discovery,
  codec, loader나 project command가 아닙니다.
- 성공은 resulting `ProjectState`, 확정 rollback/step failure/conflict는 last durable state를
  반환합니다. Commit durability unknown은 마지막 confirmed pre-step state와 전용 error를
  반환하며 실제 DB state를 rollback으로 주장하지 않습니다.
  Snapshot/history/reconstruction 전에 실패해 검증된 durable state가 없으면 empty state와
  error를 반환합니다.
- Empty plan은 metadata/recorder/schema를 만들지 않는 read-only no-op입니다.

Lifecycle 순서는 다음과 같습니다.

```text
copy/validate loaded definitions + request
→ open backend-owned revision session
→ read identities + private token from one atomic snapshot
→ Migrate-private raw-record copy/AppliedState conversion + explicit CheckHistory
→ reconstruct applied ProjectState
→ latest/target plan
→ each step: BeginFencedMigration(transition)
   → operation + recorder + successor revision in one fenced transaction
   → CommitFenced returns rolled_back / committed / unknown durability
→ resulting, last durable, or last confirmed ProjectState
```

### Optional backend/session port

기존 port를 widen하지 않고 `migrations/backend`에 다음 port를 둡니다.

```go
type HistoryTransitionKind uint8

type HistoryTransition struct {
    Migration AppliedMigration
    Kind      HistoryTransitionKind
}

type RevisionFencedBackend interface {
    OpenRevisionFencedSession(context.Context) (RevisionFencedSession, error)
}

type RevisionFencedSession interface {
    AppliedMigrationReader
    BeginFencedMigration(context.Context, HistoryTransition) (RevisionFencedTransaction, error)
    Close(context.Context) error
}

type CommitDurability uint8

const (
    CommitRolledBack CommitDurability = iota + 1
    CommitCommitted
    CommitUnknown
)

type CommitOutcome struct {
    Durability CommitDurability
}

type RevisionFencedTransaction interface {
    SchemaEditor
    Recorder
    CommitFenced(context.Context) (CommitOutcome, error)
    Rollback(context.Context) error
}
```

Transition kind는 apply/unapply 두 값만 유효하고 zero는 invalid입니다. Session의
`ReadAppliedMigrations`는 정확히 한 번 identities와 private expected token을 한 snapshot에
결속합니다. `Migrate`는 이 session method를 직접 한 번 호출해 raw identities를 복사하고
private converter에서 `AppliedState`로 검증합니다. Generic `LoadAppliedState`와 임의 detached
reader를 조립해 token binding을 우회하지 않습니다.

Snapshot read가 반환되기 전에 physical connection을 닫습니다. Session은 call 사이 connection을
pin하지 않고 immutable records, opaque expected token과 `ready/active/poisoned/closed` state만
소유합니다. 동시에 하나의 active transaction만 허용하고 repeated snapshot, active/poisoned/
closed reuse를 structured error로 거부합니다. Caller/core는 token 값을 보거나 계산하지 않습니다.

`BeginFencedMigration`은 current expected token을 transaction의 first-write gate로 사용합니다.
`CommitCommitted`만 session records/token을 committed successor로 advance하고,
`CommitRolledBack`은 이전 snapshot/token을 유지하며, `CommitUnknown`은 session을 poison합니다.
`CommitDurability` zero는 invalid backend result입니다. Legacy `Transaction.Commit`은 durability를
표현할 수 없으므로 fenced path에 사용하지 않습니다.

Core contract에서 `CommitRolledBack`이 token을 advance하지 않는 것과 transaction 실패 뒤 같은
session을 재사용할 수 있다는 것은 다른 의미입니다. SQLite product backend는 operation,
recorder, commit 또는 rollback을 포함한 어느 failed step 뒤에도 session을 poison합니다.
따라서 token은 pre-step 값으로 남지만 같은 lifecycle attempt에서 retry하거나 tail transaction을
다시 열 수 없으며 fresh reopen/reconciliation이 필요합니다.

Fenced transaction은 recorder call이 declared transition의 identity/direction과 정확히 한 번
일치하는지 commit 전에 검증합니다. 누락, 중복, 다른 identity와 반대 direction은 integrity
error이며, successor fingerprint는 session이 current full identities에 declared transition을
적용해 계산합니다. Core가 recorder와 revision successor를 서로 다른 의미로 commit할 수
없어야 합니다.

Coordinator는 open 직후 deferred cleanup closure를 등록하고, 실제 cleanup 시점에 caller
cancellation과 분리된 bounded context를 만들어 `session.Close(cleanupCtx)`를 호출합니다.
`Close`는 abandoned active transaction을 bounded rollback/discard하고 poisoned/
closed session 재사용을 막습니다. Cleanup 실패는 primary lifecycle error를 대체하지 않습니다.
이 connection-free session + mandatory close shape를 source-compatibility, cancellation,
concurrent Close waiter와 leak/discard gate로 검증했습니다.

Coordinator는 existing operation preflight/state transition/recorder 순서를 private execution
kernel로 추출하고 begin/commit strategy를 내부 주입합니다. Public `Apply`/`Unapply`/
`ExecutePlan`은 legacy `AtomicBackend.BeginMigration` + `Transaction.Commit` behavior를 보존하고,
`Migrate`만 `BeginFencedMigration` + `CommitFenced`를 사용합니다. Metadata 생성 뒤 SQLite legacy
begin은 fail-closed합니다. Private kernel은 공개 API가 아니며 optional port가 없으면 unfenced
fallback하지 않습니다.

### SQLite metadata v1과 adoption

SQLite는 recorder와 별도인 exact `godj_migration_revision` singleton metadata v1을 사용합니다.
물리 column은 다음과 같습니다.

- `singleton INTEGER NOT NULL PRIMARY KEY CHECK (singleton = 1)`
- `format_version INTEGER NOT NULL`, 값은 1
- `epoch BLOB NOT NULL`, 길이는 16 bytes이고 CSPRNG로 최초 한 번 생성
- `revision INTEGER NOT NULL`, non-negative signed int64 monotonic revision
- `history_fingerprint BLOB NOT NULL`, 길이는 32 bytes이며 sorted full recorder identities의
  canonical length-prefixed encoding에 대한 SHA-256

Fingerprint에는 unknown legacy identity도 포함합니다. Plain concatenation/delimiter encoding은
사용하지 않습니다. Encoding v1은 sorted identity count와 각 app/name UTF-8 byte length를
unsigned 64-bit big-endian으로 기록하고 raw bytes를 이어 붙입니다. Persistent epoch/revision이
cooperating writer의 주 freshness fence이고
fingerprint는 snapshot binding과 direct non-ABA recorder drift의 보조 integrity gate입니다.
Fingerprint만으로는 identities가 복원되는 ABA를 구분할 수 없습니다.

각 step은 새 pinned SQLite `*sql.Conn`에서 literal `BEGIN IMMEDIATE`를 실행하고 current
metadata와 expected epoch/revision/fingerprint를 첫 DDL/recorder write 전에 conditional
validation합니다. `database/sql.Tx`의 deferred begin을 fenced write에 사용하지 않습니다.
Declared transition을 반영한 recorder row, schema, `revision+1`과 successor fingerprint는 한
transaction으로 commit합니다. Existing schema/recorder helper는 `*sql.Tx`에 고정하지 않고
필요한 `ExecContext`/`QueryContext` generic SQL interface로 좁혀 두 transaction 경로가
공유합니다. `BUSY`/`LOCKED`는 stale과 구분합니다.

Rollback/close는 caller cancellation과 분리된 bounded cleanup context를 사용합니다. Cleanup이
실패하면 `driver.ErrBadConn`으로 physical connection을 pool에서 discard하여 abandoned manual
transaction이 재사용되지 않게 합니다.

Adoption은 다음 의미를 가집니다.

1. Metadata와 recorder가 모두 absent인 fresh database는 첫 nonempty fenced step transaction에서
   bootstrap할 수 있습니다.
2. Empty plan은 어느 table도 만들지 않습니다.
3. Metadata absent + recorder present는 recorder가 empty여도 adoption-required입니다.
4. Metadata가 존재하면 SQLite legacy `BeginMigration`은 fail-closed하여 `Apply`, `Unapply`,
   `ExecutePlan`이 non-cooperating writer가 되지 못하게 합니다.
5. Existing recorder의 최초 metadata 생성은 old writer를 멈춘 exclusive cutover와 snapshot
   검증이 필요합니다. GDJ-0018은 public adoption API를 제공하지 않습니다.

Metadata version/shape/epoch/revision/fingerprint corruption, revision overflow와 recorder 불일치는
write 전 integrity error입니다. Database copy/restore 뒤 epoch 정책, crash repair와 completed
pre-cutover non-cooperating ABA는 이 ADR에서 해결하지 않습니다.

### Error taxonomy

Public core는 backend-specific cause를 다음 structured category/code로 정규화합니다.

| Category | Code | 의미 |
|---|---|---|
| `migration_capability_error` | `revision_fence_unsupported` | optional capability 없음 |
| `migration_capability_error` | `revision_fence_adoption_required` | exclusive existing-history adoption 필요 |
| `migration_conflict_error` | `stale_history_revision` | expected revision mismatch |
| `migration_transaction_error` | `history_revision_contended` | database lock contention |
| `migration_transaction_error` | `commit_outcome_unknown` | commit durability를 증명할 수 없음 |
| `migration_transaction_error` | `commit_cleanup_failed` | commit은 durable하지만 connection cleanup이 실패함 |
| `migration_transaction_error` | `session_close_failed` | primary 오류 없이 session terminal cleanup이 실패함 |
| `migration_history_error` | `history_revision_integrity` | metadata/history/fingerprint/version/overflow 위반 |

Known inconsistent applied history는 기존 `inconsistent_applied_history`를 유지합니다. Existing
operation/recorder/begin/rollback category와 cause wrapping도 보존합니다. Commit outcome은 error와
독립적으로 state 의미를 소유합니다.

- `CommitRolledBack`: current step state/token은 advance하지 않고 pre-step state를 반환합니다.
- `CommitCommitted`: current step state/token은 advance합니다. Post-commit cleanup error가 함께
  와도 post-step state와 `commit_cleanup_failed`를 반환하고 tail은 중단합니다.
- `CommitUnknown` 또는 zero/invalid durability: `commit_outcome_unknown`, 마지막 confirmed pre-step
  state, poisoned session을 반환합니다. 이 state는 실제 database rollback 주장이 아니라 안전한
  lower bound이며 reopen/reconciliation 전 자동 retry를 금지합니다.

Primary operation/recorder/commit-durability error가 rollback/session-close/discard cleanup보다
우선합니다. Cleanup은 `session_close_failed` 등 secondary structured error로 감싸
`errors.Join(primary, secondary)`에 primary를 먼저 보존하며, primary category를 덮거나 unknown을
rolled back으로 낮추지 않습니다. Primary가 없을 때의 close error는 resulting/confirmed state와
`session_close_failed`로 반환합니다. Backend-neutral raw carrier는
`migrations/backend.RevisionFenceError`이고 kind는
`RevisionFenceFailureAdoptionRequired`, `RevisionFenceFailureStale`,
`RevisionFenceFailureContended`, `RevisionFenceFailureIntegrity`입니다. Core는 malformed typed nil,
zero 또는 unknown kind를 `history_revision_integrity`로 fail-closed합니다. Generic recorder-stage
`CapabilityError`는 기존 recorder taxonomy의 `record_failed`를 유지하고 raw fence carrier만
lifecycle category/code로 정규화합니다.

Zero/corrupt lifecycle request와 contained invalid target은 existing plan taxonomy의
`invalid_target`, mixed-direction plan은 existing `mixed_directions`로 transaction 전에
거부합니다. Lifecycle 이름 때문에 중복 category를 만들지 않습니다.

### Empty-table AddField

SQLite의 default-bearing `AddField`는 table이 empty이고 existing compiler가 지원하는 field
shape일 때만 physical persistent default 없이 허용합니다. Logical `ProjectState`는 default를
보존합니다. MIG-047 A2의 physical column은 `BOOLEAN NOT NULL`이고
`PRAGMA table_info(...).dflt_value`는 `NULL`이어야 합니다.

Nonempty table은 one-time backfill/table rebuild 없이는 의미를 보존할 수 있으므로 계속
structured capability error입니다. 이 결정은 general table remake/data migration/generated
insert default를 구현하지 않습니다.

### MIG-052 canonical ordering과 DEV-0002

Accepted ADR-0013의 canonical ascending planner order를 유지합니다. MIG-052에서 GoDj는
A3←A2←B1←A1, Django oracle은 B1←A3←A2←A1을 사용하지만 B1과 A3은 incomparable sibling이고
두 실행의 final logical state, managed DB schema, recorder history와 phase는 같습니다.

Lifecycle manifest는 MIG-052만 `deviation`으로 바꾸고 기존 Django provenance에 exact-one
`kind=decision`, `reference=DEV-0002`, `derived=false`를 추가합니다. 나머지 9개 status만
`passing`으로 전환합니다. Product expectation은 locked oracle을 복사한 뒤 다음 여섯 object
replace만 허용합니다.

- `result.plan[0]`, `result.plan[1]`, `result.plan[2]`
- `metrics.steps[0]`, `metrics.steps[1]`, `metrics.steps[2]`

`db_state`, resulting state, history, phase와 그 밖의 metrics는 deviation scope가 아닙니다.
`godj-migration-lifecycle-deviation-expected.json`과 code-owned `DEV-0002` policy가 이 sparse
scope를 양방향 검증합니다. `godjcheck`는 expectation decision으로 DEV-0001/DEV-0002 policy를
dispatch하고 unknown/mismatched decision은 actual 생성 전에 fail-closed합니다.

## 결과

- Caller는 source loader 없이도 loaded definitions와 one request로 restart-safe lifecycle을
  실행할 수 있습니다.
- Snapshot과 writes가 Executor-owned backend session에 결속되어 reader/backend mismatch와
  stale plan DDL을 막습니다.
- Existing Planner/Reconstructor와 legacy execution API를 유지하면서 optional fenced backend만
  확장할 수 있습니다.
- Persistent metadata와 migration별 CAS I/O 비용, adoption cutover와 SQLite implementation
  complexity가 추가됩니다.
- Metadata 생성 이후 legacy SQLite migration writer가 거부되므로 기존 direct `Apply`/
  `ExecutePlan` caller는 fenced lifecycle로 이동해야 합니다.
- Loaded definition API는 source format을 고정하지 않아 후속 GDJ-0019 loader contract를
  독립적으로 설계할 수 있습니다.
- ADR-0013 ordering을 보존하는 비용으로 MIG-052의 ordered plan/step만 DEV-0002가 되며,
  현재 검증 분류는 `92 passing + 5 deviation`입니다.

## 의도적으로 결정하지 않은 것

- Migration file/source encoding, loader/version protocol과 operation codec
- Public CLI/project-binary management command와 listing API
- Public existing-database adoption/repair command
- Data migration callback/plugin ABI와 historical app registry
- Commit uncertainty recovery, process-kill crash repair와 schema reconciliation
- Live schema drift introspection, database copy/restore epoch rotation과 revision overflow recovery
- Long-lived lock/lease, fairness, distributed coordination와 automatic retry
- Replacement/squash/merge/fake/fake-initial, optimizer와 conflict resolution
- PostgreSQL/MySQL/MariaDB/Oracle, multi-DB router와 relation rendering

## 검증

- External consumer compile: request constructors, `Executor.Migrate`, optional port와 existing fake
- Zero/nil/canceled request/backend/session/transaction과 input/result mutation
- Exact one atomic snapshot per lifecycle attempt, explicit history check before plan/write
- Snapshot call 전용 conversion, mandatory session Close와 ready/active/poisoned/closed misuse 거부
- Fresh/latest/prefix/no-op, named forward/reverse, app zero와 unknown legacy
- Same token two connection/process single winner와 snapshot-before-first-step stale conflict
- Competing commit between steps에서 prior own step만 durable, current/tail mutation 0
- Apply/unapply ABA, direct non-ABA history mutation와 metadata corrupt/version/overflow faults
- `BUSY`/`LOCKED` contention 분류, semantic retry 0와 exact one step attempt
- CAS/post-DDL/post-recorder/commit/cancellation rollback과 resource release 뒤 fresh success
- RolledBack/Committed/Unknown/zero commit outcome, state progression과 cleanup error priority
- Abandoned active transaction의 detached bounded cleanup과 `driver.ErrBadConn` discard
- Declared transition과 recorder identity/direction/count mismatch의 pre-commit integrity 거부
- Metadata absent/present/empty-recorder adoption matrix와 legacy writer fail-closed
- Empty-table default AddField logical default + physical no-default, nonempty capability error
- MIG-047..056 public product adapter, two-process actual byte identity와 9 exact + DEV-0002 expectation
- MIG-052 six-path sparse scope, exact-one decision provenance와 unknown policy decision fail-closed
- Static ordered 10 mismatch, manifest 9 passing + 1 deviation/provenance와
  oracle/static/SHA256SUMS/spike byte pin
- Nine product set `92 passing + 5 deviation`, 97 IDs/scenarios와 72 ordered cross-binding
- Full/race/CGO=0/vet, portable/exact Python, repeated/two-process and independent P0–P3 audit

Acceptance evidence는
[EVID-20260809-017](../status/TEST_EVIDENCE.md#evid-20260809-017--gdj-0018-revision-fenced-migration-lifecycle-product-slice)에
기록합니다. Product commit은 `d076bd20f5964074b7b76b44147ca59f7b3e6eb8`, machine/conformance
commit은 `fd49d5147beefead640f43ae6fd5c83860a17a06`, final local code checkout은
`9f51ad0da443d259940d44acbb8c3d095a9a257b`입니다. `make check`, full CGO-disabled Go,
focused repeated/race gate와 two-process 10/0-diff가 통과했습니다. `macos-15` exact job은 workflow에
추가됐지만 branch push/PR 전이므로 GitHub-hosted evidence는 아직 없습니다.

상세 allowed path와 completion gate는
[GDJ-0018](../../work/0018-revision-fenced-migration-lifecycle-product-slice.md)에 기록합니다.
