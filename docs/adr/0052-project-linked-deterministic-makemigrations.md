# ADR-0052: Project-linked Deterministic Makemigrations

- 상태: Proposed
- 날짜: 2026-08-30
- 관련 work/contract: [GDJ-0050](../../work/0050-project-linked-deterministic-makemigrations.md), MIG-099..110, Q-010, Q-012
- 선행 결정: [ADR-0016](0016-historical-project-state-reconstruction.md), [ADR-0019](0019-versioned-migration-definition-source.md), [ADR-0021](0021-project-linked-migration-check.md), [ADR-0035](0035-pre-release-current-only-format-and-generated-publication.md), [ADR-0036](0036-project-schema-generated-bundle-and-recoverable-publication.md), [ADR-0051](0051-project-linked-explicit-migrate.md)
- 대체하는 ADR: 없음

## 맥락

GoDj의 normalized `ProjectSpec`, current-only Migration Definition/`ProjectState`, strict loader, historical
reconstruction, recoverable generated-source publication과 explicit `godj migrate`는 구현됐습니다. 그러나 사용자는 schema 선언과
generated model을 바꾼 뒤 current-format migration JSON을 직접 작성해야 합니다. 이 수작업 경계가 현재
`generate -> migrate -> runserver` 개발 루프에서 가장 큰 단절입니다.

Django 6.1의 관찰 가능한 authority는 전체 project state 비교, no-change no-op, `0001_initial`, 기존 leaf의 다음 번호와
dependency, `CreateModel`/`AddField`, cross-app relation dependency, read-only `--dry-run`/`--check`입니다. Python migration
module 문법, serializer class 구조, 질문기, timestamp header/name과 출력 byte parity는 GoDj의 authority가 아닙니다.

현재 GoDj가 표현할 수 있는 operation은 `CreateModel`과 `AddField`뿐입니다. 또한 migration source discovery는 각 configured
root 바로 아래의 `.godj.json` file을 flat/no-follow로 읽고 programmatic `definition.Source`도 같은 catalog에 합칩니다.
따라서 첫 writer는 범용 rename/alter engine이 아니라 current supported domain을 정확히 생성하고 나머지를 fail-closed하는
additive-only detector여야 합니다.

## 결정 기준

- 수작업 migration JSON 없이 실제 사용자 개발 루프를 닫을 것
- Schema IR과 historical `ProjectState`를 diff의 유일한 의미 원본으로 재사용할 것
- project child가 declaration/catalog snapshot과 pure planning을, global CLI가 filesystem publication을 소유할 것
- embedded/programmatic definitions를 read-only dependency로 보존하고 project-owned app만 비교할 것
- 입력 순서, 시각, process와 무관한 migration identity, dependency, operation과 document bytes를 만들 것
- unsupported/destructive/ambiguous delta를 추측하거나 부분 게시하지 않을 것
- DB backend/opener/applied history를 열지 않는 command일 것
- 동시 writer, source mutation, cancellation과 crash에서 complete dependency-valid prefix만 남길 것

## 고려한 선택지

### Django autodetector와 Python writer surface를 넓게 포팅

Rename prompt, optimizer, custom/data operation과 Python serialization까지 한 번에 제공할 수 있지만 현재 Schema IR/operation/backend가
그 의미를 표현하지 못합니다. unsupported 의미를 문자열이나 raw SQL로 숨기게 되므로 채택하지 않습니다.

### Project runner가 migration file을 직접 기록

Config와 schema/catalog를 한 process에서 읽기 쉽지만 global selector가 보유한 physical project-root authority, 공통 cleanup과
recoverable publication 경계를 우회합니다. Project child는 immutable candidate만 반환하고 global owner가 게시하는 구조를
채택합니다.

### Read root 목록의 첫 항목을 암묵적인 write root로 사용

기존 root 순서는 check에서 canonicalized되어 write ownership 의미가 없고 여러 app에서 `0001_initial.godj.json` 이름이
충돌합니다. 첫 bounded product는 정확히 하나의 filesystem root만 writer-owned로 허용하고, app label을 포함한 flat filename을
사용합니다. 여러 writable root/app mapping은 후속 범위입니다.

