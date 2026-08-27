# ADR-0051: Project-linked Explicit Migrate

- 상태: Proposed
- 날짜: 2026-08-28
- 관련 work/contract: [GDJ-0049](../../work/0049-project-linked-migrate-and-clean-database-article-lifecycle.md), MIG-087..098, Q-010, Q-012, Q-019
- 선행 결정: [ADR-0021](0021-project-linked-migration-check.md), [ADR-0022](0022-project-runtime-and-global-migration-check.md), [ADR-0035](0035-pre-release-current-only-format-and-generated-publication.md), [ADR-0037](0037-postgresql-current-contract-backend.md), [ADR-0042](0042-project-linked-runserver-and-article-development-loop.md), [ADR-0048](0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)
- 대체하는 ADR: 없음

## 맥락

GoDj에는 current-only Definition/State와 revision-fenced SQLite/PostgreSQL migration lifecycle이 있지만, global CLI는
`godj migrations check`, `generate`와 `runserver`만 게시합니다. Article actual은 test/site 조립에서 schema를 준비하므로 새 project가
명시적 사용자 명령으로 clean database를 latest state에 수렴시키는 흐름이 없습니다.

Global process가 DB URL과 backend를 직접 소유하면 project-specific configuration, secret redaction과 multi-backend 선택이 CLI core로
새어 나옵니다. 반대로 existing read-only project-check runner를 write command로 그대로 재사용하면 private protocol 의미와 DB-free
invariant가 섞입니다. 또한 현재 short child owner의 2초 force-kill grace는 migration core의 detached 5초 cleanup bound보다 짧아
rollback/session/backend cleanup을 중간에 끊을 수 있습니다.

## 결정 기준

- Existing current-only loader/executor/backend meaning을 재사용하고 migration core를 다시 설계하지 않을 것
- DB configuration과 secret은 project-owned child에 남길 것
- Definition preflight는 backend open보다 먼저, lifecycle preflight는 schema mutation보다 먼저일 것
- Commit durability와 cleanup/rollback failure를 안전한 success 또는 retryable failure로 축약하지 않을 것
- Cancellation이 cleanup을 완료할 현실적인 시간을 주고 direct child를 반드시 reap할 것
- `runserver`는 계속 explicit generate/migrate 경계를 넘지 않을 것
- SQLite/PostgreSQL clean database, no-op와 restart를 실제 사용자 흐름으로 검증할 것

## 고려한 선택지

### Global CLI가 DSN과 backend를 직접 생성

단일 binary UX는 단순하지만 framework CLI가 project-specific driver/config/secret을 알아야 하고 argv/diagnostic leakage surface가
커집니다. Multi-backend 설정 정책도 조기에 고정하므로 채택하지 않습니다.

### Existing project-check private protocol에 write command 추가

Build/selection infrastructure를 재사용할 수 있지만 DB-free check contract와 versioned response 의미를 바꿉니다. Existing bytes를
보존하고 migrate 전용 strict protocol을 별도로 두는 쪽을 채택합니다.

### `runserver`가 시작할 때 자동 migrate

개발 첫 실행은 짧아지지만 schema mutation, commit unknown과 rollback/cleanup failure를 long-lived server startup에 숨깁니다.
Generate/check/migrate/runserver를 명시적으로 조합하는 경계를 유지하므로 채택하지 않습니다.

## 결정

1. Public command는 `godj migrate`와 `godj migrate --project <exact-godj.toml>` 두 형식만 지원합니다. Latest leaves만
   target으로 사용하며 named/zero/reverse/plan/fake/check/adopt/multi-DB option은 제외합니다.
2. Existing descriptor `[project].package`가 migrate config를 소유합니다. `migrate_package` field는 추가하지 않습니다.
3. Public `project.Config`에는 copied `MigrationDefinitionSources`와 lazy `OpenMigrationBackend`를 additive하게 추가합니다.
   Backend port는 `migrationbackend.RevisionFencedBackend`와 `Close() error`를 함께 요구합니다.
   Opener가 non-nil backend와 error를 함께 반환하면 runner가 그 부분 획득 resource를 정확히 한 번 닫고 open failure를 primary로
   유지합니다. Raw opener cause는 게시하지 않고 `migration_backend_error/backend_open_failed`로 닫습니다. Nil/typed-nil backend는
   닫지 않으며 nil error와 함께 반환되면 `migration_backend_error/invalid_backend`입니다.
4. Roots와 embedded source ID/document는 `project.Run` 진입에서 deep-copy합니다. Static sources와 flat no-follow file discovery를
   deterministic order로 합치고 `definition.Load`를 정확히 한 번 호출합니다. Static sources는 existing `migrations check`와
   새 `migrate`의 동일 catalog에 포함되지만 check는 backend opener를 호출하지 않습니다.
