---
id: GDJ-0056
status: active
updated: 2026-09-05
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "003afee4524a0294ada8f02c140781f3e1751a5c"
depends_on: ["GDJ-0030", "GDJ-0046", "GDJ-0055"]
contracts: ["Q-019"]
allowed_paths:
  - "query/error.go"
  - "query/error_test.go"
  - "db/relation.go"
  - "db/sqlite/**"
  - "conformance/systemstate/**"
  - "conformance/projectoperatorproduct/attestations/**"
  - "conformance/internal/protocol/system_state_artifacts_test.go"
  - "conformance/README.md"
  - "docs/adr/0057-sqlite-retained-connection-terminal-quarantine.md"
  - "docs/adr/README.md"
  - "docs/ARCHITECTURE.md"
  - "docs/BACKEND_MATRIX.md"
  - "docs/COMPATIBILITY.md"
  - "docs/CONCURRENCY.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0056-sqlite-retained-connection-terminal-quarantine.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0056 — SQLite Retained-connection Terminal Quarantine

## 사용자에게 보이는 결과

SQLite raw transaction의 rollback과 physical discard를 모두 확인하지 못한 경우 GoDj는 해당 connection을 pool에 돌려주지
않는 데서 끝나지 않습니다. 같은 Backend의 새 DB I/O를 stable recovery-required 오류로 즉시 거부하고, 사용자가 durable
state를 외부에서 확인한 뒤 Backend를 닫고 새로 열도록 강제합니다. 한 Backend가 명시적으로 닫히기 전까지 보유하는
unconfirmed physical connection은 최대 1개입니다.

## 목표

- Accepted ADR-0030의 no-reuse, no-retry, outer outcome marker와 exact panic rethrow 의미를 보존합니다.
- Error code가 아니라 rollback/discard가 모두 미확인이라 실제 `retain`이 필요한 모든 경로에서 quarantine을 시작합니다.
- `AtomicRelation`과 `CoordinatedAtomic`의 retention-producing raw transaction admission을 하나의 context-aware lane으로
  직렬화해 retained connection을 hard maximum 1로 제한합니다.
- 첫 retain과 같은 mutex 선형화 지점에서 Backend를 terminal recovery-required로 전환하고 기다리는 raw operation을 깨웁니다.
- Quarantine 뒤 시작하는 Query/CRUD/transaction/migration/history/version I/O를 callback, connection acquire와 SQL 전에
  stable typed error로 거부합니다.
- 이미 admission을 얻은 in-flight operation은 강제 취소하지 않습니다. Quarantine은 새 admission을 차단하는 경계입니다.
- `Backend.Close`는 새 admission을 차단하고 pool을 먼저 봉인한 뒤 pre-seal retained handle을 exact once drain합니다.
  Already-admitted operation이 Close 반환 뒤 post-seal retain에 도달하면 그 operation이 pool-sealed handle을 즉시 Close합니다.
  Quarantine error path에서는 context 없는 implicit `sql.DB.Close`를 호출하지 않습니다.
- 정상 success/confirmed rollback/confirmed discard, context cancellation, callback panic과 concurrent `Close`의 기존 의미를
  회귀 없이 검증합니다.

## 비목표

- 모든 `CodeCommitOutcomeUnknown`을 자동 quarantine하는 일반 transaction 정책. 이번 trigger는 physical cleanup 미확인으로
  retention이 실제 필요한 SQLite raw path입니다.
- PostgreSQL 또는 다른 Backend의 health/recovery API, process-global poison registry, reconciliation token/command
- automatic reopen/retry, background cleanup, timeout 뒤 unsafe `Conn.Close`, forced durable outcome 추측
- public `Health()`/metric registry, configurable retention cap, pool-size 변경 또는 driver 교체
- SQLite migration raw-connection discard 정책의 별도 재설계, Schema IR/Query AST/migration format/generated ABI 변경
- Q-017, Q-022, Form/Admin generalization, destructive migration writer와 scaffold 기능
- 이미 admission된 arbitrary query/rows/transaction을 소급 취소하거나 rollback됐다고 주장하는 것
- 같은 SQLite 파일을 사용하는 다른 Backend instance/process의 자동 격리 또는 중단
- `OpenMemory` 상태를 Close 뒤 durable하게 reconciliation할 수 있다는 보장
- 같은 Backend의 raw transaction callback에서 `AtomicRelation`/`CoordinatedAtomic`을 동기 재진입하는 nested transaction,
  savepoint 또는 reentrancy detection