### Schema 선언과 기존 catalog를 서로 다른 private invocation에서 읽기

두 호출 사이 source/schema mutation으로 존재하지 않았던 diff를 게시할 수 있습니다. 하나의 makemigrations private request가
copied `ProjectSpec`, configured filesystem sources와 programmatic sources를 함께 snapshot하고 candidate를 계산하도록 합니다.

## 결정

1. Public argv는 다음 exact 형식으로 제한합니다.

   ```text
   godj makemigrations
   godj makemigrations --check
   godj makemigrations --dry-run
   godj makemigrations --project <exact-godj.toml>
   godj makemigrations --check --project <exact-godj.toml>
   godj makemigrations --dry-run --project <exact-godj.toml>
   ```

   `--dry-run`은 candidate inventory/path/hash를 성공으로 보고하고, `--check`는 clean이면 0, candidate가 있으면 1입니다. 둘 다
   file/DB mutation은 0입니다.
2. Desired state는 `LoadProjectSpec`의 normalized app schemas로 구성합니다. Historical state는 exactly-once loaded
   `LoadedDefinitionSet`을 `LatestStateRequest()`로 재구성합니다. Pure detector는 desired app과 filesystem-source가 소유했던
   app의 합집합만 managed app으로 비교하고 programmatic-only app은 현재 historical state 그대로 보존합니다.
3. First delta domain은 다음뿐입니다.
   - history가 없는 managed app/model의 `CreateModel`
   - existing model field suffix의 nullable/no-default CharField 또는 ForeignKey `AddField`
   - new model의 current scalar/ForeignKey field shape
   - same-app model creation topology와 cross-app migration dependency
   DB-free detector는 existing row를 backfill할 수 없으므로 non-null/default-bearing field addition은 one-off default/table-remake
   policy가 생길 때까지 unsupported입니다.
4. Existing model/field order와 metadata는 desired state의 exact prefix여야 합니다. App/model/field removal, reorder, rename, alter,
   self/cyclic new relation, missing relation target, multiple same-app leaves와 operation/resource limit 초과는 structured
   unsupported/invalid result이며 candidate publication은 0입니다. Desired와 historical state가 같은 clean no-op은 successor name이
   필요하지 않으므로 noncanonical single leaf를 이유로 실패하지 않습니다. 실제 delta가 있어 successor를 만들어야 할 때만
   noncanonical leaf numbering을 structured unsupported result로 닫습니다.
5. 한 app의 한 invocation change는 한 migration으로 묶습니다. 첫 identity는 `0001_initial`; 다음 identity는 exact single
   same-app leaf의 four-digit numeric prefix 다음 번호입니다. 단일 operation은 normalized semantic model name 또는
   `<model>_<field>` slug를 사용합니다. 복합 변경은 `auto_` 뒤에
   `SHA-256("godj/migration-name/v1\x00" || <canonical-semantic-change-json>)`의 앞 6 bytes를 12자리 lowercase hex로
   붙입니다. 완전한 suffix는 `auto_<12-lowercase-hex>`이고 시각과 random 값은 사용하지 않습니다. 이 writer가 Encode에
   전달하는 persisted producer는 exact `{"name":"godj-makemigrations","version":"1"}`입니다.
6. Candidate dependencies는 same-app prior leaf와 relation target model의 실제 historical leaf 또는 새 creator candidate를 canonical
   key order로 포함합니다. Candidate app graph는 topological order로 materialize하고 canonical tie-break는 app identity입니다.
   Same-app `CreateModel`은 normalized desired model suffix order를 보존하며 relation target은 historical model 또는 그 suffix에서 먼저
   생성된 model이어야 합니다. 선언 순서를 writer가 재배열하거나 later/self/cyclic target을 추측하지 않습니다.
