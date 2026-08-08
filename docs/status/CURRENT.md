# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 machine artifact commit:
  `594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`
  (`test: lock historical project state contracts`)
- 기준 제품 commit:
  `3b0e68d6717a9612debc9cb93d03ab0f98005860`
  (`feat: reconstruct historical migration state`)
- 활성 작업 baseline commit:
  `9856fd0278162af0a5ee28dfebd4f07d93eca790`
  (`docs: complete historical state reconstruction`)
- remote: `https://github.com/progresshans/godj.git`, remote tracking ref 없음
- 현재 단계: GDJ-0017 Migration lifecycle exact contract와 revision-fence feasibility spike
- 최근 완료 작업:
  [GDJ-0016 Historical ProjectState Reconstruction Product Slice](../../work/0016-historical-project-state-reconstruction-product-slice.md)
- 활성 작업:
  [GDJ-0017 Migration Lifecycle Compatibility Contracts and Revision-Fence Spike](../../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)
- 다음 ready 작업: 없음 — GDJ-0017 exact oracle과 fence spike 감사 뒤 제품 work를 별도로 결정

## 현재 checkout에서 확인된 사실

### 제품 구현

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5입니다.
- Schema DSL과 normalized IR v2, deterministic codegen, generic Manager/QuerySet,
  typed/dynamic Query AST, SQLite query/write와 Save lifecycle 제품 단면이 구현됐습니다.
- Migration core는 versioned `ProjectState`, typed `CreateModel`/`AddField`, immutable identity
  graph와 `AppliedState`, zero-I/O `Planner`, preflighted `Executor.ExecutePlan`을 제공합니다.
- SQLite migration backend는 supported DDL과 same-transaction editor/recorder를 제공하며
  unsupported rebuild/dependency 범위는 구조화된 capability error로 거부합니다.
- 별도 `AppliedMigrationReader`, core `LoadAppliedState`와 `Planner.CheckHistory`가 durable
  recorder identity를 validated `AppliedState`로 읽고 known history를 plan 전에 검사합니다.
- Accepted [ADR-0016](../adr/0016-historical-project-state-reconstruction.md)의
  `StateReconstructor`는 loaded migration definition을 deep-copy하고 existing Planner graph/order
  kernel로 explicit empty/latest/before/after/applied `ProjectState`를 pure replay합니다.
- Zero `StateRequest`는 invalid이고 zero `StateReconstructor`는 empty graph와 같은 안전한
  immutable value입니다. Before는 명시 target set 전체를 제외하고, latest는 same-app leaf
  closure union, applied는 full-forward order의 known applied node만 replay합니다.
- Constructor input, built-in operation/nested IR과 반환 state는 alias되지 않습니다. Repeated와
  concurrent reconstruction은 deterministic하고 race-safe합니다.
- Reconstructor core는 DB handle을 받지 않고 backend/SQLite/SQL package를 import하지 않습니다.
  Applied conformance adapter만 real SQLite recorder를 mode=ro로 읽어 `LoadAppliedState`에
  전달하며 exact-one-SELECT/no-write와 capture/request call allowlist gate를 가집니다.

### 호환 계약과 machine artifact

- Protocol v2에는 8 ordered reference/product set, 87 contract가 있습니다: M1 read/metadata
  11개, write/migration 11개, Save lifecycle 12개, QuerySet evaluation/cache 11개, migration
  planning 12개, plan execution 10개, recorder restart 10개, historical-state reconstruction
  10개입니다.
- 현재 제품 분류는 `83 passing + 4 deviation`입니다. MIG-018/020/022/024만
  [DEV-0001](../DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)의
  verified deviation이며 87개 모두가 Django exact 일치라는 뜻은 아닙니다.
- 8 set의 contract ID/scenario는 전역으로 유일하고 56개 ordered cross-binding이 모두
  validation에서 거부됩니다.
- MIG-037..046 historical-state manifest는 10 `passing`, 9,197 bytes, SHA-256
  `85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c`입니다.
