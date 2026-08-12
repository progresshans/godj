# ADR-0033: Forward ForeignKey Assignment, Save, and Cache Ownership

- 상태: Accepted
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

이 ADR의 decision 상태는 **Accepted**입니다. 그 decision은 exact 23-path bounded product에 **Implemented**됐고
EVID-076의 명시된 hosted 환경에서 **Verified**됐습니다. Pinned Django 6.1이 정한
forward ForeignKey assignment/save 관찰 의미를 먼저 호환 의무로 고정하고, 그 의미를 Go value, explicit error,
project wrapper와 generated code로 옮기는 정확한 ownership/API 경계를 결정·구현합니다. `Verified`는 verification
상태이며 ADR enum을 대체하지 않습니다.

결정, local implementation과 hosted verification의 근거는 서로 재사용하지 않는 다음 증거입니다.

- GDJ-0033 activation head `a4a627a5702ac9db4ee8c39706ff098783a9c5e6`은 EVID-072/run
  `31566524953`의 exact 26/26 jobs·326/326 steps를 통과했습니다.
- 그 exact head에서 분리한 no-product Phase-B worktree는 patch SHA-256
  `8329bb0ae76dc3297ad692cd447d11f11cc6578b574202c72dc7c0d754b6c566`으로 아래 API와 상태 머신의
  compile/runtime feasibility를 증명했고 독립 감사 P0/P1/P2/P3=`0/0/0/0`을 받았습니다.
- Decision-documentation head `9d728610acbe037bab73fde8910cc80ae8411691`은 EVID-074/run `31574653183`의 별도
  exact 26/26 jobs·326/326 steps와 audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다.
- Exact 23-path product diff `b760d6d7...`는 EVID-075의 final local normal/race/CGO0/vet/Linux386/full `./...`,
  measured inventory와 audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다.
- Exact implementation head `be6f3d4e0838929fe96ec156ec0647845d905ea6`은 EVID-076/run `31586910749`의 unique
  exact 26/26 jobs·326/326 steps, four-coordinate 715/715/0 inventory와 independent audit
  P0/P1/P2/P3=`0/0/0/0`을 통과했습니다. EVID-072/074를 implementation proof로 재사용하지 않았습니다.

Implementation과 verification은 **SQLite/AutoField bounded product**에 한정됩니다. REL-002는 manifest/actual에서
`passing`이고 aggregate는 `122 passing + 5 deviation + 0 oracle_locked`, relation 12/12입니다. 이
EVID-076 append와 completion/status transition은 exact completion head `81f4aacb...`의 EVID-077/run
`31590911735`에서 별도 검증됐습니다. EVID-077 append와 terminal evidence/status transition은 completion run이
재귀적으로 증명하지 않으므로 다시 별도 exact-head CI가 필요합니다.

ADR-0032가 Accepted한 bounded Gate 0의 `Backend=db.Queryer+db.Mutator`, `Using`, all-model wrappers, read/eager
surface와 one-companion publication은 재개방하지 않습니다. Callback 뒤 session-origin wrapper 동작도 계속
noncontractual입니다.

## Django가 정한 호환 의미

다음은 Go-only 설계 선택지가 아니라 pinned Django 6.1의 관찰 의미입니다.

1. 관계 객체 assignment는 target primary key를 source FK scalar에 반영하고, 그 관계 cache를 exact assigned
   object로 warm합니다.
2. Raw FK scalar setter는 값이 **달라질 때만** 기존 관계 cache를 지웁니다. 같은 scalar를 다시 설정하면 warm
   cache를 보존합니다.
3. Assigned target에 primary-key presence가 없으면 source Save는 query/mutation/plan I/O 전에
   `model_state_error/unsaved_related_object`로 실패합니다. Required와 nullable relation에 동일합니다.
4. Primary key가 수동으로 present인 target은 실제 DB row가 없어도 preflight를 통과합니다. Numeric value가
   `0`인지 아닌지는 savedness가 아니며 physical FK constraint가 row 존재를 판단합니다.
5. Assignment 시점에 key가 없었던 target이 같은 wrapper의 Save로 key를 얻고 source scalar가 계속 empty이면,
   source Save 준비가 그 key를 한 번 다시 읽어 source scalar에 반영합니다.