7. Writer-owned filesystem root는 첫 product에서 configured `MigrationDefinitionRoots`가 정확히 하나이고 그 root가 이미
   존재하는 physical directory일 때만 활성화합니다. Missing root를 자동 생성하지 않으며 mutation 0으로 실패합니다.
   Programmatic `MigrationDefinitionSources`는 read-only입니다. File roster는 flat
   `<app>_<migration-name>.godj.json`을 사용해 multi-app identity collision을 피합니다. Existing differently named source는 읽되
   덮어쓰거나 rename하지 않습니다. Global owner는 selected project root에서 fd-relative `openat`/`O_NOFOLLOW`로 writer root를
   열고 device/inode를 seal한 retained directory authority를 discovery, lock, CAS, temp write, rename과 directory fsync까지
   유지합니다. Root/member path rebound, symlink, non-regular member 또는 identity mismatch는 target mutation 전에 conflict입니다.
8. `internal/migrationautodetect` leaf package가 immutable plan과 structured error를 소유합니다. `migrations` root가
   `migrations/definition`을 import하는 cycle은 만들지 않습니다. `migrations/definition`은 current wire의 deterministic encoder를
   소유하고 caller가 supplied producer provenance를 전달합니다.
9. 별도 strict version-1 makemigrations private protocol은 one request에서 schema와 catalog를 snapshot하고 bounded candidate
   roster/document와 normalized `ProjectSpec`/catalog semantic digest를 반환합니다. Global owner는 runner build 전에 retained
   project-root authority로 selected descriptor와 bounded declaration/build-input source namespace의 independent fingerprint를
   캡처합니다. 최소 inventory는 exact `godj.toml`, selected runner와 project-owned local package의 Go/build/embed input,
   `go.mod`/`go.sum` 및 in-scope workspace metadata와 committed generated manifest/source의 canonical path/mode/bytes입니다.
   Physical catalog CAS는 `godj/makemigrations-source-catalog/v1\x00` domain에서 `uint64-be(source-count)` 뒤 canonical source-ID
   order의 source ID length/bytes와 exact document length/bytes를 hash하고 semantic `LoadedDefinitionSet.Digest()`와 구분합니다.
   Programmatic catalog는 같은 framing과 exact `godj/makemigrations-programmatic-source-catalog/v1\x00` domain을 사용합니다. Candidate
   CAS는 source fingerprint, normalized `ProjectSpec` digest, generated-bundle/schema identity와 physical/semantic catalog digest를
   함께 결속합니다. Stale child가 자체 보고한 token만 서로 비교하는 것은 CAS가 아닙니다. First rename 직전에 global owner가
   retained authority에서 independent source fingerprint와 descriptor/root identity를 다시 검증하고, 하나라도 달라지면 candidate
   file 0의 structured source conflict로 종료합니다. Build 뒤 child 실행 전에도 같은 build-input fingerprint를 확인해 이미 stale인
   binary를 실행하지 않습니다. Programmatic source snapshot digest는 filesystem catalog token과 분리하고, compiled declaration
   source fingerprint와 child response의 semantic catalog digest를 함께 검증합니다. Existing check/generate/migrate protocol bytes는
   바꾸지 않습니다.
   여기서 one-request snapshot은 OS 전체의 원자 snapshot이 아니라 copied `project.Config`, exactly-once declaration loader와 한 번의
   filesystem catalog capture가 한 child request 안에서 동일 plan authority를 만든다는 뜻입니다. Physical filesystem catalog,
   programmatic source catalog, semantic `LoadedDefinitionSet`과 normalized `ProjectSpec`은 서로 다른 versioned digest domain을 사용하고,
   `GeneratedBundle.SnapshotSHA256()`는 generator ABI까지 포함한 별도 generated identity로 유지합니다. Phase B read-only 경계는 build
   전 fingerprint capture, build 뒤 child 실행 전 재검증과 child response 공개 전 재검증까지 소유합니다. 같은 inode의 in-place byte
   변경도 conflict여야 하며 existing project-generate source namespace token은 이 CAS authority로 재사용하지 않습니다.
