# ADR-0033: Forward ForeignKey Assignment, Save, and Cache Ownership

- 상태: Proposed
- 날짜: 2026-08-12
- 관련 work/contract:
  [GDJ-0033](../../work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md), REL-002, Q-013, Q-017
- 선행 결정: [ADR-0011](0011-m2-save-lifecycle-orchestration.md),
  [ADR-0012](0012-queryset-evaluation-cache-ownership.md),
  [ADR-0024](0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md),
  [ADR-0026](0026-forward-foreign-key-object-cache-and-nullability.md),
  [ADR-0032](0032-production-forward-project-facade-and-additive-first-publication.md)
- 대체하는 ADR: 없음

## 상태와 범위

이 ADR은 **Proposed**입니다. Django 6.1이 이미 정한 forward ForeignKey assignment/save observable semantics와,
그 의미를 Go value/pointer/codegen API로 번역할 때 필요한 ownership decision을 분리합니다. Phase A source audit와
Phase B no-product compile/runtime feasibility가 clean일 때만 Phase C에서 exact public names, return shapes, generated
bytes와 error/cache contract를 Accepted로 전환합니다.

Clean baseline은 `8748bb495e682d53e0d07c5e8f8fd0236ed5c9ed`의 EVID-071/run `31563615648`입니다.
Baseline product는 exact 12 adapters/127 contracts=`121 passing + 5 deviation + 1 oracle_locked`, relation 11/12이고
REL-002만 `oracle_locked`입니다. 이 activation documentation tree는 source/product bytes를 바꾸지 않으며 자체
exact-head CI는 `not run/pending`입니다.

ADR-0032가 Accepted한 bounded Gate 0 facade, `Backend=db.Queryer+db.Mutator`, `Using`, all-model project wrappers,
required/nullable read accessors와 single companion first-publication은 재개방하지 않습니다. 이 ADR은 그 facade의
forward relation assignment와 save/cache ownership만 다룹니다.

## Django가 이미 결정한 의미

Pinned Django 6.1에서 다음은 Go-only 선택지가 아니라 호환 관찰 기준입니다.

1. Forward relation object assignment는 source raw FK를 target key로 맞추고 accessor cache를 그 exact assigned
   object로 warm합니다.
2. Raw FK assignment가 이전 scalar와 다르면 cached relation object를 지웁니다.
3. Cached assigned target에 primary key가 없으면 source save는 database mutation 전에 unsaved-related-object
   오류로 실패합니다. Nullable relation도 silent data loss를 피하기 위해 같습니다.
4. Primary key가 수동으로 존재하지만 row가 아직 없는 target은 preflight를 통과하며 database FK constraint가
   결과를 결정합니다.
5. Relation assignment 뒤 same target object가 저장돼 key를 얻으면 source save preparation이 target key를 다시
   relation scalar에 반영합니다.
6. Nullable clear는 raw FK NULL과 relation cache absent를 함께 설정합니다.
7. Database rollback은 application memory object를 자동 rewind하지 않습니다.

Reference implementation은 Python descriptor와 mutable object identity를 사용합니다. GoDj는 그것을 복제하지 않고
result/side effect/error timing/transaction meaning을 explicit method와 structured error로 번역합니다.
Required nil-like assignment를 Go API가 허용할지 construction에서 거부할지와 그 exact error timing은 Go-only
Phase C decision입니다. Django의 in-memory allowance를 nullable clear API와 섞어 미리 결정하지 않습니다.

## 결정 기준

- REL-002 oracle phase/category/code/metrics/DB state와 일치하고 false green이 없음
- Assignment 직후 accessor query 0과 exact assigned target publication
- No-PK와 manually key-present target을 구분
- Raw scalar change가 stale assigned cache를 반환하지 않음
- Nullable clear가 scalar NULL/cache absent를 함께 표현
- Target-later-key reconciliation이 source save 직전 안정적으로 동작
- Original source wrapper의 derivation immutability
- Session origin, wrapper ownership과 copy misuse가 fail-closed
- Existing Manager/WriteDescriptor를 재사용하고 relation-specific save engine을 중복 구현하지 않음
- Existing app-generated exact 13을 다시 쓰지 않음
- General identity map, reverse/write/delete facade와 migration을 주장하지 않음

## Proposed leading candidate

### Fresh source derivation, exact assigned target pointer

Relation assignment는 original source를 변경하지 않고 fresh project `*BlogPost` wrapper를 반환하는 후보입니다.
Derived source는 다음 private state를 소유합니다.

- raw `blog.Post` value와 source PK-presence
- exact facade state/origin pointer
- required/nullable relation별 `unassigned`, `assigned-present`, `assigned-absent` tri-state
- assigned-present일 때 exact `*AuthorsAuthor` pointer

같은 derived source의 accessor는 assigned-present pointer를 그대로 반환하고 query하지 않습니다. 이는 별도 query로
materialize한 같은 row의 stable pointer identity나 global identity map을 뜻하지 않습니다.

