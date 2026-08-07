---
id: GDJ-0004
status: active
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "6e7c15a806ebf65e756372cd1d10e68ac629e207"
depends_on: ["GDJ-0003"]
contracts: ["MOD-001..MOD-007", "MIG-001..MIG-004"]
allowed_paths: ["go.mod", "go.sum", "Makefile", "schema/**", "codegen/**", "orm/**", "query/**", "db/**", "migrations/**", "internal/**", "conformance/**", "examples/**", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Write와 Migration Walking Skeleton

## 사용자에게 보이는 결과

한 generated `Article` model에서 create, partial update, explicit NULL, instance delete와
transaction commit/rollback을 타입 안전하게 실행하고, 같은 Schema IR에서 최소 migration
state를 만들어 SQLite에 CreateModel/AddField/reverse를 적용할 수 있습니다.

완료 시 GoDj adapter가 GDJ-0003에서 잠근 MOD-001..007과 MIG-001..004를 실제 제품
package로 실행해 Django oracle과 일치해야 합니다.

## 목표 흐름

```text
generated create/patch input
→ explicit change state
→ mutation plan
→ SQLite write + transaction
→ normalized MOD observations

Schema IR snapshot
→ versioned ProjectState + Operations
→ Executor + SQLite Schema Editor
→ Recorder
→ normalized MIG observations
```

## 시작 전 결정 spike

1. ADR-0009의 tri-state core가 nullable/non-null field를 external compile test에서 올바르게
   제한하는지 최소 두 API 형태로 비교합니다.
2. loaded model state를 직접 mutate/save할지 generated patch/manager를 첫 public 단면으로
   둘지 MOD 계약과 Go 사용성을 기준으로 좁힙니다.
3. ADR-0010의 ProjectState/Operation/Executor package dependency가 generated model을
   import하지 않는지 compile fixture로 검증합니다.
4. SQLite transaction, DDL atomicity, recorder 갱신과 context cancellation을 한 connection
   경계에서 처리할 수 있는지 실패 주입으로 확인합니다.
5. spike 결과로 ADR-0009/0010을 Accepted로 올리거나 대안과 실패 근거를 기록합니다.

## 제한 범위

- M1 `Article` 한 model과 Auto/Char/Boolean/nullable Char field
- one-row create, explicit field partial update, one instance delete
- transaction commit과 callback/error rollback
- typed immutable mutation plan과 parameter binding
- versioned project/model state의 최소 snapshot
- `CreateModel`, nullable `AddField`, AddField reverse
- SQLite migration recorder의 app/name key
- operation/recorder atomic failure와 connection recovery
- MOD-001..007, MIG-001..004 GoDj adapter

## 비목표

- bulk create/update/delete와 upsert
- model hook/signal, cascade/relation
- dirty tracking 전체와 QuerySet write breadth
- public migration file encoding, autodetector, optimizer, merge/squash
- data migration callback ABI와 public historical model API
- public `godj makemigrations/migrate` CLI
- multi-process migration lock와 crash recovery
- PostgreSQL 또는 다른 backend

## 구현 순서

1. compile/runtime spike와 Accepted ADR-0009/0010으로 API·backend 경계를 닫았습니다.
2. explicit write state와 immutable mutation plan을 unit/property test로 만듭니다.
3. codegen이 Article create/patch API와 typed field binding을 생성하도록 확장합니다.
4. SQLite insert/update/delete와 transaction callback을 context/resource cleanup 계약으로
   구현합니다.
5. versioned ProjectState, 최소 Operation, Executor, Recorder와 SQLite Schema Editor를
   generated model package에 의존하지 않게 구현합니다.
6. failure injection으로 DDL/recorder rollback과 connection recovery를 검증합니다.
7. GoDj adapter를 두 번째 manifest에 연결하고 실제 결과에 따라 red를 거쳐 passing으로 전환합니다.
8. full/race/codegen/CGO=0/differential gate와 상태 문서를 갱신합니다.

## 완료 gate

- [x] ADR-0009/0010의 채택이 compile/runtime 증거와 함께 기록됨
- [ ] nullable/non-null/omitted write API positive·negative external compile test 통과
- [ ] mutation plan 불변성, binding과 zero-I/O validation test 통과
- [ ] create/update/delete/transaction context·cleanup·rollback integration test 통과
- [ ] migration state/forward/backward/recorder/failure test 통과
- [ ] migration package가 generated/current model package를 import하지 않는 gate 통과
- [ ] MOD-001..007과 MIG-001..004가 모두 `passing` 또는 승인된 `deviation`
- [ ] generation drift, `go test`, `go vet`, relevant race, `CGO_ENABLED=0` 통과
- [ ] CURRENT/matrix/evidence/work와 manifest 상태가 같은 checkout을 가리킴

## 재개 시 첫 작업

`main@6e7c15a806ebf65e756372cd1d10e68ac629e207`의 clean worktree를 baseline으로
기록했고 두 spike와 ADR 채택을 마쳤습니다. 다음은 `Change[T]`/`NullableChange[T]`,
DB 독립 mutation plan과 Schema IR v2 default를 unit test와 함께 구현하는 것입니다.

## 결정 spike 결과

### Write API

- Go 1.26.5 별도 module에서 단일 tri-state `Change[T]`, pointer nullable과
  `Change[T]`/`NullableChange[T]` + generated builder 후보를 실제 compile했습니다.
- 단일 tri-state 후보는 non-null `Title`에 NULL이 compile되어 거부했습니다.
- Pointer 후보는 alias와 generic type inference 문제가 있어 거부했습니다.
- Generated immutable `With...` builder, model-typed `Mutation[M]`과 Manager write API를
  [ADR-0009](../docs/adr/0009-m2-explicit-write-change-state.md)에서 채택했습니다.

### Migration core

- 별도 module의 `modernc.org/sqlite v1.56.0`/SQLite 3.53.3에서 CreateModel/AddField,
  reverse, operation/recorder/reverse-recorder failure rollback, connection recovery,
  race와 `CGO_ENABLED=0`을 실행했습니다.
- `migrations/backend` interface와 `migrations` core, `db/sqlite` 구현 의존 방향 및
  transaction 전 state preflight를 [ADR-0010](../docs/adr/0010-m2-migration-state-and-executor-boundary.md)에서
  채택했습니다.
- Native DROP COLUMN은 indexed field에서 실제 실패했으므로 첫 단면은 nullable
  unindexed field로 제한하고 dependency에는 구조화된 capability error를 반환합니다.