10. Normal mode global owner는 retained project root와 dedicated writer lock 아래 current schema/catalog를 다시 snapshot하고
    plan합니다. Dedicated lock은 별도 control file이 아니라 retained writer-directory inode 자체의 duplicated fd에 건 `flock`이며,
    publisher는 그 fd와 device/inode identity를 publication 완료까지 재검증합니다. Candidate 전체와 각 dependency prefix를 기존
    source에 합쳐 strict-load/latest reconstruct하며 final managed state exact equality를 검증합니다. Publication 직전
    source/catalog CAS와 no-overwrite를 재검증합니다. Normal, `--dry-run`, `--check`는
    같은 bounded producer, filename/project-relative path, source ID, roster, definition Encode/strict combined Load/reconstruct와 모든
    resource-limit preflight를 공유합니다. `--dry-run`/`--check`는 project tree에 lock/temp/control file을 만들지 않고 recovery-required
    상태도 read-only로 진단하며 정리하지 않습니다.
    Current private protocol/snapshot은 한 plan의 candidate를 최대 64개로 제한합니다. 이는 historical catalog 최대 2,048 source와
    별도인 hard support ceiling이며 app filter나 automatic batching이 없으므로 65개 이상 pending candidate는 재실행으로 전진하지
    않고 structured resource-limit failure로 닫습니다. Recovery 뒤 writer directory의 existing entry와 candidate 합은 최대
    65,536개입니다. 모든 mode가 target/temp component의 실제 부재를 probe해 filesystem `NAME_MAX`와 case-fold collision도 normal
    rename 전에 같은 결과로 닫습니다.
11. Multi-file publication은 topological candidate order의 atomic no-replace append를 사용합니다. Reserved temp basename은
    `.godj-makemigrations-tmp-v1-<64-lowercase-hex>`이며 hex는
    `SHA-256("godj/migration-temp/v1\x00" || uint64-be(len(target-basename)) || target-basename ||
    uint64-be(len(exact-document-bytes)) || exact-document-bytes)`의 64 lowercase hex입니다. 이 namespace는 GoDj writer 전용이고 recovery는 regular
    file의 exact definition bytes에서 다시 도출한 target basename/digest가 일치할 때만 ownership을 증명합니다. 각 temp는
    same-directory/same-device exclusive mode `0600` regular file로 complete write/fsync한 뒤 Linux `RENAME_NOREPLACE` 또는
    Darwin exclusive rename으로 게시하고 target identity/bytes와 directory fsync를 확인합니다. 모든 접근은 retained dirfd-relative
    no-follow입니다. 기존 migration file은 수정·삭제하지 않습니다. No-replace rename 전 cancellation/failure는
    definite-not-published이며 writer가 ownership과 exact state를 증명한 자기 temp만 정리합니다. Rename 성공 뒤 target 확인 또는
    directory fsync가 실패하면 ordinary rollback/failure나 success로 추측하지 않고 structured
    `publication_recovery_required`/outcome-unknown을 반환하며 visible target을 삭제·덮어쓰거나 자동 retry하지 않습니다. 정상 write의
    per-file commit point는 target directory fsync 성공입니다. Later candidate failure는 이미 commit된 strict-load 가능한 durable
    dependency prefix를 보존합니다. Source/catalog CAS는 각 candidate rename 전, root/path/lock identity는 first rename, 각 rename
    뒤와 success 반환 전에 다시 확인합니다. 이미 durable prefix가 생긴 뒤 source drift나 path rebind를 발견하면 prefix를 보존하고
    recovery/rerun-required로 반환합니다. 다음
    normal invocation은 같은 lock 아래 fresh second snapshot을 얻고 bounded owned-temp namespace와 visible catalog를 재검증합니다.
    Complete temp는 strict current-format definition bytes/producer/temp digest로 self-authenticate하며, incomplete temp는 fresh candidate의 deterministic
    target/document 이름과 exact byte prefix에 유일하게 결속될 때만 두 번의 동일 inode/mode/size/catalog-seal scan과 source/catalog
    CAS 뒤에 제거·directory fsync합니다. Exact valid target만 보이면 target directory를 fsync해 catalog에 채택합니다. Recovery 뒤
    catalog가 fresh snapshot과 그대로 일치하는지 다시 seal/CAS하므로 그 plan을 publication authority로 유지할 수 있습니다. Target과
    temp가 동시에 있거나 ownership 또는 exact visible/durability state를 증명할 수 없거나 reserved namespace member가 candidate와
    일치하지 않으면 아무것도 지우지 않고 recovery-required로 fail-closed합니다. Whole-batch manifest/journal은 만들지 않습니다.
    Writer root는 compiled project declaration이 소유하므로 normal mode는 initial read-only child snapshot으로 exact root를 확인한 뒤
    lock을 획득하고, 같은 built runner에 fresh second private request를 보내 lock 아래 schema/catalog를 다시 plan합니다. 두 request는
    각각 one-request snapshot 계약을 만족하며 initial result를 그대로 게시 authority로 재사용하지 않습니다. First/every rename 직전
    source/catalog/root CAS는 이 Phase C publication 경계가 소유합니다. Publisher가 아직 연결되지 않은 intermediate Phase B에서
    candidate가 있는 bare normal command는 read-only plan을 게시 성공으로 가장하지 않고 detail-free
    publication-unavailable failure로 닫습니다. Clean bare normal command는 write 없이 성공합니다. Phase C normal success는
    `generated`, ordinary publication failure와 recovery-required는 exit 3입니다.
    지원 filesystem은 Darwin/Linux local filesystem 중 directory `flock`, regular-file/directory `fsync`와 kernel atomic
    no-replace rename을 제공하는 범위입니다. `ENOSYS`/`EINVAL`/`EOPNOTSUPP`에서 overwrite 가능한 fallback은 사용하지 않습니다.
    성공한 file/directory `fsync` syscall이 durability contract 경계이며 Darwin `F_FULLFSYNC`나 임의 hardware power-loss까지
    증명한다고 주장하지 않습니다. Reserved temp namespace는 cooperative GoDj writer 전용입니다. POSIX에는 inode-conditioned
    unlink가 없으므로 같은 namespace에서 경쟁하는 non-cooperative local actor와 distributed/network filesystem 안전성은
    비범위입니다.