6. Assignment 시점에 이미 key가 있었던 target의 key가 나중에 달라지면 source는 새 key를 자동 추종하지
   않습니다. Assignment 때 복사한 scalar를 유지하고 stale target cache를 폐기합니다.
7. Nullable clear는 raw FK NULL과 cached absent를 함께 게시합니다.
8. Transaction rollback은 DB를 되돌리지만 application heap의 PK, scalar 또는 cache를 자동 rewind하지 않습니다.

REL-002의 기존 oracle payload는 3번의 new source + no-PK target 실패, DB unchanged와 I/O 0만 비교합니다. 나머지
항목은 GoDj supplemental runtime/compile gate이며 REL-002 reference bytes에 몰래 추가하지 않습니다.

## 결정

### 공개 API

다음 surface를 GDJ-0033의 exact public API로 고정합니다.

```go
func (q AuthorsAuthorQuery) New(value authors.Author) (*AuthorsAuthor, error)
func (q BlogPostQuery) New(value blog.Post) (*BlogPost, error)

func (model *AuthorsAuthor) Save(ctx context.Context) error
func (model *BlogPost) Save(ctx context.Context) error

func (model *BlogPost) WithAuthor(target *AuthorsAuthor) (*BlogPost, error)
func (model *BlogPost) WithReviewer(target *AuthorsAuthor) (*BlogPost, error)
func (model *BlogPost) WithAuthorID(id int64) (*BlogPost, error)
func (model *BlogPost) WithReviewerID(id int64) (*BlogPost, error)
func (model *BlogPost) ClearReviewer() (*BlogPost, error)
```

- `New`는 query root에 있지만 Filter/OrderBy/Limit plan을 읽지 않는 pure wrapper construction입니다.
- `Save`는 같은 wrapper의 model/PK-presence를 성공 결과로 갱신합니다.
- `With*`와 `ClearReviewer`는 original source를 바꾸지 않고 fresh `*BlogPost`를 반환합니다.
- Required relation에는 `ClearAuthor`를 만들지 않습니다. Nil target은 pre-I/O `invalid_plan`이고 nullable NULL은
  `ClearReviewer`로만 표현합니다.
- `WithReviewerID`는 `int64`를 받고 NULL pointer overload를 제공하지 않습니다.

이 이름들은 bounded forward-write surface에서 canonical입니다. Reverse manager, general write facade, relation
migration 또는 장기 single-logical-model UX 전체를 canonicalize하지 않습니다.

### Source와 target state

Project wrapper가 raw model과 별도로 다음 state를 소유합니다.

- source PK presence
- required FK scalar presence
- relation별 cache state: cold/unassigned, assigned-present, assigned-absent
- assigned target pointer
- assignment 당시 target key가 없었는지 나타내는 pending bit
- exact facade state/origin pointer와 copy-detection self sentinel

Scalar presence, cache state와 pending은 서로 다른 축입니다. 다음 경우를 구분합니다.

```text
BlogPost.New(raw AuthorID=0)   -> required scalar unset
BlogPost.New(raw AuthorID!=0)  -> required scalar present
WithAuthorID(0)                -> explicit scalar present
loaded AuthorID=0              -> scalar present
PK-present target key 0        -> scalar present
no-PK target assignment        -> assigned pending; scalar non-authoritative
nullable raw nil/ClearReviewer  -> assigned absent
```

따라서 numeric zero 하나만으로 PK/FK presence나 persistedness를 보편적으로 판정하지 않습니다. `New`의 raw
required-FK 경계만 zero를 unset으로 해석하고, explicit/loaded/PK-present-target zero는 present입니다. Target
savedness는 언제나 descriptor PK presence로 판정합니다. Pending required FK는 Go의 `int64`로 Django의 `None`을
나타낼 수 없으므로 pending state가 raw scalar보다 authoritative합니다. Pending 상태에서
`Author(ctx)`/`Reviewer(ctx)`는 exact assigned target을 I/O 0으로 반환하지만 `Unwrap`과 `Save`는
`unsaved_related_object`로 실패하여 misleading raw scalar를 공개하거나 plan에 넣지 않습니다.

