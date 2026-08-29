---
id: GDJ-0050
status: active
updated: 2026-08-30
baseline_branch: "feature/pre-release-compatibility-reset"
baseline_commit: "162b03d65b2392c2ef0647dd7157e686e57de5b3"
depends_on: ["GDJ-0016", "GDJ-0019", "GDJ-0022", "GDJ-0036", "GDJ-0037", "GDJ-0049"]
contracts: ["MIG-099..MIG-110", "DEV-0010", "Q-010", "Q-012"]
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
  - "conformance/cmd/godjcheck/**"
  - "conformance/internal/protocol/**"
  - "conformance/migrationwriterproduct/**"
  - "conformance/postgresproduct/**"
  - "conformance/runserverproduct/**"
  - "conformance/systemstate/attestations/**"
  - "conformance/README.md"
  - "Makefile"
  - ".github/workflows/ci.yml"
  - "docs/adr/0052-project-linked-deterministic-makemigrations.md"
  - "docs/adr/README.md"
  - "docs/CAPABILITY_CATALOG.md"
  - "docs/COMPATIBILITY.md"
  - "docs/DEVIATIONS.md"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/SOURCES.md"
  - "docs/TESTING.md"
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
protocol/publication adapter가 모두 없었습니다. Phase C checkpoint에는 CLI/private protocol과 recoverable publication이
존재했지만 product adapter는 아직 없었습니다. Phase D actual comparison은 아래 observation을 실행한 뒤
MIG-099/100/101/102/108/109/110을 `passing`, MIG-103..107을 DEV-0010 `deviation`으로 전환했습니다.

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

- [x] Pre-existing retained physical root, exactly-one writer root, app-prefixed roster와 directory-inode lock/CAS 구현
- [x] Normal mode는 initial read-only snapshot으로 writer root를 확인한 뒤 lock 아래 fresh second private request로 다시 plan하고,
      first/every rename 직전 source/catalog/root identity CAS를 재검증. 각 private request 내부는 schema+catalog one-snapshot 계약을 유지
- [x] Kernel atomic no-overwrite append, fault/cancel dependency-prefix resume와 concurrent serialized replan 구현
- [x] Reserved owned-temp namespace와 rename-after/directory-fsync unknown outcome의 structured recovery-required/fresh-invocation
      recovery를 구현하고 empty/mid-write temp는 fresh candidate 이름·byte prefix와 결속하며 unknown file/symlink/non-regular/path
      rebound는 mutation 전 거부
- [x] Two-app cross-app FK second-model fixture에서 initial/repeat actual publication 구현
- [x] Existing-history additive plan과 unsupported detector/preflight를 fail-closed 검증
- [x] Actual public generated candidate -> SQLite migrate/no-op/fresh-process restart E2E 구현

### Phase D — PostgreSQL and product publication

- [x] Clean PostgreSQL 17.10 generated candidate -> migrate/no-op/restart와 secret/DB-zero boundary 검증
- [x] MIG-099..110 actual adapter/strict comparison을 등록하고 passing/deviation을 증거대로 전환
- [x] External module이 public API와 project runner만 사용함을 검증

### Phase E — frozen milestone

- [x] Affected normal/race/CGO0/vet/generated drift와 scoped inventory 통과
- [x] Frozen behavioral source에서 source-bound PostgreSQL attestation을 두 번 독립 재캡처하고 exact artifact 게시
- [x] Full `make ci`, all-package Linux/386 compile-only, repository-external archive와 independent audit를 attestation publication
      descendant에서 한 번 실행
- [ ] Exact submitted-head Hosted matrix 뒤 ADR/status/evidence terminal 동기화

## 완료 조건

- [x] 사용자는 supported additive schema를 `godj makemigrations`로 strict current definition에 게시할 수 있음
- [x] no-change/repeat/dry-run/check는 exact mutation/exit 계약을 만족함
- [x] unsupported/ambiguous delta와 source race는 partial/stale candidate 없이 fail-closed함
- [x] candidate bytes는 deterministic하고 strict load/latest reconstruction이 desired managed state와 같음
- [x] same/cross-app ForeignKey와 structurally different second model을 실제 generate/migrate/restart로 검증함
- [x] concurrent/fault/cancel publication은 complete dependency prefix와 no-overwrite/fresh resume를 만족함
- [x] SQLite/PostgreSQL/external consumer와 affected/final local gate를 실제 실행 증거로 기록함
- [ ] Exact submitted-head Hosted gate를 실제 실행 증거로 기록함
- [x] ADR-0052, Q-010/Q-012, matrix/CURRENT/work/evidence가 현재 Phase E local-final 상태와 일치함