12. 이 command는 `OpenMigrationBackend`를 호출하지 않고 DB/introspection/applied-recorder를 읽지 않습니다. Generated definition을
    실제 `godj migrate`로 SQLite/PostgreSQL clean database에 적용하는 것은 product E2E 검증이지만 autodetection 입력은 아닙니다.
13. MIG-099..110은 Django-observable과 GoDj decision authority를 분리한 새 mixed-authority set입니다. Python source byte parity가
    아니라 semantic candidate와 public behavior를 비교하고 deterministic bytes/publication safety는 GoDj-only contract로 둡니다.
14. Phase D actual comparison은 MIG-099/100/101/102/108/109/110을 `passing`으로, MIG-103..107을
    [DEV-0010](../DEVIATIONS.md#dev-0010--godj-migration-writer의-current-format과-안정-오류-taxonomy)의
    exact 19-result-selector `deviation`으로 게시합니다. Relation `on_delete`의 current IR 의미, timestamp-free digest name,
    JSON definition roster/output와 closed GoDj failure taxonomy가 Django의 Python writer surface와 다른 부분만 sparse replacement로
    허용합니다. Reference aggregate는 24 sets/273 contracts/552 bindings=`237 passing + 24 deviation + 12 oracle_locked`, product는
    23 adapters/261 contracts=`237 passing + 24 deviation`이며 MIG-075..086만 reference-only locked로 남습니다.
15. Product E2E는 writer command의 backend open/database mutation 0을 먼저 증명한 뒤 생성된 exact definitions를 기존 public
    `godj migrate`에 전달합니다. PostgreSQL 17.10에서는 first migrate, recorder/revision/schema fingerprint가 불변인 second no-op와
    distinct-process restart를, repository-external public-import-only module에서는 SQLite publish/migrate/no-op/data restart를
    검증합니다. 테스트 command는 bounded process group을 종료·reap하고 raw database URL/secret을 failure log에 출력하지 않습니다.
    `go test -race`는 harness만 계측하며 일반 `go build` child binary까지 계측했다고 주장하지 않습니다.

## 결과

- 지원 가능한 current schema 변경은 수작업 JSON 없이 strict current definition으로 연결됩니다.
- Existing loader/reconstructor/executor/backend와 generated code ABI는 바뀌지 않습니다.
- 첫 CLI 이름은 일반적이지만 destructive/rename/custom operation을 지원한다고 과장하지 않습니다.
- Exactly-one filesystem root 제약은 multi-root read configuration을 없애지 않으며 writer invocation만 fail-closed합니다.
- Flat app-prefixed file roster는 current discovery에 맞지만 Django app-local directory layout과 다릅니다. App-to-root mapping은 실제
  second project 요구가 생길 때 별도 current-format reset/ADR로 결정합니다.
- Retained physical authority와 independent declaration-source/catalog CAS는 각 verification point에서 관찰한 source drift와 path
  rebind를 fail-closed합니다. Adversarial mutate-and-revert, distributed filesystem과 non-cooperative writer 일반 안전성은 제공하지
  않습니다. Source namespace 밖에서 arbitrary runtime I/O를 하는
  `LoadProjectSpec`은 이 보장에 포함되지 않으며 declaration loader는 copied project-owned source의 pure snapshot이어야 합니다.
- 여러 app candidate의 중간 실패는 all-or-nothing이 아니라 dependency-valid durable prefix입니다. 이는 existing `migrate`의
  durable-prefix/resume 의미와 일치하며 append-only source에 whole-batch replacement framework를 도입하지 않습니다.
- 한 command에서 65개 이상 pending candidate를 자동 분할하지 않습니다. Current 64-candidate ceiling은 성능 권고가 아니라
  fail-closed support limit이며 app filter/batching을 별도 결정하기 전까지 그대로 유지합니다.

## 의도적으로 결정하지 않은 것

- Delete/Remove/Alter/Rename operation과 interactive rename/default/backfill 질문
- self/circular relation을 위한 migration splitting
- merge/squash/optimizer, multiple leaves, `--empty`, `--name`, `--update`, app filter
- RunSQL/Go data migration, custom operation serialization과 historical model API
- DB adoption/introspection, applied-history consistency와 multi-DB router
- multiple writable roots 또는 app-to-root mapping
- distributed filesystem/non-cooperative writer 일반 지원
- installed CLI/project library/generator semver와 general definition upgrader

## 검증

- Django 6.1 exact tag `fe0a859...`: no-change, initial, next leaf, CreateModel/AddField, same/cross-app dependency,
  dry-run/check observable contract
- Pure normal/race/property tests: managed-app/dependency input permutation, repeat detection, deep-copy, prefix rejection,
  nullable/no-default AddField, topology/cycle, versioned name digest와 limit boundary. Model/field declaration order는 semantic order이므로
  permutation 대상이 아닙니다.
- Encoder golden + strict `definition.Load` round-trip + latest reconstructed managed state exact equality
- Private protocol duplicate/unknown/trailing/non-UTF8/oversize/short-write와 existing protocol byte no-diff
- Publication fault injection, concurrent writer serialized replan, source CAS mutation, cancellation/crash와 valid-prefix/residue scan
- External project: generated definitions -> SQLite/PostgreSQL `godj migrate` -> second no-op -> restart read
- Affected normal/race/CGO-disabled/vet/generated drift와 final frozen milestone의 hosted matrix 한 번

Phase D implementation `21d88c99b2d9733736fa68b34a7c0827a781ee70`, tree
`976671ce53d9a5f6ae39d3fa2aea3546db5e4bc9`은 PostgreSQL 17.10 normal/race/CGO-disabled actual, external module
normal/race/CGO-disabled, MIG-099..110 strict product comparison과 affected normal/vet/generated drift를 통과했습니다. Current manifest는
9,227 bytes/SHA-256 `90bce609ffb4f771007379495629a31efbf00594dca16f9efe875005e97f1c72`, DEV-0010 fixture는
7,242 bytes/SHA-256 `74617f20f72ecd5b26284ae8cffb7a1c408cdef03e0933d457beeb82f9f4718e`입니다. 여러 독립 감사에서
P0/P1은 없었습니다. Bounded process-group reap, PostgreSQL second-noop revision/schema fingerprint, oracle-blind forbidden fixture,
failure output redaction과 MIG-110 durable-prefix seal을 보강하고 재검증했습니다.

이 문서는 Phase E의 full `make ci`, Linux/386, current source-bound PostgreSQL attestation과 exact submitted-head Hosted matrix가
끝날 때까지 `Proposed`입니다. 위 local checkpoint만으로 terminal `Accepted` 또는 GDJ-0050 completion을 주장하지 않습니다.
