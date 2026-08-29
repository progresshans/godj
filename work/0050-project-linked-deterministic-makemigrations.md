---
id: GDJ-0050
status: active
updated: 2026-08-30
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "162b03d65b2392c2ef0647dd7157e686e57de5b3"
depends_on: ["GDJ-0016", "GDJ-0019", "GDJ-0022", "GDJ-0036", "GDJ-0037", "GDJ-0049"]
contracts: ["MIG-099..MIG-110", "Q-010", "Q-012"]
allowed_paths:
  - "cmd/godj/**"
  - "project/**"
  - "migrations/**"
  - "internal/migrationautodetect/**"
  - "internal/projectcheck/**"
  - "internal/projectgenerate/**"
  - "internal/projectmigration/**"
  - "internal/compiletest/**"
  - "examples/**"
  - "conformance/contracts/**"
  - "conformance/fixtures/**"
  - "conformance/oracles/django-6.1-sqlite-darwin-arm64/**"
  - "conformance/runners/django/**"
  - "conformance/runners/godj/**"
  - "conformance/internal/protocol/**"
  - "conformance/migrationwriterproduct/**"
  - "conformance/postgresproduct/**"
  - "conformance/systemstate/attestations/**"
  - "conformance/README.md"
  - "Makefile"
  - ".github/workflows/ci.yml"
  - "docs/adr/0052-project-linked-deterministic-makemigrations.md"
  - "docs/adr/README.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0050-project-linked-deterministic-makemigrations.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# GDJ-0050 — Project-linked Deterministic Makemigrations

## 사용자에게 보이는 결과

Project schema 선언을 current generated model과 같은 원본에서 읽어 strict current migration definition을 만들고, 그 결과를
existing `godj migrate`로 적용할 수 있습니다.

```text
godj generate
godj makemigrations
godj migrate
godj runserver
```

첫 public scope는 `CreateModel`과 `AddField`로 표현 가능한 scalar/ForeignKey additive change입니다. 삭제·rename·alter처럼 아직
표현할 수 없는 변경은 migration을 추측하지 않고 기존 catalog를 그대로 보존한 채 structured failure로 닫습니다.

## 목표

- `LoadedDefinitionSet`의 latest historical `ProjectState`와 normalized `ProjectSpec`의 managed app state를 pure diff합니다.
- No-change, fresh initial, appended model/field와 same/cross-app ForeignKey dependency를 deterministic plan으로 만듭니다.
- Current Definition format 1의 canonical encoder와 strict load/reconstruct round-trip을 추가합니다.
- One private request가 project schema와 filesystem/programmatic definition catalog를 함께 snapshot합니다.
- Global CLI가 exact `makemigrations`, `--dry-run`, `--check`, `--project` argv와 DB-free closed result를 게시합니다.
- Exactly-one writer root, app-prefixed flat filenames, no-overwrite/CAS와 dependency-valid prefix publication을 구현합니다.
- Candidate publication 전에 combined catalog가 strict load되고 latest managed state가 desired state와 exact 일치함을 증명합니다.
- 구조가 다른 두 app/cross-app relation fixture와 external project에서 생성 -> migrate -> restart를 검증합니다.
- MIG-099..110 mixed Django/GoDj authority contract를 별도 set으로 게시합니다.

## 비목표

- DeleteModel, RemoveField, AlterField와 모든 rename operation
- interactive rename/default/backfill prompt, custom name과 timestamp fallback
- self/circular new relation splitting, multiple leaf merge, squash와 optimizer
- `--empty`, `--merge`, `--update`, app filter, custom/data/RunSQL operation
- DB introspection/adoption, applied history check와 multi-DB router
- multiple writable roots/app-to-root mapping과 distributed filesystem writer
- Definition/Schema IR/ProjectState version 추가, existing generated ABI 또는 backend lifecycle 변경
- installed runner/library/generator semver와 general upgrade/repair
- Q-019 retained unknown-outcome DB resource policy

## 선행 조건과 기준 상태

- Baseline `162b03d65b2392c2ef0647dd7157e686e57de5b3`는 clean GDJ-0049 terminal documentation head입니다.
- GDJ-0049/ADR-0051/MIG-087..098은 completed/Accepted/12 passing이고 exact predecessor source
  `a909692...`는 EVID-146 hosted 41/41·464/464를 통과했습니다.
- Current-only IR/Definition/State, opaque loaded set, historical reconstructor, `ProjectSpec`, project selector/runner,
  recoverable generated publication과 explicit SQLite/PostgreSQL migrate를 재사용합니다.