Relation-format 모델을 target으로 직접 만들 때 manual-PK presence를 표현하는 범용 constructor는 이 ADR에서
보장하지 않습니다. Scalar-format `AuthorsAuthor`의 generated presence-aware value, loaded/saved relation-format
wrapper와 exact GDJ-0033 product fixture만 지원합니다.

### Assignment, reconciliation과 오류 우선순위

Source Save는 다음 순서로 fail-closed preflight를 수행합니다.

1. receiver/self/context 같은 structural validation을 수행합니다.
2. Phase 1은 모든 relation-cache tuple을 canonical normalized source-model identity + relation field `Name` 순서로
   검증하고 snapshot합니다.
3. Phase 2는 모든 assigned target origin을 같은 canonical 순서로 검증하면서 target PK를 edge별 정확히 한 번
   snapshot합니다.
4. Phase 3은 그 snapshot의 PK presence가 없는 첫 target을 같은 canonical 순서의
   `model_state_error/unsaved_related_object`로 반환합니다.
5. 세 phase가 모두 끝난 뒤 required scalar가 unset인 첫 field를 `field_error/required_field`로 반환합니다.
6. 검증을 마친 snapshot으로 candidate raw/write/object/cache를 전부 구성합니다.
7. 모든 validation/rebuild가 성공한 뒤에만 준비된 pointer/state를 error-free assignment로 게시하고 Manager plan/I/O로
   진입합니다.

이 corrected three-phase 순서는 declaration order가 아니라 generated surface의 canonical name order입니다. 앞선
Author no-PK가 뒤 Reviewer corrupt cache/self/origin을 가리거나 그 반대가 되는 경우도 전체 preflight가 I/O 0/source
unchanged로 먼저 거부합니다. Schema field
declaration permutation은 provenance/input hash를 바꾸지만 public surface와 error precedence를 바꾸지 않습니다.

Pending-at-assignment target이면서 source scalar가 계속 empty인 경우만 later-key reconciliation 대상입니다.
Caller가 source scalar를 명시적으로 바꾸면 그 선택이 이기고 pending cache를 폐기합니다. Key-present assignment 뒤
target key가 달라져도 source scalar는 assignment 당시 값을 유지하고 selected edge cache만 cold로 전환합니다.
전체 `(presence, value)` tuple이 같은 scalar derivation은 warm cache를 유지하고, tuple이 달라진 derivation은 selected
edge만 cold로 만듭니다.

Manual-PK target row가 DB에 없을 때는 REL-002가 아니라 실제 backend mutation 한 번까지 진행합니다. 이 packet은
새 SQLite FK error category/code를 만들지 않습니다. Structured driver/backend cause와 DB unchanged만 supplemental
gate로 확인합니다.

### Per-edge copy-on-write cache

Fresh source derivation은 cache/evaluation/flight/mutex를 original과 공유하지 않습니다.

- 변경한 edge는 새 warm/absent/cold cell을 얻습니다.
- 변경하지 않은 ready/absent edge는 snapshot을 독립 cell에 복제합니다.
- cold 또는 in-flight edge는 derived wrapper에서 독립 cold로 시작합니다.
- target wrapper pointer 값은 같은 assignment/local materialization 안에서 보존할 수 있지만 cache cell 자체와
  cross-materialization identity는 공유하지 않습니다.
- eager query는 result publication 전에 selected edge의 project cache를 hydrate합니다.

이 규칙은 selected Author를 바꾸어도 already-warm Reviewer를 유지하면서, original wrapper의 후속 cache publication이
derived wrapper로 새는 일을 막습니다.

### Write implementation boundary

`blog.Post`에 PK-presence field를 넣거나 existing app-generated exact 13을 다시 쓰지 않습니다. Project companion
내부에 private write model/descriptor와 `orm.Manager[private]`를 생성합니다. App descriptor의 metadata/scan/clone을
재사용하고, FK는 `query.Integer`/`query.Null` write value로 local render합니다.

Generic core 변경은 다음 두 seam에 한정합니다.

- `query.CodeUnsavedRelatedObject`
- `orm.mutationValueMatches`의 ForeignKey integer acceptance

SQLite compiler, schema/IR, migrations, DB session, existing app generated files는 수정하지 않습니다. Candidate facade,
golden, collision, last-good, external consumer와 product runtime을 함께 검증한 뒤 existing project companion 한 파일만
deterministically replace합니다.