## 진행 기록

- [x] 조사 — current architecture/status, numbering과 Django 6.1 source/test authority 병렬 감사
- [x] 설계/ADR — additive-only managed-app diff와 global publication owner를 Proposed로 활성화
- [x] Phase A 구현 — pure detector/current encoder와 reference-only artifact scaffold
- [x] Phase B 구현 — project-owned one-request snapshot, strict private v1 wire와 global read-only CLI/CAS
- [x] Phase C recoverable publication/SQLite vertical 구현
- [x] Phase D PostgreSQL/product adapter/external consumer 구현
- [x] Phase A 테스트 — affected normal/race/CGO0/vet/386, Python/reference contract와 deterministic artifact
- [x] Phase A 문서와 인수인계
- [x] Phase B 테스트 — affected normal/race/CGO0/vet/count-10, actual clean CLI/generate drift와 네 차례 독립 감사
- [x] Phase B 문서와 인수인계
- [x] Phase C 테스트 — affected normal/race/CGO0/vet/count-10, SIGKILL fault points, Linux/amd64 compile-only와 actual
      cross-app SQLite lifecycle
- [x] Phase C 문서와 인수인계
- [x] Phase D 테스트 — PostgreSQL 17.10 normal/race/CGO0, MIG-099..110 strict actual, external module normal/race/CGO0,
      affected normal/vet/generate drift와 여러 독립 감사 pass
- [x] Phase D 문서와 인수인계

## 수정 파일

- `internal/migrationautodetect/**`: pure additive diff, deterministic naming/dependency/topology와 deep-copy/error tests
- `migrations/definition/{definition.go,external_test.go,encode*.go}`: public current deterministic encoder와 bounded round-trip tests
- `conformance/contracts/migration-writer-manifest.json`, `conformance/fixtures/godj-migration-writer-not-implemented.json`,
  `conformance/fixtures/godj-migration-writer-deviation-expected.json`,
  `conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-writer-oracle.json`: MIG-099..110 reference/product lock과 DEV-0010
- `conformance/runners/{django,godj}/migration_writer_*`, `conformance/cmd/godjcheck/**`,
  `conformance/internal/protocol/migration_writer_artifacts_test.go`: mixed-authority runner, oracle-blind actual, strict sparse deviation와 lock
- `Makefile`, `.github/workflows/ci.yml`, aggregate protocol locks: 23번째 product adapter, isolated external package,
  PostgreSQL 21-test selector와 relation 929-test ownership inventory wiring
- `internal/projectmigration/**`: immutable project schema/catalog snapshot, deterministic candidates와 separate bounded v1 wire
- `internal/projectcheck/{makemigrations_*,linked/makemigrations_*}`: exact global modes, retained source/catalog/root CAS,
  directory-inode lock, no-replace durable-prefix publication, complete/incomplete temp recovery와 closed failure/process/cleanup contract
- `cmd/godj/{main_unix.go,makemigrations_unix_test.go,makemigrations_publish_unix_test.go}`,
  `project/{project_unix.go,project_unix_test.go}`: public dispatch, opaque bounded config ownership, external-project DB-zero proof와
  actual cross-app publish -> SQLite migrate/no-op/fresh-process restart
- `cmd/godj/makemigrations_postgres_unix_test.go`: PostgreSQL generated migrate/no-op/restart, revision/schema no-op fingerprint와
  DB-zero/secret/process-group boundary
- `conformance/migrationwriterproduct/**`: repository-external public-import-only module의 actual publish -> SQLite migrate/no-op/restart
- `internal/projectcheck/makemigrations_conformance_unix.go`: arbitrary public callback을 노출하지 않는 closed internal fault selector
- `docs/adr/0052-project-linked-deterministic-makemigrations.md`, `work/0050-project-linked-deterministic-makemigrations.md`:
  Proposed decision와 active execution packet
