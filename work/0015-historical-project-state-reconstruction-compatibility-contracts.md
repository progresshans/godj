---
id: GDJ-0015
status: completed
updated: 2026-08-08
baseline_branch: "main"
baseline_commit: "a9ce9597551840f1be8e1f27006d427842f38081"
depends_on: ["GDJ-0014"]
contracts: ["MIG-037..MIG-046", "Q-012"]
allowed_paths: ["Makefile", ".github/workflows/ci.yml", "NOTICE.md", "conformance/contracts/migration-state-reconstruction-manifest.json", "conformance/runners/django/runner.py", "conformance/runners/django/migration_state_reconstruction_scenarios.py", "conformance/runners/django/tests/test_migration_state_reconstruction_scenarios.py", "conformance/runners/django/tests/test_runner_safety.py", "conformance/runners/django/tests/test_scenarios.py", "conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json", "conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS", "conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json", "conformance/internal/protocol/migration_state_reconstruction_artifacts_test.go", "conformance/internal/protocol/write_migration_artifacts_test.go", "conformance/cmd/godjcheck/main_test.go", "conformance/README.md", "docs/**", "work/**"]
integration_owner: "one primary agent"
---

# Historical ProjectState Reconstruction Compatibility Contracts

## 사용자에게 보이는 결과

특정 migration 직전·직후 또는 durable applied history 시점의 logical model/field
state를 loaded migration definition에서 재구성하는 의미를 Django 6.1 exact
reference로 고정합니다. Live database schema와 현재 generated model을 historical
state로 오인하지 않고, cross-app dependency와 unrelated applied branch를 누락하지
않는 다음 제품 단면의 기준이 됩니다.

## 목표

- MIG-037..046 exact Django/SQLite disposable state-reconstruction probe
- Explicit empty, first migration before/after와 linear intermediate state 의미 고정
- Cross-app dependency, multiple target/shared dependency와 omitted-target latest leaves 의미 고정
- Durable applied prefix에서 startup `ProjectState`를 재구성하는 의미 고정
- Known unrelated applied branch는 포함하고 unknown legacy identity는 schema로 만들지
  않는 경계 고정
- Canonical normalized app/model/field observation와 before/after database zero-mutation 검증
- Eighth manifest/oracle/static fixture, provenance와 checksum 구축
- Eight-set global identity/scenario uniqueness와 56 ordered cross-binding 거부
- 기존 `73 passing + 4 deviation` 제품 결과와 일곱 artifact set 보존

## 비목표

- GoDj historical-state reconstructor, loader 또는 새 public API 구현
- `migrations/**`, `migrations/backend/**`, `db/sqlite/**` 제품 source 수정
- `conformance/runners/godj/**` 제품 adapter 수정 또는 MIG-037..046 구현
- 현재 `Planner`, `AppliedState`, `ProjectState`, `Operation`, `Executor` public shape 변경
- Migration file encoding, Go module/directory discovery와 public loader ABI
- Public `godj migrate`, `showmigrations`, `makemigrations`와 listing API
- Live database schema introspection, drift repair와 schema/recorder reconciliation
- Read/reconstruct/plan/execute를 하나로 묶는 lifecycle API
- Multi-process lock, revision token, concurrent writer 직렬화와 crash recovery
- Data migration callback, historical app/model mutation API
- Replacement/squash/merge/fake/fake-initial, optimizer와 conflict resolution
- Unmigrated app/`real_apps`, relation rendering, PostgreSQL 또는 multi-DB router
- 기존 일곱 manifest/oracle/static/deviation payload 또는 meaning 변경

## 선행 조건과 기준 상태

- Baseline product commit:
  `main@a9ce9597551840f1be8e1f27006d427842f38081`
- [GDJ-0014](0014-recorder-backed-restart-planning-product-slice.md)는 별도 raw reader,
  `LoadAppliedState`/`CheckHistory`와 MIG-027..036 live adapter를 구현했습니다.