- Locked Django oracle은 89,997 bytes, SHA-256
  `bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`, locked static
  fixture는 1,715 bytes, SHA-256
  `9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1`로 변경되지 않았습니다.
  Locked `SHA256SUMS`도 baseline과 byte-identical하며 파일 SHA-256은
  `2da1f862ada632a9db2406672f0ac9209c066ae6b822afe1b47f321fdaea40c8`입니다.
- 두 독립 Go actual은 각각 89,867 bytes, SHA-256
  `a307d185e5a3c67a679f62bfa4575f6f43ef8ad41e55c78fdf34d5acb5866e44`로 byte-identical하고
  locked Django oracle과 protocol 의미상 MIG-037..046 10개가 0-diff입니다.
- Static fixture comparison은 의도한 exit 1과 ordered status mismatch 10개를 유지합니다.
  등록되지 않은 historical-state scenario는 product binary가 exit 2/no actual로 거부합니다.
- Historical state normalizer는 lowercase model key, explicit table/column, declaration-order
  field와 kind/PK/null/max-length/default tagged value를 보존합니다. Boolean kind/default type,
  non-char `max_length=null`, absent default가 exact gate에 포함됩니다.
- Live adapter는 contract ID/oracle/static payload dispatch를 하지 않고 arbitrary ID와
  definition/request/target/dependency/applied/live-DB mutation을 observation에 전파합니다.
  Deliberately divergent `godj_state_*` table inventory는 capture 전후 같고 DDL/write/기타
  non-SELECT metric은 0입니다.
- Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3이고 exact Django reference는
  Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`, SQLite 3.50.4입니다.

### 활성 GDJ-0017 범위

- MIG-047..056은 fresh/prefix/no-op/latest, named forward/reverse, app zero target, unknown
  legacy, inconsistent known history, middle failure와 fresh durable restart를 잠글 아홉 번째
  exact contract 후보입니다.
- Phase 후보는 MIG-047..053/056 `commit`, MIG-054 `evaluation`, MIG-055 `rollback`입니다.
  MIG-054는 public command orchestration의 explicit
  `loader.check_consistent_history(connection) → target/plan → migrate` preflight이며
  `plan_invoked=false`, transaction/DDL/write 0을 요구합니다.
- MIG-056은 `:memory:` 재사용을 restart로 부르지 않고 temporary file database를 닫고 새
  connection/loader/executor로 여는 fresh durable restart를 관찰합니다.
- MIG-051/052 reverse는 abstract ordered step outcome과 final schema/recorder만 비교하고
  physical reverse transaction topology는 기존 DEV-0001/ADR-0014에 남깁니다.
- Contract 단계는 `migrations/**`, `db/**`, `conformance/runners/godj/**`를 수정하지 않습니다.
  Product runner는 새 scenario를 exit 2/no actual로 거부해야 합니다.
- 별도 `conformance/lifecyclefence/**` spike는 identities와 opaque revision을 같은 snapshot으로
  읽고 각 migration transaction이 첫 DDL/write 전에 expected revision을 검증하는 가설만
  시험합니다. Outer transaction, automatic retry와 unsupported backend silent fallback은
  허용하지 않습니다.
- 완료 목표는 기존 `83 passing + 4 deviation`과 새 `10 oracle_locked`를 분리한 9 set/97
  contract, 72 ordered cross-binding입니다. 아직 달성한 상태가 아닙니다.

### 검증 증거

- GDJ-0015의 exact Django reference, portable/exact Python, 8-set/56 cross-binding와
  fail-closed baseline은
  [EVID-20260808-014](TEST_EVIDENCE.md#evid-20260808-014--gdj-0015-historical-projectstate-reconstruction-compatibility-contracts)에
  기록했습니다.
- GDJ-0016의 public reconstructor, deep-copy/race/error/source/driver gates, eighth live adapter,
  two-run byte identity와 10/0-diff, `83 passing + 4 deviation`, `make check`와 독립 P0–P3 감사는
  [EVID-20260808-015](TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
  기록했습니다.
- 최종 local gate는 `make check`, uncached full Go/race/CGO=0/vet, focused
  migration/conformance/compile/source/mutation tests를 통과했습니다.
- GitHub Actions workflow는 push하지 않아 hosted 실행 증거가 없습니다. 외부 Django
  checkout은 수정하지 않았습니다.

## 확정된 결정

- Exact Python/Django/SQLite/platform/locale profile과 protocol v2 fail-closed binding을
  유지합니다.
- [ADR-0010](../adr/0010-m2-migration-state-and-executor-boundary.md)에 따라 한 migration의
  operation과 recorder는 같은 backend transaction에서 commit/rollback됩니다.
- [ADR-0013](../adr/0013-immutable-migration-planner.md)에 따라 identity graph/applied state는
  historical `ProjectState`와 분리되고 Planner는 pure zero-I/O computation입니다.
- [ADR-0014](../adr/0014-migration-plan-execution-atomic-reverse.md)와 DEV-0001에 따라 plan은
  migration별 commit되고 reverse schema/recorder는 같은 transaction입니다.
- [ADR-0015](../adr/0015-recorder-backed-applied-state.md)에 따라 raw recorder read,
  `LoadAppliedState`와 explicit history check는 write transaction interface와 분리됩니다.
- [ADR-0016](../adr/0016-historical-project-state-reconstruction.md)에 따라 historical state의
  의미 소스는 loaded migration definition이고 live schema/current generated model이 아닙니다.
  Immutable reconstructor는 operation state transition만 dependency order로 replay합니다.
- 이 제품 단면은 read → reconstruct → plan → execute를 하나의 atomic lifecycle로 만들지
  않습니다. Snapshot revision/session binding, multi-process lock와 recovery를 보장하지 않습니다.

## 현재 차단 요인과 미결정 사항

외부 blocker는 없습니다. GDJ-0017에서 다음을 근거로 확정해야 합니다.

1. MIG-047..056 exact payload/provenance와 reverse topology를 제외한 비교 경계
2. Atomic history snapshot과 successful step의 successor revision을 stale window 없이
   handoff할 backend capability shape
3. Revision conflict/unsupported capability의 structured error와 existing fake source compatibility
4. 후속 Q-012: migration definition source/loader, data callback, crash repair/lock/lease
5. Q-011/Q-010/Q-013: request lifetime, public CLI/project protocol, cross-app relation loader

## 다음 정확한 작업

GDJ-0017의 첫 작업은 pinned Django executor/loader/recorder와 upstream tests에서
MIG-047..056 provenance를 contract별로 확인하고 disposable exact probe를 만드는 것입니다.
동시에 제품 package를 건드리지 않는 `conformance/lifecyclefence/**` harness에서 atomic
snapshot/revision과 each-step before-DDL validation을 test-first로 검증합니다. Exact contract와
spike가 완료되기 전에는 GDJ-0016을 public migrate lifecycle, source discovery, concurrent
writer fencing 또는 crash-safety 지원으로 확장 해석하지 않습니다.

## 작업 재개 체크포인트

- 활성 work baseline: `main@9856fd0278162af0a5ee28dfebd4f07d93eca790`
- 제품 commit: `main@3b0e68d6717a9612debc9cb93d03ab0f98005860`
- Machine baseline: `main@594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`
- 활성 work: [GDJ-0017](../../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)
- ready work: 없음
- locked Django oracle/static/SHA256SUMS와 DEV-0001을 변경하지 않음
- 건드리면 안 되는 외부 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- 전체 local gate와 exact regeneration check: `make check`
- Portable CI equivalent: `make ci`
- 현재 가장 위험한 과장: exact lifecycle oracle이나 격리 revision-fence spike를 product
  adapter, public migrate API, distributed lock 또는 crash recovery로 표현하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