Raw `AuthorID`/`ReviewerID`에서 source를 명시적으로 다시 파생하면 assigned override를 폐기하고 scalar-derived cold
relation state로 돌아갑니다. Nullable clear는 assigned-absent와 raw NULL을 함께 만듭니다. Exact public helper names와
whether clear/scalar derivation is method or builder remain noncanonical until Phase C.

### Same target wrapper in-place Save and reconciliation

Leading candidate에서 target wrapper Save는 existing `authors.AuthorObjects.Save(..., &target.model)`로 same
`*AuthorsAuthor` wrapper의 model/key-presence를 갱신합니다. Derived source가 보존한 exact assigned target pointer를
source Save 직전에 다시 읽으면 assignment 당시 no-PK였던 target이 그 사이 저장돼 얻은 key를 reconcile할 수 있습니다.

Source Save는 target descriptor key/presence를 mutation plan 직전에 **한 번 snapshot**하고 이후 plan 중 변화를
관찰하지 않습니다. Same wrapper의 concurrent Save/access는 caller synchronization 없이는 지원하지 않으며 data race가
될 수 있습니다. ADR-0011/0012의 concurrency 원칙을 따릅니다.

이 후보는 다음을 보장하지 않습니다.

- 다른 materialization이나 process에서 같은 row wrapper pointer가 같음
- target wrapper downstream relation cache 공유
- callback 이후 expired session wrapper의 deterministic success/failure
- outer transaction rollback 뒤 target/source heap state 자동 복구

### Project-private write model

`blog.Post`에는 PK-presence bit가 없고 Go companion은 다른 package type에 field를 추가할 수 없습니다. App-level
relation `WriteDescriptor`를 넣으려면 existing generated app file을 다시 써야 하며 ADR-0024의 plain main-model
boundary와 exact13 lock을 깨뜨립니다.

따라서 Phase B의 leading candidate는 project companion 안의 private
`blogPostWriteModel{model blog.Post; primaryKeyPresent bool}`와 private descriptor를 사용해 generic
`orm.Manager.Save`를 재사용합니다. Core seam은 `orm/write.go`가 ForeignKey scalar integer/NULL을 안정적으로
accept하는 것과 `query.CodeUnsavedRelatedObject` 후보뿐입니다.

Manually key-present but unpersisted target은 unsaved-related preflight를 통과해야 합니다. Supported physical SQLite
FK가 target row 부재를 authoritative integrity error로 거부합니다.

### Unsaved construction and key presence

REL-002는 existing query result만으로 검증할 수 없습니다. New source와 new no-PK target을 facade wrapper로 만드는
construction seam이 필요합니다. Leading candidate는 generated/raw input과 descriptor가 가진 hidden PK-presence를
project wrapper state로 옮깁니다.

- New `AuthorsAuthor` target은 `AuthorDescriptor` presence가 false이며 numeric ID 값으로 savedness를 추론하지 않습니다.
- New `BlogPost` source는 private write-model `primaryKeyPresent=false`이고 loaded query wrapper만 true입니다.
- Required `blog.Post.AuthorID int64`는 Django의 pending `None`을 표현할 수 없으므로 no-PK assignment의 authoritative
  상태는 assigned/pending tri-state입니다. 이때 raw zero는 savedness나 valid FK가 아니며 target descriptor presence를
  먼저 검사해 private descriptor field derivation/plan construction 전에 REL-002로 차단합니다.
- Accepted Gate 0 `Unwrap() blog.Post` 역시 pending `None`을 표현하지 못합니다. Pending 동안 authoritative surface는
  exact assigned target을 query 0으로 반환하는 relation accessor와 tri-state이고, raw scalar/`Unwrap` representation은
  Phase C decision입니다. Loaded source의 이전 nonzero FK를 새 no-PK target의 authoritative key로 누출하지 않습니다.
- Manually key-present target은 descriptor presence true를 보존하므로 DB row가 없어도 preflight를 통과합니다.
- Manually key-present target은 ID 0/nonzero 모두 descriptor presence에 따라 preflight를 통과하고 physical FK가
  row 존재를 판단합니다.
- Constructor/builder exact names와 raw/generated input shape는 Phase C 전까지 noncanonical입니다.
- External compile gate는 new source+new target assignment→source Save no-PK failure와, same target wrapper Save 뒤 same
  derived source Save reconciliation을 모두 포함해야 합니다.

## Validation and error proposal

- Assigned target에 key가 없으면 frozen REL-002와 같은 `model_state_error/unsaved_related_object` 후보, source DB
  mutation 0.
- Required FK의 raw zero는 pending no-PK 상태를 우회하는 scalar key로 해석하지 않으며 private write plan은 생성하지
  않음.
- New-source zero와 loaded-source old-nonzero를 각각 검증하며, target Save가 same wrapper에 key presence를 게시하기
  전에는 raw reconcile을 하지 않음.