- 다음 ID 공간 GDJ-0050, ADR-0052, MIG-099..110과 EVID-147은 activation 시점에 비어 있음을 확인했습니다.
- Worktree는 activation 전에 clean이었고 보존할 user dirty file은 없습니다.

## Django Reference / Contract

- Exact Django profile: 6.1 tag/commit `fe0a859f537d4238cf49fca39073513206f83122`; local installed core file과
  official tag의 audited 4개 source는 byte-identical입니다.
- Reference paths:
  - `django/db/migrations/autodetector.py::MigrationAutodetector.changes`
  - `django/db/migrations/migration.py::Migration.suggest_name`
  - `django/db/migrations/writer.py::MigrationWriter`
  - `django/core/management/commands/makemigrations.py`
  - `tests/migrations/test_autodetector.py`
  - `tests/migrations/test_commands.py`
- Django authority: no-change, initial, next leaf dependency, CreateModel/AddField, same/cross-app relation dependency,
  `--dry-run` no write와 `--check` clean/drift exit.
- GoDj authority: current JSON bytes, timestamp-free determinism, DB-zero, source CAS, no-overwrite, atomic recovery와 closed unsupported delta.
- Python module/header/output bytes, internal class/questioner와 timestamp fallback은 비교하지 않습니다.

### Phase A MIG-099..110 reference

Phase A에서 exact 12개 manifest/NI/oracle을 별도 set으로 byte-lock해 `oracle_locked`로 게시했습니다. 당시에는 public CLI/private
protocol/publication adapter가 모두 없었습니다. Current Phase B에는 read-only CLI/private protocol만 존재하며 publication/product
adapter는 없으므로 actual product가 같은 observation을 만족한 뒤에만 `passing`으로 전환합니다.

| ID | Scenario | Required observation |
|---|---|---|
| MIG-099 | `no_changes_clean` | latest managed state와 desired가 같으면 candidate/write/DB open 0, exit 0 |
| MIG-100 | `fresh_initial` | history 없는 app마다 `0001_initial`과 exact CreateModel state 생성 |
| MIG-101 | `repeat_after_initial_noop` | generated source를 load한 fresh repeat는 no-op, prior bytes 불변 |
| MIG-102 | `deterministic_candidate` | input permutation/process/time과 무관한 roster/name/dependency/operation/document bytes |
| MIG-103 | `relation_dependency_topology` | supported same-app target creator 선행과 cross-app target migration dependency |
| MIG-104 | `additive_model_and_field_tail` | appended model과 nullable/no-default Char/FK field는 next number, same-app leaf dependency와 exact CreateModel/AddField |
| MIG-105 | `dry_run_no_mutation` | write mode와 같은 plan/path/hash, file/DB mutation 0, exit 0 |
| MIG-106 | `check_clean_and_drift` | clean 0, pending candidate 1, 두 경우 file/DB mutation 0 |
| MIG-107 | `unsupported_delta_fail_closed` | removal/reorder/rename/alter/self-cycle와 delta에 successor가 필요한 noncanonical leaf는 structured failure와 candidate 0 |
| MIG-108 | `snapshot_and_protocol_boundary` | schema+catalog one request snapshot, strict bounded wire, existing protocol byte no-diff |
| MIG-109 | `atomic_concurrent_publication` | two writers는 lock 아래 replan하고 overwrite/stale false success 0, 각 visible file은 complete |
| MIG-110 | `interruption_recovery_and_roundtrip` | fault/cancel 뒤 strict-load 가능한 dependency prefix, fresh resume/final desired equality, unsafe residue 0 |

## 설계와 가설

- [ADR-0052](../docs/adr/0052-project-linked-deterministic-makemigrations.md)가 activation 설계 정본입니다.
- Pure detector는 `internal/migrationautodetect`에 두어 premature public API와 root `migrations -> definition` import cycle을
  만들지 않습니다.
- Definition encoder는 strict wire owner인 `migrations/definition`에 둡니다.
- Project child는 plan/candidate bytes를 만들 뿐 filesystem을 쓰지 않습니다. Global `internal/projectmigration` owner가 retained root,
  lock, CAS, validation과 atomic no-replace prefix publication을 소유합니다.
- First writer invocation은 exactly one configured filesystem root를 요구하고 programmatic sources는 read-only입니다.
- Managed app은 desired `ProjectSpec.Apps`와 file-discovered migration app의 합집합입니다. Programmatic-only app은 historical state로
  보존합니다.