- compatibility/source/status/capability/roadmap mirrors: 실제 Phase A-E local-final 상태와 provenance

## 결정된 사항

- 2026-08-29: 현재 제품 루프의 가장 큰 단절인 writer/autodetector를 Q-019 hardening보다 먼저 진행합니다. DB-free packet이라
  Q-019는 blocker가 아니며 production readiness 전 P1 후속으로 유지합니다.
- 2026-08-29: Product 1.0 전체 scope를 선행 동결하지 않고 current supported operation을 end-to-end로 닫는 wide bounded packet을
  채택합니다.
- 2026-08-29: Django observable contract와 GoDj deterministic/publication strengthening을 같은 결과 set에서 authority별로 구분합니다.
- 2026-08-30 (Phase A-C checkpoint): MIG-099..110은 reference-only `oracle_locked`였습니다. Phase B public read-only CLI/private
  protocol은 그 상태를 승격하지 않았고 Phase C publication 뒤에도 product adapter는 별도 Phase D로 남겼습니다.
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
- 2026-08-30: Current pending plan은 최대 64 candidates의 hard support ceiling입니다. Historical catalog 2,048 source와
  구분하며 app filter/automatic batching이 없으므로 65개 이상 pending candidate를 재실행 가능한 batch라고 표현하지 않습니다.
  Recovery 뒤 writer directory entry와 candidate 합은 최대 65,536입니다.
- 2026-08-30: 별도 lock/control file 대신 retained writer-directory inode에 `flock`하고 duplicated lock fd identity를 끝까지
  검증합니다. Reserved temp namespace와 unlink recovery는 cooperative GoDj writer 전용이며 non-cooperative local actor는
  비범위입니다.
- 2026-08-30: Phase C publisher는 Darwin/Linux local filesystem의 directory lock/fsync와 kernel no-replace rename을 요구합니다.
  Unsupported syscall/filesystem에는 overwrite fallback하지 않으며 successful `fsync`를 literal hardware power-loss 증명으로
  과장하지 않습니다.
- 2026-08-30: Phase D actual은 MIG-099/100/101/102/108/109/110을 `passing`, MIG-103..107을 exact 19-result-selector
  DEV-0010 `deviation`으로 게시합니다. Current reference는 24/273/552=`237 passing + 24 deviation + 12 oracle_locked`,
  product는 23/261=`237 passing + 24 deviation`이며 MIG-075..086만 locked/unregistered로 남습니다.
- 2026-08-30: `go test -race`는 harness를 계측하지만 테스트가 일반 `go build`로 만든 child CLI까지 race 계측하지 않습니다.
  증거는 실제 subprocess 의미가 race harness 아래 실행됐다고만 표현하고 child binary 계측을 과장하지 않습니다.
- 2026-08-30: 첫 exact-head Hosted 진단에서 제품 assertion과 무관한 source-bound attestation stale, direct-import lock drift,
  cold-cache setup proxy 차단을 확인해 국소 교정했습니다. 같은 실행의 macOS Intel relation 및 portable race에서는 broad package
  병렬 실행이 child build를 압박해 conformance 전용 90초/10분 경계에 닿았습니다. 테스트 범위는 유지하되 무거운 GoDj runner,
  `godjcheck`, multi-runtime worker 패키지를 portable core와 분리해 `-p=1`로 실행하고 relation lane은 relation을 소유하는 정확한
  GoDj runner 7-test와 `godjcheck` 6-test selector만 직렬화합니다. 전체 집계 패키지는 portable gate에서 계속 실행하고 relation
  lane이 우연히 소유하던 full `godjcheck` CGO-disabled coverage도 portable CGO-disabled gate 한 곳으로 이전합니다. Multi-runtime
  worker의 CGO-disabled 범위는 새로 넓히지 않습니다. Migration command의 90초 subprocess 경계는 유지하고 aggregate policy
  test와 package 경계만 각각 15분/20분으로 두되 Hosted job 자체의 상한도 유지합니다. 이는 제품 transaction deadline 변경이
  아니라 false timeout을 줄이는 검증 harness 교정입니다. 새 normal selector inventory는 두 독립 실행에서 모두
  929 run/929 pass/0 skip, 94,689 payload bytes, SHA-256 `e7314f9c6ccfef3c469c7df6f90114fd98a91e094f347c9240829ceff05fad9a`였습니다.
