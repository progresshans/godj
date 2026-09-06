# ADR-0048: Database-Coordinated System State and Shared CSRF Key Ring

> 결정 이유를 보존한 기록이다. 현재 API·지원 범위는 [현행 아키텍처](../ARCHITECTURE.md)와
> [구현 현황](../status/IMPLEMENTATION_MATRIX.md)를 따른다. 옛 내부 파일 구성·단계별 검증 절차는 현재 호환 요구가 아니다.
> 당시의 전체 기록과 실행 증거는 [고정 원문](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)에 있다.

- 상태: Accepted
- 날짜: 2026-08-26
- 관련 work/contract: [GDJ-0046](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/work/0046-database-coordinated-multi-runtime-system-state-and-shared-csrf-keys.md),
  SYS-013..020, Q-020
- 확장하는 ADR: [ADR-0047](0047-explicit-single-runtime-system-state.md)
- 대체하는 ADR: 없음
- 후속 수정: [ADR-0056](0056-explicit-operator-provisioning-and-open-existing.md)이 concurrent implicit bootstrap observation을
  explicit cooperative provision-once로 current-only 대체

## 맥락

ADR-0047은 current migration 기반 durable credential/session/audit와 sequential restart를 검증했지만 DB/schema당 하나의 live Runtime을
operator precondition으로 두었습니다. `systemstate.Runtime`의 mutex는 한 Go object 안에서만 writer를 직렬화하고, SQLite ordinary
transaction은 deferred이며 PostgreSQL ordinary transaction은 READ COMMITTED입니다. 따라서 두 Runtime이 같은 DB를 쓰면 credential
bootstrap, session create/capacity/touch/rotate, audit prune와 Article read-modify-write의 check와 mutation 사이에 다른 Runtime이 들어올
수 있습니다.

Session digest는 이미 versioned domain-separated SHA-256으로 저장되고 raw bearer는 persistence/diagnostic에서 제외됩니다. 문제는 digest
도입 여부가 아니라 여러 transaction이 같은 digest와 capacity를 어떤 database authority로 선형화하는가입니다. 마찬가지로 현재 CSRF
token은 process-local random MAC key를 사용하므로 서로 다른 Runtime이 발급한 정상 token을 검증하지 못합니다.

## 결정 기준

- 같은 SQLite file/PostgreSQL schema의 cooperative Runtime 사이 durable correctness
- callback once, rollback과 commit-outcome-unknown no-retry
- current Schema IR/definition/digest/generated ABI와 sessions.Store 보존
- migration/runtime lock ordering과 backend resource cleanup
- secret-free configuration/error/diagnostic surface
- staged deployment key rotation과 bounded verification work
- 실제 두 process와 두 backend에서 결정적으로 검증 가능한가

## 고려한 선택지

### 선택지 A — General Unique/Integer/revision/CAS IR을 먼저 도입

Session digest constraint와 conditional update를 일반 모델 기능으로 제공할 수 있고 non-cooperative writer에도 강합니다. 그러나 field/constraint
IR, definition codec/digest, historical state, codegen, SQLite remake와 PostgreSQL catalog까지 동시에 확장합니다. Audit capacity, Article
transaction과 shared CSRF key는 이것만으로 해결되지 않습니다.

### 선택지 B — Backend coordinated-atomic SPI와 injected CSRF key ring

Backend가 같은 transaction 안에서 database-scoped fence를 얻은 뒤 callback을 한 번 실행합니다. PostgreSQL은 schema/domain scoped
advisory transaction lock, SQLite는 pinned connection의 `BEGIN IMMEDIATE`를 사용합니다. `systemstate.Runtime`의 모든 writer와 final
startup inspection을 같은 coordination domain으로 보내면 current schema와 Store API를 바꾸지 않고 cooperative multi-runtime을 선형화할 수 있습니다.
Opaque active/validation key ring은 browser-facing CSRF token을 deployment 사이에서 검증하게 합니다.

### 선택지 C — Process mutex를 유지하고 collision/error만 감지

Duplicate/corrupt state를 fail-closed할 수는 있지만 lost update, capacity overshoot와 cross-process CSRF rejection을 예방하지 못합니다.
운영자에게 one-runtime을 계속 요구하므로 Q-020의 다음 답이 되지 않습니다.

## 결정

선택지 B를 bounded prototype 방향으로 채택합니다.

1. `db`에 additive coordinated-atomic interface를 둡니다. Fence acquisition은 callback보다 먼저 완료되어야 하고, acquisition 전
   error/cancellation은 callback 0회, 성공 시 callback 정확히 1회입니다.
2. PostgreSQL은 configured schema와 exact `godj/postgres/coordinated-atomic/v1` domain을 length-delimited hash로 묶어 blocking
   transaction advisory lock을 얻습니다. Migration revision-lock domain과 key를 재사용하지 않고 한 transaction은 둘 중 하나만
   얻습니다. Nested/cross-domain backend transaction은 caller contract에서 금지합니다.
3. SQLite는 pinned physical connection에서 `BEGIN IMMEDIATE` 후 callback과 literal COMMIT/ROLLBACK을 실행합니다. Backend는
   BUSY/LOCKED acquire를 자동 retry하지 않고 configured driver busy timeout만 기다리며 acquire 실패는 callback 0회입니다.
4. Ordinary `db.Atomic` 의미는 바꾸지 않습니다. `systemstate.Backend`만 coordinated capability를 요구하고 Runtime mutex는 local
   contention optimization으로 축소합니다.