- Existing model/field order는 exact prefix입니다. Existing model의 AddField는 DB-free로 populated SQLite에도 안전한
  nullable/no-default CharField 또는 ForeignKey만 허용합니다. 이 restriction은 rename/backfill heuristic을 피하고 unsupported
  mutation을 canonical point에서 닫습니다.
- 한 app에 한 candidate migration, first `0001_initial`, 이후 exact single numeric leaf successor를 사용합니다. Composite change
  suffix는 `godj/migration-name/v1` domain의 canonical semantic SHA-256 앞 12 lowercase hex로 결정하고 exact producer는
  `godj-makemigrations`/`1`입니다. Clean no-op은 successor naming을 요구하지 않습니다.
- Writer root는 사전에 존재하는 exact one physical directory로 제한하고 retained device/inode + fd-relative no-follow authority를
  publication 완료까지 유지합니다.
- Runner build 전 declaration/build-input source fingerprint, normalized ProjectSpec/generated identity와 catalog digest를 묶은
  independent CAS를 first rename 직전에 다시 확인합니다. Stale child self-report만 비교하지 않습니다.
- Rename 뒤 directory durability를 증명하지 못하면 ordinary failure/success로 추측하지 않고 recovery-required/unknown으로
  반환합니다. Next normal invocation만 owned temp/catalog를 lock 아래 복구·fresh replan하며 read-only mode는 진단만 합니다.

## 구현 단계

### Phase A — activation, reference and pure boundaries

- [x] Current status/numbering/roadmap와 Django 6.1 exact source/test authority 감사
- [x] Proposed ADR-0052와 active GDJ-0050의 scope, non-scope, owner와 planned MIG-099..110 고정
- [x] Mixed-authority manifest/oracle/NI actual을 등록하고 existing artifact/product aggregate no-diff 확인
- [x] Pure `internal/migrationautodetect` plan/error/deep-copy contract 구현
- [x] Current Definition deterministic encoder와 strict combined round-trip 구현

### Phase B — project-linked snapshot and public read-only modes

- [x] `project.Config` snapshot owner와 separate strict makemigrations private protocol 구현
- [x] Exact global argv, project selection/build/cleanup와 schema+catalog one-request planning 구현
- [x] `--dry-run` and `--check`의 deterministic result/exit/file-zero/DB-zero 구현
- [x] Normal/dry-run/check가 exact producer, filename/project-relative path, source ID, roster와 resource limit을 같은 pure preflight로
      검증하고 host-independent report를 반환
- [x] 모든 semantic candidate를 먼저 Encode하고 existing catalog와 strict Load/reconstruct한 뒤에만 result를 반환하며,
      escaped NUL 등 raw state보다 wire가 커지는 document-limit failure를 publication 전 0-candidate로 닫기
- [x] Runner build 전 declaration/build-input source fingerprint를 캡처하고 build 뒤 child 실행 전과 child response를 공개하기 전에
      retained project-root authority에서 independent source/catalog CAS를 재검증. Phase B의 read-only 결과는 같은 inode의 in-place
      descriptor/build-input 변경도 stale로 닫음
- [x] Existing check/generate/migrate protocol bytes와 opener-zero invariant 보존

### Phase C — recoverable write and multi-app vertical

- [ ] Pre-existing retained physical root, exactly-one writer root, app-prefixed roster와 dedicated lock/CAS 구현
- [ ] Normal mode는 initial read-only snapshot으로 writer root를 확인한 뒤 lock 아래 fresh second private request로 다시 plan하고,
      first/every rename 직전 source/catalog/root identity CAS를 재검증. 각 private request 내부는 schema+catalog one-snapshot 계약을 유지
- [ ] Atomic no-overwrite append, fault/cancel dependency-prefix resume와 concurrent serialized replan 구현
- [ ] Reserved owned-temp namespace와 rename-after/directory-fsync unknown outcome의 structured recovery-required/fresh-invocation
      recovery를 구현하고 unknown file/symlink/non-regular/path rebound를 mutation 전 거부
- [ ] Two-app cross-app FK second-model fixture에서 initial/additive/repeat/unsupported flow 구현
- [ ] Generated candidate -> SQLite migrate/no-op/restart actual E2E 구현

### Phase D — PostgreSQL and product publication

- [ ] Clean PostgreSQL 17.10 generated candidate -> migrate/no-op/restart와 secret/DB-zero boundary 검증
- [ ] MIG-099..110 actual adapter/strict comparison을 등록하고 passing/deviation을 증거대로 전환
- [ ] External module/archive가 public API와 project runner만 사용함을 검증

