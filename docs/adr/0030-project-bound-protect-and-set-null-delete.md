# ADR-0030: Project-bound `PROTECT` and `SET_NULL` Delete

- 상태: Accepted
- 날짜: 2026-08-11
- 관련 work/contract:
  [GDJ-0030](../../work/0030-project-bound-protect-and-set-null-delete.md), REL-007, REL-008, Q-013, Q-017
- 선행 결정: [ADR-0008](0008-m1-sqlite-driver-and-execution-boundary.md),
  [ADR-0009](0009-m2-explicit-write-change-state.md),
  [ADR-0023](0023-symbolic-relation-binding-and-shared-relation-ast.md),
  [ADR-0024](0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md),
  [ADR-0026](0026-forward-foreign-key-object-cache-and-nullability.md)
- 대체하는 ADR: 없음

## 상태와 범위

이 ADR은 bounded SQLite REL-007/008 low-level delete engine에 한해 **Accepted**입니다. Exact clean activation baseline은
`d0396c76d016c0f0335b484fbad56c70b80cf6d4`와
[EVID-20260811-058](../status/TEST_EVIDENCE.md#evid-20260811-058--gdj-0029-terminal-exact-head-ci-and-gdj-0030-activation-baseline)입니다.
Implementation head `c3803acba1929921f23e4751679dc21d4bba9c0f`의
[EVID-20260812-061](../status/TEST_EVIDENCE.md#evid-20260812-061--gdj-0030-github-hosted-exact-26-job-implementation-head-ci) /
[run 31510689383](https://github.com/progresshans/godj/actions/runs/31510689383)은 exact 26/26 jobs·326/326
recorded steps와 independent audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. 현재 제품은 exact
`121 passing + 5 deviation + 1 oracle_locked`, relation 11/12이며 REL-002만 locked입니다. 이 exact 15-file
completion-documentation tree 자체 CI는 `not run/pending`이고 implementation run은 그 later tree의 proof가 아닙니다.

채택한 결정은 SQLite에서 하나의 project binding이 아는 **모든 incoming ForeignKey**를 기준으로 target 한 행을
삭제하는 저수준 engine입니다. `PROTECT`와 `SET_NULL`은 같은 incoming-edge snapshot, fingerprint, pinned
connection과 transaction을 사용해야 하므로 REL-007/008을 나누지 않습니다. 두 contract만 `passing`으로 바꾼
classification이 exact `121 passing + 5 deviation + 1 oracle_locked`, relation 11/12입니다. REL-002는 그대로
locked입니다.

Canonical `project.Using(backend)` facade, relation-aware model method, queryset delete, cascade graph, cache
invalidation, migration/DDL과 non-SQLite backend는 이 ADR이 결정하지 않습니다. Q-013은 `Partial`, Q-017은
P1/open으로 유지합니다.

## 맥락

Django의 `on_delete` collector는 target delete 전에 incoming relation을 project-wide하게 수집합니다. REL-007은
Author 1을 참조하는 두 Post를 모두 보고 `integrity_error/protected_foreign_key`를 반환하며 UPDATE/DELETE와 DB
state 변화가 없어야 합니다. REL-008은 Author 2를 참조하는 nullable reviewer 두 행을 UPDATE로 NULL 처리한 뒤
target 한 행을 DELETE하고, 두 mutation을 transaction 하나에서 수행해야 합니다.

Existing `orm.Manager.Delete`와 `db.Mutator`는 한 table의 explicit-key DELETE만 소유합니다. 이 API를 relation
collector로 소급 확장하면 기존 callers와 spies가 project binding이나 transaction capability 없이도 relation
policy를 수행한다고 오해하게 됩니다. Existing `db.Atomic`은 `database/sql.BeginTx` 기반 callback이고 SQLite의
deferred transaction입니다. PROTECT scan 뒤 concurrent insert를 막으려면 첫 statement 전부터 write reservation을
잡아야 하므로 이 packet에는 별도 additive relation transaction port가 필요합니다.

SQLite schema-level `ON DELETE SET NULL`을 사용하면 REL-008 result는 우연히 맞아도 GoDj collector가 UPDATE를
먼저 실행했다는 product proof가 사라집니다. Fixture FK는 `NO ACTION` 또는 `RESTRICT`로 유지하고 framework가
canonical UPDATE와 DELETE를 직접 실행해야 합니다. 또한 `BEGIN IMMEDIATE`와 connection-local FK pragma만으로는
COMMIT 뒤 대기 writer가 orphan을 만드는 것을 막지 못합니다. 모든 declared incoming edge에 metadata와 일치하는
source-table/column→target-table/key physical SQLite FK가 있어야 하고, fixture는 `PRAGMA foreign_key_list`로 그
`NO ACTION`/`RESTRICT` constraint를 증명합니다. Relation DDL은 이 packet 밖이므로 이는 runtime 보장이 아니라
supported schema precondition입니다.

## 결정 기준

- Existing `db.Mutator`/`db.Session`/`db.Atomic`, `orm.Manager.Delete`와 generated outputs를 변경하지 않음
- Canonical `Bind()`가 소유하는 authoritative declared project universe와 같은 universe의 모든 incoming
  `PROTECT`/`SET_NULL` edge를 runtime binding에서 처리
- Stale generated policy를 exact incoming-edge SHA-256 mismatch로 pre-I/O 거부
- 모든 PROTECT source row를 source identity+PK로 distinct 수집한 뒤 mutation 전에 typed error 반환
- SET_NULL bulk UPDATE 전부와 target exact-one DELETE를 하나의 pinned SQLite transaction에서 수행
- Relation transaction과 테스트가 소유한 모든 competing writer connection에서 connection-local
  `PRAGMA foreign_keys=1`을 각각 transaction/write 전에 확인
- 모든 declared incoming edge에 metadata와 일치하는 physical SQLite `NO ACTION`/`RESTRICT` FK가 존재한다는
  supported schema precondition과 fixture `PRAGMA foreign_key_list` 증명
- `BEGIN IMMEDIATE`, no retry와 explicit busy/cancellation/rollback behavior
- Caller target은 clone/key preflight하고 commit 성공 뒤에만 primary-key state를 clear
- `Delete`가 반환하는 모든 error는 `(0, error)`이고 caller pointer/cache를 바꾸지 않음; literal COMMIT error는
  durability unknown
- Authoritative declared-universe deterministic generator, v3 target pre-byte rejection과 exact generated-union lock
- Django 결과/부작용/error 의미를 비교하되 SQL 문자열과 transaction opening mechanism parity는 요구하지 않음

## 고려한 선택지

### Existing `Manager.Delete`를 project-aware하게 변경

기존 signature에는 project binding과 atomic backend가 없고 `db.Mutator` 구현 전부를 깨뜨립니다. 이미 accepted된
one-row delete 의미도 바뀌므로 거부하고 별도 `RelationDeleter`를 추가합니다.

### Database FK action에 위임

PROTECT/SET_NULL 결과 일부는 재현할 수 있지만 framework collector의 protected-row count와 UPDATE→DELETE 순서,
stale policy 검증을 증명하지 못합니다. Fixture-level `ON DELETE SET NULL`은 false green이므로 거부합니다.

### Existing `db.Atomic` 안에서 먼저 SELECT한 뒤 write

SQLite deferred transaction에서는 PROTECT scan 뒤 다른 connection이 referencing row를 insert할 수 있습니다.
나중에 write upgrade가 busy로 실패할 수도 있고 collector가 본 graph와 mutation graph가 달라집니다. Existing API를
바꾸지 않고 additive `db.RelationAtomic`의 `AtomicRelation`이 pinned connection에서 `BEGIN IMMEDIATE`를 실행합니다.

### `BEGIN IMMEDIATE` 실패를 자동 retry

Context budget, fairness와 duplicate callback execution 정책이 별도 계약 없이 생깁니다. GDJ-0030은 retry하지 않고
driver/backend error를 반환합니다. Caller pointer는 그대로이고 호출자가 명시적으로 다시 시도합니다.

### Target package가 가진 outgoing edge만 보거나 raw slice의 완전성을 추측

`PROTECT`와 `SET_NULL`은 target이 아니라 다른 app source에 선언됩니다. Standalone generator는 caller가 raw
slice에서 아예 뺀 undeclared app을 알아낼 수 없습니다. 따라서 canonical project orchestration이 `Bind()`에 쓰는
것과 같은 authoritative **declared project universe**를 generator에 제공하는 것이 precondition입니다. Pure
generator는 주어진 universe만 검증하고 완전성을 추측하지 않습니다. Cold runtime binder가 full `Bind()` snapshot과
generated fingerprint를 비교해 stale/partial companion mismatch를 I/O 전에 거부합니다.

## 결정

### Additive immutable mutation plan and DB ports

```go
type RelationSetNullPlan struct { /* private */ }

func NewRelationSetNullPlan(
    table string,
    foreignKey FieldRef,
    targetKey Value,
) RelationSetNullPlan
func (p RelationSetNullPlan) Table() string
func (p RelationSetNullPlan) ForeignKey() FieldRef
func (p RelationSetNullPlan) TargetKey() Value
func (p RelationSetNullPlan) Equal(RelationSetNullPlan) bool
```

The plan means one canonical bulk `UPDATE <source> SET <fk>=NULL WHERE <fk>=<target key>`. It is distinct from
one-row `UpdatePlan` and carries no arbitrary assignments.

```go
const CodeProtectedForeignKey = "protected_foreign_key"
const CodeCommitOutcomeUnknown = "commit_outcome_unknown"
const CodeTransactionOutcomeUnknown = "transaction_outcome_unknown"

type ProtectedForeignKeyError struct { /* private */ }
func NewProtectedForeignKeyError(
    protectedSourceRows int64,
) (*ProtectedForeignKeyError, error)
func (e *ProtectedForeignKeyError) Error() string
func (e *ProtectedForeignKeyError) Unwrap() error
func (e *ProtectedForeignKeyError) ProtectedSourceRows() int64
```

The public constructor is required so ORM, as an external consumer of `query`, can construct the typed error. A count less
than or equal to zero returns nil plus `query_error/invalid_plan`. A valid error unwraps to
`query.Error{Category: integrity_error, Code: protected_foreign_key}` and preserves `errors.Is`/`errors.As`.
`CodeCommitOutcomeUnknown` is the stable backend code used only when the literal COMMIT call returns an error.
`CodeTransactionOutcomeUnknown` is used only after the relation session marks mutation-possible immediately before a
`Mutator`/`RelationMutator` call and neither rollback nor forced discard can be confirmed. In this bounded deleter the first
such entry is SET_NULL or target DELETE. It does not imply that COMMIT was called. These exact markers let callers distinguish both
reconciliation-required outcomes from safely terminated pre-COMMIT failures through `errors.Is`/`errors.As`.

```go
type RelationMutator interface {
    RelationSetNull(context.Context, query.RelationSetNullPlan) (int64, error)
}

type RelationSession interface {
    Session
    RelationMutator
}

type RelationAtomic interface {
    AtomicRelation(context.Context, func(RelationSession) error) error
}
```

These interfaces are additive. `Mutator`, `Session`, `Atomic` and their method sets remain byte/source compatible.
`RelationSession` is valid only during its callback. The SQLite backend implements the new port; non-SQLite backends do
not claim support merely because the neutral interfaces exist. `AtomicRelation` calls its callback zero times on
precondition/begin failure; otherwise exactly once synchronously, never concurrently, retried, or after method return. A
callback error must prevent COMMIT and remain reachable in the returned error. Swallowing that error or committing anyway is
an explicit port violation whose DB outcome GoDj cannot guarantee.

### Pinned SQLite relation transaction

`Backend.AtomicRelation` acquires one `*sql.Conn`, reads `PRAGMA foreign_keys` on that exact connection, requires integer
1, and only then executes raw `BEGIN IMMEDIATE`. `database/sql` does not track that raw transaction, so callback query,
SET_NULL, DELETE and terminal SQL all use the pinned connection and the connection may not return to the pool while a raw
transaction might remain active. The relation session becomes inactive before terminal SQL.

Callback error, callback-context cancellation, statement failure, unexpected affected-row count and literal COMMIT error
all attempt raw `ROLLBACK` with a bounded cancellation-independent cleanup context, never the canceled callback context. A
rollback failure or uncertain autocommit state forces physical-connection discard (for example, a `Conn.Raw` callback
returning `driver.ErrBadConn`). Confirmed rollback permits normal `Close`; confirmed `driver.ErrBadConn`/`sql.ErrConnDone`
means the physical connection is discarded/done. If forced-discard confirmation also fails, calling `Close` is forbidden
because it could return an active raw transaction to the pool: the backend retains that `*sql.Conn` as poisoned/unreleased,
accepts resource loss and returns the cleanup error. The relation cleanup helper returns an explicit
termination-confirmed bool; only `driver.ErrBadConn`/`sql.ErrConnDone` confirm discard/done, while a nil `Conn.Raw` return is
unconfirmed. Existing migration discard-helper semantics are not reused blindly. `Open` initializes a private, comparable
pointer to per-Backend retention state; no process-global registry is used. While the Backend remains open, a later borrower
must never receive that poisoned physical connection or inherit its raw transaction, although its retained lock may leave
other connections BUSY or blocked. On returned-error paths,
the primary failure and any cleanup/discard failure are both preserved. A callback panic is separate: mark the session
inactive, run detached rollback/discard cleanup, then re-panic the exact original value unchanged. Cleanup error is
best-effort/non-returnable on that panic path, but confirmed discard or poisoned-handle retention must still prevent pooled
transaction inheritance.
A caller that recovers the exact panic cannot inspect a stable cleanup marker and must reconcile externally before retry.
A pre-COMMIT failure with confirmed rollback or discard leaves DB state unchanged. The relation session sets a private
mutation-possible bit immediately before every `Insert`/`Update`/`Delete`/`RelationSetNull` call. If that bit is set and neither rollback nor
forced-discard confirmation succeeds, the pointer stays unchanged and the returned error is the outermost
`*query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown, Cause:
errors.Join(primary, rollbackError, discardError)}`. This shape makes `errors.As` select the stable marker even when the
primary is another `*query.Error`, while `errors.Is` still reaches every cause. DB outcome is unknown and requires external
reconciliation; this code does not mean COMMIT was called. Raw-BEGIN/callback-0 failure, pre-mutation read/PROTECT/resource
failure, confirmed rollback, confirmed discard/done, the panic rethrow path and literal COMMIT error do not use
`CodeTransactionOutcomeUnknown`. No pre-COMMIT path uses `CodeCommitOutcomeUnknown`.

Only an error returned by the literal COMMIT call becomes
an error chain containing `*query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown,
Cause: ...}`; `errors.Is(err, &query.Error{Category: query.CategoryBackend, Code:
query.CodeCommitOutcomeUnknown})` and the underlying cause must both succeed even when cleanup error is joined. The commit
marker is likewise the outermost `*query.Error`, with literal COMMIT and cleanup errors joined in its Cause. It returns
`(0, error)`, has unknown durability even if later rollback/discard succeeds, never clears the caller key, and performs no
internal automatic retry. For either outcome-unknown marker, the caller must not explicitly invoke `Delete` again before external reconciliation. This packet
exposes no poison token, reconciliation fence, or registry and does not claim to detect or reject that new caller invocation.
A successful COMMIT is authoritative and is not downgraded by
later callback-context state or connection-return/close failure. A raw `BEGIN IMMEDIATE` error, including BUSY, may not be
treated as proof that no physical transaction began: it executes no callback and no retry, force-discards the pinned
connection through `Conn.Raw`→`driver.ErrBadConn`, preserves primary+discard error, and on confirmed discard has a clean
reborrow gate. Failed
discard confirmation follows the same operation-time Close-0 poisoned-retention rule: the poisoned physical connection is
never reborrowed, but its lock may keep other connections BUSY until Backend close. It is neither `CodeTransactionOutcomeUnknown` nor
`CodeCommitOutcomeUnknown` because no mutation callback ran.

`Backend.Close` resolves the accepted retained-resource loss without risking pool reuse. It first wins the existing close
CAS and calls `sql.DB.Close`, which seals the pool, then unconditionally seals and drains the private retained set even if
DB close returned an error. Retained `Conn.Close` calls enter `database/sql`'s terminal driver-close path and cannot
re-pool their driver connections. `Conn.Close` does
not surface an underlying driver-close error, so the contract preserves only DB-close and other errors actually returned by
`database/sql` and claims terminal close attempt/no reuse, not driver-close success. The retention mutex/closed latch makes retain-vs-close linearizable:
pre-seal retains are included in the drain, post-seal retains close immediately, and repeated Backend close remains
idempotent. This is sequential idempotence only; the existing CAS does not promise that a concurrent losing Close waits for
the winning close/drain. An uninitialized private state makes relation use fail closed; existing non-relation internal Backend literals remain
source-compatible.

`BEGIN IMMEDIATE` is an intentional GoDj/SQLite race-safety choice, not a claim that Django emitted identical transaction
SQL. It prevents a concurrent writer from adding a newly protected/source row between the complete incoming scan and
mutation only for connections that cooperate with SQLite locking. After COMMIT, FK rejection/no-orphan also requires every
declared incoming edge to have a metadata-matching physical SQLite FK using `NO ACTION`/`RESTRICT`; the fixture proves it
with `PRAGMA foreign_key_list`, but the packet neither emits nor runtime-validates relation DDL. SQLite FK enforcement is per
connection, so the relation connection and every owned/competing writer connection in the file-backed two-connection tests
must each prove `PRAGMA foreign_keys=1`. One no-wait writer proves BUSY/no retry; another writer waits through COMMIT and
then proves physical-FK rejection/no orphan. GoDj `Open` does not automatically enforce this pragma on every connection.
Missing/mismatched physical constraints and out-of-band or FK-off writers can bypass the safety precondition and are
explicitly unsupported.

### Project-bound relation deleter

```go
func BindRelationDeleter[M any](
    binding ProjectBinding,
    identity ir.ModelIdentity,
    descriptor WriteDescriptor[M],
    expectedIncomingPolicySHA256 string,
) (RelationDeleter[M], error)

func (d RelationDeleter[M]) Delete(
    ctx context.Context,
    backend db.RelationAtomic,
    target *M,
) (int64, error)
```

Binding is cold and zero-I/O. It accepts only a named, non-pointer, zero-size descriptor whose metadata exactly matches
the bound target. The runtime target must be relation-free scalar fields plus exactly one AutoField primary key.
`ProjectBinding` does not retain `Schema.FormatVersion`, so direct `BindRelationDeleter` validates only structural shape plus
`WriteDescriptor` and makes no v2/v3 claim. IR-v2 target enforcement belongs strictly to
`GenerateProjectRelationDelete`, which rejects a generated v3 target before returning bytes. A direct binder succeeds only
when the target has at least one supported incoming many-to-one edge; a zero-incoming target returns
`query_error/invalid_plan` before I/O and remains on the existing plain
`Manager.Delete` surface. Every incoming source model must also expose exactly one AutoField primary key so protected rows
have a stable identity. Every incoming many-to-one edge in the runtime `Bind()` snapshot must be supported
`PROTECT` or nullable `SET_NULL`. The runtime canonicalizes all incoming edge records and requires a lowercase 64-hex
SHA-256 equal to the generated expected value. Reordered semantic inputs canonicalize to the same digest; missing/extra or
policy/nullability/physical-name changes within the declared universe fail before I/O.

The fingerprint version and record encoding are internal but locked by runtime/codegen cross-tests: target identity/table/
AutoField plus each incoming source identity/table/primary key, source FK field/column/nullability/cardinality and policy,
sorted by source identity+field and length-delimited before SHA-256. Runtime and generator encoders are locked to the same
table-driven vectors and cross-tests; any two implementations must produce byte-identical records and digest.

`Delete` validates non-nil context/backend/target, reads the caller `*target` primary key first and requires present/non-NULL
integer, then creates `snapshot := CloneWriteModel(*target)`. It re-reads the snapshot key and requires presence/type/value
exact equality with the caller key. It then creates `clearProbe := CloneWriteModel(snapshot)`, calls
`ClearPrimaryKey(&clearProbe)`, and requires `PrimaryKey(clearProbe)` to equal `query.Integer(0)` with `present=false`.
Every canonical non-PK field must return `ok=true` from `WriteFieldValue` on both snapshot and clearProbe with equal values.
Clone key drift, Clear no-op/key residue or non-PK mutation is cold `query_error/invalid_plan`, `(0,error)`, backend/
`AtomicRelation` I/O 0 and unchanged caller. No new public descriptor method is added; deterministic/pure behavior of the
named zero-size `WriteDescriptor` methods is an extension-contract precondition. Only after this probe may plans be built.
The ORM wraps the transaction callback with an active/single-invocation guard and atomically seals it immediately after
`AtomicRelation` returns. The seal snapshots completed callback count/result and is the linearization point for the outer
result and later caller-key clear. A backend returning nil without a callback, or a second/concurrent entry registered
before seal, produces `backend_error/invalid_plan`, `(0,error)`, no caller key clear, and no mutation from the rejected entry.
An entry at or after seal is itself rejected with `backend_error/invalid_plan` and performs no mutation, but cannot
retroactively change the completed outer result or restore/revise the caller key. Because the interface exposes no
backend-return hook, a malicious first callback racing `AtomicRelation` return and completing before seal is
indistinguishable from a synchronous callback; it remains a port violation whose detection and outer result are not
guaranteed. Tests synchronize late invocation on the seal rather than wall-clock Go function return. A backend that
swallows a callback error or commits prior callback work also violates the port; DB outcome is then not guaranteed.
Every PROTECT `query.Plan` selects exactly source AutoField PK **and source FK** and
filters that selected FK by exact target key; PK-only projection is invalid because current SQLite `compileScalar` requires
the condition field in selected model metadata. Inside `AtomicRelation` it queries every PROTECT edge in canonical order,
scans both values and requires the scanned FK to be non-NULL and exactly equal to the target key before collecting the PK.
A nil or typed-nil rows result with nil error is `backend_error/invalid_plan` without calling a rows method. Nil/typed-nil
rows returned with a primary error preserve that error without method calls; genuinely non-nil rows returned with an error
are closed exactly once and any close error is joined without hiding the primary. A mismatched scanned FK is a backend
contract violation. `Next`, `Scan`, `Rows.Err`, `Close` or context failure aborts before mutation, closes every acquired
genuine rows value exactly once and rolls back. All rows are drained and resource results validated before any mutation. In
particular, ignoring a terminal `Rows.Err`/`Close` and misclassifying the edge as zero protected rows is forbidden.
Duplicates are removed by `(source ModelIdentity, primary-key Value)`: the same
source row found through two PROTECT edges counts once, while equal numeric keys from different source models count twice.
Any match returns a typed `*query.ProtectedForeignKeyError`; `ProtectedSourceRows()` is the distinct `int64` count and
`errors.Is` reaches `query.Error{Category: integrity_error, Code: protected_foreign_key}`. No SET_NULL or DELETE runs in
that path.

When there is no protected row, each SET_NULL edge is bulk-updated in canonical order, then the target `DeletePlan` must
report exactly one affected row. Each `RelationSetNull` affected count must be non-negative: zero is valid for a generic
edge, while a negative count is a backend contract violation that returns `(0,error)` and rolls back. The REL-008 fixture
observes exact SET_NULL affected rows 2. A target DELETE count other than one is `unexpected_rows_affected` and rolls the
whole transaction back. After
`AtomicRelation` reports committed success, and only then, `ClearPrimaryKey(target)` runs and `Delete` returns `(1, nil)`.
The return count is committed target rows only and never includes SET_NULL affected rows. Every error returned by
`Delete`, including a
durability-unknown COMMIT error, returns `(0, error)` and leaves caller bytes/key state unchanged. Only the conformance
adapter maps successful 1 to both oracle fields `deleted_total` and `target_deleted`. There is no global cache registry or
invalidation; previously loaded wrappers may be stale, and that policy remains Q-017 work.

### Deterministic project generator

```go
const ProjectRelationDeleteGeneratorVersion = "godj-codegen-rel-delete-project-v1"

func GenerateProjectRelationDelete(
    packageName string,
    packages []RelationObjectPackage,
) ([]byte, error)
```

The raw slice is the authoritative declared project universe supplied by the same canonical orchestration that owns
generated `Bind()`. The pure generator validates only that supplied universe; an entirely omitted undeclared app is
undetectable here. It emits one project companion for IR-v2 scalar/AutoField targets of supported incoming edges and rejects
an incoming relation targeting IR v3 before returning bytes. It emits deleter fields only for targets with at least one
supported incoming edge. Input reordering produces identical bytes/digest. Missing/extra
or changed metadata **within the declared universe** changes/rejects the candidate, while full runtime `Bind()` plus the
embedded digest rejects a stale or partial companion before I/O.

The exact generated file/public surface is:

```go
// zz_godj_relation_delete.go
const GoDjProjectRelationDeleteGeneratorVersion = "godj-codegen-rel-delete-project-v1"

type RelationDeleters struct {
    AuthorsAuthor orm.RelationDeleter[authors.Author]
}

func BindRelationDeleters() (RelationDeleters, error)
```

Field names are deterministic `<ExportedPackageAlias><ModelGoName>` using the existing `RelationObjectPackage.Alias`
convention; the fixture alias `authors` emits `AuthorsAuthor`. Alias and `AppLabel` may differ, so adversarial deterministic
and namespace/compile gates cover that case. `BindRelationDeleters` calls generated `Bind()` once, derives the canonical
unique incoming-target identity set from `ProjectBinding.ForwardRelations()`, and compares it exactly with the generated
emitted-target identity set before binding any field. An added or removed eligible target therefore rejects the aggregate
cold; each matching target is then bound with its exact fingerprint and static
`var _ orm.WriteDescriptor[T] = Descriptor{}` assertion. This detects a stale delete companion against a current binding,
but cannot detect binding and delete companions that are both stale relative to an entirely undeclared source; authoritative
generation/check remains the precondition. No per-target
exported binder or alias is emitted. A zero-target universe (no target with a supported incoming edge) emits the version
constant, empty `RelationDeleters`, and a binder that still requires successful `Bind()` before returning the zero
aggregate. Namespace collision, exact exported
surface and external-package compilation are gates. No facade, manager or model method is added.

The GDJ-0030 compile artifact is a separate `conformance/relationdeleteproduct/**` exact thirteen-file union: the accepted
twelve `relationselectproduct` prerequisites plus one new project delete companion. `relationselectproduct/**`, existing
generators and all prior generated files remain byte-locked. Generation failure and union compile failure preserve last-good
bytes.

## 결과

### 기대 장점

- Delete policy is resolved from the same immutable project graph as query/object relations.
- With matching physical FK constraints, cooperating FK-on SQLite writers cannot slip between graph scan and mutation or
  create an orphan after COMMIT; missing/mismatched constraints and FK-off/out-of-band writers are unsupported.
- SET_NULL order and affected rows are framework-observable instead of delegated to schema side effects.
- Existing one-row write/transaction interfaces and canonical facade question remain undisturbed.

### 비용과 제약

- SQLite needs a second pinned-connection transaction implementation because `database/sql.BeginTx` cannot request
  `BEGIN IMMEDIATE` portably.
- Declared-universe incoming-edge fingerprints add strict regeneration requirements when project relation policy changes.
- No retry means real write contention is visible to callers.
- Successful delete does not update other already-loaded object/reverse/eager caches.

## 의도적으로 결정하지 않은 것

REL-002 relation assignment/cache invalidation, CASCADE/RESTRICT/DO_NOTHING, queryset/bulk delete, recursive collector,
multi-table inheritance, OneToOne/ManyToMany, signals/hooks, canonical facade/model method, global cache invalidation,
schema/migration emission and PostgreSQL/MySQL/Windows support remain outside this ADR. Q-013 stays `Partial`; Q-017 stays
P1/open. Writers that bypass SQLite locks or use `PRAGMA foreign_keys=0`, and automatic enforcement of FK pragmas by
`Open`, are also outside the supported safety boundary. Missing/mismatched physical SQLite relation constraints are an
unsupported schema, not something this packet's runtime or DDL layer repairs.

## Acceptance 증거

Separate implementation head `c3803acb...`의 EVID-061/run `31510689383`이 다음 exact GDJ-0030 work gates를
통과해 이 bounded decision을 Accepted로 전환했습니다: REL-007/008
oracle-blind product actuals; PROTECT nil/typed-nil/partial/error rows no-call/close-once gates; canceled-context rollback,
raw-BEGIN-error/rollback forced-discard and reborrow; outermost typed transaction-unknown positive plus raw-BEGIN/
pre-mutation/confirmed-cleanup/panic/COMMIT exclusion matrix; outermost typed COMMIT-unknown failure injection; competing FK-on connection
tests; fake nil-without-callback and pre-seal double/concurrent callback outer-failure gates plus a seal-synchronized
rejected-entry-only port-violation gate and an explicitly undetectable return-to-seal first-entry race; race/CGO0/vet; exact
PK+FK projection/scan validation; `PRAGMA foreign_key_list` physical-schema proof; non-negative SET_NULL count gates;
forced-discard-failure Close-0/poisoned-retention/no-pool-reuse proof;
DB-close-before-retained-drain ordering, drain-on-DB-close-error, retain-vs-close race, post-seal immediate terminal close
attempt/no re-pool and idempotent Backend close proof, without claiming a hidden driver-close error;
thirteen-file generation including alias≠app-label namespace/compile and last-good locks; manifest exact two-status
transition/revert; independent audit and exact hosted CI. Activation baseline EVID-058/EVID-060은 implementation
proof로 재사용하지 않았습니다. 이 status transition을 포함한 exact 15-file completion-documentation head의
hosted CI는 별도 `not run/pending`이며 Draft PR은 merge하지 않았습니다.
