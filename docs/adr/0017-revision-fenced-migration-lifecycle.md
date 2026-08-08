# ADR-0017: Migration lifecycle은 각 migration transaction에서 recorder revision을 검증한다

- 상태: Proposed
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0017, MIG-047..MIG-056, Q-012
- 대체하는 ADR: 없음

## 맥락

[ADR-0015](0015-recorder-backed-applied-state.md)는 durable recorder를 read-only snapshot으로
읽고 known history를 검사하는 경계를, [ADR-0016](0016-historical-project-state-reconstruction.md)은
loaded migration definition에서 historical `ProjectState`를 pure replay하는 경계를
채택했습니다. [ADR-0013](0013-immutable-migration-planner.md)의 Planner와
[ADR-0014](0014-migration-plan-execution-atomic-reverse.md)의 `ExecutePlan`을 이어 붙이면 단일
프로세스에서 필요한 계산과 실행 요소는 모두 존재합니다.

그러나 현재 경계는 다음 순서의 최신성을 보장하지 않습니다.

```text
LoadAppliedState → CheckHistory → Reconstruct → Plan → ExecutePlan
```

Recorder snapshot을 읽은 뒤 다른 process가 migration을 commit하면 첫 실행자는 stale
`AppliedState`, `ProjectState`와 plan으로 DDL을 시작할 수 있습니다. Recorder read와 실행을
한 편의 API로 감싸는 것만으로는 이 TOCTOU가 사라지지 않습니다. 하나의 outer transaction은
문제를 숨길 수 있지만 ADR-0014가 검증한 migration별 commit과 실패 시 last durable state를
깨뜨립니다.

GDJ-0017은 먼저 MIG-047..056으로 Django의 fresh/prefix/target/reverse/failure/restart lifecycle
의미를 exact contract로 잠그고, GoDj-specific revision fence는 제품 source와 분리된
`conformance/lifecyclefence/**` feasibility spike로 검증합니다. 따라서 이 ADR은 spike 감사 전
`Proposed`이며 public API 지원을 뜻하지 않습니다.

## 결정 기준

- ADR-0014의 migration별 commit, failed-step rollback과 last durable state 보존
- Recorder identities와 freshness token을 같은 atomic snapshot에서 읽음
- Stale 실행이 current step의 첫 DDL/recorder write 전에 fail-closed함
- Unknown legacy recorder identity를 포함한 history 변화도 감지함
- Pure reconstructor/planner와 backend I/O 경계를 유지함
- 기존 `AppliedMigrationReader`, `AtomicBackend`, `Transaction`과 external fake의 source
  compatibility를 가능한 한 보존함
- Unsupported backend가 unfenced execution으로 조용히 fallback하지 않음
- Conflict에서 자동 retry해 caller target/definition 의도를 재해석하지 않음
- Context cancellation, rollback과 concurrent contender에서 resource leak이 없음
- Public loader/CLI/data callback ABI를 성급하게 freeze하지 않음

## 고려한 선택지

### Recorder snapshot 뒤 process-local mutex만 사용

한 프로세스의 goroutine은 직렬화할 수 있지만 다른 process와 별도 executable의 writer는
막지 못합니다. Snapshot과 첫 DDL 사이 stale window도 그대로입니다.

### Lifecycle 전체를 하나의 outer database transaction으로 실행

Read부터 모든 DDL/recorder update를 한 transaction에 넣으면 snapshot 결속은 단순해질 수
있습니다. 하지만 중간 실패 때 앞선 migration까지 rollback되어 Django reference와
ADR-0014의 per-migration durable prefix를 바꿉니다. 장시간 DDL transaction과 lock scope도
불필요하게 커집니다.

### Execution 전에 revision을 한 번만 비교

첫 step 이전 conflict는 감지하지만 migration별 commit 사이에 다른 writer가 들어오면 다음
step이 stale plan으로 실행됩니다. Read-then-compare가 transaction 밖에 있으면 compare와 DDL
사이에도 다시 TOCTOU가 생깁니다.

### 각 migration transaction 안에서 expected revision 검증

History identities와 opaque revision을 같은 snapshot에서 읽고, 각 step transaction이 첫
DDL/write 전에 expected revision을 검증합니다. Successful schema/recorder mutation과 다음
revision을 같은 transaction에 결속하면 outer transaction 없이 step 사이 conflict도
fail-closed할 수 있습니다. Backend capability와 token handoff 복잡도가 추가되므로 spike가
필요합니다.

### Database advisory lock 또는 장기 lease를 lifecycle 전체에 유지

지원 backend에서는 직렬화 수단이 될 수 있지만 SQLite portability, lock loss, lease renewal,
fairness와 crash semantics를 한꺼번에 결정해야 합니다. GDJ-0017의 최소 stale-write fence보다
범위가 크며 revision conflict가 제공하는 명시적 fail-closed 결과를 대체하지 않습니다.

## 제안 결정

제품 구현으로 승격할 경우 migration lifecycle은 recorder history에 결속된 opaque revision
fence를 사용합니다.

```text
HistorySnapshot{identities, revision}
→ validate history + reconstruct state + plan
→ for each planned migration:
   begin migration transaction
   validate expected revision in transaction before DDL/recorder write
   execute operations and recorder mutation
   atomically bind the committed successor revision
   commit exactly one migration
```

- `revision`은 caller가 해석·정렬·증가시키지 않는 opaque equality token입니다.
- Snapshot의 identities와 revision은 같은 database view에서 얻어야 하며 unknown recorder
  identity도 token 의미에 포함합니다.
- Revision validation은 각 migration transaction 내부에서 첫 schema/recorder mutation 전에
  일어납니다. Transaction 밖의 check는 안전성 근거가 아닙니다.