- 현재 제품 분류는 `73 passing + 4 deviation`이고 reference contract는
  총 77개, set은 일곱 개입니다.
- [ADR-0010](../docs/adr/0010-m2-migration-state-and-executor-boundary.md)은 Schema IR
  `ProjectState`/typed operation을, [ADR-0013](../docs/adr/0013-immutable-migration-planner.md)은
  identity-only Planner와 `AppliedState` 분리를 Accepted했습니다.
- [ADR-0015](../docs/adr/0015-recorder-backed-applied-state.md)는 recorder key를 읽고
  history를 검증하지만 `ProjectState`를 재구성하지 않습니다.
- [ADR-0016](../docs/adr/0016-historical-project-state-reconstruction.md)은 loaded full
  definition의 pure state replay를 Proposed로 두며, 계약 결과 전에 public API 고정 또는
  제품 지원을 의미하지 않습니다.
- Locked Django reference·static payload와 product commit을 입력으로 두고 contract-only
  machine artifact commit을 별도로 만듭니다.

## Django Reference / Contract

Exact profile은 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`,
CPython 3.14.3, SQLite 3.50.4, UTC/C locale입니다. 우선 확인할 pinned
provenance는 다음입니다.

- `django/db/migrations/graph.py`: `_generate_plan()`, `make_state()`
- `django/db/migrations/loader.py`: `MigrationLoader.project_state()`
- `django/db/migrations/migration.py`: `Migration.mutate_state()`
- `django/db/migrations/executor.py`: `_create_project_state()`, empty/forward migrate startup
- `tests/migrations/test_loader.py`: target state, repeated/shared migration plan
- `tests/migrations/test_executor.py`: unrelated applied migration state preservation

모든 contract의 phase는 `evaluation`입니다.

| ID | 잠근 외부 동작 |
|---|---|
| MIG-037 | Explicit empty node set은 format-valid empty `ProjectState`를 반환 |
| MIG-038 | 첫 migration의 `before` state는 해당 operation을 포함하지 않은 empty state |
| MIG-039 | 첫 migration의 `after` state는 그 migration의 ordered state operation을 모두 반영 |
| MIG-040 | Linear graph의 middle `after` state는 dependency prefix와 target을 포함하고 descendant는 제외 |
| MIG-041 | 같은 middle target의 `before` state는 dependency prefix는 포함하고 target 변경은 제외 |
| MIG-042 | Cross-app target state는 타 app dependency의 model/field state를 먼저 포함 |
| MIG-043 | Multiple target state는 shared dependency를 중복 적용하지 않고 branch union을 반영 |
| MIG-044 | Target을 생략한 latest state는 모든 leaf closure의 logical state를 포함 |
| MIG-045 | Durable known applied prefix startup state는 applied migration만 replay하고 unapplied descendant/current-latest state를 제외 |
| MIG-046 | Startup state는 unrelated known applied branch를 포함하되 unknown legacy applied identity를 schema로 materialize하지 않음 |

MIG-037..044는 graph/loaded definition projection, MIG-045..046은 recorder-applied startup
projection입니다. 두 mode를 하나의 scenario로 합쳐 before/after와 applied 의미를
흐리지 않습니다.

## Observation payload과 비교 경계

각 observation은 최소한 다음을 포함합니다.

- Request mode: explicit nodes/latest/applied, target app/name과 before/after
- Applied mode의 sorted `(app, name)` identities
- Canonical `ProjectState`: format version, sorted apps/models, model `db_table`,
  declaration-order fields의 name/column/kind/primary-key/nullability/max-length와 default
  presence/value·타입
- Definition graph fact: sorted nodes/dependencies와 requested position; private object ID/DFS path는
  제외
- Deliberately empty 또는 divergent live managed schema의 before/after inventory
- Capture 중 DDL/write/기타 non-SELECT 0, state unchanged와 replay source fact

SQL text, SELECT 횟수, Python object/cache identity, `Apps` registry object, physical map/field
storage, migration module import order와 incomparable sibling의 private DFS order는 비교하지
않습니다. 모델/field의 logical normalized meaning과 target inclusion/exclusion, dependency
closure, applied membership을 비교합니다.

Unmigrated app/`real_apps`는 현재 GoDj app registry가 없으므로 fixture에 섞지 않고
MIG-037..046 payload에서 제외합니다. Relation rendering도 Q-013/M3 전에 이 set으로
끌어오지 않습니다.

## False-green 위험과 필수 gate

- **Live schema introspection 위장**: capture DB를 의도적으로 empty 또는 requested
  historical state와 다르게 두고, logical result는 definition replay에서 와야 합니다.
- **Current schema 재사용**: middle/before/applied-prefix contract가 unapplied descendant field를
  포함하면 실패해야 합니다.
- **Setup state 재사용**: runner는 capture용 loader/executor/state object를 새로 만들고
  setup `ProjectState`/apps cache를 전달하지 않습니다.
- **Target-only replay**: cross-app dependency, multiple target/shared dependency와 latest leaves가
  누락되면 semantic mutation test가 comparator mismatch를 내야 합니다.
- **Plan-tail-only startup**: MIG-046의 unrelated known applied branch가 result state에서
  사라지면 실패해야 합니다.
- **Unknown identity schema 조작**: unknown recorder key는 applied observation에 남아야
  하지만 dummy app/model을 만들거나 known replay를 막으면 실패해야 합니다.
- **Contract-ID hardcode**: scenario input의 operation/target/applied fixture를 변경하면 result
  ProjectState와 DB observation에 파생 변화가 나야 합니다.
- **Comparator omission**: model/field inclusion, table/column, field kind/primary-key/null/
  max-length/default 중 하나를 mutate하면 exact mismatch가 나야 합니다.
- **Mutation disguise**: unchanged result만 보지 말고 DB before/after와 non-SELECT/DDL/write
  count를 같이 비교합니다.
- **Hash/map order**: two random-hashseed process의 canonical bytes가 다르면 oracle를
  잠그지 않습니다.
- **False product support**: GoDj runner는 새 scenario를 exit 2/no actual output으로
  fail-closed해야 하며 총 87개를 제품 passing으로 표현하지 않습니다.

## 설계와 가설

[ADR-0016](../docs/adr/0016-historical-project-state-reconstruction.md)의 검증 대상은
loaded full migration definition을 dependency order로 pure forward state replay하는 별도 immutable
reconstructor입니다. Identity-only Planner에 operation을 저장하거나 live DB를
source of truth로 삼지 않습니다.

계약 work에서는 Go public name, zero value, cache와 exact error code를 확정하지
않습니다. Django result와 false-green gate가 잠긴 뒤 별도 API spike로
`StatePosition`/`StateReconstructor` 후보, Planner graph kernel 공유와 state error taxonomy를
검증합니다.

## 구현 단계

1. Pinned Django graph/loader/migration/executor와 upstream test provenance를 contract별로
   연결합니다.
2. Temporary migration modules·disposable SQLite probe로 MIG-037..046을 독립 실행합니다.
3. Canonical ProjectState normalizer와 deliberately divergent DB before/after capture를 먼저
   test합니다.
4. Eighth manifest, Django registry/runner, oracle와 explicit static fixture를 연결합니다.
5. ProjectState/target/applied/graph/metrics semantic mutation이 comparator mismatch를 내는지
   검증합니다.
6. Product binary의 unknown scenario exit 2/no output을 검증하고 GoDj adapter source는
   추가하지 않습니다.
7. Two random-hashseed process oracle byte identity와 checksum을 고정합니다.
8. Eight set의 global ID/scenario uniqueness와 56 ordered cross-binding을 전부 거부합니다.
9. 기존 seven product set의 `73 passing + 4 deviation`, full/race/CGO=0/vet와
   portable/exact Python을 회귀 검증합니다.
10. 독립 contract/false-green audit 후 ADR-0016을 수정하고 제품 work를 별도
    작성합니다.

## 완료 조건

- [x] MIG-037..046 exact disposable probe와 payload review
- [x] Contract ID/scenario/title/provenance/phase/comparison dimension 10개 잠금
- [x] Empty, first before/after와 linear middle before/after state 검증
- [x] Cross-app dependency, multiple/shared target와 latest leaves state 검증
- [x] Applied prefix, unrelated known branch와 unknown legacy exclusion state 검증
- [x] Logical state가 deliberately divergent live DB/generated-latest에서 파생되지 않음
- [x] Capture의 DB before/after unchanged와 DDL/write/non-SELECT 0
- [x] Two-process random-hashseed oracle byte identity와 SHA-256 checksum
- [x] Static fixture MIG-037..046 ordered `not_implemented` mismatch 정확히 10개
- [x] Product adapter 없음이 exit 2/no actual output으로 드러남
- [x] Eight set global ID/scenario uniqueness와 56 ordered cross-binding 거부
- [x] ProjectState/target/applied/graph/metrics mutation이 comparator mismatch를 냄
- [x] Contract ID/result hardcode와 setup state/cache 재사용 감사 통과
- [x] Existing `73 passing + 4 deviation` 제품 conformance 회귀 없음
- [x] 완료 상태를 `73 passing + 4 deviation + 10 oracle_locked`, reference 87개로
  구분하고 87개 전체를 제품 통과로 표현하지 않음
- [x] 기존 일곱 artifact payload/checksum과 DEV-0001 의미 불변
- [x] Full Go/race/CGO=0/vet, portable/exact Python, checksum과 Markdown/link gate 통과
- [x] ADR/work/CURRENT/matrix/evidence가 같은 checkout을 가리킴

## 진행 기록

- [x] GDJ-0014 recorder-backed read/check/plan 제품 경계와 10-contract 0-diff 완료
- [x] Recorder identity만으로 historical ProjectState를 복원할 수 없는 gap 분리
- [x] Contract-only 범위와 Proposed ADR-0016 작성
- [x] Pinned source/provenance와 one-off exact probe
- [x] Eighth machine artifact·false-green gate
- [x] Full verification·독립 audit·handoff

## 수정 파일

Machine artifact commit `594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`는 다음을
변경했습니다.

- `conformance/contracts/migration-state-reconstruction-manifest.json`: MIG-037..046
  ordered `oracle_locked` 계약과 provenance
- `conformance/runners/django/migration_state_reconstruction_scenarios.py` 및 전용 test:
  fresh loaded definition/public startup replay와 divergent live DB observation
- `conformance/oracles/**/migration-state-reconstruction-oracle.json`, static fixture와
  `SHA256SUMS`: exact oracle와 명시적 미구현 baseline
- `conformance/internal/protocol/migration_state_reconstruction_artifacts_test.go` 및 기존
  artifact test: 8-set identity/cross-binding/hash/semantic mutation gate
- Django runner registry/safety tests, `godjcheck` fail-closed test와 `Makefile`: reference
  검증 wiring만 추가하고 제품 adapter target은 기존 7개로 보존
- 이 work와 [ADR-0016](../docs/adr/0016-historical-project-state-reconstruction.md):
  explicit empty/latest 경계와 observed field dimensions 보강

`migrations/**`, `db/**`, `conformance/runners/godj/**` 제품 source는 수정하지 않았습니다.

## 결정된 사항

- 2026-08-08: 다음 순서는 schema DSL/file loader 선행 구현이 아니라
  historical-state exact contract으로 고정합니다.
- 2026-08-08: Loaded migration definition의 state replay를 의미 소스로 두고 live
  DB/current generated model introspection을 금지합니다.
- 2026-08-08: Graph projection의 target before/after와 recorder-applied startup projection을
  동일 set 안의 별도 scenario로 구분합니다.
- 2026-08-08: New contract는 `oracle_locked`/static `not_implemented`로 남겨 제품
  구현을 거짓으로 표시하지 않습니다.
- 2026-08-08: MIG-037 explicit empty와 MIG-044 omitted latest는 서로 다른 tagged request
  의미이며 nil/empty variadic 차이로 public API를 추론하지 않습니다.
- 2026-08-08: Applied startup은 private helper가 아니라 public
  `MigrationExecutor.migrate(targets=[], plan=[])`를 관찰합니다.
- 2026-08-08: Canonical state는 lowercase model key, explicit table/column, declaration-order
  field kind/PK/null/max-length와 supported scalar default를 보존합니다.

## 남은 제한과 후속 결정

외부 blocker는 없습니다. 다음 항목은
[GDJ-0016](0016-historical-project-state-reconstruction-product-slice.md)에서 결정합니다.

- Public reconstructor/position type의 이름, zero-value와 exact error taxonomy
- Planner와 state reconstructor의 immutable graph kernel 공유 방식
- Full definition/operation clone 계약과 후속 loader ownership
- Result cache가 필요한지와 cache key/version/ownership

Relations/`real_apps`, replacement/squash, file loader/CLI, data callback과 lifecycle lock은
GDJ-0016에도 포함하지 않습니다.

## 테스트 증거

- Baseline:
  [EVID-20260808-013](../docs/status/TEST_EVIDENCE.md#evid-20260808-013--gdj-0014-recorder-backed-restart-planning-product-slice)
- Product baseline: `a9ce9597551840f1be8e1f27006d427842f38081`, `73 passing + 4 deviation`
- Machine artifact: `594bd9c68b609ea8c6dfb0a3a5dcf9466a336972`
- Final evidence:
  [EVID-20260808-014](../docs/status/TEST_EVIDENCE.md#evid-20260808-014--gdj-0015-historical-projectstate-reconstruction-compatibility-contracts)
- Portable Python 114개 중 exact-only 11 skip, exact profile 114/114 pass
- Not run: GitHub-hosted CI — branch를 push하지 않음

## 위험과 rollback

- Django private object/API를 payload에 직접 노출하면 Python 구현을 복제하게 됩니다.
- `ProjectState`를 DB schema로 관찰하면 simple fixture에서 false green이 됩니다.
- Applied target tail만 replay하면 unrelated known branch가 누락됩니다.
- Unknown recorder key를 dummy schema로 변환하면 history identity와 model semantics가 뒤섞입니다.
- Field payload를 name만 비교하면 null/default/kind regression을 놓칩니다.
- Contract rollback은 eighth manifest/runner/oracle/static/protocol wiring만 되돌리고
  `a9ce959` 제품 source, MIG-001..036 artifact와 외부 Django checkout을 보존합니다.

## 다음 정확한 작업

[GDJ-0016](0016-historical-project-state-reconstruction-product-slice.md)에서 loaded
definition을 deep-copy하는 immutable reconstructor, explicit tagged empty/latest/before/after/
applied request, existing planner graph kernel 재사용과 10개 live GoDj adapter를 구현합니다.
Locked Django oracle/static fixture bytes는 변경하지 않습니다.

## 결과와 인수인계

MIG-037..046은 exact Django 6.1 oracle 10 `observed`, manifest 10 `oracle_locked`, static
10 `not_implemented`로 잠겼습니다. Reference는 87개지만 제품 분류는 계속
`73 passing + 4 deviation`이며 GoDj product binary는 새 scenario를 exit 2/no output으로
거부합니다. 독립 감사의 public/private path, lexical dependency, DDL insertion과
ID-dispatch/wrong-wrapper 변이를 모두 회귀 gate가 차단했습니다. 다음 제품 작업은 이
contract를 구현하되 public loader/CLI와 lock 범위를 넓히지 않습니다.