## 선행 조건과 기준 상태

- Baseline은 clean `003afee4524a0294ada8f02c140781f3e1751a5c`, tree
  `77d19c56d3e86d99e123c5ac3ea5436d949cb9a5`입니다.
- [ADR-0030](../docs/adr/0030-project-bound-protect-and-set-null-delete.md)은 unconfirmed cleanup connection을 Backend-private
  retained slice에 누적하고 explicit Close에서 pool-first drain하는 현재 계약입니다.
- [Q-019](../docs/OPEN_QUESTIONS.md#q-019--sqlite-unknown-outcome-retained-connection-resource-policy)는 long-lived
  Backend에서 이 보유량이 무제한 증가하는 P1 문제를 별도 work에서 결정하도록 요구합니다.
- `Open`은 retention state를 초기화하지만 현재 `accepting()`은 lease가 아닌 순간 snapshot입니다. `retain()`은 첫 fault 뒤에도
  계속 append하고 Query/CRUD/Atomic/migration entry는 retention state를 확인하지 않습니다.
- GDJ-0055 exact product source `0b5b6fc6...`, tree `dac6baa7...`의 local A/B/archive와 Hosted run `33899930122`
  79/79 jobs·797/797 steps는 terminal baseline입니다. 이번 source 변경은 그 증거를 재사용하지 않습니다.

## Django Reference / Contract

- 이 resource ownership과 Go `database/sql` pool 격리는 Django observable parity가 아니라 Go-specific decision authority입니다.
- 새 Django oracle/contract ID를 만들지 않고 Q-019와 existing REL-007/008, SYS transaction regression을 기준으로 합니다.
- `transaction_outcome_unknown`은 pre-COMMIT mutation-possible cleanup 미확인, `commit_outcome_unknown`은 literal COMMIT error라는
  기존 구분을 바꾸지 않습니다.
- Raw BEGIN/callback-0 cleanup 미확인과 panic cleanup도 outcome marker 유무와 무관하게 quarantine trigger입니다. Non-panic
  trigger는 기존 outer marker의 cause에서 recovery-required에 도달할 수 있습니다. Panic trigger는 원래 value를 exact
  repanic하므로 해당 호출에 marker를 덧붙이지 않고 이후 새 I/O에서 recovery-required를 관찰합니다.

## 설계와 가설

- Proposed [ADR-0057](../docs/adr/0057-sqlite-retained-connection-terminal-quarantine.md)이 ADR-0030의 retained-resource
  lifetime만 보완합니다.
- Additive stable code는 `query.CodeBackendRecoveryRequired = "backend_recovery_required"`이며 외부 표기는
  `backend_error/backend_recovery_required`입니다. Existing outcome code나 `invalid_plan`을 재사용하지 않습니다.
- Private retention state가 `open / quarantined / closing / sealed`와 one-operation admission ownership을 함께 관리합니다.
- Waiter는 context cancellation을 존중합니다. Release 뒤 normal successor는 진행하지만 first retain/quarantine 또는 Close 뒤에는
  connection을 얻거나 callback을 실행하지 않습니다.
- 유효한 context의 cancellation과 lifecycle transition이 경합하면 `closing/sealed → invalid_plan`,
  `quarantined → backend_recovery_required`, 그 외 open state에서만 `ctx.Err()` 순으로 결정합니다. 이미 취소된 context는
  healthy admission을 얻지 않습니다.
- Root Backend I/O와 pre-created migration session의 새 I/O도 같은 availability check를 거칩니다. 이미 시작된 operation은
  in-flight로 분류해 completion을 허용합니다.
- Retained count 1은 모든 retain-producing product path가 shared admission을 얻은 뒤에만 physical connection을 acquire한다는
  architecture test와 race test로 증명합니다.

## 구현 단계

### Phase A — Decision lock and regression-first state machine

1. ADR-0057과 exact trigger/admission/error/Close order를 Proposed로 고정합니다.
2. Sequential fault가 첫 retained connection 뒤 second connection/callback 없이 recovery-required가 되는 red test를 만듭니다.
3. Normal successor, canceled waiter, first-fault waiter, retain-vs-Close와 panic recovery race를 고정합니다.

### Phase B — Private lifecycle and common I/O gate

1. Context-aware shared raw admission과 terminal quarantine state를 구현합니다.
2. Additive stable recovery-required error를 root I/O와 pre-created migration session의 새 I/O 경계에 연결합니다.
3. Existing outer outcome marker/cause reachability와 no-retry를 보존합니다.

### Phase C — Fault, race and whole-backend verification

1. Query/Exec/CRUD/Atomic/AtomicRelation/CoordinatedAtomic/migration/history/version entry의 post-quarantine I/O 0을 검증합니다.
2. Retained count `<=1`, explicit pool-first Close, exact-once drain, single-owner concurrent Close race-safety와 no goroutine leak을
   검증합니다. Concurrent Close loser는 winner의 drain 완료를 기다리는 completion barrier가 아닙니다.
3. Existing SQLite relation/system-state flow와 normal/race/CGO-disabled/vet/generated-drift를 실행합니다.

### Phase D — Frozen integration and publication

1. Integrated docs에서 ADR-0030 historical behavior와 ADR-0057 current behavior를 구분하고 Q-019를 Resolved로 올립니다.
2. 이번 `db/**`·`query/**` 변경으로 stale해지는 system-state와 project-operator source-bound PostgreSQL checked attestation을
   각각 independent A/B와 byte comparison으로 재캡처하고 두 `SHA256SUMS`, artifact lock과 `conformance/README.md`를
   exact publication에 동기화합니다.
3. Frozen source에서 full local/386/relation/archive와 exact-head Hosted matrix를 한 번 실행합니다.

## 완료 조건

- [x] 첫 unconfirmed cleanup이 marker 유무와 무관하게 terminal recovery-required state를 게시
- [x] 같은 Backend의 이후 새 DB I/O가 connection/callback/SQL 전에 stable typed error로 종료
- [x] retention-producing raw operation의 context-aware shared admission과 retained count hard maximum 1
- [x] already-admitted operation 경계, canceled waiter와 Close race가 deterministic하고 race-clean
- [x] existing commit/transaction outcome marker, joined cause, no-retry, panic exact-value와 confirmed cleanup 의미 보존
- [x] explicit `Backend.Close`의 pool-first exact-once drain, one CAS owner와 sequential post-completion idempotence
- [x] affected normal/race/CGO-disabled/vet 및 required SQLite system-state/relation product gate
- [ ] system-state/project-operator 각각의 final source-bound A/B, full/386/relation/archive와 exact-head Hosted gate
- [ ] ADR/integrated docs/Q-019/status/evidence 동기화와 독립 P0..P3 audit

## 진행 기록

- [x] 조사: Q-019, ADR-0030, current retention/Backend/migration entry와 tests를 baseline에서 재감사
- [x] 설계/ADR: terminal quarantine + shared context-aware raw admission + explicit Close ownership을 Proposed로 선택
- [x] 구현: Phase A/B shared admission, terminal quarantine, common I/O gate, migration-session state preservation와 explicit
  Close ownership
- [x] 테스트: latest implementation tree의 affected normal/race/CGO-disabled/vet, generated drift, required SQLite product와
  독립 P0..P3 checkpoint review
- [ ] 문서와 인수인계

## 수정 파일

- `work/0056-sqlite-retained-connection-terminal-quarantine.md`: active 범위와 gate
- `docs/adr/0057-sqlite-retained-connection-terminal-quarantine.md`: Proposed retained-resource 결정
- `docs/{OPEN_QUESTIONS,ROADMAP}.md`, `docs/status/CURRENT.md`, `work/README.md`, `docs/adr/README.md`: activation index
- `query/{error.go,error_test.go}`: additive stable recovery-required code와 literal/classification lock
- `db/relation.go`, `db/sqlite/{backend,write,relation_transaction,coordinated_transaction,migration_*}.go`: caller contract,
  shared admission, common availability gate, Close ownership과 migration-session state preservation
- `db/sqlite/{relation_transaction,coordinated_transaction,migration_history,transaction_internal,quarantine_surface}_test.go`:
  trigger/waiter/Close/cause/state/surface/race regression

## 결정된 사항

- 2026-09-05: Q-019가 현재 유일한 P1 runtime correctness/resource-lifetime gap이고 GDJ-0055가 이를 즉시 후속으로
  명시했으므로 다른 breadth 기능보다 먼저 GDJ-0056으로 활성화합니다.
- 2026-09-05: 첫 retain에서 implicit `sql.DB.Close`를 실행하지 않습니다. Error path의 무기한 대기와 named in-memory DB
  소멸을 피하고 pool seal/drain은 explicit Close가 소유합니다.
- 2026-09-05: 단순 quarantine flag만으로는 already-passed concurrent raw calls가 임의 개수 retain할 수 있으므로 shared
  context-aware one-operation admission을 함께 요구합니다.
- 2026-09-05: `invalid_plan`, `transaction_outcome_unknown`, `commit_outcome_unknown`은 각각 다른 의미이므로 post-quarantine
  operational rejection에 재사용하지 않습니다.
- 2026-09-05: Stable operational rejection은 `query.CodeBackendRecoveryRequired = "backend_recovery_required"`로 확정합니다.
  Closed receiver와 nil context는 기존 `invalid_plan`, published closing/sealed는 closed `invalid_plan`, published quarantine은
  recovery-required, healthy open state의 cancellation은 `ctx.Err()`를 반환합니다.
- 2026-09-05: Close CAS winner 한 개만 pool-close/drain을 소유합니다. Concurrent loser는 winner completion을 기다리지 않으며
  double-close하지 않고 반환하고, 완료 뒤 sequential call만 idempotent입니다.
- 2026-09-05: Root migration entry와 pre-created revision session은 common lifecycle gate를 I/O 전에 확인합니다. Quarantine과
  healthy context cancellation은 Open/Ready session을 poison하지 않고, static input/state validation의 기존 우선순위도 보존합니다.

## 미결정/Blocker

- 현재 blocker는 없습니다.

## 테스트 증거

- Evidence ID:
  [EVID-180](../docs/status/TEST_EVIDENCE.md#evid-20260905-180--gdj-0056-sqlite-terminal-quarantine-implementation-checkpoint)
- Exact implementation source: `9f17528f1751db9cf1c0dda3435fc1bfbd7a6f19`, tree
  `271cf8d6df0f7a1a48eda9fe9fb46d1da456b1f2`
- Passed:
  - `go test ./query ./db/sqlite -count=1`
  - `go test -race ./db/sqlite -count=1`
  - `CGO_ENABLED=0 go test ./query ./db/sqlite -count=1`
  - `go vet ./query ./db/sqlite`
  - `make generate-check`
  - `go test -count=1 ./conformance/relationdeleteproduct ./conformance/migrationrelationproduct ./conformance/systemstate/product`
  - `go test -count=1 ./conformance/projectoperatorproduct` (`245.002s`)
  - Close/seal race focused normal count 20과 race count 5
  - `git diff --check`
- Independent review: state/admission/Close, migration-session/cause/surface coverage에서 P0/P1/P2=0; 발견된 P2/P3는 현재
  implementation/test tree에 반영했습니다.
- Not run at this checkpoint: independent PostgreSQL A/B, attestation publication, full `make ci`, Linux/386,
  relation inventory/archive, exact-head Hosted

## 위험과 rollback

- Shared admission은 same-Backend raw writer의 대기 위치를 SQLite `BEGIN IMMEDIATE` 앞 local gate로 옮깁니다. Context
  cancellation, no retry와 cross-Backend database coordination 의미를 별도로 검증합니다.
- Availability snapshot 뒤 quarantine되는 already-admitted non-raw operation은 완료될 수 있습니다. 이를 “quarantine 뒤 모든
  CPU instruction/I/O 0”으로 과장하지 않습니다.
- Pool seal 전 poisoned `Conn.Close`는 active raw transaction을 재풀링할 수 있으므로 cap overflow를 Close로 해결하지 않습니다.
- Public code 추가는 additive지만 caller branching surface가 되므로 ADR/compile/error tests 없이 이름을 바꾸지 않습니다.
- Same-Backend raw callback의 동기 nested call은 shared admission을 스스로 기다릴 수 있습니다. 이번 packet은 savepoint나
  reentrancy detection을 제공하지 않으며 caller context cancellation로 bounded되어야 합니다.
- Source-bound attestation과 relation inventory는 source freeze 전 반복 캡처하지 않습니다.

## 다음 정확한 작업

Clean implementation source `9f17528f...`를 기준으로 system-state와 project-operator PostgreSQL checked attestation을 각각
independent A/B로 재캡처하고 exact publication descendant를 검증합니다.

## 결과와 인수인계

GDJ-0056은 active입니다. 다른 제품 기능, residual probe branch와 global CI topology를 이번 packet에 섞지 않습니다.
