---
id: GDJ-0030
status: completed
updated: 2026-08-12
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "d0396c76d016c0f0335b484fbad56c70b80cf6d4"
depends_on: ["GDJ-0029"]
contracts: ["REL-007", "REL-008", "Q-013", "Q-017"]
allowed_paths:
  - ".github/workflows/ci.yml"
  - "Makefile"
  - "query/error.go"
  - "query/relation_mutation.go"
  - "query/relation_mutation_test.go"
  - "db/relation.go"
  - "orm/relation_delete.go"
  - "orm/relation_delete_test.go"
  - "orm/relation_delete_external_test.go"
  - "db/sqlite/backend.go"
  - "db/sqlite/relation_mutation.go"
  - "db/sqlite/relation_mutation_test.go"
  - "db/sqlite/relation_transaction.go"
  - "db/sqlite/relation_transaction_test.go"
  - "db/sqlite/integration_test.go"
  - "codegen/project_relation_delete.go"
  - "codegen/project_relation_delete_test.go"
  - "codegen/testdata/relation_delete/**"
  - "conformance/README.md"
  - "conformance/contracts/relation-manifest.json"
  - "conformance/relationdeleteproduct/**"
  - "conformance/runners/godj/relation_scenarios.go"
  - "conformance/runners/godj/runner_test.go"
  - "conformance/runners/django/tests/test_relation_scenarios.py"
  - "conformance/cmd/godjcheck/main_test.go"
  - "conformance/internal/protocol/product_compare_test.go"
  - "conformance/internal/protocol/relation_artifacts_test.go"
  - "conformance/internal/protocol/migration_project_check_artifacts_test.go"
  - "conformance/internal/protocol/write_migration_artifacts_test.go"
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_delete/**"
  - "docs/ARCHITECTURE.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0030-project-bound-protect-and-set-null-delete.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0029-one-hop-forward-select-related-product-slice.md"
  - "work/0030-project-bound-protect-and-set-null-delete.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Project-bound `PROTECT` and `SET_NULL` Delete

## 결과 목표

GDJ-0030은 locked Django 6.1 relation contracts REL-007/008을 하나의 bounded SQLite low-level product slice로
구현·검증했습니다. Canonical `Bind()`와 같은 authoritative declared project universe의 incoming ForeignKey policy를
exact fingerprint로 seal하고, `PROTECT` row가 하나라도
있으면 mutation 없이 typed error를 반환하며, 그렇지 않으면 모든 `SET_NULL` bulk UPDATE 뒤 target exact-one
DELETE를 pinned `BEGIN IMMEDIATE` transaction 하나에서 commit합니다.

완료 product는 REL-007/008만 `passing`으로 바꾼 exact
`121 passing + 5 deviation + 1 oracle_locked`, relation 11/12입니다. REL-002는 locked로 유지합니다.
ADR-0030은 bounded engine slice에 한해 Accepted이고 Q-013은 `Partial`, Q-017은 P1/open입니다.

## 기준 상태와 선행 증거

- Exact baseline: `d0396c76d016c0f0335b484fbad56c70b80cf6d4`.
- [EVID-058](../docs/status/TEST_EVIDENCE.md#evid-20260811-058--gdj-0029-terminal-exact-head-ci-and-gdj-0030-activation-baseline)의
  run `31484369693`은 exact 26/26 jobs·326/326 recorded steps와 four-coordinate
  630/630/0·63,928 bytes·SHA-256
  `4415fd69844d3754c5ba42adf50ba8fc86e6a499065240b470c2436b21222bca`를 통과했습니다.
- Manifest baseline은 exact 10,788 bytes/SHA-256
  `64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141`입니다.
- Target manifest는 REL-007/008 status만 바꾼 exact 10,776 bytes/SHA-256
  `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`입니다. 두 status를 되돌리면 baseline
  bytes/SHA가 정확히 복구돼야 합니다.
- Oracle/static/SHA, schemas, migrations, `go.mod`/`go.sum`, all existing generators/generated files and
  `conformance/relationselectproduct/**` are frozen.
- EVID-058은 clean terminal baseline만 증명합니다. 이 activation diff와 이후 implementation head에는 각각 별도
  exact-head 검증이 필요합니다.

## Locked REL-007/008 외부 동작

### REL-007 — PROTECT

- Author 1을 참조하는 source Post 두 행을 모두 distinct source identity+PK로 수집합니다.
- 반환 error는 `integrity_error/protected_foreign_key`; `errors.As`로 typed
  `*query.ProtectedForeignKeyError`를 얻고 `ProtectedSourceRows()==2`입니다.
- UPDATE count 0, DELETE count 0, database state unchanged입니다.
- 한 incoming edge만 보거나 첫 row에서 조기 중단하거나 DB constraint error만 normalize하면 false green입니다.

### REL-008 — SET_NULL

- Author 2를 참조하는 nullable reviewer source 두 행을 one canonical bulk UPDATE로 NULL 처리합니다.
- Mutation order는 `UPDATE`, `DELETE`; affected source rows 2, deleted target rows 1, transaction count 1입니다.
- 반환 rows는 1이고 target Author 2만 사라지며 모든 Post reviewer 값은 NULL입니다.
- Fixture FK는 metadata와 일치하는 physical `NO ACTION`/`RESTRICT`; `PRAGMA foreign_key_list`로 검증하며 schema-level
  `ON DELETE SET NULL`과 trigger는 금지합니다.

두 scenario는 existing Django oracle result/error/db_state/metrics를 읽어 expected payload를 재생하지 않는
oracle-blind actual이어야 합니다. Django runner behavior와 frozen oracle/static bytes는 바꾸지 않습니다.

## Frozen additive surface

### Query and error

```go
type RelationSetNullPlan struct { /* private */ }
func NewRelationSetNullPlan(table string, foreignKey FieldRef, targetKey Value) RelationSetNullPlan
func (p RelationSetNullPlan) Table() string
func (p RelationSetNullPlan) ForeignKey() FieldRef
func (p RelationSetNullPlan) TargetKey() Value
func (p RelationSetNullPlan) Equal(RelationSetNullPlan) bool

