# Revision-fence candidate design note

## Candidate state

Spike metadata는 singleton row의 `epoch`, non-negative `revision`, 32-byte
`history_fingerprint`를 사용합니다. 이 table/encoding은 저장 형식 제안의 실현 가능성을
시험하기 위한 것이며 public contract가 아닙니다.

Snapshot은 하나의 SQLite read transaction에서 metadata와 전체 sorted recorder identity를
읽습니다. Ready metadata의 fingerprint와 실제 identities가 다르면 snapshot 자체를 반환하지
않습니다. Metadata가 아직 없으면 canonical identities와 `uninitialized` token을 반환합니다.

## Initialized transition

```text
BEGIN IMMEDIATE
read current identities and metadata
require current fingerprint == stored fingerprint
require expected epoch/revision/fingerprint == current metadata
CAS metadata to revision+1 and expected successor fingerprint
perform one step's schema and recorder mutation
require actual successor identities/fingerprint == claimed successor
COMMIT
```

`BEGIN IMMEDIATE`가 database-wide writer reservation을 얻으므로 검증 이후 다른 writer가
끼어들 수 없습니다. Conditional update는 equality token 검증과 successor generation을 같은
durable transition으로 만듭니다. Domain mutation이나 최종 fingerprint 검사가 실패하면
metadata claim도 함께 rollback됩니다.

Initialized singleton row에 한해서는 conditional `UPDATE`를 첫 statement로 사용하는
deferred transaction도 row write-lock을 얻는 CAS가 될 수 있습니다. 그러나 bootstrap의
table-absence 확인과 history 재검증까지 하나의 규칙으로 설명하고 SQLite busy-snapshot
분기를 피하기 위해 이 spike는 pinned `BEGIN IMMEDIATE`를 최소 후보로 택합니다. General
application transaction의 DSN을 바꾸지 않고 migration 전용 connection/connector에만 적용해야
합니다.

## Uninitialized transition

```text
BEGIN IMMEDIATE
require metadata is still absent
read current canonical identities
require fingerprint == expected uninitialized snapshot fingerprint
create singleton metadata with a fresh epoch and successor revision
perform one step's schema and recorder mutation
verify claimed successor fingerprint
COMMIT
```

Stale이면 metadata creation 전에 반환하고 transaction을 rollback합니다. 두 bootstrap
contender는 SQLite writer reservation으로 직렬화되어 하나만 initialize할 수 있습니다. Fresh
epoch은 제품 승격 시 collision-resistant하게 생성해야 합니다.

이 transition은 protocol 도입 전에 revision을 갱신하지 않은 writer의 completed ABA를
구별할 수 없습니다. Existing deployment adoption에는 old writers를 멈춘 exclusive cutover와
초기 snapshot 검증이 필요합니다. Opportunistic silent adoption으로 이 제약을 숨겨서는 안
됩니다.

## Per-step handoff

각 성공 step은 metadata, schema, recorder를 한 transaction에서 commit하고 successor snapshot을
반환합니다. Coordinator 후보는 그 successor만 다음 step의 expected token으로 사용합니다.
다른 writer가 step 사이 commit하면 이전 own step은 durable한 채 다음 CAS가 stale로
실패합니다. Commit error 뒤 durability가 불명확하면 token을 추측하거나 자동 retry하지 않고
새 atomic snapshot부터 시작해야 합니다.

## Capability shape

제품 승격 시 기존 `AppliedMigrationReader`, `AtomicBackend`, `Transaction`에 method를 추가하는
방식은 external fake의 source compatibility를 깨뜨릴 수 있습니다. 별도 optional capability가
atomic history snapshot과 fenced step transaction을 함께 제공하고, coordinator가 capability
부재를 structured unsupported로 거부하는 방향이 최소 후보입니다. 정확한 public type/function,
zero value, error category/code는 이 spike에서 결정하지 않습니다.

Test-only candidate coordinator는 이 optional port만 type-assert하고 actual immutable
`ProjectState`를 성공한 step 뒤에만 갱신합니다. Conflict에서는 tail을 호출하지 않고 이전
commit의 `ProjectState`와 snapshot을 반환합니다. 별도 external-package fake는 기존 public
reader/backend interface만 계속 구현하며, unsupported path가 그 legacy read/begin으로
fallback하지 않는지 call count로 고정합니다.

SQLite 구현은 snapshot read와 migration writer를 구분해야 합니다. Writer는 migration 전용
pinned connection에서 immediate transaction을 사용하고, reader는 normal read transaction으로
identities와 metadata를 한 view에서 읽습니다. 성공 commit 뒤에만 successor token을 caller에게
노출합니다.

## Guarantee boundary and risks

- Safety는 recorder mutation을 포함한 모든 migration writer가 fence를 거칠 때 완전합니다.
- Direct lower-level writer가 recorder만 바꾸면 stored fingerprint와 actual history의 non-ABA
  mismatch는 다음 snapshot/step에서 fail-closed합니다. Direct writer의 completed ABA와 recorder
  밖 live-schema drift는 감지하지 못합니다.
- SQLite `PRAGMA data_version`은 connection-local이고 durable CAS token이 아니며,
  `schema_version`은 recorder-only change를 포함하지 않습니다. `user_version`은 application-wide
  namespace이고 conditional singleton-row transition보다 경계가 불명확합니다.
- `BUSY`/`LOCKED`는 contention이지 history mismatch가 아닙니다. 모든 SQL stage의 error는
  같은 SQLite result-code normalizer를 거칩니다. Caller가 명시적으로 새 lifecycle을 시작할
  수 있지만 spike 내부에는 semantic retry/backoff가 없고 coordinator attempt count는 1입니다.
- Revision overflow, database copy/restore 뒤 epoch 정책, metadata schema upgrade, kill/crash
  reconciliation은 제품 설계 전에 별도 결정해야 합니다.
- `BEGIN IMMEDIATE`는 SQLite writer를 database-wide로 직렬화해 migration에는 적합하지만
  일반 query/write transaction에 전역 적용하면 안 됩니다.