- Successful step의 recorder mutation과 successor revision은 같은 transaction에 결속됩니다.
  다음 step은 그 committed successor만 expected revision으로 사용합니다. Exact interface는
  spike 뒤 결정합니다.
- Revision mismatch는 current step의 durable mutation을 0으로 만들고 structured conflict를
  반환합니다. Orchestrator의 returned state는 이전에 성공적으로 commit된 마지막
  `ProjectState`입니다.
- Plan 전체를 감싸는 outer transaction은 사용하지 않습니다. 이전 step의 commit은 conflict나
  뒤 step 실패 뒤에도 durable합니다.
- Conflict를 자동 retry하지 않습니다. Caller가 새 snapshot에서 history check,
  reconstruction과 planning을 다시 수행해야 합니다.
- Fence capability가 없는 backend는 legacy unfenced execution으로 fallback하지 않고
  structured capability error로 거부합니다.

Lifecycle coordinator 후보는 loaded immutable migration definition, snapshot reader,
`StateReconstructor`, `Planner`와 step executor를 조합합니다. Pure components에 DB handle이나
revision logic을 넣지 않습니다. Existing ports를 embed해 모든 fake를 즉시 깨뜨리기보다 별도
revision-capable backend/transaction port를 우선 검증하지만 public type/function 이름과
capability discovery shape는 아직 결정하지 않습니다.

## 오류와 state 경계

- Initial snapshot read/validation error는 transaction/DDL 전에 반환합니다.
- Known inconsistent history는 기존 `inconsistent_applied_history` 의미를 유지합니다.
- Revision mismatch와 fence unsupported는 semantic graph/history error와 구분되는 structured
  conflict/capability error 후보입니다. Final category/code는 spike에서 정합니다.
- Current step의 validation, operation, recorder 또는 commit 실패는 existing rollback 의미를
  보존하고 last durable state를 반환해야 합니다.
- Cancellation은 새 step의 첫 write를 막거나 active transaction을 best-effort rollback하고
  resource를 해제해야 합니다. Cleanup semantics는 existing executor 계약을 따릅니다.
- Schema drift가 recorder revision 없이 발생한 경우는 이 fence가 감지한다고 주장하지
  않습니다.

## 결과와 비용

- Recorder snapshot 이후 다른 writer가 commit한 stale lifecycle이 DDL을 시작하는 경로를
  각 step boundary에서 닫을 수 있습니다.
- Per-migration commit과 restartable durable prefix를 보존합니다.
- Unknown legacy history 변경도 token mismatch로 감지할 수 있습니다.
- Backend는 atomic history snapshot, transactional revision validation과 successor binding을
  구현해야 합니다. SQLite recorder metadata/storage와 failure recovery 설계 비용이 생깁니다.
- 매 migration마다 revision validation I/O와 contention이 추가됩니다.
- Automatic retry가 없으므로 conflict를 받은 caller는 lifecycle을 명시적으로 다시 시작해야
  합니다.

이 결과는 GDJ-0017 spike가 안전성 invariant와 source compatibility를 통과한 뒤에만
Accepted로 승격합니다. Spike가 transaction-before-DDL validation 또는 successor binding을
입증하지 못하면 이 제안은 수정하거나 Rejected합니다.

## 의도적으로 결정하지 않은 것

- Public lifecycle coordinator/request/target type과 zero-value semantics
- Migration file/source loader, operation codec/version과 module discovery
- Global CLI/project-binary version handshake와 management commands
- Data migration callback/plugin ABI와 historical app registry
- Long-lived lock/lease, fairness, leader election과 distributed database protocol
- Automatic retry/backoff와 conflict merge
- Process kill/crash repair, schema/recorder reconciliation와 drift introspection
- Replacement/squash/merge/fake/fake-initial, optimizer와 migration conflict resolution
- PostgreSQL advisory lock, MySQL/MariaDB/Oracle와 multi-DB router
- Relations/`real_apps`와 historical relation rendering

## 검증

GDJ-0017은 제품 source를 바꾸지 않고 다음을 확인합니다.

- MIG-047..056 exact oracle: fresh/prefix/no-op/named/zero/unknown/history/failure/restart
- Phase 047..053/056 `commit`, 054 `evaluation`, 055 `rollback`
- MIG-054는 public command orchestration의 explicit
  `loader.check_consistent_history(connection) → target/plan → migrate`이며
  `plan_invoked=false`, transaction/DDL/write 0; `migrate()` 자체의 암묵적 check로 표현하지 않음
- MIG-056은 temporary file database close/reopen과 fresh connection/loader/executor를 사용해
  in-memory state/cache 재사용을 restart로 오인하지 않음
- MIG-051/052 reverse는 abstract step outcome과 final schema/recorder만 잠그고 physical
  transaction topology는 DEV-0001/ADR-0014에 남겨 새 deviation을 만들지 않음
- Ninth set 10 `oracle_locked`, product runner exit 2/no actual와 기존 8-set product 회귀
- Stale initial token에서 transaction은 first DDL/recorder write 전에 실패하고 mutation 0
- Migration step 사이 competing commit에서 prior own step만 durable, current/tail mutation 0
- 같은 revision의 simultaneous contender 중 최대 하나가 step을 commit하고 duplicate 없음
- Successful step과 successor token 사이에 stale-acceptance window가 없음
- Unsupported capability의 structured fail-closed와 auto retry 0
- Cancellation/error/rollback 뒤 connection/transaction release와 fresh success
- 두 connection/process, repeated/race run과 fault injection
- Existing migration별 execution/recorder/reconstructor/planner tests와 external fake compile 회귀

상세 completion gate와 수정 허용 경로는
[GDJ-0017](../../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)에
기록합니다.