### Phase E — frozen milestone

- [ ] Affected normal/race/CGO0/vet/generated drift와 scoped inventory 통과
- [ ] Full `make ci`, Linux/386, repository-external archive와 independent audit를 frozen source에서 한 번 실행
- [ ] Source-bound PostgreSQL attestation이 바뀌면 current source에서 재캡처
- [ ] Exact submitted-head Hosted matrix 뒤 ADR/status/evidence terminal 동기화

## 완료 조건

- [ ] 사용자는 supported additive schema를 `godj makemigrations`로 strict current definition에 게시할 수 있음
- [ ] no-change/repeat/dry-run/check는 exact mutation/exit 계약을 만족함
- [ ] unsupported/ambiguous delta와 source race는 partial/stale candidate 없이 fail-closed함
- [ ] candidate bytes는 deterministic하고 strict load/latest reconstruction이 desired managed state와 같음
- [ ] same/cross-app ForeignKey와 structurally different second model을 실제 generate/migrate/restart로 검증함
- [ ] concurrent/fault/cancel publication은 complete dependency prefix와 no-overwrite/fresh resume를 만족함
- [ ] SQLite/PostgreSQL/external consumer와 affected/final hosted gate를 실제 실행 증거로 기록함
- [ ] ADR-0052, Q-010/Q-012, matrix/CURRENT/work/evidence가 실제 상태와 일치함

## 진행 기록

- [x] 조사 — current architecture/status, numbering과 Django 6.1 source/test authority 병렬 감사
- [x] 설계/ADR — additive-only managed-app diff와 global publication owner를 Proposed로 활성화
- [x] Phase A 구현 — pure detector/current encoder와 reference-only artifact scaffold
- [x] Phase B 구현 — project-owned one-request snapshot, strict private v1 wire와 global read-only CLI/CAS
- [ ] Phase C-D publication/product 구현
- [x] Phase A 테스트 — affected normal/race/CGO0/vet/386, Python/reference contract와 deterministic artifact
- [x] Phase A 문서와 인수인계
- [x] Phase B 테스트 — affected normal/race/CGO0/vet/count-10, actual clean CLI/generate drift와 네 차례 독립 감사
- [x] Phase B 문서와 인수인계

## 수정 파일

- `internal/migrationautodetect/**`: pure additive diff, deterministic naming/dependency/topology와 deep-copy/error tests
- `migrations/definition/{definition.go,external_test.go,encode*.go}`: public current deterministic encoder와 bounded round-trip tests
- `conformance/contracts/migration-writer-manifest.json`, `conformance/fixtures/godj-migration-writer-not-implemented.json`,
  `conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-writer-oracle.json`: MIG-099..110 reference lock
- `conformance/runners/django/migration_writer_*`, `conformance/internal/protocol/migration_writer_artifacts_test.go`: mixed authority runner/tests와 lock
- `Makefile`, `.github/workflows/ci.yml`, aggregate protocol locks: new reference set inventory wiring; product job/adapter count 불변
- `internal/projectmigration/**`: immutable project schema/catalog snapshot, deterministic candidates와 separate bounded v1 wire
- `internal/projectcheck/{makemigrations_*,linked/makemigrations_*}`: exact global read-only modes, retained source/catalog CAS,
  project child snapshot과 closed failure/process/cleanup contract
- `cmd/godj/{main_unix.go,makemigrations_unix_test.go}`, `project/{project_unix.go,project_unix_test.go}`:
  public dispatch, opaque bounded config ownership와 actual external-project DB-zero/read-only proof
- `docs/adr/0052-project-linked-deterministic-makemigrations.md`, `work/0050-project-linked-deterministic-makemigrations.md`:
  Proposed decision와 active execution packet
- compatibility/source/status/capability/roadmap mirrors: 실제 Phase A+B checkpoint 상태와 provenance

## 결정된 사항

- 2026-08-29: 현재 제품 루프의 가장 큰 단절인 writer/autodetector를 Q-019 hardening보다 먼저 진행합니다. DB-free packet이라
  Q-019는 blocker가 아니며 production readiness 전 P1 후속으로 유지합니다.
- 2026-08-29: Product 1.0 전체 scope를 선행 동결하지 않고 current supported operation을 end-to-end로 닫는 wide bounded packet을
  채택합니다.
