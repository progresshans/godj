# ADR-0047: Explicit Single-Runtime System State

- 상태: Proposed
- 날짜: 2026-08-24
- 관련 work/contract: [GDJ-0045](../../work/0045-durable-single-runtime-system-state-and-article-restart.md),
  SYS-001..012, Q-020, M6
- 선행 결정: [ADR-0035](0035-pre-release-current-only-format-and-generated-publication.md),
  [ADR-0037](0037-postgresql-current-contract-backend.md),
  [ADR-0044](0044-session-auth-csrf-and-bounded-article-admin.md),
  [ADR-0046](0046-json-serializer-and-session-authenticated-article-api.md)
- 대체하는 ADR: 없음

## 맥락

Current Article site의 user credential, session과 audit history는 process memory에만 있습니다. Article row와 migration history는
SQLite/PostgreSQL restart를 견디지만 server를 다시 시작하면 login이 풀리고 Admin history가 사라집니다. Runtime startup마다
password를 다시 hash하고 memory store를 만드는 composition은 개발 walking skeleton에는 충분했지만 실제 framework system
state 경계가 아닙니다.

System state를 durable하게 만들 때 DB unique constraint와 general schema field를 함께 도입하면 current definition wire/digest,
historical state, codegen metadata, SQLite remake/catalog와 PostgreSQL index validation까지 한 번에 확장됩니다. 반대로 application이
table을 즉석에서 만들거나 raw SQL로 관리하면 GoDj의 canonical migration lifecycle을 우회합니다.

## 결정 기준

- 명시 migration과 current-only format 원칙
- clean restart 뒤 사용자-visible auth/session/history 의미
- credential/session/CSRF secret 비노출
- Article mutation과 audit의 transaction 원자성
- commit outcome unknown과 retry ownership
- single/multi-process concurrency 주장의 정확성
- 기존 IR/codegen/backend를 불필요하게 재기준화하지 않는 bounded 구현
- SQLite와 PostgreSQL에서 같은 contract를 검증할 수 있는가

## 고려한 선택지

### 선택지 A — process memory를 유지하고 restart는 login으로 복구

구현은 작지만 session/history persistence 요구를 충족하지 못하고 process lifetime을 제품 storage lifetime으로 오인하게 합니다.

### 선택지 B — `Unique` IR과 database-enforced multi-process writer safety를 함께 도입

Non-cooperating writer와 concurrent process까지 방어할 수 있습니다. 그러나 definition/digest/codegen/catalog 전 범위를 바꾸며 first
durable slice보다 훨씬 큰 schema feature packet이 됩니다. General multi-process topology가 아직 결정되지 않은 상태에서 public
IR을 먼저 넓히지 않습니다.

### 선택지 C — current IR의 명시 system migration과 one-runtime ownership

Auto/Char/Boolean만으로 세 table을 만들고 DB/schema당 동시에 하나의 runtime만 소유하게 합니다. Runtime mutex와 transaction
안에서 lookup/write를 직렬화하고 0/1 cardinality를 매번 검증합니다. DB unique는 없으므로 duplicate를 복구하지 않고 fail-closed하며
multi-process/direct writer는 명시적 비목표로 남깁니다.

## Proposed 결정

1. `godj_system.0001_initial`은 caller가 current migration definition/executor로 명시 적용합니다. `siteapp.New`와
   `systemstate.Open`은 migration, adoption 또는 repair를 실행하지 않습니다.
2. System schema는 current Auto/Char/Boolean IR만 사용한 admin credential, session, audit 세 모델입니다. String payload는
   current-only version tag, canonical encoding과 명시적 byte/item cap을 가지며 unknown/malformed/oversize를 거부합니다.
3. DB/schema당 동시에 살아 있는 `systemstate.Runtime`은 하나입니다. Clean restart는 이전 runtime/listener/backend handle이 모두
   종료된 뒤 새 runtime이 같은 DB를 여는 경우입니다.
4. Admin row와 session digest는 DB `UNIQUE`가 아닌 non-null Char입니다. Process mutex + transaction의 0/1 lookup이 cooperative
   writer를 직렬화하고 2+행은 선택·삭제·덮어쓰기 없이 fail-closed합니다.
5. First auth scope는 정확히 한 admin입니다. Empty table에서만 one-time bootstrap하고, restart에서는 principal/username/active/
   ordered permission/password verify가 모두 같을 때 zero-write로 materialize합니다. Mismatch는 startup failure입니다.
6. Session DB에는 raw bearer를 넣지 않고 domain-separated SHA-256 digest와 bounded record payload만 저장합니다. Existing
   `sessions.Manager`가 ID entropy, expiry, touch, rotate, flush와 capacity 의미를 계속 소유합니다.
7. CSRF signing key는 process-local CSPRNG로 유지합니다. Restart 전 masked token은 거부하고 authenticated safe request가 새 token을
   발급합니다. 이 차이는 SYS-009의 DEV-0008 한 lane에만 한정합니다.
8. Article Admin mutation은 Article DML과 redacted audit append/prune을 같은 `db.Atomic` callback에서 수행합니다. Hook은 borrowed
   `db.Session` 밖으로 escape하지 않으며 nested transaction과 post-commit append를 만들지 않습니다.
9. Confirmed rollback은 Article/audit 모두 0입니다. Commit outcome unknown은 automatic retry, rollback-after-unknown, synthetic audit,
   success response와 verified-commit claim을 만들지 않습니다.
10. Startup readiness는 applied migration identity, required table/field query 가능성과 strict stored-row decode를 확인합니다. General
    physical drift/index/type introspection 또는 online repair를 지원한다고 주장하지 않습니다.

정확한 exported `systemstate` constructor/config/store 이름과 Article transaction hook shape는 Phase B~D compile/race prototype 뒤
이 ADR을 Accepted로 전환할 때 동결합니다.

## 결과

- 기존 current migration과 query/write/transaction 경계를 재사용해 restart-preserving vertical slice를 빠르게 만들 수 있습니다.
- Runtime product composition에서 memory fallback과 silent auto-migrate가 사라집니다.
- Duplicate/corrupt row를 감지할 수 있지만 DB가 concurrent non-cooperating writer를 예방하지는 않습니다. 운영자는 one-runtime
  topology를 지켜야 하며 이를 production multi-process 지원으로 확대해서는 안 됩니다.
- PostgreSQL AutoField sequence는 rollback에도 gap이 생길 수 있어 audit order는 monotonic이지만 contiguous하지 않습니다.

## 의도적으로 결정하지 않은 것

- Multi-process/distributed runtime, DB unique/index IR와 external writer ownership
- User/group/content type/object permission, password lifecycle와 Django DB/password wire compatibility
- Persistent CSRF key, background reaper, online repair/adoption와 unknown-commit reconciliation
- API-specific audit policy, general system settings/autodiscovery와 M6 completion
- Production server/cookie/proxy/TLS와 multi-DB routing

## 검증

- [ ] SYS-001..012 exact reference/decision contracts와 Proposed DEV-0008 sparse selector
- [ ] Explicit SQLite/PostgreSQL system migrate/reopen/no-op; missing schema에서 DDL/bootstrap/listener 0
- [ ] Bootstrap idempotency/mismatch/duplicate/corrupt and secret-free failure
- [ ] Digest-only Store, expiry/touch/capacity/reap/rotate/logout normal/race/fault tests
- [ ] Article/audit same-transaction commit/rollback/unknown tests
- [ ] Distinct process A/B/C SQLite와 PostgreSQL restart E2E, raw secret/log/temp leak 0
- [ ] Affected normal/race/CGO0/vet, final full/386/external/audit와 exact hosted matrix
