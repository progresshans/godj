# ADR-0057: SQLite Retained-connection Terminal Quarantine

- 상태: Proposed
- 날짜: 2026-09-05
- 관련 work/contract: GDJ-0056, Q-019, REL-007/008
- 보완하는 ADR: ADR-0030

## 맥락

ADR-0030은 SQLite raw transaction의 rollback과 `driver.ErrBadConn` discard를 모두 확인하지 못하면 `*sql.Conn.Close`를
호출하지 않고 Backend-private set에 보유합니다. Pool이 열려 있을 때 Close하면 아직 active일 수 있는 raw transaction을 다음
borrower에게 돌려줄 수 있기 때문입니다. Explicit `Backend.Close`는 `sql.DB.Close`로 pool을 먼저 봉인하고 retained handles를
drain하므로 no-reuse 자체는 안전합니다.

현재 state의 `accepting()`은 lease가 아닌 snapshot이고 `retain()`은 Backend가 닫힐 때까지 계속 append합니다. Long-lived
Backend에서 fault가 반복되면 physical connection, transaction lock과 driver resource가 무제한 누적될 수 있습니다. 첫 fault 뒤에도
Query/CRUD/Atomic/migration entry는 이 상태를 모르며 caller의 “reconciliation 전 재호출 금지”를 runtime이 집행하지 않습니다.

## 결정 기준

- Unconfirmed raw transaction을 pool에 절대로 돌려주지 않을 것
- Long-lived sequential fault에서 retained resource를 hard bounded할 것
- Existing outcome marker, cause chain, panic exact rethrow와 no-retry를 보존할 것
- Context cancellation과 concurrent Close에서 deadlock/leak가 없을 것
- Error path에서 context 없는 implicit pool Close나 durable outcome 추측을 하지 않을 것
- Public API와 non-SQLite backend 변경을 최소화할 것
- 새 I/O rejection과 already-admitted operation의 경계를 검증 가능하게 만들 것

## 고려한 선택지

### 선택지 A — 현재 unbounded retention 유지

No-reuse 의미는 안전하지만 sequential fault마다 resource와 lock이 누적됩니다. Q-019 P1을 해결하지 못합니다.

### 선택지 B — 첫 retain에서 즉시 `sql.DB.Close`와 drain

물리 resource를 빠르게 회수하고 모든 DB entry를 닫는 장점이 있습니다. 그러나 `DB.Close`는 context를 받지 않고 driver/in-flight
operation을 기다릴 수 있어 원래 오류 반환을 무기한 막을 수 있습니다. Named in-memory database는 마지막 connection close와 함께
사라질 수 있고, Close와 retain의 단일 owner/completion을 새로 설계해야 합니다. 이번 최소 packet에는 채택하지 않습니다.

### 선택지 C — Retention cap만 두고 cap 초과 connection을 Close

Pool이 봉인되기 전 unconfirmed raw connection을 Close하면 재풀링 위험이 있어 안전하지 않습니다. Cap 초과 뒤 pool을 강제로
닫는 방식은 선택지 B의 대기/ownership 문제를 다시 만듭니다.

### 선택지 D — Shared raw admission + terminal quarantine + explicit Close

Retention을 만들 수 있는 `AtomicRelation`과 `CoordinatedAtomic`이 physical connection acquire 전에 하나의 context-aware admission을
얻습니다. 한 operation만 retain 가능하므로 hard maximum은 1입니다. 첫 retain은 같은 private state에서 terminal quarantine을
게시해 새 Backend I/O와 기다리는 raw operation을 거부합니다. Pool seal과 drain은 기존 explicit Close 순서를 유지합니다.

동일 SQLite Backend의 raw writer concurrency는 local gate에서 직렬화되지만 SQLite의 one-writer `BEGIN IMMEDIATE` 의미와 맞고,
대기는 context cancellation을 존중합니다. 채택합니다.

## 결정

1. Quarantine trigger는 `CodeCommitOutcomeUnknown` 같은 error code가 아니라 rollback과 physical discard가 모두 미확인이라
   connection을 실제 retain하는 사건입니다. Raw BEGIN failure, mutation 전 callback/resource failure와 panic cleanup도 포함합니다.