- 2026-08-29: Django observable contract와 GoDj deterministic/publication strengthening을 같은 결과 set에서 authority별로 구분합니다.
- 2026-08-30: MIG-099..110은 reference-only `oracle_locked`입니다. Phase B public read-only CLI/private protocol은 그 상태를
  승격하지 않으며 product adapter/publication은 아직 미구현으로 분리합니다.
- 2026-08-30: Clean no-op은 noncanonical leaf라도 successor naming이 필요 없으므로 성공합니다. Delta가 생겨 successor가 필요한
  noncanonical leaf만 structured unsupported로 닫습니다.
- 2026-08-30: Composite name은 exact versioned digest domain, Definition producer는 `godj-makemigrations`/`1`로 고정합니다.
- 2026-08-30: Source CAS는 catalog token만으로 충분하지 않으며 retained physical source fingerprint와 semantic ProjectSpec/catalog
  digest를 결속합니다. Rename 뒤 directory durability unknown은 recovery-required로 분류하고 자동 retry/rollback을 금지합니다.
- 2026-08-30: Phase B는 build 전/후와 child response 전의 read-only descriptor/build-input/catalog CAS까지 소유하고, lock 아래 fresh
  replan과 first/every rename 직전 CAS는 Phase C publication 책임으로 분리합니다. Phase C 전 pending normal command는 read-only plan을
  게시 성공으로 가장하지 않고 structured publication-unavailable로 닫고, clean normal command는 write 없이 성공합니다.
- 2026-08-30: `GeneratedBundle.SnapshotSHA256()`는 generator ABI를 포함한 generated identity이며 normalized `ProjectSpec` semantic
  digest나 physical/programmatic catalog digest와 동일시하지 않습니다. 세 authority는 별도 versioned domain으로 검증합니다.

## 미결정/Blocker

- No current blocker for Phase C start. Multiple writable roots, destructive operation과 self/cyclic relation splitting은 명시적 후속 범위입니다.

## 테스트 증거

- Evidence IDs: EVID-147, EVID-148
- Result: Phase A pure/reference gate와 Phase B strict private protocol/global read-only CLI/CAS scoped gate가 통과했습니다. Exact command,
  implementation commit/tree와 CI #166 predecessor diagnostic은
  [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#evid-20260830-148--gdj-0050-phase-b-project-linked-read-only-makemigrations-checkpoint)에 기록합니다.
- Not run: Phase C publication/concurrency/recovery fault tests, SQLite/PostgreSQL generated-writer E2E, MIG-099..110 product adapter,
  full `make ci`, source-bound PostgreSQL attestation recapture와 current exact-head Hosted final

## 위험과 rollback

- Public CLI 이름이 broad support로 오해될 수 있으므로 result/error와 capability docs에 additive-only 범위를 반복해 명시합니다.
- Diagnostic definition clones를 실행 authority로 재사용하지 않고 candidate 전체를 strict load/reconstruct해 다시 seal합니다.
- Phase B command는 기존 migration bytes나 DB를 수정하지 않는 read-only 경계입니다. Publication path는 아직 없고 구현 커밋은
  독립적으로 revert 가능합니다.
- Multi-app dependency/cycle 처리를 잘못하면 historical state가 달라지므로 strict round-trip equality가 mandatory pre-publication gate입니다.
- Workflow/attestation source를 바꾸면 기존 source-bound evidence를 새 head에 재사용하지 않습니다.

## 다음 정확한 작업

1. Phase C fault model에서 retained physical writer root, dedicated lock과 fresh second private replan을 먼저 고정
2. First/every rename CAS, atomic no-replace dependency-prefix publication과 recovery-required outcome을 구현
3. Two-app cross-app FK fixture에서 generated candidate -> SQLite migrate/restart 수직 단면을 검증

## 결과와 인수인계

GDJ-0049 terminal product protocol bytes는 보존됩니다. GDJ-0050 Phase A의 pure detector/current encoder와 reference-only
MIG-099..110 lock 위에 Phase B exact public `makemigrations` argv, separate strict private protocol, opaque project config snapshot,
deterministic dry-run/check/clean-normal과 retained build-input/catalog CAS를 연결했습니다. Pending normal은 Phase C 전
`publication_unavailable`로 닫고 command 전체는 file/DB mutation 0입니다. Filesystem lock/publication/recovery, product adapter와
SQLite/PostgreSQL writer E2E는 아직 없습니다. Source-bound PostgreSQL attestation은 workflow change 뒤 stale이므로 Phase E frozen
milestone에서만 재캡처합니다.