const CodeProtectedForeignKey = "protected_foreign_key"
const CodeCommitOutcomeUnknown = "commit_outcome_unknown"
const CodeTransactionOutcomeUnknown = "transaction_outcome_unknown"

type ProtectedForeignKeyError struct { /* private */ }
func NewProtectedForeignKeyError(protectedSourceRows int64) (*ProtectedForeignKeyError, error)
func (e *ProtectedForeignKeyError) Error() string
func (e *ProtectedForeignKeyError) Unwrap() error
func (e *ProtectedForeignKeyError) ProtectedSourceRows() int64
```

`Unwrap()` exposes existing stable `query.Error` taxonomy with category `integrity_error` and new code
`protected_foreign_key`. Public construction is required because ORM is an external package consumer. Constructor input
`protectedSourceRows <= 0` returns nil plus `query_error/invalid_plan`. Protected source identities/keys are internal
immutable collection state; public minimum surface is the distinct `int64` count required by REL-007. External-package
construction plus `errors.Is`/`errors.As` and bounded Linux/386 compilation are required gates.
`CodeCommitOutcomeUnknown` is reserved for a literal COMMIT-call error. `CodeTransactionOutcomeUnknown` is reserved for a
pre-COMMIT path where the relation session marked mutation-possible immediately before a Mutator/RelationMutator call and
neither rollback nor forced discard can be confirmed; it does not
mean COMMIT was called. Both must remain distinguishable with `errors.Is`/`errors.As` from safely terminated failures.

### DB ports

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

Existing `Mutator`, `Session`, `Atomic`, SQLite `Atomic` and all old test doubles remain unchanged. The new SQLite callback
is `Backend.AtomicRelation`: it uses one pinned connection, verifies `PRAGMA foreign_keys=1` on it, then executes
raw `BEGIN IMMEDIATE`. No retry is permitted. A raw BEGIN error, including BUSY, force-discards that physical connection
because execution outcome cannot be inferred from the returned error; the callback never runs. If discard cannot be
confirmed, it must not call Close and instead retains the poisoned/unreleased `*sql.Conn`, accepting resource loss.
`Open` initializes a private per-Backend retention-state pointer; no process-global registry is introduced. `Backend.Close`
first wins the existing close CAS and calls `sql.DB.Close`, then seals and drains that retained set even when DB close
returns an error, preserving DB-close plus any observable retained-handle close errors. Since `sql.DB` is already closed,
drained `Conn.Close` calls cannot return a physical connection to the pool and instead trigger the terminal driver-close
path; `database/sql` does not surface an underlying driver-close error through `Conn.Close`, so only the no-repool/attempt
boundary is claimed. A retain that races before the seal is drained; one observed after the seal is
closed immediately. A nil/uninitialized private state makes relation use fail closed while old internal Backend literals
that do not use relation transactions remain source-compatible. Precondition or begin failure invokes callback 0;
otherwise callback is exactly once synchronously, never concurrent/retried/after return.
Callback error cannot commit and must remain reachable from the returned error.

### ORM binder and delete

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

Binder rules:

- named non-pointer zero-size descriptor only;
- exact binding identity/descriptor metadata match;
- runtime relation-free scalar fields plus exactly one AutoField target and exact `WriteDescriptor`; `ProjectBinding` drops
  `Schema.FormatVersion`, so direct binder makes no v2/v3 claim;
- generator-only target IR format v2; generated v3 target rejects before bytes;
- at least one supported incoming many-to-one edge; a zero-incoming target returns `query_error/invalid_plan` before I/O
  and is not a relation-deleter target;
- exactly one AutoField primary key on every incoming source model so PROTECT identity enumeration is stable;
- every incoming edge in the runtime full `Bind()` snapshot must be supported many-to-one `PROTECT` or nullable
  `SET_NULL`;
- exact full incoming-edge fingerprint lowercase 64-hex match before I/O;
- reordered semantic input canonicalizes to the same digest; missing/extra/stale metadata within the authoritative declared
  universe returns structured invalid-plan and publishes no deleter.

Delete rules:

1. Validate context/backend/pointer; read caller primary key as present/non-NULL integer; create `snapshot` clone and require
   its key presence/type/value exact equality. Create `clearProbe := CloneWriteModel(snapshot)`, clear its key, require
   `PrimaryKey(clearProbe)==query.Integer(0)` with `present=false`, and require every canonical non-PK `WriteFieldValue` on
   snapshot/clearProbe to be `ok=true`/equal. Any clone key drift, Clear no-op/residue or non-PK mutation is cold
   `query_error/invalid_plan`, `(0,error)`, `AtomicRelation`/backend I/O 0 and unchanged caller.
2. Arm an active/single-callback guard. Immediately after `AtomicRelation` returns, atomically seal the guard and snapshot
   completed callback count/result. Atomic success without callback, or a second/concurrent callback entry registered before
   seal, is `backend_error/invalid_plan`, `(0,error)`, no key clear and no mutation from the rejected invocation. An entry at
   or after seal is itself rejected with `backend_error/invalid_plan` and mutation 0, but cannot retroactively change the
   completed outer result or caller key. Caller key clear is allowed only from a sealed exactly-one-completed-success
   snapshot. The relation session marks mutation-possible immediately before every `Insert`/`Update`/`Delete`/
   `RelationSetNull` call; this bounded deleter first reaches it at SET_NULL or target DELETE. A malicious first callback
   racing backend return and completing before seal is indistinguishable from a valid
   synchronous callback; it is a port violation whose detection/outer result this interface does not guarantee. A backend
   swallowing a callback error/committing is also a port violation with unguaranteed DB outcome.
3. Build each PROTECT query with exactly selected source AutoField PK+source FK and exact FK=target condition, plus
   canonical SET_NULL plans and target delete plan, without I/O. PK-only projection is forbidden by current SQLite
   `compileScalar` selected-condition-field validation.
4. Enter relation transaction and query **all** PROTECT edges in canonical order; scan both selected values, require
   non-NULL FK exact target-key equality, drain/validate/close every rows value before mutation.
5. Distinct matching source PKs by source identity+PK. Any match returns typed protected error and executes no mutation.
6. Otherwise execute every canonical SET_NULL bulk plan, then target DELETE; target affected rows must equal 1.
7. Commit succeeds before caller `ClearPrimaryKey`; return `(1, nil)` for committed target rows only.

Every error returned by `Delete` is `(0, error)` and leaves caller pointer bytes/key unchanged; SET_NULL affected rows are
never part of the return value. GoDj performs no internal automatic retry. For errors matching
`CodeTransactionOutcomeUnknown` or `CodeCommitOutcomeUnknown`, the caller must not explicitly invoke `Delete` again until it
has externally reconciled DB state, but this
packet exposes no poison token, reconciliation fence or registry and does not claim to detect or reject that new invocation.
Only the conformance adapter maps a successful return 1 to both oracle `deleted_total` and `target_deleted`.

Successful commit does not invalidate previously loaded forward/reverse/eager caches. The private per-Backend connection
retention state is cleanup ownership, not a global model/cache, retry-fence or reconciliation registry.

## SQLite transaction and compiler gates

- `RelationSetNullPlan` compiles to one parameterized `UPDATE <source> SET <fk>=NULL WHERE <fk>=?`; identifier/value/
  nullability validation is pre-execution and affected count is driver-reported. Count 0 is valid for a generic edge;
  negative is a backend contract violation that rolls back and returns `(0,error)`. The oracle fixture observes exact 2.
- The same pinned `*sql.Conn` performs FK pragma verification, raw `BEGIN IMMEDIATE`, all relation reads/writes and terminal
  SQL. Because `database/sql` does not track the raw transaction, the session is inactive before terminal SQL and the
  connection cannot return to the pool while transaction state may remain active. Every owned/competing writer connection
  in tests separately proves `PRAGMA foreign_keys=1` before writing.
- Callback/session use after return is structured failure. Callback panic marks the session inactive, runs detached
  rollback/discard, then re-panics the exact original value unchanged. Cleanup error is best-effort/non-returnable on this
  path; confirmed discard or per-Backend poisoned-handle retention still prevents physical transaction inheritance. A caller that recovers the panic cannot inspect a
  stable cleanup marker and must externally reconcile before retry.
- Atomic callback contract is 0 calls on precondition/begin failure, otherwise exactly one synchronous call; concurrent,
  retry or after-return calls are forbidden. Callback error prevents COMMIT and remains reachable in returned error.
- Raw `BEGIN IMMEDIATE` error, including BUSY, invokes no callback/retry, force-discards via
  `Conn.Raw`→`driver.ErrBadConn` and preserves primary+discard error. Confirmed discard/done permits a clean reborrow;
  unconfirmed discard calls Close 0 and retains the poisoned connection so no later borrower can inherit that physical
  transaction. The retained lock may keep other connections BUSY or blocked until `Backend.Close`. It is not
  `CodeTransactionOutcomeUnknown` or `CodeCommitOutcomeUnknown`.
- Callback error/cancellation, statement and unexpected-count failures use a bounded cancellation-independent cleanup
  context to attempt raw ROLLBACK. Rollback failure or uncertain autocommit forces physical-connection discard (for example
  `Conn.Raw` returning `driver.ErrBadConn`). Confirmed rollback uses normal Close; confirmed discard/done releases it.
  Unconfirmed discard must call Close 0 and retain the poisoned `*sql.Conn` so it cannot return to the pool; a later borrow
  must never inherit that physical transaction, but may remain BUSY/blocked until `Backend.Close`. Preserve primary plus
  cleanup error. The relation session sets mutation-possible immediately
  before any `Insert`/`Update`/`Delete`/`RelationSetNull` call. Confirmed rollback/discard leaves pre-COMMIT DB state
  unchanged. If mutation-possible is set and both rollback and forced-discard confirmation fail, return an outermost
  `backend_error/transaction_outcome_unknown` whose Cause joins primary+rollback+discard errors, leave the pointer unchanged,
  and require reconciliation. This outer shape must win `errors.As` even when primary is another `*query.Error`, while all
  causes remain reachable. Raw-BEGIN/callback-0, pre-mutation read/PROTECT/resource failure, confirmed cleanup, panic rethrow
  and literal COMMIT error do not use this code; no
  pre-COMMIT path uses `CodeCommitOutcomeUnknown`.
- Relation cleanup returns an explicit termination-confirmed bool. Only `driver.ErrBadConn`/`sql.ErrConnDone` confirm
  forced discard/done; `Conn.Raw` nil is unconfirmed and cannot authorize Close/pool return. Do not copy the existing
  migration helper's looser nil-success behavior. Raw-nil fakes cover mutation-possible marker vs raw-BEGIN/callback-0 no
  marker, operation-time Close 0, poisoned retention and no reuse. Backend-owned retention uses a private mutex plus closed
  latch: `Backend.Close` calls `sql.DB.Close` first, then unconditionally seals/takes/drains retained handles, even after a
  DB-close error, and preserves all errors that `database/sql` actually returns. Pre-seal retains are drained, post-seal
  retains enter the same terminal close path immediately, and a second Backend close is idempotent. The underlying
  driver-close error is not observable through `Conn.Close`; the contract is one terminal close attempt and no pool return.
  This is the only point at which an unconfirmed retained handle is closed. Sequential second-close idempotence is frozen;
  the existing CAS does not promise that a concurrent losing Close waits for the winner's drain to finish.
- Successful COMMIT is authoritative; no post-commit context or connection-return/close error converts durable success into
  an error. Only a literal COMMIT-call error returns
  an outermost `*query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown,
  Cause: ...}`; stable taxonomy and joined literal-COMMIT/cleanup causes remain reachable through `errors.Is`/`errors.As`.
  It attempts rollback/discard cleanup but remains durability-unknown, returns `(0,error)`, leaves the caller key intact and
  performs no internal automatic retry. For either outcome-unknown marker, explicit caller re-invocation before external reconciliation is a caller contract
  violation that this packet does not claim to detect or reject.
- A real file-backed two-connection gate proves both a no-wait competing writer receives BUSY with no retry and a waiting
  FK-on writer proceeds only after COMMIT, then receives FK rejection and creates no orphan. In-memory-only fakes are
  insufficient.
- Every declared incoming edge must have a metadata-matching physical SQLite FK with `NO ACTION`/`RESTRICT`. Fixture
  `PRAGMA foreign_key_list` and the real race prove this precondition; relation DDL/runtime schema repair is out of scope.
- SQLite FK enforcement is per connection. `Open` does not automatically enforce it on every pooled/out-of-band connection.
  A missing/mismatched physical constraint or FK-off/out-of-band writer can bypass integrity and is unsupported; the
  framework does not claim to block it.
- Typed-nil backend/session, nil context/callback and forged/corrupt plan fail closed.

`BEGIN IMMEDIATE` is an intentional Go safety decision, not Django SQL compatibility. We compare result, DB state, error and
transaction/mutation meaning, not exact transaction-opening SQL.

## Codegen and exact union

```go
const ProjectRelationDeleteGeneratorVersion = "godj-codegen-rel-delete-project-v1"
func GenerateProjectRelationDelete(packageName string, packages []RelationObjectPackage) ([]byte, error)
```

- Canonical orchestration must pass the same authoritative declared project universe used by generated `Bind()`; the pure
  generator validates only the supplied universe and cannot detect an entirely omitted undeclared app.
- Emit delete fields only for IR-v2 scalar/AutoField targets with at least one supported incoming edge/policy.
- Any incoming edge targeting IR v3, namespace/import collision or unsupported key/policy fails before returning bytes.
  Reordered semantic packages produce identical bytes/digest. Missing/extra/changed metadata within the declared universe
  changes/rejects generation; a stale/partial companion against full runtime `Bind()` fails cold before I/O.
- Each emitted target includes exact policy SHA and static
  `var _ orm.WriteDescriptor[Target] = TargetDescriptor{}` compile assertion.

Exact output file/public surface is:

```go
// zz_godj_relation_delete.go
const GoDjProjectRelationDeleteGeneratorVersion = "godj-codegen-rel-delete-project-v1"