2. `AtomicRelation`과 `CoordinatedAtomic`은 같은 Backend-private context-aware one-operation admission을 사용하며 admission 뒤에만
   `database.Conn`을 호출합니다. Success/confirmed cleanup은 release하고 waiter를 깨웁니다.
3. 첫 retain은 retained connection을 exact one 저장하고 state를 terminal quarantine으로 바꿉니다. 이후 waiter와 새 raw call은
   connection acquire/callback 전에 실패합니다.
4. Backend의 Query, Exec, SQLiteVersion, CRUD, Atomic, relation/coordinated transaction, migration/history와 pre-created migration
   session의 새 I/O는 common availability state를 확인합니다. Quarantine 뒤 새 admission은 additive stable
   `backend_error/backend_recovery_required`로 실패합니다.
5. `invalid_plan`, `transaction_outcome_unknown`, `commit_outcome_unknown`은 각각 caller-plan, pre-COMMIT termination, literal
   COMMIT durability를 뜻하므로 quarantine rejection에 재사용하지 않습니다. Trigger operation의 기존 outer marker는 유지하고
   recovery-required marker는 cause chain에서 도달 가능하게 합니다.
6. Availability check를 통과해 이미 시작된 operation과 open rows/transaction은 강제 취소하지 않습니다. 보장 경계는 quarantine
   linearization 뒤의 새 admission입니다.
7. Quarantine은 implicit `sql.DB.Close`, retry, reopen 또는 durable-state reconciliation을 수행하지 않습니다. Caller는 외부 확인 뒤
   `Backend.Close`하고 fresh Backend를 열어야 합니다.
8. Explicit Close는 pool을 먼저 봉인하고 retained exact-one handle을 terminally Close합니다. Close와 retain race에서 pre-seal
   retained handle은 drain되고 post-seal retain은 pool이 봉인된 뒤 Close됩니다. Sequential idempotence와 existing error joining을
   보존합니다.
9. Quarantine은 Backend instance-local입니다. 같은 SQLite 파일을 사용하는 다른 Backend/process를 자동 중단하지 않습니다.
   Named in-memory database는 explicit Close 뒤 데이터가 소멸할 수 있으므로 durable reconciliation 가능성을 주장하지 않습니다.

## 결과

- Long-lived Backend에서 sequential cleanup fault가 반복되어 retained slice가 자라는 경로가 사라집니다.
- Caller는 panic recovery나 marker 없는 raw-BEGIN cleanup fault 뒤에도 다음 I/O에서 stable operational 상태를 알 수 있습니다.
- 같은 Backend의 relation/coordinated raw operation은 DB connection을 점유하기 전 local serialization을 거칩니다.
- Public 변화는 additive query error code 하나이고 Backend/DB interfaces나 health method는 추가하지 않습니다.
- Reconciliation command/token과 arbitrary in-flight cancellation은 제공하지 않습니다.

## 의도적으로 결정하지 않은 것

- Generic `Atomic`, migration lifecycle 또는 PostgreSQL의 모든 unknown outcome에 대한 blanket quarantine
- Public health snapshot, metric registry, configurable cap/fairness, automatic shutdown/reopen
- Driver가 멈춘 explicit Close의 context-aware termination
- Already-admitted query/rows/transaction 강제 취소
- SQLite migration discard helper의 별도 physical-retention policy
- 같은 file을 사용하는 다른 Backend/process의 health 전파
- Named in-memory database의 Close 이후 durable reconciliation

## 검증

- First unconfirmed cleanup 뒤 retained exact 1, second sequential raw call connection/callback/SQL 0
- Normal first operation 뒤 waiter 진행, canceled waiter callback/connection 0, future call 정상
- First fault와 waiter/Query/CRUD/Atomic/migration entry race에서 post-linearization new-I/O 0
- Raw BEGIN, pre-mutation, mutation-possible, literal COMMIT와 panic cleanup의 quarantine trigger/outer marker matrix
- Confirmed rollback/discard/success는 quarantine하지 않음
- Explicit Close의 database-first drain, retain-vs-Close, exact-once handle close와 idempotent/concurrent race
- Existing REL-007/008와 cooperative system-state SQLite flows의 normal/race/CGO-disabled regression