5. Definition load가 성공하기 전에는 opener를 호출하지 않습니다. 이후 backend open 1회,
   `Executor.Migrate(ctx, loaded, LatestLifecycleRequest())` 1회, backend close 1회를 수행합니다.
6. Existing project-check private protocol은 byte-for-byte 보존합니다. Migrate는 별도 version-1 private argument/request/response를
   사용하며 duplicate/unknown/trailing/non-UTF8/oversize와 partial write를 fail-closed합니다.
7. Global process는 database driver/URL/secret, SQL, definition document와 raw cause를 parse 또는 publish하지 않습니다. Project
   opener만 ambient child environment를 읽고 user-visible/private response는 bounded category/code와 summary scalar만 담습니다.
8. CLI는 migration 또는 process failure를 자동 retry하지 않습니다. Commit unknown은 rollback 없이 terminal unknown으로,
   committed+cleanup/close/write failure는 “DB가 전진했을 수 있는 nonzero”로 게시하고 fresh invocation으로 reconciliation합니다.
9. Joined error classification은 commit unknown, non-nil rollback cause, commit cleanup, session close, primary migration code 순서입니다.
   Rollback failure는 `migration_transaction_error/rollback_failed`로 닫습니다. Outer backend close failure는 기존 opener/core
   failure를 덮어쓰지 않고 bounded `cleanup_failed` observation으로 보존하며, 선행 failure가 없을 때만
   `migration_backend_error/backend_close_failed`가 primary가 됩니다.
10. Migrate private invocation의 `project.Run`이 SIGINT/SIGTERM을 cancellation context로 변환합니다. Parent는 signal을 project
    runner에 전달하고, core의 순차 rollback 5초 + deferred session close 5초보다 엄격히 긴 15초 grace 동안 실제 child exit를
    기다립니다. 남은 5초는 outer backend close와 response margin이며, grace를 넘긴 경우에만 process group force-kill을 시도하고
    항상 direct child를 reap합니다.
11. Article project runner는 `examples/article/migrations` file source와 `systemstate.InitialDefinitionSource()`를 함께 제공합니다.
    Site와 runner는 외부 import 가능한 Article-owned `examples/article/databaseconfig` package의 config/opener를 공유하되 error에는
    environment key만 포함합니다.
12. `runserver`는 current bundle preflight만 유지하고 implicit generate/migrate/retry를 하지 않습니다. PostgreSQL server/database/
    empty schema provisioning은 test/deployment owner의 책임입니다.
13. MIG-087..098은 GoDj-specific oracle-blind decision/product contracts입니다. Phase A에서는 `oracle_locked`, actual adapter가 동일한
    observation을 만족한 뒤에만 `passing`입니다. Expected deviation은 0이며 retired MIG-075..086을 재사용하지 않습니다.

## 결과

- Existing migration formats, planner/executor/backend와 transaction taxonomy를 바꾸지 않고 사용자 명령을 추가할 수 있습니다.
- Project code가 configuration과 secret을 소유하므로 global CLI는 backend-neutral하게 유지됩니다.
- Build toolchain은 existing trusted local ambient environment를 받습니다. Untrusted toolchain secret isolation은 별도 production
  hardening 범위입니다.
- Catalog 전체가 한 transaction이 아니므로 later failure 뒤 durable prefix가 남을 수 있습니다. 이는 fresh invocation resume로
  명시적으로 검증합니다.
- Response/workspace cleanup failure는 이미 committed된 DB를 되돌리지 못합니다. Nonzero가 반드시 “변경 없음”을 뜻하지 않는다는
  운영 의미를 문서화합니다.
- Public API는 backend opener와 source injection이 늘지만, check/generate/runserver 경로는 opener를 호출하지 않아 existing
  no-DB-I/O contract를 보존합니다.
- `Close() error` 자체는 context-aware하지 않으므로 cooperative project closer가 15초 outer grace를 넘기면 parent force-kill이
  최종 bound가 됩니다. 정상/fault tests는 두 core cleanup window 뒤 outer close/response가 끝날 시간을 명시적으로 검증합니다.

## 검증 경계

- Public compile, exact argv/deleted-CWD precedence와 old protocol byte identity
- Strict migrate protocol, source copy/load-before-open, nil/typed-nil/open/close/error precedence와 short write
- SQLite fresh/latest, prefix/tail, second no-op, failure/resume, two-child fence와 interrupt cleanup
- PostgreSQL 17.10 fresh/no-op/contention, explicit migrate→runserver→restart와 secret scanner
- Normal/race/CGO-disabled/vet, generated drift, Linux/386 compile, repository-external archive와 one final exact hosted matrix

## 미해결 항목

- Q-010: installed runner/library version negotiation과 general upgrade/repair UX
- Q-012: writer/autodetector, reverse/target/plan/fake/custom operation과 broader public migration CLI
- Q-019: unknown-outcome retained resource의 bounded quarantine/reconciliation 정책
- Production deployment, secret manager, multi-DB router와 non-cooperative external writer