type RelationDeleters struct {
    AuthorsAuthor orm.RelationDeleter[authors.Author]
}

func BindRelationDeleters() (RelationDeleters, error)
```

Field naming is deterministic `<ExportedPackageAlias><ModelGoName>` from existing `RelationObjectPackage.Alias`; fixture
alias `authors` emits `AuthorsAuthor`. Alias≠`AppLabel` has an adversarial deterministic/namespace/compile gate.
`BindRelationDeleters` calls generated `Bind()` once, derives the canonical unique incoming-target identity set from
`ProjectBinding.ForwardRelations()`, and compares it exactly with the generated emitted-target identity set before binding
any field. Added/removed eligible targets reject the whole aggregate cold; matching targets then undergo exact fingerprint
validation. This detects a stale delete companion against a current binding but cannot detect binding and delete companions
that are both stale relative to an entirely undeclared source; authoritative generation/check remains the precondition. No
per-target exported binder/alias or facade/manager/model method is emitted. A zero-target universe (no target with a
supported incoming edge) emits the version constant, empty aggregate and binder; that binder still requires successful
`Bind()` before returning zero. Namespace, exact exported-surface and external-package compilation gates are mandatory.

- New `conformance/relationdeleteproduct/**` exact union is the accepted twelve-file select-related prerequisite union plus
  this one companion = exact 13. It is separate from and must not rewrite `relationselectproduct/**`.
- Pure generation, input-order determinism, exact exported surface, external compile, generated no-rewrite, corruption,
  v3-target rejection and last-good preservation are required.

No final line/byte/inventory prediction is recorded before implementation. The implementation evidence must measure it.

## Product and false-green gates

- Query plan immutability/validation; public constructor rejects rows<=0; external ORM construction,
  `ProtectedForeignKeyError` errors.Is/errors.As/`ProtectedSourceRows` and Linux/386 compile tests.
- Binding rejects zero/typed-nil/pointer/stateful descriptor, wrong identity/metadata, stale/partial fingerprint and
  unsupported target structural shape/key/policy/nullability. Direct binder does not inspect schema format version;
  generator separately rejects a v3 target pre-bytes.
- Aggregate binding compares the full binding's canonical unique incoming-target set with the emitted target set before any
  per-target publication; added/removed target companion and per-target fingerprint drift are cold no-I/O failures. It does
  not claim to detect a binding and delete companion that are both stale relative to an undeclared source.
- Delete rejects caller key absence/NULL/non-integer and corrupt `CloneWriteModel` key absence/type/value change before
  plans/`AtomicRelation`. A clear probe also rejects Clear no-op/key residue/non-PK mutation via canonical
  `WriteFieldValue` before/after equality. Each returns `query_error/invalid_plan`, `(0,error)`, backend I/O 0 and unchanged
  caller. No new descriptor method is added; named zero-size descriptor methods must be deterministic/pure by extension
  contract.
- PROTECT enumerates all edges/rows, distincts globally and performs UPDATE 0/DELETE 0 with unchanged DB/caller. One source
  row found through two PROTECT edges counts 1; equal numeric PKs from two different source models count 2.
- Each PROTECT edge rejects nil/typed-nil rows+nil as `backend_error/invalid_plan` without method calls. Nil/typed-nil
  rows+error preserves primary without method calls; genuinely non-nil rows+error closes once and joins close error without
  hiding primary. `Next`/`Scan`/`Rows.Err`/`Close`/context failures abort before mutation, close every acquired genuine rows
  value exactly once and return `(0,error)` with rollback and unchanged caller/DB. Exact selected/scanned PK+FK shape,
  non-NULL FK target equality and all-rows drain are
  asserted. Ignoring terminal `Rows.Err`/`Close` and deleting after a false protected-zero is banned.
- Mixed PROTECT+SET_NULL proves any protected row prevents every mutation.
- SET_NULL proves exact UPDATE→DELETE, rows 2→1, one transaction and caller key clear only after commit.
- SET_NULL rows affected permits 0, rejects negative as backend contract violation/rollback, and fixture actual observes 2.
- Fault injection proves pre-COMMIT read/update/delete/context/unexpected-count failures use a cancellation-independent
  rollback or forced physical-connection discard, preserve primary+cleanup error and never leak a raw transaction into a
  reborrowed connection. Confirmed cleanup proves unchanged DB; mutation-possible plus unconfirmed rollback+discard proves
  outermost typed `transaction_outcome_unknown`, primary+rollback+discard reachability and reconciliation-required without
  using `CodeCommitOutcomeUnknown`. Raw-BEGIN/callback-0, pre-mutation read/PROTECT/resource failure, confirmed cleanup,
  panic rethrow and literal COMMIT prove that transaction code absent. A primary `*query.Error` fixture proves `errors.As`
  returns the outer marker first.
  Literal COMMIT-call error alone proves typed `commit_outcome_unknown`, best-effort cleanup,
  durability-unknown `(0,error)`, unchanged pointer, backend attempt exact 1 and internal automatic retry 0. For either
  outcome-unknown marker, caller
  re-invocation before external reconciliation is documented as forbidden but is not asserted as runtime-rejected because
  this packet has no poison token/fence/registry. Canceled-context rollback,
  forced-discard and reborrow are explicit gates.
- Callback panic cleanup proves detached rollback/discard and, when cleanup is unconfirmed, poisoned-handle retention with
  no inherited physical transaction, then recovers the exact original panic value unchanged; cleanup error is not promised
  as a returned value on this path.
- Fake backends returning nil without callback or registering twice/concurrently before guard seal prove ORM
  `backend_error/invalid_plan`, `(0,error)`, unchanged key and rejected-invocation mutation 0. A separate seal-synchronized
  late fake proves only that the late invocation itself returns `backend_error/invalid_plan` and mutates 0; it must not
  assert retroactive changes to the already-decided outer result or caller key. A first entry intentionally racing backend
  return→seal has no detection/outer-result assertion beyond race safety. Swallowed callback error/committed prior work is
  recorded as a port violation with unguaranteed DB outcome.
- Raw-BEGIN success-followed-by-error/BUSY fault proves callback 0, retry 0, forced physical discard, primary+discard error
  preservation and, on confirmed discard, clean reborrow without `CodeCommitOutcomeUnknown`.
- Forced-discard-failure fake proves operation-time Close call 0, per-Backend poisoned connection retention/resource loss,
  cleanup error preservation and no poisoned-handle reuse by a later pool borrow. Backend-close gates prove DB-close-before-
  drain ordering, drain despite DB-close error, exactly-once terminal `Conn.Close` attempt/no re-pool, post-seal immediate
  terminal close path, retain-vs-close race safety and idempotent second close. A custom driver proves invocation/order but
  the contract does not claim an underlying driver-close error that `database/sql` discards. Other connections may remain
  BUSY until that explicit close. The second-close gate is sequential and does not invent a concurrent Close-waiter
  contract.
- Per-connection FK=1 for relation/every test writer plus `PRAGMA foreign_key_list` proof for every declared incoming edge;
  file/two-connection no-wait BUSY/no-retry and wait-through-commit physical-FK rejection/no-orphan; session expiry gates.
- Generator alias≠app-label determinism, namespace and external compile prove exact
  `<ExportedPackageAlias><ModelGoName>` field naming.
- Normal and `-race`, CGO-disabled, `go vet`, exact Ubuntu Linux/386 bounded set and clean-worktree gates.
- External compile asserts the added private pointer state preserves `Backend` value comparability and the existing public
  method set; no direct mutex/slice field or new public retention API is allowed.
- Oracle/static/SHA/schema/migrations/go.mod/sum and all existing generated/generator/relationselectproduct byte locks.
- Manifest changes exactly REL-007/008 and exact revert restores 10,788-byte SHA
  `64ce839aba22cac015bb512f646a913d9a850912fa8405e65d6d25af14fb8141`.
- `godjcheck` expected/executed/skipped, protocol count/status, all four relation-product coordinates and independent
  P0/P1/P2/P3 audit are exact and non-skipping.

## 명시적 비목표와 frozen paths

REL-002, assignment/FK cache invalidation, CASCADE/RESTRICT/DO_NOTHING, recursive/queryset/bulk delete, signals/hooks,
OneToOne/ManyToMany, canonical `project.Using` facade, global cache invalidation, schema/DDL/migration codec and
PostgreSQL/MySQL/Windows are out of scope. Automatic per-connection FK enforcement, FK-off/out-of-band writers and
transaction/commit-outcome-unknown reconciliation is unsupported; missing/mismatched physical SQLite incoming FKs are also
unsupported.
The framework does not claim to prevent or repair those integrity bypasses.

Except for `allowed_paths`, every repository path is frozen. In particular existing public interfaces, `query/mutation.go`,
`db/db.go`, `orm/write.go`, schema/IR/DSL/migration sources, `go.mod`, `go.sum`, oracle/static/SHA artifacts, every prior
generator/generated fixture and `conformance/relationselectproduct/**` must remain byte-identical. New neutral APIs live in
new files where specified; `query/error.go` only receives the frozen additive query/error surface above. The existing
`db/sqlite/backend.go` may change only to attach/initialize the private comparable retention-state pointer and to perform the
DB-close-before-retained-drain lifecycle; its public method set and signatures remain unchanged.

## 체크리스트

- [x] GDJ-0029 terminal exact-head CI and clean GDJ-0030 baseline recorded as EVID-058.
- [x] GDJ-0030 active work and ADR-0030 Proposed boundary created.
- [x] REL-007/008 indivisible semantics, additive APIs, transaction/fingerprint/codegen and false-green gates frozen.
- [x] First activation hosted false-negative의 `go test -json | tee` output backpressure를 재현하고 direct-file
  capture/compact post-process evidence fix와 protocol gate를 EVID-059에 기록했습니다.
- [x] Corrected activation exact-head local/hosted validation recorded across EVID-059/060, separately from EVID-058.
- [x] Query/db/ORM/SQLite implementation and focused normal/race/fault/concurrency tests complete.
- [x] Deterministic generator, exact thirteen-file separate union and compile/last-good locks complete.
- [x] REL-007/008 oracle-blind actual and exact manifest two-status transition/revert complete.
- [x] Full local CI, four-coordinate hosted CI, independent audit and exact implementation inventory recorded.
- [x] Completion docs update ADR to Accepted/work to completed only for the bounded slice.
- [ ] Exact 15-file completion-documentation head receives its own hosted CI, separate from implementation EVID-061.
- [ ] Later terminal evidence/status records that completion head without recursive proof reuse.

## 현재 blocker와 다음 정확한 작업

외부 제품 blocker는 없습니다. Activation commit `83e6ea05...`의 run `31498696555`는 product test failure가
아니라 macOS Intel Actions-log backpressure로 package output `WaitDelay`가 만료된 25/26 false-negative였고,
stabilization head `48472a1c...`의 run `31503631942`가 corrected activation 26/26을 EVID-060에서 통과했습니다.
Implementation head `c3803acba1929921f23e4751679dc21d4bba9c0f`의 Draft PR #1
[run 31510689383](https://github.com/progresshans/godj/actions/runs/31510689383)은 exact 26/26 jobs·326/326
recorded steps, four-coordinate 687/687/0·69,597 bytes·SHA-256
`363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`, full Ubuntu `make ci`, exact
Darwin/four Python, actual Linux/386와 independent audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. EVID-061을
근거로 work를 completed, ADR-0030을 bounded slice에 한해 Accepted로 전환합니다. 다음은 exact 15-file
completion-documentation head의 별도 CI와 terminal evidence/status이고, 그 뒤 Q-017 facade/API
compile-usability work/ADR을 별도로 활성화합니다. Draft PR은 사용자 요청 전 merge하지 않습니다.

## 인수인계

- Baseline: `d0396c76d016c0f0335b484fbad56c70b80cf6d4`, EVID-058/run `31484369693`.
- Activation attempt: `83e6ea05e5c224a39f1d1d43aa17a3e58cf81c98`, EVID-059/run `31498696555` failed
  only on hosted JSON-output backpressure; it is not activation acceptance evidence.
- Corrected activation: `48472a1cba1ec706939f362ebdb1c4bea7f825eb`, EVID-060/run `31503631942`, exact
  26/26 jobs·326/326 steps success; it is not later implementation evidence.
- Implementation: `c3803acba1929921f23e4751679dc21d4bba9c0f`, EVID-061/run `31510689383`, exact
  26/26 jobs·326/326 steps success; four relation inventories each 687/687/0·69,597 bytes·SHA-256
  `363c4e165d7a051d68e45353e1ead697d9493f2322b61187a9ad83af8e7607b9`.
- Current: `121+5+1`, relation 11/12; REL-002 unchanged/locked. Manifest 10,776 bytes/SHA-256
  `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`; exact thirteen-file digest
  `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`.
- ADR-0030 Accepted, work completed, Q-013 Partial, Q-017 P1/open; no canonical facade.
- Required evidence separation: baseline terminal, activation exact head, implementation exact head, completion-documentation
  exact head and later terminal record are never reused across trees.