5. Final credential/readiness/bootstrap, session Create/Touch/Rotate/Delete/capacity/reap, audit history/append/prune와 Article mutation은
   하나의 database/schema coordination domain을 공유합니다.
6. 같은 DB/schema에 참여하는 cooperative Runtime은 normalized `SessionLimits`, `MaxSessions`, `AuditCapacity`, absolute/idle
   lifetime policy와 compatible UTC clock basis가 동일한 deployment profile을 사용해야 합니다. 이 값은 DB에 persist되지 않으므로
   동일성은 operator precondition이며 이번 schema/API가 서로 다른 값을 감지하거나 협상한다고 주장하지 않습니다.
7. Existing digest-only storage와 sessions.Store signature는 유지합니다. General Unique/Integer/CAS IR은 SYS-013..020을 위해 추가하지
   않습니다.
8. Concurrent operation은 DB fence acquisition order로 선형화합니다. Logout-first는 later rotate를 막지만 rotate-first 뒤 old-ID logout이
   replacement family를 철회하지는 않습니다. Strong family revocation은 별도 state/contract입니다.
9. `web/sessionauth`는 active key 하나와 bounded validation key set을 가진 opaque immutable CSRF key ring을 선택적으로 받습니다.
   Active key는 새 MAC만 만들고 verification은 모든 configured key를 bounded constant-time comparison합니다.
10. Key ring이 없을 때의 process-local CSPRNG는 single-runtime/development behavior로 남습니다. Multi-runtime actual과 제품 composition은
   같은 explicit ring을 주입합니다.
11. Callback, transaction, acquire 또는 literal commit은 framework가 자동 retry하지 않습니다. Acquire failure는 새 public code 없이
    context 또는 backend cause를 보존하고 rollback 불확실성은 기존 transaction-outcome-unknown, commit error는 기존
    commit-outcome-unknown과 reconciliation ownership을 유지합니다.

SYS-013 product actual은 두 독립 backend handle에서 획득 전 callback 0회, 획득 뒤 callback 1회, 실제 cancellation/rollback과
SQLite deferred-constraint COMMIT failure를 관측합니다. 공개 SQLite driver 경계에서 실제로 유발할 수 없는 rollback terminal
uncertainty를 conformance adapter가 성공 transaction 뒤 합성하지 않으며, 그 분류와 physical cleanup은 `db/sqlite`의
package-private fault test가 소유합니다. 실제 distinct-process barrier와 restart는 SYS-020이 별도로 소유합니다.

## 결과

- DB가 여러 cooperative Runtime의 correctness owner가 되고 process mutex는 유일한 fence가 아니게 됩니다.
- System physical schema, migration definition, digest와 generated source를 재기준화하지 않고 multi-runtime 기반을 만들 수 있습니다.
- SQLite는 database-wide writer serialization이라 더 강한 contention을 가질 수 있습니다. PostgreSQL은 schema 단위로
  직렬화됩니다.
- 모든 system-state/Article writer가 coarse fence를 공유하므로 첫 구현은 correctness 우선이며 throughput 최적화는 측정 뒤 분리합니다.
- Shared key ring은 distribution/provider가 아니라 already-loaded material 경계이므로 deployment가 같은 bounded set을 주입해야 합니다.
- Session/capacity/audit policy 값도 DB authority가 아니라 deployment가 동일한 normalized profile로 주입해야 합니다. Persisted
  configuration negotiation은 이 ADR의 지원 범위가 아닙니다.

## 현재 증거 전달

SYS-020은 실제 PostgreSQL 두 프로세스 검증의 profile, source binding, secret-free facts를 사용한다.
제품 관찰자는 oracle/expected를 읽지 않는다. Consumer는 canonical JSON, checksum, fingerprint와 현재 source binding을 검증한다.
Missing, trailing/oversize, wrong fingerprint, stale source와 failure fact는 actual publication 전에 거부한다.

GDJ-0057부터 PostgreSQL producer가 성공한 같은 CI 실행의 artifact를 consumer에 전달한다.
Checkout, repository, run ID와 attempt를 검증하고, source binding은 실행 스크립트와 관찰자도 포함한다.
매 변경의 live actual을 저장소에 커밋하거나 과거 checked bytes와 cmp하는 절차는 폐기한다.
과거 actual은 codec fixture이며 현재 검증의 근거가 아니다. Artifact는 CI에서 90일 보존하므로 장기 milestone 인용에는
source와 run URL을 남기고 만료 전에 필요한 산출물을 별도로 보존한다.

[당시 구현·실행 기록](https://github.com/progresshans/godj/blob/da1bfc524c4f205075fc7fac7f00b437473a5e1f/docs/adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)은 Git에 보존한다.

## 의도적으로 결정하지 않은 것

- Direct SQL/non-cooperative writer, DB UNIQUE/index와 general CAS/row-lock Query AST
- Live migration과 serving 동시 실행, leader election과 distributed cache/session backend
- Session family/generation/tombstone과 rotate-first family-wide logout
- Runtime별 session/capacity/audit policy persistence, mismatch detection과 online negotiation
- Persistent key DB, KMS/Vault integration, automatic distribution와 unlimited history
- Cookie/JWT/OAuth/refresh/password-reset/Admin-notice key를 하나의 key type으로 통합
- Production topology, multi-DB/router와 MySQL/MariaDB/Oracle
- Existing opaque secret 값 전체에 arbitrary invalid fmt verb hardening을 소급 적용하는 repository-wide formatting 정책. 새 key ring과
  Article internal startup Config는 이 방어까지 포함하지만 documented ordinary diagnostic과 실제 product callsite 밖의 기존 타입은
  별도 defense-in-depth 후보입니다.