- 2026-08-30: Frozen source `ed2e049e2a53eadd6f2e77ffcec002c5da2d21eb`에서 PostgreSQL 17.10 source-bound
  attestation을 서로 다른 container/cluster/volume/network로 두 번 독립 캡처해 byte-identical 1,134-byte artifact
  `8d8bdd0d...`를 게시했습니다. Publication head `af3aad4f133d13bdf65ba8afa43e518d17bf34cc`에서 focused
  normal/race/CGO0/vet, full `make ci`, 115-package Linux/386 compile-only, exact 929 relation inventory와 1,181-file
  repository-external archive gate가 모두 통과했습니다. Exact submitted-head Hosted와 성공 뒤 terminal 상태 동기화만 남습니다.

## 미결정/Blocker

- No current blocker for the exact submitted-head Hosted gate. Multiple writable roots, 65+ pending candidate batching,
  destructive operation과 self/cyclic relation splitting은 명시적 후속 범위입니다. Current source-bound PostgreSQL attestation과
  local frozen milestone은 완료됐지만 Hosted 성공 전에는 terminal acceptance를 주장하지 않습니다.

## 테스트 증거

- Evidence IDs: EVID-147, EVID-148, EVID-149, EVID-150, EVID-151
- Result: Phase A pure/reference, Phase B strict private protocol/read-only CLI/CAS, Phase C recoverable normal publication/SQLite lifecycle와
  Phase D PostgreSQL/product adapter/external module scoped gate가 통과했습니다. Phase E source freeze, independent A/B PostgreSQL
  attestation, artifact publication, full/386/relation/archive local final은
  [EVID-151](../docs/status/TEST_EVIDENCE.md#evid-20260830-151--gdj-0050-first-hosted-diagnostic-and-frozen-local-final)에
  기록합니다.
- Not run: corrected exact submitted-head Hosted final. Linux publisher runtime은 실행하지 않았고 Linux/386은 compile-only입니다.

## 위험과 rollback

- Public CLI 이름이 broad support로 오해될 수 있으므로 result/error와 capability docs에 additive-only 범위를 반복해 명시합니다.
- Diagnostic definition clones를 실행 authority로 재사용하지 않고 candidate 전체를 strict load/reconstruct해 다시 seal합니다.
- Phase B read-only mode는 기존 migration bytes나 DB를 수정하지 않습니다. Phase C normal mode만 cooperative local writer root에서
  no-replace append하며 실패 뒤 durable prefix를 보존합니다.
- Multi-app dependency/cycle 처리를 잘못하면 historical state가 달라지므로 strict round-trip equality가 mandatory pre-publication gate입니다.
- Workflow/attestation source를 바꾸면 기존 source-bound evidence를 새 head에 재사용하지 않습니다.

## 다음 정확한 작업

Phase E local frozen milestone은 완료됐습니다. 다음 정확한 순서는 다음입니다.

1. EVID-151 local-final documentation descendant를 exact submitted head로 push
2. 그 head의 Hosted matrix를 실행하고 모든 failure/cancellation/required skip을 분류
3. Green exact head에서 ADR-0052 Accepted, GDJ-0050 completed와 terminal status/evidence를 동기화

## 결과와 인수인계

GDJ-0049 terminal product protocol bytes는 보존됩니다. Phase D는 기존 Phase A-C writer 위에 oracle-blind MIG-099..110 actual,
DEV-0010 strict sparse policy, PostgreSQL 17.10 generated migrate/no-op/restart와 repository-external public-module SQLite lifecycle을
연결했습니다. Fault 뒤 durable prefix는 fresh resume 전후 SourceID, bytes와 inode가 불변임을 검증하고, external failure harness는
secret scan 전에 output을 로그로 재노출하지 않습니다. Product aggregate는 23/261=`237 passing + 24 deviation`으로 전환됐고
MIG-075..086만 reference-only locked입니다. Current source-bound attestation과 local final은 끝났지만
ADR-0052/GDJ-0050은 exact submitted-head Hosted 성공 전까지 Proposed/active로 유지합니다.