- Nil/typed-nil/zero/dereference-copy/cross-model/cross-origin wrapper와 invalid state는 structured pre-I/O error.
- Original binder/backend/wrapper validation과 unsaved-related validation의 precedence는 adversarial test로 고정.
- Stable contract는 category/code와 phase/side effect이며 detail message는 noncontractual.
- Failed save와 rollback은 private wrapper state를 implicit rewind하지 않음.

Exact error constant, public method names와 return types는 Phase C 전에는 Proposed입니다.

## Session and transaction boundary

Facade origin이 `db.Session`이면 callback 내부 사용만 지원합니다. Warm assigned cache는 backend call 없이 성공할 수
있어 callback 이후 always-fail을 강제하지 않습니다. Target/source wrapper를 callback 밖으로 escape시키는 동작은
noncontractual입니다.

Outer `Atomic`/`AtomicRelation` rollback은 DB를 되돌리지만 target Save가 wrapper에 게시한 PK나 fresh derived source의
raw FK/cache를 자동 복원하지 않습니다. Caller는 error/outcome을 확인해 fresh load 또는 explicit derivation을 선택해야
합니다. Unknown transaction outcome과 retained SQLite connection policy는 Q-019 및 ADR-0030 후속이며 이 ADR은
`db/**`를 변경하지 않습니다.

## Generated and publication boundary

- Existing app-generated exact 13은 byte-for-byte 보존합니다.
- Current project facade companion 하나의 deterministic replacement 후보만 허용합니다.
- Generator/golden/collision/last-good/no-rewrite, actual product compile와 external consumer를 함께 검증합니다.
- REL-002 status publication은 Go protocol/godjcheck hard locks와 Python의 product-manifest status-vector assertion만
  measured integration update로 맞춥니다. Django scenario execution, oracle payload/checksum과 static fixture는 바꾸지
  않습니다.
- Q-017의 prerequisite generator-version/output-digest provenance는 coordinated multi-file upgrade/general `--check`
  전에 필요하지만 REL-002 single-companion replacement의 unconditional blocker는 아닙니다.
- Broader generated upgrade에 필요한 file-set transaction/manifest/repair는 이 ADR의 범위가 아닙니다.

## Phase gates

### Phase A — semantics audit

- Django descriptor scalar/cache mutation
- `_prepare_related_fields_for_save` no-PK/manual-PK/later-key 의미
- nullable clear, raw FK invalidation과 rollback memory non-rewind
- REL-002 exact oracle payload/phase/category/code/metrics/DB-state
- `select_related` Resolve/Bind original cause loss의 narrow remediation 경계

### Phase B — no-product feasibility

- Fresh source derivation과 exact assigned target pointer preservation
- Unsaved source/target construction과 descriptor PK-presence ownership; numeric ID inference negative
- Target wrapper in-place Save, later key reconciliation과 one-time pre-plan snapshot
- Assignment warm cache, raw scalar invalidation, nullable clear
- No-PK I/O0와 manual-PK database constraint
- Nil/zero/copy/cross-origin/session misuse
- Generic Manager/private descriptor reuse와 current facade compile

### Phase C — freeze or stop

Phase B가 clean이면 exact names/return shapes/error/cache/generated bytes를 이 ADR에 기록하고 Accepted로 전환한 뒤
work allowlist 안에서만 bounded implementation을 진행합니다. Feasibility가 실패하거나 allowlist 밖 core/backend/app
generated file이 필요하면 Accepted/implementation으로 진행하지 않고 별도 activation을 요구합니다.

## Consequences if accepted

- Django의 implicit descriptor 경험은 Go에서 explicit error-returning derivation/save로 보이지만 cache/side-effect
  의미는 보존됩니다.
- Original source value를 보존하면서 exact assigned target pointer에 한해 later-key reconciliation을 얻습니다.
- Same target wrapper in-place mutation은 편리하지만 caller synchronization과 rollback 후 memory 관리가 필요합니다.
- App model을 변경하지 않고 project companion이 relation-aware write state를 소유합니다.
- REL-002가 통과해도 Q-013/Q-017 전체는 Partial/open으로 남습니다.

## 명시적 비결정

- Public `WithAuthor`/`Save`/clear/scalar helper names와 exact return shape
- Reverse assignment/manager, multi-hop write, OneToOne/ManyToMany
- Cross-materialization target identity와 identity map
- Delete facade, PROTECT payload redesign, CASCADE/recursive delete
- Relation migration/constraint codec와 general generated upgrade/CLI
- Q-019 retained-connection policy와 callback-after-return lifetime enforcement
- PostgreSQL/MySQL/Windows 또는 broad non-SQLite support

## Activation evidence boundary

EVID-071/run `31563615648`은 exact GDJ-0032 terminal head만 증명하며 GDJ-0033을 activate/accept/implement하지
않습니다. 이 EVID append와 exact 14-path activation documentation tree 자체 exact-head CI는 `not run/pending`이고
run `31563615648`을 그 proof로 재사용하지 않습니다. Draft PR #1은 merge하지 않습니다.