## Session, concurrency와 rollback

- Session-origin facade는 callback 내부에서만 지원합니다. Callback 이후 warm cache success/cold failure 어느 쪽도
  장기 계약으로 만들지 않습니다.
- 같은 target wrapper의 concurrent Save/access/assignment는 caller synchronization 없이는 지원하지 않으며 data
  race가 될 수 있습니다.
- Save 성공 뒤 outer rollback 또는 backend failure가 발생해도 target PK와 derived source memory를 자동 복원하지
  않습니다. Caller가 transaction outcome을 확인하고 reload/re-derive합니다.
- Unknown transaction outcome의 retained connection policy는 Q-019/ADR-0030 후속 범위입니다.

## Publication과 호환 경계

- Existing app-generated exact 13은 byte-for-byte 보존합니다.
- Current project facade companion 하나의 deterministic replacement만 허용합니다.
- REL-002 publication 때 Go runner/manifest/protocol/godjcheck와 Python manifest-status assertion을 measured result에
  맞춥니다.
- Django scenario, oracle payload, checksum, static historical not-implemented fixture는 byte-frozen입니다.
- Q-017 project-generation manifest/component ABI는 coordinated multi-file upgrade/general `--check` 전에 별도
  결정합니다. Single-companion REL-002 publication의 선행 조건은 아닙니다.

## 거부한 대안

- Numeric ID의 0/비0으로 savedness 추론
- DB existence를 preflight query로 확인
- Existing app model/generated exact 13에 write-state 추가
- Original과 derived wrapper가 low-level evaluation/cache state를 공유
- 모든 relation cache를 fresh/cold로 재구성
- Key-present assignment 뒤 target PK 변경을 자동 추종
- Required relation에 nil/clear overload 제공
- Relation-specific save engine을 generic Manager와 별도로 구현

## 결과와 제한

- Django의 implicit descriptor 경험은 Go에서 explicit error-returning derivation/Save로 보이지만, assignment/cache,
  PK-presence, rollback 의미는 보존됩니다.
- REL-002가 구현돼 relation 12/12가 되더라도 Q-013은 relation 전체 API/다중 backend 범위 때문에 `Partial`, Q-017은
  broader public facade/generated-upgrade 정책 때문에 open입니다.
- Accepted Gate 0의 `Unwrap`-only 장기 UX, read/write capability 분리와 facade namespace는 후속 Q-017 compile
  prototype 대상입니다. 이 ADR은 그 문제를 해결했다고 주장하지 않습니다.
- Low-level typed `select_related`가 Resolve/Bind original cause를 zero query로 잃는 P2는 독립 remediation입니다.
- Relation-capable migration tuple/ProjectState/codec/SQLite FK DDL/apply-unapply/restart는 별도 contract-first vertical
  packet입니다.

## 증거와 재귀 경계

EVID-072는 activation head `a4a627a...`만 증명하고 EVID-073은 primary product를 바꾸지 않은 detached Phase-B
prototype와 local commands만 증명합니다. EVID-074/run `31574653183`은 exact decision-documentation head
`9d728610...`의 hosted 26/26·326/326과 audit P0..P3=0을 증명하지만 implementation proof로 재사용하지 않습니다.
EVID-075는 exact 23-path bounded product가 local `122 passing + 5 deviation + 0 oracle_locked`, relation 12/12와
corrected three-phase/final gates를 통과한 pre-hosted evidence입니다. EVID-076/run `31586910749`은 combined exact
31-path implementation head `be6f3d4e...`를 unique hosted gate에서 증명합니다. EVID-076을 포함하는 exact
15-document completion head `81f4aacb...`는 EVID-077/run `31590911735`의 별도 exact 26/26·326/326과 audit
P0..P3=0을 통과했습니다. EVID-072/074/076을 later head의 proof로 재사용하지 않았고 run `31590911735`도 EVID-077을
포함하는 exact seven-document terminal tree의 proof로 재사용하지 않습니다. Q-013은 `Partial`, Q-017은 P1/open이고
typed generated `select_related` cause-loss P2, relation-capable migration, reverse/general facade와 non-SQLite backend는
별도 범위입니다. Terminal baseline 전에는 다음 work를 active/ready로 만들지 않고 Draft PR #1도 merge하지 않습니다.
