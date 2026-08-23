# Django 호환성 정책

- 상태: Accepted
- 초기 프로필: `django-6.1`
- 기준 태그: Django `6.1`, commit `fe0a859f537d4238cf49fca39073513206f83122`
- 마지막 검증: 2026-08-23
- 현재 형식 mirror 검토: 2026-08-21

GoDj의 호환성은 Python 코드를 실행하는 능력이 아니라 **사용자가 관찰할 수 있는 개념, 결과, 부작용, 오류, transaction 의미**를 Go API에서 재현하는 정도입니다.

## Pre-release current-format policy

Accepted [ADR-0035](adr/0035-pre-release-current-only-format-and-generated-publication.md)에 따라 첫 외부 alpha
이전의 GoDj 내부 format/generated ABI는 하위호환 대상이 아닙니다. 이는 Django observable contract를
버린다는 뜻이 아니라, 개발 중 GoDj v2/v3를 배포된 두 세대처럼 동시에 읽지 않는다는 뜻입니다.

- Schema IR, Definition wire/digest와 historical `ProjectState`는 각각 current version 1 하나만 지원합니다.
  Unknown version은 계속 fail-closed합니다.
- `definition.Load`는 opaque `migrations.LoadedDefinitionSet`을 만들고 public lifecycle은
  `Executor.Migrate(ctx, loaded, request)` 하나입니다. Raw scalar `Apply`/`Unapply`/`ExecutePlan`은 별도
  `DirectExecutor` 책임이고 relation-bearing raw input은 capability error입니다.
- Backend는 mandatory `MigrationCapabilities`와 `BeginMigration(HistoryTransition, MigrationIntent)` 하나를
  사용합니다. Historical scalar/relation state도 하나의 current `StateReconstructor` 계약으로 수렴합니다.
- MIG-057..064는 single `format_version`, current digest, `execution`/`lifecycle`/`session_open_calls` vocabulary와
  opaque loaded executor 의미로 재기준화됐습니다. MIG-065..074의 definition-set digest/provenance도 같은 current
  format으로 재기준화됐으며 contract ID와 `passing` 상태는 유지합니다.
- GDJ-0035 Phase-B의 legacy tuple/profile/promotion artifact와 product publication sequence는 retire됐습니다.
  그 정확한 옛 bytes/EVID는 역사 기록입니다. 현재 checked-in MIG-075..086 manifest/oracle은 ADR-0035
  current-only 진단 reference로 다시 생성됐고 reference aggregate에는 포함되지만, 계속
  `oracle_locked`/unregistered라 제품 지원 또는 status 전환 주장은 아닙니다.

Current-only reset은 EVID-103의 hosted gate까지 완료됐습니다. GDJ-0037의 format-1 manifest read-old/write-current는
첫 alpha 전 내부 generated upgrade/recovery 계약이며 공개 하위호환 보장은 아닙니다. GDJ-0037은 final
full/386/repository-external source-clean-copy local gates와 exact correction head `d4643068...`의
[EVID-105](status/TEST_EVIDENCE.md#evid-20260821-105--gdj-0037-exact-head-hosted-completion) / CI #103
26/26 jobs·326/326 steps를 통과해 project publication 하위 경계에서 completed/hosted-verified됐습니다. Q-010은
`Partial`, Q-017은 P1/open이며 first-alpha 이후 일반 upgrader/semver 호환은 여전히 open입니다.

## 프로필 범위

초기 정본은 Django 6.1 final입니다. 현재 로컬
`/Users/hanhyeonjin/Documents/django`의 checkout은 Django 6.2 alpha가 포함된
`main`이므로 그대로 oracle로 사용하지 않습니다. M0는 PyPI `Django==6.1`과 공식
tag commit을 다음 exact profile로 고정했습니다.

| 항목 | 고정값 |
|---|---|
| Profile ID | `django-6.1-sqlite-darwin-arm64` |
| Django | 6.1, commit `fe0a859f537d4238cf49fca39073513206f83122` |
| Django wheel SHA-256 | `6c132cd980c9392b06807d4ca52d72530d631dc65a85d9dacede00a780cefbbe` |
| Python | uv-managed CPython 3.14.3 |
| SQLite | 3.50.4, exact `sqlite_source_id` 포함 |
| Platform | darwin/arm64 |
| Django settings | `USE_TZ=true`, `TIME_ZONE=UTC`, `LANGUAGE_CODE=en-us` |
| Process locale | `LC_ALL=C`, `TZ=UTC` |
| Dependency lock | `uv.lock`, hash와 uv 0.10.12를 profile에 기록 |

정본 JSON은
[`conformance/profiles/django-6.1-sqlite-darwin-arm64.json`](../conformance/profiles/django-6.1-sqlite-darwin-arm64.json)입니다.
CPython 3.14.3은 Django 6.1이 지원하는 minor이지만 현재 최신 micro는 아닙니다.
따라서 이 조합을 “Django가 최신 micro에서 공식 지원하는 profile”이 아니라 GoDj가
재현한 exact reference profile로 표현합니다.

이 표의 uv 0.10.12는 historical exact darwin artifact payload의 일부입니다. 일상 local/Ubuntu
portable 및 별도 Python compatibility matrix는 uv 0.12.3을 사용합니다. Compatibility matrix는 exact
CPython 3.12.13/3.13.15/3.14.3/3.14.7과 같은 Django/asgiref/sqlparse dependency를 검증하도록
구성됐고 GDJ-0022 fix head run `31329294154`에서 네 leg 모두 통과했습니다. Historical exact darwin은
계속 uv 0.10.12를 사용합니다.

DRF와 Channels에 해당하는 `api`, `realtime` 모듈은 장기 제품 범위에 포함되지만, 참조 프로젝트와 정확한 버전은 아직 정하지 않았습니다. Django 6.1 호환이라고 해서 DRF/Channels 호환까지 자동으로 주장하지 않습니다.

## 호환성 차원

| 차원 | 목표 |
|---|---|
| 개념 | Django 사용자가 대응 개념과 수명주기를 이해할 수 있음 |
| 동작 | 결과, 순서, 부작용, 오류 범주, transaction 의미가 계약과 일치 |
| 구조 | Go 관례 안에서 앱·파일·명령 구조가 친숙함 |
| 데이터 | table/column/relation/auth 관례와 이행 경로를 단계적으로 지원 |
| 내부 구현 | 호환 목표가 아님 |
| Python source/API | 호환 목표가 아님 |

## 비교 우선순위

1. 반환 결과와 DB/외부 부작용
2. 오류 분류와 발생 시점
3. transaction, locking, rollback 의미
4. Model과 QuerySet의 공개 동작
5. 프로젝트와 출력 구조
6. Query AST의 GoDj 내부 불변 조건
7. SQL의 의미
8. 필요한 좁은 경우에만 SQL 문자열

두 backend가 같은 의미의 SQL을 다르게 생성할 수 있으므로 SQL 문자열 동일성은 기본 합격 조건이 아닙니다.

## 계약 단위

각 동작에는 안정적인 ID를 부여합니다.

```text
META-xxx Profile, provenance, harness protocol
SCH-xxx  Schema와 model metadata
GEN-xxx  Code generation
QRY-xxx  QuerySet과 lookup
MOD-xxx  Model lifecycle
REL-xxx  Relation과 loading
MIG-xxx  Migration
FRM-xxx  Form/validation
ADM-xxx  Admin
AUT-xxx  Auth/session/permission
WEB-xxx  Routing/middleware/template
API-xxx  Serializer/API
RTM-xxx  Realtime
GIS-xxx  Spatial
I18N-xxx Locale/timezone/translation
CTR-xxx  Contrib와 infrastructure
DB-xxx   Backend 공통 계약
```

계약에는 최소한 다음을 기록합니다.

- contract ID와 설명
- 정확한 reference profile
- Reference provenance: Django source/문서/test 경로 또는 Accepted ADR decision
- 입력, fixture, 연산
- 결과, 순서 보장, 부작용
- 안정적인 오류 범주
- 대상 backend와 환경
- 관련 GoDj 테스트/runner 경로
- 의도적 차이와 ADR
- 현재 상태와 마지막 검증 checkout

초기 계약 목록과 상태는
[`conformance/contracts/manifest.json`](../conformance/contracts/manifest.json)이 실행
정본이고 [status/IMPLEMENTATION_MATRIX.md](status/IMPLEMENTATION_MATRIX.md)가 사람이
보는 구현 상태를 요약합니다. Strict validator가 profile, 순서, contract별 실행 phase,
provenance, 비교 차원과 observation payload를 함께 검증합니다.

GDJ-0003에서 manifest가 expected phase를 직접 선언하도록 wire protocol을 v2로
승격했습니다. v1 artifact를 조용히 새 의미로 해석하지 않으며 profile과 모든 ordered
manifest/oracle/explicit not-implemented fixture는 같은 v2 envelope를 사용합니다.

GDJ-0003의 write/migration 확장은 기존 11개의 행동 의미, ID와 passing 상태를 유지하고
[`write-migration-manifest.json`](../conformance/contracts/write-migration-manifest.json)에
MOD-001..007과 MIG-001..004를 별도 ordered set으로 둡니다. 두 set은 같은 exact
profile을 공유하지만 contract ID/order, phase와 payload 선언이 다르므로 oracle을 서로
바꿔 사용할 수 없습니다.

GDJ-0005는 같은 profile에
[`save-lifecycle-manifest.json`](../conformance/contracts/save-lifecycle-manifest.json)을
세 번째 ordered set으로 추가했습니다. MOD-008..019는 fully loaded instance save,
`update_fields`, force mode, explicit PK와 rollback 뒤 object/DB state를 다룹니다. 세
manifest의 contract ID와 scenario는 전역으로 겹치지 않으며 모든 ordered cross-pair가
validation에서 거부됩니다. Protocol v2에 별도 manifest digest는 없으므로 이 전역
uniqueness gate를 유지하고, 동일 ID/order/phase를 재사용해야 할 필요가 생길 때만
wire v3 set identity를 검토합니다.

GDJ-0007은
[`query-cache-manifest.json`](../conformance/contracts/query-cache-manifest.json)을 네 번째
ordered set으로 추가했습니다. QRY-011..021은 repeated/empty/stale evaluation, chain,
Count/Exists, iterator, cold index/First, failure retry와 evaluated source의 fresh copy를
다룹니다. 네 manifest의 contract ID/scenario는 전역으로 유일하고, 모든 12개 ordered
cross-pair가 거부됩니다. GDJ-0007 완료 당시 이 set은 exact Django oracle만
`oracle_locked`인 reference 계약이었고 GoDj 지원으로 세지 않았습니다. GDJ-0008은 같은
manifest를 실제 generated model/QuerySet/SQLite adapter에 연결해 11개를 `passing`으로
올렸습니다. 등록되지 않은 임의 scenario는 set 상태와 관계없이 계속 fail-closed합니다.

GDJ-0009는
[`migration-planning-manifest.json`](../conformance/contracts/migration-planning-manifest.json)을
다섯 번째 ordered set으로 추가했습니다. MIG-005..016은 linear/applied-pruned/prior/zero/
cross-app plan, caller-ordered target과 shared dependency, retained branch, missing target,
inconsistent history, missing dependency와 cycle을 다룹니다. 다섯 manifest의 contract
ID/scenario는 전역으로 유일하고 모든 20개 ordered cross-pair가 거부됩니다.

MIG-005..014는 planning 요청/preflight의 `evaluation`, graph construction 자체가 실패하는
MIG-015/016은 `construction` phase입니다. Missing target은
`migration_plan_error/target_not_found`, inconsistent history는
`migration_history_error/inconsistent_applied_history`, missing dependency와 cycle은
`migration_graph_error` category의 `dependency_not_found`/`dependency_cycle`을 사용합니다.
Raw exception message와 cycle traversal order는 계약하지 않습니다.

GDJ-0009 완료 당시 이 set은 12개 `oracle_locked`, Django oracle 12개 `observed`, static
GoDj fixture 12개 `not_implemented`였습니다. 따라서 당시 reference contract는 총 57개지만
제품 `passing`은 기존 45개뿐이었고, Product `godjcheck`는 planning scenario를 exit 2/no
output으로 거부했습니다.

GDJ-0010은 [ADR-0013](adr/0013-immutable-migration-planner.md)의 immutable identity graph,
별도 AppliedState, named/zero target과 structured planning error를 실제 public API로
구현했습니다. Fifth adapter가 public Planner를 실행해 MIG-005..016도 semantic 0-diff가
되었고 GDJ-0010 완료 당시 다섯 제품 set의 57개가 모두 `passing`이었습니다. Static fixture의 ordered 12
`not_implemented` mismatch와 unknown scenario fail-closed는 구현 전 상태를 녹색으로 만들지
않는 회귀로 계속 보존합니다.

GDJ-0011은
[`migration-execution-manifest.json`](../conformance/contracts/migration-execution-manifest.json)을
여섯 번째 ordered reference set으로 추가했습니다. MIG-017..026은 linear forward/backward,
applied prefix와 unrelated branch, forward/backward operation/recorder failure, mixed plan
preflight와 empty no-op를 다룹니다. 여섯 manifest의 contract ID/scenario는 전역으로
유일하고 모든 30개 ordered cross-pair가 거부됩니다.

External execution metrics는 connection summary와 compact ordered steps만 계약합니다. Raw
render/operation/recorder/transaction event는 runner 내부 live assertion이며 Django private
choreography를 외부 표면으로 고정하지 않습니다. Historical state before/after는 MIG-019에만
포함하고, MIG-023/024의 recorder sentinel은 실제 recorder write 전 실패를 뜻하는
`fault_point=before_record_write`입니다.

MIG-024는 Django backward가 schema transaction을 먼저 commit한 뒤 recorder row를
삭제한다는 경계를 그대로 보존합니다. A3 unapply와 A2 schema reverse 뒤 A2 recorder
before-write fault가 발생해 최종 schema는 A1, recorder는 A1/A2이며 phase는 `commit`입니다.
이는 GoDj의 기존 same-transaction reverse와 다릅니다.

GDJ-0011 완료 시 새 manifest 10개는 `oracle_locked`, Django oracle은 10 `observed`, static
fixture는 10 `not_implemented`입니다. Product adapter는 exit 2/no output으로 fail-closed하며
`make godj-conformance`는 기존 다섯 adapter의 57개만 실행합니다. 따라서 총 reference
contract 67개를 제품 통과로 표현하지 않습니다.

[ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md)와
[DEV-0001](DEVIATIONS.md#dev-0001--역방향-migration의-schema와-recorder를-같은-transaction으로-처리)은
Django `schema_then_record` 대신 GoDj same-transaction reverse를 유지하는 결정입니다.
GDJ-0012는 `ExecutePlan`과 live SQLite adapter를 구현해 MIG-017/019/021/023/025/026을
`passing`, MIG-018/020/022/024를 승인된 `deviation`으로 검증했습니다. 제품 분류는
GDJ-0012 완료 당시 `63 passing + 4 deviation`이었으며, reference 67개 전부가 Django와
exact 일치한다고 표현하지 않습니다.

GDJ-0013은
[`migration-restart-manifest.json`](../conformance/contracts/migration-restart-manifest.json)을
일곱 번째 ordered reference set으로 추가했습니다. MIG-027..036은 recorder table
absent/empty, record/unrecord 뒤 fresh read, database isolation, applied-prefix tail,
fully-applied empty plan, unknown legacy row, explicit known-history preflight와 middle-failure
restart를 다룹니다. 모든 계약은 `evaluation` phase이며 read/planning 전후 recorder/schema
state가 같고 DDL/write/기타 non-SELECT statement가 0인지 비교합니다.

GDJ-0013 완료 당시 새 manifest는 10 `oracle_locked`, Django oracle은 10 `observed`, static
fixture는 10 `not_implemented`였습니다. 제품 adapter는 exit 2/no actual output으로
fail-closed했고 기존 제품 분류는 `63 passing + 4 deviation`이었습니다. 일곱 manifest의
ID/scenario는 전역으로 유일하고 모든 42개 ordered cross-pair가 거부됩니다. 당시 reference
총계 77개를 제품 통과로 표현하지 않습니다.

MIG-035는 `MigrationExecutor`가 자동으로 history를 검사한다고 주장하지 않습니다.
Migrate-style explicit `check_consistent_history`가 plan 호출 전에
`migration_history_error/inconsistent_applied_history`로 실패하는 경계를 고정합니다.
MIG-036은 process kill/crash가 아니라 앞선 durable commit 뒤 기존 instance를 버리고 같은
database에서 fresh recorder/executor를 구성해 남은 tail을 다시 계획하는 의미입니다.

GDJ-0014는 별도 `AppliedMigrationReader`, core `LoadAppliedState`와
`Planner.CheckHistory`, SQLite read-only recorder reader와 일곱 번째 live adapter를
구현했습니다. Absent read는 table을 만들지 않고, raw identity validation과 unknown legacy
preservation을 거쳐 fresh file-backed backend에서 tail을 계획합니다. MIG-027..036은 10개
모두 `passing`이며 GDJ-0014 완료 당시 일곱 제품 set의 분류는 `73 passing + 4 deviation`입니다. 네
DEV-0001 계약이 남아 있으므로 77개 모두가 Django exact 일치한다는 뜻은 아닙니다.

완료된 [GDJ-0015](../work/0015-historical-project-state-reconstruction-compatibility-contracts.md)는
[`migration-state-reconstruction-manifest.json`](../conformance/contracts/migration-state-reconstruction-manifest.json)을
여덟 번째 ordered reference set으로 추가했습니다. MIG-037..046은 explicit empty,
first/middle before·after, cross-app dependency, multiple target/shared dependency,
omitted-target latest leaves, applied-prefix startup과 unrelated-known branch inclusion을
다룹니다. Unknown legacy identity는 applied observation에는 남지만 schema state로
materialize하지 않습니다. 모든 계약은 `evaluation` phase이고 logical state와 함께
deliberately divergent live database의 before/after 불변, DDL/write/기타 non-SELECT 0을
비교합니다.

GDJ-0015 완료 시 새 manifest는 10 `oracle_locked`, Django oracle은 10 `observed`, static
fixture는 10 `not_implemented`입니다. Product `godjcheck`는 exit 2/no actual output으로
fail-closed했고 당시 기존 일곱 product set의 분류는 `73 passing + 4 deviation`이었습니다.
여덟 manifest의 ID/scenario는 전역으로 유일하고 모든 56개 ordered cross-pair가
거부됩니다. 따라서 reference 총계 87개를 제품 통과로 표현하지 않습니다.

완료된 [GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)과
Accepted [ADR-0016](adr/0016-historical-project-state-reconstruction.md)은 loaded definition을
deep-copy하는 immutable `StateReconstructor`, explicit empty/latest/before/after/applied
request와 여덟 번째 live adapter를 구현했습니다. Applied scenario는 real SQLite recorder를
read-only로 읽어 `LoadAppliedState`를 거치며, core는 DB/backend I/O가 없습니다.
MIG-037..046은 10 `passing`이고 GDJ-0016 완료 당시 제품 분류는
`83 passing + 4 deviation`입니다.
Recorder identity만으로 definition을 만들거나 read/reconstruct/plan/execute atomicity를
보장하지 않습니다.

## 계약 상태

설계·구현 상태와 실행 상태를 구분합니다.

```text
계약 실행 상태:
draft → oracle_locked → red → passing
                         └→ deviation
```

- `draft`: 계약 내용이나 환경이 아직 바뀔 수 있음
- `oracle_locked`: provenance가 명시된 reference 결과가 재현 가능하게 고정됨. Pinned Django
  exact observation 또는 Accepted ADR의 GoDj decision oracle일 수 있으며 manifest가 source/
  decision 종류, version과 `derived` 여부를 구분함
- `red`: GoDj가 실행되지만 계약을 통과하지 못함
- `passing`: 명시된 profile/backend에서 통과 증거가 있음
- `deviation`: 차이를 의도적으로 수용했고 deviation/ADR이 연결됨

미구현 기능을 skip하여 녹색으로 만드는 것은 금지합니다. 미구현은 명시적 상태와 실패/미지원 결과로 드러나야 합니다.

## 초기 계약 후보

M0에서는 범용 Django 테스트 전체를 옮기지 않고 11개의 작은 계약을 고정했습니다.

- exact lookup
- ASCII `icontains`
- 여러 filter의 AND 결합
- QuerySet 체이닝과 원본 query plan 불변성
- `order_by`와 limit
- 빈 결과
- nullable 값과 `isnull`
- 잘못된 field와 lookup의 오류 의미
- 실제 I/O 전 지연 평가
- 결과 cache 의미는 GDJ-0007의 별도 QuerySet evaluation/cache set에서 추가

M0에서는 Django oracle만 고정되어 `oracle_locked`였고 미구현 fixture가 11개
mismatch를 내는지 확인했습니다. M1에서는 `Article` 한 모델의 typed/dynamic lookup,
동일 AST, SQLite 실행과 runtime metadata adapter가 11개 계약 모두 oracle과
일치하므로 manifest 상태를 `passing`으로 올렸습니다. 이 상태는 M1의 정확한
profile/backend와 기록된 evidence에 한정되며 Django 전체 ORM 호환을 뜻하지 않습니다.

GDJ-0003은 다음 11개를 추가했습니다.

- auto PK create와 nullable create 값
- partial update의 omitted와 explicit NULL
- instance delete
- transaction commit과 rollback error
- CreateModel, nullable AddField와 reverse
- migration recorder와 atomic operation failure recovery

GDJ-0004는 generated create/patch, generic Manager write, SQLite transaction과 최소
migration executor/editor/recorder를 실제 adapter에 연결했습니다. 두 번째 manifest의
11개도 Go SQLite 3.53.3에서 Django oracle과 일치해 `passing`입니다. 이 작업 완료
당시에 검증된 contract set은 M1 read/metadata 11개와 M2 제한 write/migration
11개였습니다. 이는 Django ORM/Migration 전체 호환을 뜻하지 않았고, instance
`Save()`와 migration file/graph/lock 등은 후속 계약 범위로 남겼습니다. Static `not_implemented`
fixture의 11개 mismatch는 구현 전 상태를 녹색으로 만들지 않는 회귀 증거로 유지합니다.

GDJ-0005는 Save lifecycle 12개를 Django exact oracle에 고정했고, GDJ-0006은 이 set을
generated model/Manager/SQLite 실제 제품 경로로 실행해 `passing`으로 올렸습니다.
기본 fully loaded save는 dirty-only가 아니라 writable concrete field 전체를 쓰고,
explicit `update_fields`는 named field만 쓰며 empty iterable은 zero-I/O no-op입니다.
Force validation과 missing-row `NotUpdated`, explicit PK의 UPDATE/UPDATE→INSERT, 명시적
transaction rollback 뒤 model field/assigned PK가 메모리에 남는 의미도 분리했습니다.
GDJ-0006 완료 당시 제품에서 검증된 세 set은 11 + 11 + 12, 총 34개 `passing`이었습니다.
Static fixture의 정확한 12개 mismatch는 현재 제품 결과가 아니라 구현 전 상태를 녹색으로
만들지 않는 false-green 회귀 증거로 유지합니다.

GDJ-0007은 Q-007의 QuerySet evaluation/cache/terminal 의미를 QRY-011..021의 네 번째
set으로 먼저 고정했습니다. 성공한 empty/non-empty full evaluation cache, stale snapshot,
chain/fresh 독립성, cold/warm terminal과 실패 재시도를 step별 query count와 DB state로
비교하며, 당시 oracle만 `oracle_locked`였습니다.

GDJ-0008은 [ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)의 direct value-copy
state 공유, chain/fresh 독립 state, 같은 state `All` singleflight, owner/waiter cancellation
격리, generated deep clone과 terminal API를 실제 제품 경로에 구현했습니다. 네 번째
adapter의 11개도 Django oracle과 의미적으로 0-diff여서 GDJ-0008 완료 당시 검증 범위는
11 + 11 + 12 + 11, 총 45개 `passing`입니다. 두 독립 Go actual은 각각 56,283 bytes이고
SHA-256
`c7ccad635a13e3e071cba4d46b79d3110e24b2e9501a1ca95054ded520b0fa92`로 서로
byte-identical합니다. 이는 56,426 bytes, SHA-256
`d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682`인 Django oracle과
byte-identical하다는 뜻이 아닙니다. Protocol comparator가 계약된 result/error/DB
state/metrics 의미의 0-diff를 확인한 것입니다.

Explicit query-cache static fixture의 정확한 11개 `not_implemented` mismatch는 현재 제품
actual이 아니라 구현 전 false-green 회귀 증거로 그대로 유지합니다. 임의 unknown
scenario도 실제 adapter 결과로 가장하지 않고 fail-closed합니다. 실행 증거는
[EVID-20260808-007](status/TEST_EVIDENCE.md#evid-20260808-007--gdj-0008-queryset-evaluation-and-cache-product-slice)에
기록합니다.

GDJ-0009은 dependency-required plan 순서, caller target order와 shared dependency
deduplication을 고정하고 planning 전후 recorder/schema state와 DDL/write/non-SELECT 0을
함께 비교합니다. Django private `iterative_dfs`의 incomparable sibling tie-break, Python
graph object와 cycle message/path는 호환 계약이 아닙니다. Two-process exact oracle,
20 ordered cross-binding, static 12 mismatch와 mutation 증거는
[EVID-20260808-008](status/TEST_EVIDENCE.md#evid-20260808-008--gdj-0009-migration-planning-compatibility-contracts)에
기록합니다. [ADR-0013](adr/0013-immutable-migration-planner.md)은 후속 GoDj 제품이 불변
identity graph와 별도 applied state를 쓰도록 결정했지만, Accepted ADR만으로 이 12개가
`passing`인 것은 아닙니다.

GDJ-0010은 ADR-0013을 구현해 MIG-005..016을 실제 public Planner 경로로 실행합니다.
두 독립 Go actual은 각각 39,094 bytes, SHA-256
`eb5bf3b6f41855684582f67b3be675da42975b8fc1ed9c7085f6d35a078eac32`로 서로
byte-identical하며, 39,139 bytes의 Django oracle과는 protocol 의미상 12개 0-diff입니다.
Planning state/zero metrics는 실제 DB probe가 아니라 backend import가 없는 pure structural
adapter에서 산출합니다. GDJ-0010 완료 당시 제품 검증 합계는 57개였고, 상세 증거는
[EVID-20260808-009](status/TEST_EVIDENCE.md#evid-20260808-009--gdj-0010-immutable-migration-planner-product-slice)에
기록합니다.

GDJ-0011의 sixth manifest는 8,720 bytes, SHA-256
`f414cd7a495f6e6765df06ca1427485ecc16a8d19c344f190f5f1421dc2a517d`, Django oracle은
47,119 bytes, SHA-256
`641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e`, static fixture는
1,685 bytes, SHA-256
`6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04`입니다. 두 독립
random-hashseed process와 checked-in oracle은 byte-identical합니다. Static ordered 10
mismatch, product unsupported exit 2/no actual output, 30 cross-binding과 compact step
mutation gate는
[EVID-20260808-010](status/TEST_EVIDENCE.md#evid-20260808-010--gdj-0011-migration-plan-execution-compatibility-contracts)에
기록합니다. 이는 GDJ-0011 완료 당시의 artifact/status 증거입니다.

GDJ-0012는 manifest status와 deviation provenance만 변경해 9,120 bytes, SHA-256
`1857dcf375ed09f8566798ce662c72a86ef41706e478eef6f208077b156886e9`로 만들었고,
locked Django oracle과 static fixture bytes는 유지했습니다. Sparse product expectation은
4,685 bytes, SHA-256
`568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547`입니다. 두 독립 Go
actual은 각각 47,446 bytes, SHA-256
`f191d116cc38194e2019df358c31f752101fdacb005d9cc442b701d8d4afde4b`로 byte-identical하며,
reference를 원 manifest로 먼저 검증한 뒤 DEV-0001의 등록된 차이만 적용한 product
expectation과 10개 0-diff입니다. 상세 증거는
[EVID-20260808-011](status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)에
기록합니다.

GDJ-0013의 seventh manifest는 10,225 bytes, SHA-256
`93e25d02208a765001760f76715ff6e9642451c5823efc62cc40b1d249dbd42b`, Django oracle은
33,888 bytes, SHA-256
`90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727`, static fixture는
1,715 bytes, SHA-256
`31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55`입니다. 두 독립
random-hashseed process와 checked-in oracle은 byte-identical합니다. Static ordered 10
mismatch, product exit 2/no output, 42 cross-binding과 recorder/identity/alias/plan/history/
restart/zero-mutation gate는
[EVID-20260808-012](status/TEST_EVIDENCE.md#evid-20260808-012--gdj-0013-recorder-backed-restart-planning-compatibility-contracts)에
기록합니다.

GDJ-0014는 같은 manifest에서 status만 10 `passing`으로 전환해 10,165 bytes, SHA-256
`79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290`로 만들었습니다.
33,888-byte Django oracle과 1,715-byte static fixture의 bytes/hash는 유지했습니다. 두 독립
Go actual은 각각 33,795 bytes, SHA-256
`f9e4d3dc7078426f06a08374a36a670a36e1fa2ae08562fd08f80e91db1b31cb`로
byte-identical하고 locked oracle과 protocol 의미상 10개 0-diff입니다. Static ordered 10
mismatch와 42 cross-binding은 계속 false-green gate이며, 상세 증거는
[EVID-20260808-013](status/TEST_EVIDENCE.md#evid-20260808-013--gdj-0014-recorder-backed-restart-planning-product-slice)에
기록합니다.

GDJ-0015의 eighth manifest는 9,257 bytes, SHA-256
`04b7e92a5bbf9ff50f0247be7708dfb18a5534e40bac86a518a6b744fc0ef728`, Django oracle은
89,997 bytes, SHA-256
`bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9`, static fixture는
1,715 bytes, SHA-256
`9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1`입니다. 두 독립
random-hashseed process와 checked-in oracle은 byte-identical합니다. Static ordered 10
mismatch, product exit 2/no output, 56 cross-binding과 state/request/applied/graph/DB/metrics
mutation gate는
[EVID-20260808-014](status/TEST_EVIDENCE.md#evid-20260808-014--gdj-0015-historical-projectstate-reconstruction-compatibility-contracts)에
기록합니다. 이 시점의 분류는 `73 passing + 4 deviation + 10 oracle_locked`이며 87개
전체가 제품 지원이라는 뜻이 아닙니다.

GDJ-0016은 eighth manifest status 10개만 `passing`으로 전환해 9,197 bytes, SHA-256
`85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c`로 만들었습니다.
Locked Django oracle/static/SHA256SUMS는 변경하지 않았습니다. 두 독립 Go actual은 각각
89,867 bytes, SHA-256
`a307d185e5a3c67a679f62bfa4575f6f43ef8ad41e55c78fdf34d5acb5866e44`로
byte-identical하고 oracle과 protocol 의미상 10개 0-diff입니다. GDJ-0016 완료 당시 8 product
set은 `83 passing + 4 deviation`, 87 unique contract와 56 ordered cross-binding을 검증했습니다.
Static fixture는 ordered 10 mismatch를 유지합니다. 상세 증거는
[EVID-20260808-015](status/TEST_EVIDENCE.md#evid-20260808-015--gdj-0016-historical-projectstate-reconstruction-product-slice)에
기록합니다.

완료된 [GDJ-0017](../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)은
[`migration-lifecycle-manifest.json`](../conformance/contracts/migration-lifecycle-manifest.json)을
아홉 번째 ordered reference set으로 추가했습니다. MIG-047..056은 fresh/prefix/latest no-op,
named forward/reverse, app zero target과 cross-app dependent, unknown legacy preservation,
explicit history preflight, middle failure와 file-backed fresh restart를 다룹니다. Phase는
MIG-047..053/056 `commit`, MIG-054 `evaluation`, MIG-055 `rollback`입니다. Reverse의 physical
transaction topology는 이 payload에 중복 고정하지 않고 DEV-0001/ADR-0014에 남깁니다.

Manifest는 13,680 bytes/SHA-256
`23a9e919edff932ae781f0768aeaf7f184fe392ec53598fa18524cf50d979a8e`, oracle은 98,436
bytes/SHA-256 `7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`,
static fixture는 1,681 bytes/SHA-256
`b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`입니다. 새 10개는
`oracle_locked`, Django oracle은 `observed`, static fixture는 `not_implemented`입니다.
아홉 set의 97 ID/scenario는 유일하고 72 ordered cross-binding이 거부됩니다. 제품 adapter가
없으므로 GDJ-0017 완료 당시 분류는 `83 passing + 4 deviation + 10 oracle_locked`이며 97개
전체가 제품 지원이라는 뜻이 아닙니다. Revision-fence spike는 Accepted ADR-0017의
feasibility evidence일 뿐 제품 compatibility claim이 아닙니다.

완료된 [GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md)은 ninth
manifest를 public `Executor.Migrate`와 revision-fenced SQLite live adapter에 연결했습니다.
Fresh/prefix/no-op, named forward/reverse, unknown legacy, history preflight, middle rollback과
file reopen resume의 9개는 `passing`입니다. MIG-052만 Accepted ADR-0013의 canonical sibling
order를 보존하는 DEV-0002 `deviation`이며, sparse expectation은 ordered
`result.plan[0..2]`와 `metrics.steps[0..2]` 여섯 path만 바꿉니다. 기존 DEV-0001 네 계약과
locked Django observation은 변경하지 않았습니다.

현재 manifest는 13,735 bytes/SHA-256
`5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0`, DEV-0002
expectation은 6,769 bytes/SHA-256
`58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b`입니다. Locked oracle과
static fixture는 각각 98,436 bytes/
`7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc`, 1,681 bytes/
`b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722`로 유지됩니다. 두
independent Go actual은 각각 98,304 bytes/SHA-256
`a32e768323dae33a312267d5f8041818570d55f1fd887b29580cf8d4c5b3064b`이고 10-contract
product expectation과 일치합니다. GDJ-0018 완료 당시 제품 분류는 9 adapter의
`92 passing + 5 deviation`; 97 unique contract와 72 ordered cross-binding이었습니다.

Accepted [ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md)의 호환 경계는
exact-one opaque session snapshot, SQLite `BEGIN IMMEDIATE` epoch/revision/fingerprint fence와
no-fallback/no-automatic-adoption입니다. `CommitRolledBack`은 confirmed state와 token을 advance하지
않고 SQLite session을 poison해 같은 call에서 재시도하지 않습니다. Empty-table default-bearing
`AddField`는 logical default를 보존하되 physical persistent default를 만들지 않으며 nonempty
table은 계속 unsupported입니다.

### Historical GDJ-0019..0022 definition/project-check snapshot

다음 단락의 tuple, `Set.Migrate`, artifact hash와 집계는 각 완료 checkout의 증거입니다. 현재 계약은
문서 상단의 pre-release current-format policy와 MIG-057..074 재기준화를 따릅니다.

완료된 [GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)은
[`migration-definition-source-manifest.json`](../conformance/contracts/migration-definition-source-manifest.json)을
열 번째 ordered reference set으로 추가했습니다. MIG-057..064는 explicit document bytes,
strict JSON v1과 exact tuple `(1,1,1,2)`, fully normalized Schema IR v2, closed
`CreateModel`/non-PK `char`·`boolean` `AddField`, loader-owned atomic snapshot, canonical digest와
stage-major failure precedence를 고정합니다. MIG-064는 public Django graph/executor handoff의
reference-only success observation이며 Go product 지원 계약이 아닙니다.

여덟 contract 모두 Accepted [ADR-0019](adr/0019-versioned-migration-definition-source.md)을
`kind=decision`, `derived=false`로 기록합니다. MIG-057과 MIG-064만 pinned Django 6.1
source/test provenance를 추가해 identity/dependency/ordered operation과 public executor의 실제
공통 동작을 구분합니다. 따라서 strict JSON, tuple, codec, digest와 precedence를 Django 형식이나
Django observation으로 과장하지 않습니다.

GDJ-0019 completion manifest는 5,195 bytes/SHA-256
`8a5f914a05eaa6382d1f43589743e4e8ba466b747e6fa80eb1cabef61bb924e6`, locked oracle은
29,851 bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static
not-implemented fixture는 1,574 bytes/
`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`입니다. 갱신된
`SHA256SUMS`는 959 bytes/
`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`입니다. GDJ-0019 완료
당시 reference는 10 set/105 unique contract/90 ordered cross-binding이고 새 8개는
`oracle_locked`였습니다. 제품 loader와 열 번째 GoDj adapter가 없었으므로 당시 제품 분류는
9 adapter의 `92 passing + 5 deviation`으로 유지됐습니다.

완료된 [GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)과 Accepted
[ADR-0020](adr/0020-migration-definition-loader-product-shape.md)은 public leaf package
`migrations/definition`에 caller-provided `Source`를 소비하는 bounded loader를 구현했습니다.
`Load(...Source)`는 raw document를 보존하지 않는 loader-owned immutable `Set`과 value-only
`LoadReport`를 atomic publish하며 zero `Set`은 canonical empty set입니다. Accessor, nested
operation/IR와 failure graph mapping은 fresh deep copy이고, `Set.Migrate`는 fresh definitions와
immutable request value를 existing `Executor.Migrate`에 정확히 한 번 전달합니다.

Public resource contract는 source 2,048, SourceID 1,024 bytes, document 1 MiB, batch 16 MiB,
JSON depth 64, document values 65,536, batch values 262,144, migration별 dependencies 2,047,
operations 2,048, `CreateModel` fields 2,048의 exact 10 cap입니다. Strict scanner는 any-depth
duplicate, invalid UTF-8/surrogate/numeric lexeme와 RFC 6901 failure ordering을 bounded하게
검증합니다. Source-owned failure는 정확히 9개 source error code만 사용하고 resource breach는
stable limit context로 표현합니다. Existing graph `*migrations.PlanningError`와 lifecycle error는
wrap/reclassify/retry하지 않아 원래 identity와 `errors.As` 의미를 보존합니다.

GDJ-0020 manifest는 status-only 5,147 bytes/SHA-256
`688556c4a338e4ad7f580bfcd4d6121ddda0e72c871d1bfba625c352d22c3488`입니다. Locked oracle
29,851 bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static fixture
1,574 bytes/`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`와
`SHA256SUMS` 959 bytes/`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`는
변경하지 않았습니다. MIG-057..064는 Django 결과 parity가 아닌 Accepted ADR decision-reference
8 `passing`이며, 성공 검증 문구도 `locked reference oracle`로 Django-derived set의
`locked Django oracle`과 구분합니다. GDJ-0020 완료 당시 제품 분류는 10 adapter/105 contract의
`100 passing + 5 deviation`; 90 ordered cross-binding이었습니다.

완료된 [GDJ-0021](../work/0021-migration-project-check-compatibility-contracts.md)과 Accepted
[ADR-0021](adr/0021-project-linked-migration-check.md)은
[`migration-project-check-manifest.json`](../conformance/contracts/migration-project-check-manifest.json)을
열한 번째 ordered reference set으로 추가합니다. MIG-065..074는 exact `godj.toml` selection,
canonical descriptor v1, build/project-runner protocol, flat no-follow source discovery,
`definition.Load` exactly-once와 DB/lifecycle call 0, public exit `0/1/2/3/130`을 고정합니다. 이는
Django의 DB-aware `migrate --check`, model-drift `makemigrations --check` 또는 Python module
discovery에서 파생한 parity가 아닙니다. 열 contract 모두 exact
`kind=decision`, `reference=ADR-0021`, `derived=false`이며 GDJ-0021 완료 당시 status는
`oracle_locked`였습니다.

GDJ-0022의 status-only 전환 뒤 project-check manifest는 4,520 bytes/SHA-256
`0bbf254e80fea17b52070d0589da5ddcd401ff67440062a89b4fcd3e8309c048`, locked reference oracle은
19,971 bytes/`49f50b97bfa1973cef6fe464296a7c973b87e4ad1f9aaefecee24ab64f04d4d2`, static
not-implemented fixture는 1,729 bytes/
`86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680`입니다. 기존 checksum 10줄은
byte-for-byte prefix로 보존하고 11번째 oracle line만 append한 `SHA256SUMS`는 1,061 bytes/
`74b5b253b2026b98ff4cf5a6abce4c0aa4881488df6c874c9012050495b0b59f`입니다.

Protocol 분류는 11 reference set/115 unique contract/110 ordered cross-binding입니다. Static
oracle/not-implemented comparison은 exit 1과 MIG-065..074 ordered status mismatch 10개를 계속 냅니다.
Completed [GDJ-0022](../work/0022-migration-project-check-product-slice.md)와 Accepted
[ADR-0022](adr/0022-project-runtime-and-global-migration-check.md)는 independent global/linked/protocol
kernel, exact public project facade와 actual product adapter를 추가했습니다. MIG-065..074는 10
`passing`이고 GDJ-0022 완료 당시 제품은 11 adapter/115 contract의 `110 passing + 5 deviation`이었습니다.
`conformance/projectcheck/**` test-only proof는 byte-preserved 독립 gate이며 product가 import/read하지
않습니다.

Completed [GDJ-0023](../work/0023-foreign-key-relation-compatibility-contracts-and-binding-feasibility.md)은
twelfth ordered reference set `relation-manifest.json`의 REL-001..012를 고정했습니다. 이 set은 pinned
Django 6.1/SQLite의 cross-app ForeignKey metadata, unsaved-target failure, lazy cache,
forward/reverse path, nullable/isnull, PROTECT/SET_NULL, `select_related`와 reverse `prefetch_related`
외부 동작을 result/DB state/query·JOIN·mutation metrics로 고정합니다. 새 12개는 `oracle_locked`이며
product support가 아닙니다. Aggregate는 12 reference
sets/127 contracts/132 ordered cross-bindings이고 GDJ-0023 완료 당시 product는 11 adapters/115 contracts의
`110 passing + 5 deviation`을 유지했습니다. Test-only relation-binding proof와 Accepted ADR-0023의
Go-specific compile/AST/field-union direction evidence를 Django oracle payload에 섞거나 REL `passing`으로
세지 않습니다. Implementation head의 exact 22/22 hosted acceptance는 EVID-032에 기록했으며
PostgreSQL/MySQL/Windows 또는 relation product 지원 증거가 아닙니다.

Completed [GDJ-0024](../work/0024-autofield-foreign-key-schema-ir-vnext-and-rel001-product-metadata.md)와
Accepted [ADR-0024](adr/0024-autofield-foreign-key-schema-ir-vnext-and-project-binding.md)는 이 reference를
변경하지 않고 REL-001 metadata 하나만 제품화한 exact boundary입니다. Scalar v2 bytes를 보존하고 explicit
IR v3 ForeignKey source, v2 target/v3 source의 additive `GoDjRelationSchema` companion, atomic
`orm.BindProject`와 full 12-output partial product comparison을 사용합니다. REL-001만 observed/passing,
REL-002..012는 ordered payload-free not-implemented/oracle-locked이므로 GDJ-0024 completion 집계는
product `12 adapter sets/127 contracts = 111 passing + 5 deviation + 11 oracle_locked`, relation 1/12로 제한합니다.
Query/load/cache/write/delete/DDL/migration
codec와 PostgreSQL/MySQL/Windows 호환은 이 metadata-only packet의 목표가 아닙니다. Final GDJ-0023
baseline EVID-034는 exact 22/22를 통과했지만 GDJ-0024 activation/implementation 증거로 재사용하지 않습니다.
GDJ-0024 implementation head `05e6e218db16e17ce13f7b504a01c603041e4a2a`의 exact 26/26 hosted
acceptance는 [EVID-20260810-036](status/TEST_EVIDENCE.md#evid-20260810-036--gdj-0024-github-hosted-exact-26-job-implementation-head-ci)에
별도로 기록합니다.

Completed [GDJ-0025](../work/0025-forward-foreign-key-predicate-product-slice.md)와 Accepted
[ADR-0025](adr/0025-forward-foreign-key-predicate-and-sqlite-inner-join.md)는 reference bytes를 바꾸지 않고
REL-004만 두 번째 actual relation contract로 구현했습니다. Required `author__name`/`author__id` exact
predicate의 결과, construction/evaluation query count, one reusable INNER JOIN과 DB 불변만 비교합니다.
Implementation head `98db55a30ff71a2f2f70722cb569a046208a5403`의 exact 26/26 hosted acceptance는
[EVID-20260810-040](status/TEST_EVIDENCE.md#evid-20260810-040--gdj-0025-github-hosted-exact-26-job-implementation-head-ci)에
기록하며 product aggregate는 `112 passing + 5 deviation + 10 oracle_locked`, relation REL-001/004
2/12입니다. Loader/cache, nullable/reverse/eager, write/delete/DDL/migration, PostgreSQL/MySQL/Windows는
호환 claim이 아닙니다.

Completed [GDJ-0026](../work/0026-forward-foreign-key-object-cache-and-nullability-product-slice.md)과 Accepted
[ADR-0026](adr/0026-forward-foreign-key-object-cache-and-nullability.md)은 REL-003 required lazy cache와 REL-006
nullable access/`isnull`만 하나의 bounded product comparison으로 동결합니다. REL-003은 actual SQLite에서
freshly loaded Post 10의 Author cold/warm query count `[1,0]`, REL-006은 Post 11 Reviewer null access 0과
typed/dynamic isnull result `[11]`/SELECT 1/JOIN 0을 비교합니다. Completion target은
`114 passing + 5 deviation + 8 oracle_locked`, relation REL-001/003/004/006 4/12로 충족됐습니다. Exact
implementation head `5be46141...`의 [run 31370313755](https://github.com/progresshans/godj/actions/runs/31370313755)은
26/26 jobs·326/326 recorded steps를 성공했습니다. Existing oracle/static/SHA와 prior generated product
bytes는 immutable입니다. Reverse/eager/write/delete/DDL/migration, broader target와 non-SQLite 호환은
claim하지 않습니다.

Completed [GDJ-0027](../work/0027-reverse-foreign-key-accessor-and-lookup-product-slice.md)과 Accepted
[ADR-0027](adr/0027-reverse-foreign-key-accessor-and-lookup.md)은 reference bytes를 바꾸지 않고 REL-005만
actual로 전환했습니다. Freshly loaded Author 1 accessor는 ordered Post IDs `[10,11]`, SELECT 1/JOIN 0이고,
typed/dynamic `posts__title=Alpha`는 같은 reverse Plan으로 Author IDs `[1]`, SELECT 1/INNER JOIN 1입니다.
Exact implementation head `7db68415...`의 run `31419940399`가 26/26 hosted gate를 통과해 GDJ-0027 완료 시점
classification은 `115 passing + 5 deviation + 7 oracle_locked`, relation 5/12입니다. REL-012 prefetch와
eager/write/delete/DDL/migration/non-SQLite 호환은 포함하지 않습니다.

Completed [GDJ-0028](../work/0028-reverse-foreign-key-prefetch-product-slice.md)과 Accepted
[ADR-0028](adr/0028-reverse-foreign-key-prefetch.md)은 locked REL-012 하나만 actual로 전환했습니다.
Stable cross-runtime comparison은 ordered `[(1,[10,11]),(2,[]),(3,[12])]`, statement kinds two SELECT,
primary/batch SELECT 각 1, batch predicate column `author_id`, JOIN 0, related-access extra query 0, batch key count 3과
unchanged DB state입니다. Exact sorted args `[1,2,3]` and mutation-free trace는 protocol field가 아니라 successful
actual publication 전 Go compiler/product internal gate이며 oracle/static/protocol shape는 불변입니다. Primary query와
batch query는 분리하고 generated project companion이 returned owner wrapper의 exact `.Posts().All()` cache만
warm합니다. Existing oracle/static/SHA와 prior generated/relation-product bytes는 불변입니다. Activation
baseline [EVID-050](status/TEST_EVIDENCE.md#evid-20260811-050--gdj-0027-terminal-exact-head-ci-and-gdj-0028-activation-baseline)과
activation run `31429245980`은 이 implementation proof로 재사용하지 않았습니다. Exact implementation head
`4858ab88...`의 [run 31432551159](https://github.com/progresshans/godj/actions/runs/31432551159)가 26/26 hosted
gate를 통과해 GDJ-0028 completion classification은 `116 passing + 5 deviation + 6 oracle_locked`, relation
6/12입니다.
Custom `Prefetch`, related filter/order 재소비, eager REL-009..011, write/delete/DDL/migration과 non-SQLite
호환은 이 packet 밖입니다.

Completed [GDJ-0029](../work/0029-one-hop-forward-select-related-product-slice.md)과 Accepted
[ADR-0029](adr/0029-one-hop-forward-select-related.md)는 REL-009/010/011을 함께 여는 bounded engine 경계입니다.
REL-009는 plain/eager 동일 result와 SELECT 4→1, INNER JOIN 1, access-extra 3→0을, REL-010은 middle NULL을
포함한 result와 SELECT 1/LEFT OUTER JOIN 1/access-extra 0을, REL-011은 reverse multi-valued path의
`field_error/invalid_related_path`/I/O 0을 요구합니다. Typed와 dynamic positive path 및 reverse rejection은
하나의 project resolver와 immutable `RelationProjection`으로 수렴해야 하며, joined source/target은 additive
projection scanners로 한 번에 decode됩니다. Oracle/static/SHA/protocol bytes, Django scenario behavior와 existing
generated files는 frozen입니다. EVID-054/run `31436881856`은 exact clean baseline only이며 target
`119 passing + 5 deviation + 3 oracle_locked`, relation 9/12의 호환 증거로 재사용하지 않았습니다. Exact
implementation head `c02aab67...`의 EVID-056/run `31470292759`가 26/26·326/326 hosted gate와
four-coordinate 630/630/0 inventory를 통과해 그 implementation head의 aggregate가 product가 됐습니다. Multiple/nested/reverse eager,
write/delete/DDL/migration/non-SQLite 호환은 claim하지 않습니다.

장기 relation UX는 Django 6.1의 의미를 reference로 삼습니다: raw FK와 relation accessor 분리, 같은 model
instance/accessor의 lazy cache와 eager/prefetch warming, reverse manager, origin DB affinity입니다. GoDj는 이를
Python descriptor나 runtime registry가 아니라 explicit `context.Context`/`error`, project codegen과 exact
backend/session binding으로 번역합니다. Unified `project.Using(backend)` bounded forward facade는 GDJ-0032에서
implemented됐지만 FK assignment/save/cache invalidation, reverse chaining과 broader write names는 Q-013/Q-017의
후속이며 아직 passing contract가 아닙니다.

Completed [GDJ-0030](../work/0030-project-bound-protect-and-set-null-delete.md)과 Accepted
[ADR-0030](adr/0030-project-bound-protect-and-set-null-delete.md)은 locked REL-007/008을 하나의 SQLite low-level
delete packet으로 구현·검증했습니다. REL-007은 모든 protected source row를 distinct identity+PK로 보고
`integrity_error/protected_foreign_key`, `ProtectedSourceRows()==2`, UPDATE/DELETE 0과 unchanged DB를 요구합니다.
같은 source row가 두 PROTECT edge에서 보이면 global count 1, 서로 다른 source model의 같은 numeric PK는 count
2입니다. REL-008은 source
두 행 SET_NULL UPDATE 뒤 target exact-one DELETE, transaction 1과 committed DB state를 요구합니다. Fixture
constraint는 `NO ACTION`/`RESTRICT`이고 DB-level SET NULL은 false green입니다. GoDj의 pinned
`PRAGMA foreign_keys=1` + `BEGIN IMMEDIATE`는 모든 owned writer connection이 FK-on이라는 precondition 아래의
intentional Go/SQLite safety mechanism이며 Django SQL 문자열 호환 주장이 아닙니다. 모든 declared incoming
edge에는 metadata-matching physical `NO ACTION`/`RESTRICT` SQLite FK가 필요하고 fixture `PRAGMA foreign_key_list`가
이를 증명합니다. Missing/mismatched constraint와 FK-off/out-of-band writer는 unsupported이고 `Open` 또는 relation
DDL이 이를 자동 보장한다고 주장하지 않습니다. Successful deleter return
1만 adapter가 oracle `deleted_total`/`target_deleted` 양쪽에 매핑합니다. `Delete`가 반환하는 모든 error는 0이고 COMMIT error는
stable `backend_error/commit_outcome_unknown`, durability unknown/unchanged pointer/internal automatic retry 0입니다.
`transaction_outcome_unknown` 또는 `commit_outcome_unknown`인 경우 caller는 external reconciliation 전 명시적으로
재호출해서는 안 되지만, 이 packet은 그 의무를 runtime에서 탐지하거나 거부하는 poison token/fence/registry를 제공하지 않습니다.
Pre-COMMIT error는 canceled callback context와 독립된 rollback 또는 forced connection discard로 raw transaction
pool reuse를 막습니다. Confirmed rollback/discard는 unknown code를 쓰지 않고, relation session이 모든 mutator 호출
직전에 표시한 mutation-possible 뒤 두 confirmation이 모두 실패한 경우만 stable
`backend_error/transaction_outcome_unknown`으로 external reconciliation 필요성을
표시합니다. Raw BEGIN error는 callback/retry 없이 force-discard하며 이 code를 쓰지 않습니다. SET_NULL
affected count는 0 이상만 허용하고 fixture는 exact 2입니다. Implementation head `c3803acb...`의
EVID-061/run `31510689383`이 exact 26/26·326/326 hosted gate와 four-coordinate 687/687/0 inventory를 통과했습니다.
GDJ-0030 completion manifest는 REL-007/008 status-only transition 뒤 10,776 bytes/SHA-256
`3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`, exact thirteen-file generated union은
SHA-256 `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`였습니다. 그 bounded delete product는 exact
`121 passing + 5 deviation + 1 oracle_locked`, relation 11/12이고 REL-002와 broader delete/facade/backend
호환은 locked/open입니다.

Completed [GDJ-0033](../work/0033-forward-foreign-key-assignment-save-and-cache-ownership.md)과 Accepted
[ADR-0033](adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md)은 remaining REL-002 하나의
assignment/save/cache ownership을 Django-first로 고정하고 bounded SQLite/AutoField product로 구현·검증했습니다.
다음은 Python 구현을 복제하는 선택지가 아니라 observable compatibility gate입니다.

- Relation object assignment는 source raw FK를 target key와 맞추고 같은 accessor cache를 exact assigned object로
  warm합니다.
- Raw FK의 전체 `(presence,value)` tuple이 달라지면 이전 relation cache를 지우고, 같은 tuple을 다시 설정하면
  cache를 보존합니다.
- Assigned target에 primary key가 없으면 nullable 여부와 무관하게 source save가 database mutation 전에
  `model_state_error/unsaved_related_object`로 실패해야 합니다.
- Primary key가 수동으로 있지만 target row가 없는 경우 preflight를 통과하고 physical SQLite FK constraint가
  결과를 결정해야 합니다.
- Assignment 당시 no-PK였던 same target object가 저장돼 key를 얻고 source scalar가 계속 empty이면 source save
  직전에 key를 다시 읽어 FK를 reconcile합니다. Caller가 scalar를 바꾸면 그 선택이 이깁니다. Assignment 당시
  key-present였던 target key가 뒤에 달라지면 old source scalar를 유지하고 stale target cache를 지웁니다.
- Nullable clear는 raw FK NULL과 cached absent를 함께 설정합니다. Required relation의 nil-like value는 memory에서
  표현되더라도 database constraint 성공을 뜻하지 않습니다.
- Transaction rollback은 target/source Go wrapper memory를 자동 rewind하지 않습니다.

Go translation은 original source를 유지하는 fresh derived wrapper, exact assigned target pointer의 local ownership,
target wrapper in-place Save, pending-only one-time key reconciliation, corrected canonical three-phase preflight와 relation별 COW cache로
Accepted했습니다. Public surface는 query-root `New`, wrapper `Save`, `WithAuthor`/`WithReviewer`, scalar helpers와
`ClearReviewer`입니다. Scalar presence/cache/pending을 분리합니다. Required raw zero는 `New`에서 unset,
`WithAuthorID(0)`와 loaded zero는 present이고 numeric ID로 savedness를 추론하지 않습니다. Pending target 오류는
required-unset보다 먼저 canonical normalized source-model identity + relation field name order로 반환하며 candidate
state가 모두 준비되기 전에는 publish하지
않습니다. 이는
별도 materialization 사이 target pointer identity/global identity map을 주장하지 않습니다.

Activation EVID-072/run `31566524953`, no-product EVID-073와 decision EVID-074/run `31574653183`은 각각
activation/feasibility/decision만 증명합니다. Exact implementation head `be6f3d4e0838929fe96ec156ec0647845d905ea6`은
EVID-076/run `31586910749`의 별도 exact 26/26 jobs·326/326 steps를 통과했습니다. Current bounded product는 exact
`122 passing + 5 deviation + 0 oracle_locked`, relation 12/12이고 REL-002는 `passing`입니다. Q-013은 broader
relation/backend 범위 때문에 `Partial`, Q-017은 raw-model UX/capability/namespace/reverse/general upgrade 때문에
P1/open입니다. Typed generated `select_related` cause-loss P2, relation-capable migration, reverse/general facade와
non-SQLite backend는 이 verified 범위에 포함하지 않습니다.

Completed GDJ-0034는 위 GDJ-0033 verified 범위를 소급 확대하지 않고 typed generated `select_related`의 좁은
진단 P2를 별도 exact head에서 수정·검증했습니다. Resolve 및 required/nullable bind failure는 기존 context
precedence 뒤 동일한 structured error identity와 category/code/detail/unwrap chain으로 pre-I/O 반환됩니다. Dynamic
path와 실제 zero/corrupt query의 기존 오류 의미, 정상 eager SQL/result/cache/query-count는 그대로입니다. Exact
implementation head `3099bd62...`의 EVID-081/run `31605477297`은 unique 26/26 jobs·326/326 steps를 통과했지만,
Django oracle, relation manifest/status, static fixture와 checksum은 바꾸지 않았으므로 REL-009/010/011과 aggregate
product 분류도 그대로입니다.

GDJ-0021 implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`은 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)의 기존 full/exact 2개,
project-check 4개, actual SQLite 4개인 exact 10 hosted execution을 모두 통과했습니다. 이는
MIG-065..074 reference-only/test-only proof와 현재 SQLite 제품 범위의 검증이며, PostgreSQL/MySQL
지원 증거가 아닙니다. 해당 backend를 추가할 때는 service-only green을 금지하고 digest-pinned
service image, health check, UTC timezone과 C locale 또는 명시적으로 승인된 collation, actual
query/write/transaction/schema/migration/recorder/revision-lifecycle 및 durable restart/persistence
contract를 모두 실행해야 합니다. Expected contract 수와 executed 수가 같고 `skipped=0`,
`continue-on-error` 없음, final clean worktree를 required gate로 둡니다.

GDJ-0022 workflow는 기존 10 hosted execution에 actual product Linux/macOS x64/arm64 4개와 Ubuntu
Python compatibility 4개를 더해 exact 18 required execution으로 확장됐습니다. Local EVID-027의
`make ci`/historical exact oracle와 별도로, fix head
`3dfeff2a881a3313883729943519896798d92afc`의 EVID-028은 exact 18/18 hosted success를 기록합니다.
Initial implementation run은 네 Python pre-test uv assertion 실패 뒤 취소됐으며 Python/Django 자체의
실패로 분류하지 않습니다.

제품 commit `6172d843a4bb234592cafc176a8d1191933b141c`은 Draft PR #1의
[run 31309152526](https://github.com/progresshans/godj/actions/runs/31309152526)에서 Ubuntu 24.04와
macOS 15 arm64 job이 모두 통과했고, Ubuntu는 focused test를 실제 Linux/386 runtime에서
실행했습니다. 이 증거는 explicit-source SQLite product slice에 한정됩니다. File/directory/
module/remote discovery, CLI, writer/upgrade/cache, executable/custom/data/raw-SQL operation,
existing database adoption/repair, crash reconciliation과 non-SQLite backend는 계속 미지원입니다.

## GDJ-0040 Phase A composable Boolean predicate reference boundary

QRY-034..043은 pinned Django 6.1/SQLite exact profile에서 scalar exact/`icontains` OR, grouped OR+AND,
non-null/nullable NOT, canonical implicit AND, nested connector/source reuse, distinct/stable slicing, projection 밖
predicate field와 filtered Count/Max를 관찰합니다. Manifest의 10개 contract는 모두 `oracle_locked`, oracle은
`observed`, ordered static fixture는 10개 `not_implemented`입니다. 이 reference-only 상태는 GoDj query tree,
SQLite/PostgreSQL compiler나 Article search의 구현/지원 증거가 아닙니다.

Artifact lock은 manifest 8,135 bytes/
`8ed9ef62b568a2bf4843e3136574c3d73d5571ddd4fe7f1efad0493c7300e895`, oracle 41,264 bytes/
`8b087a394b52620b84d510d6981e77171179ac3690fda738261bf64bea00583e`, ordered NI fixture 1,715 bytes/
`0df907357fcab944272eb45158189e68520e3567678c57995e05c5a0feccbffb`입니다. Exact reference aggregate는
15 sets/161 unique contracts+scenarios/210 ordered cross-bindings의
`134 passing + 5 deviation + 22 oracle_locked`입니다. Product aggregate는 query-expression adapter를 추가하지
않아 13 adapters/139 contracts의 `134 passing + 5 deviation + 0 oracle_locked`로 유지됩니다.

Focused scenario 7/7, exact focused runner 60/60, portable focused runner 60 tests/16 expected skips, 전체 portable
Python 236 tests/21 expected skips가 통과했습니다. Exact semantic registry는 161 scenarios/702,415 bytes/
SHA-256 `aa0d321264e0ad9eed1818d1530a51d18592c16d509c51417e4bdf598655b10e`입니다. Reference drift와
oracle/static `contractcheck`도 통과했지만 QRY-034..043을 `passing`으로 올리려면 Phase B/C의 independent GoDj
actual과 comparator evidence가 별도로 필요합니다.

## GDJ-0040 Phase B/C current Boolean predicate product boundary

Product source `86d6b169...`와 actual conformance `0ec6f385...`는 Phase A oracle/static bytes를 입력으로 읽지
않는 GoDj handler를 연결하고 QRY-034..043을 10/10 `passing`으로 전환했습니다. Manifest는 status 10개만
바뀐 8,075 bytes/SHA-256 `e4160851da2e0820dc4f9f2e8c9e9c2d4d372cde426622b4fea5def51739ea69`입니다.
Oracle 41,264 bytes/`8b087a39...`와 ordered not-implemented fixture 1,715 bytes/`0df90735...`, shared
15-line checksum catalog는 불변입니다.

두 독립 actual은 각각 41,134 bytes/SHA-256
`20b5cf0a332d9d85394a2021fc0b1e8839f9e57994b9c278a7f8bcce8e5f918a`로 byte-identical하고 locked
Django result/error/DB-state/metrics와 protocol difference 0입니다. Raw JSON field order와 byte equality는
비교 계약이 아닙니다. Current reference aggregate는 15/161/210=
`144 passing + 5 deviation + 12 oracle_locked`, product는 14 adapters/149 contracts=
`144 passing + 5 deviation`입니다.

Go-native 경계는 별도로 다음을 강제합니다: model-safe typed connector compile, one authoritative immutable where
tree, depth 64/node 1,024, deterministic DFS arguments/placeholders, nullable odd-NOT guard, root-conjunctive relation
leaf만 허용, malformed/cross-source/over-limit pre-I/O failure, SQLite/PostgreSQL Article exactly-two-query flow입니다.
[EVID-112](status/TEST_EVIDENCE.md#evid-20260823-112--gdj-0040-boolean-predicate-and-article-search-phase-bc-local-checkpoint)는
affected/local PostgreSQL/audit checkpoint이고
[EVID-113](status/TEST_EVIDENCE.md#evid-20260823-113--gdj-0040-frozen-source-final-local-gates)은
full/386/775-file source-clean-copy를 통과했습니다. Corrected submitted head `136e825...`의
[EVID-115](status/TEST_EVIDENCE.md#evid-20260823-115--gdj-0040-corrected-exact-head-hosted-completion) /
run `32642341459`은 exact 27/27 jobs·341/341 steps와 QRY-034..043 actual 10/10을 통과해 이 bounded
product boundary를 hosted-verified로 닫았습니다.

## GDJ-0041 typed comparison and field-reference boundary

GDJ-0041 Phase A는 QRY-044..053의 Integer ordering boundary, range composition/reuse, same-row `F` comparison,
nullable negation, projection과 Count/Max observable을 같은 query-expression set에 추가했습니다. 그 Phase A
manifest는 16,652 bytes/SHA-256
`90adeee098285a3b6581a3d0029c22ee115351f21483f4d704101813bbe940e3`이고 신규 10개는
`oracle_locked`였습니다. Oracle과 ordered NI fixture는 각각 87,852/2,465 bytes와
`4efa5c26f5f17c77e7ef65a0bbdb00cff72835c9a98642726bd61f5524e1ec6f`/
`7ab556ff1f6b77f5e1d4614d6d752cabd6f3428572558d39007e9cd15972f6c2`입니다. 당시 reference는
15/171/210=`144 passing + 5 deviation + 22 oracle_locked`, product는 actual 등록 전
14/159=`144 passing + 5 deviation + 10 oracle_locked`였습니다. 이 수치는 Phase A 역사 경계입니다.

Current source는 Accepted [ADR-0041](adr/0041-typed-scalar-comparisons-and-field-references.md)에 따라
Integer/String literal range와 sealed same-model/same-kind `orm.F`를 private literal/list/field RHS union에
구현합니다. Source inventory/kind/malformed union은 pre-I/O 검증하고, SQLite/PostgreSQL field RHS는 quote된
identifier라 parameter를 소비하지 않습니다. Nullable LHS/RHS complement guard는 odd `NOT`에만 적용되며 같은
field guard는 중복하지 않습니다. Bounded Article `min_id`/`max_id`/`title_matches_summary` flow는 invalid 입력
DB I/O 0과 성공 projection+aggregate 두 query를 유지합니다.

Current manifest는 16,592 bytes/
`a32365e72bff2f96d576dc2a6322c703c6f0cf7c277776f6b326eda47cf9de17`이고 oracle/NI fixture는 Phase A bytes를
유지합니다. 두 독립 oracle-blind actual은 87,592 bytes/
`c8762a8a728440e8b7c42c705aad9635f902100041c0171cdb121880b3813a7c`로 byte-identical하며
QRY-034..053 20/20, 신규 QRY-044..053 10/10 zero-diff입니다. Current reference는
15/171/210=`154 passing + 5 deviation + 12 oracle_locked`, product는
14/159=`154 passing + 5 deviation + 0 oracle_locked`입니다. Frozen source `7f2bb223...`의 local-final gates와
submitted head `e97a4e3...`의 [EVID-118](status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion) /
run `32647746430` exact 27/27 jobs·341/341 steps, 네 플랫폼 968/968/0과 PostgreSQL 17.10 actual이 통과했습니다.
QRY-044..053은 hosted `Verified`, GDJ-0041은 completed입니다.

## 데이터 호환성

다음은 장기 대상이며 구현 전에는 지원을 주장하지 않습니다.

- Django 기본 table naming과 primary key 관례
- ForeignKey column과 ManyToMany join table
- auth, permission, content types 데이터
- Django password hash 문자열
- 기존 Django DB introspection과 import/export
- timezone, collation, decimal, UUID, bytes 직렬화 의미

데이터 호환은 destructive fixture가 아닌 disposable database와 migration round-trip으로 검증합니다.

## 의도적 차이

다음과 같은 차이는 합리적일 수 있습니다.

- Python exception hierarchy를 Go error category와 `errors.Is/As`로 표현
- Python의 동적 속성 대신 generated typed field와 explicit metadata API 사용
- WSGI/ASGI 또는 sync/async API 쌍 대신 Go request/goroutine/context 모델 사용
- Django Admin의 흐름은 보존하되 DOM/CSS를 GoDj 자체 UI로 제공
- backend별 unsupported feature를 명시적 error로 반환

차이는 “Go니까 다르게 했다”로 끝내지 않습니다. 관찰 가능한 영향, migration 비용, 관련 계약, 대안을 [DEVIATIONS.md](DEVIATIONS.md)에 기록하고 필요한 ADR을 연결합니다. Mismatch가 발생했다는 사실만으로 deviation이 승인되지는 않습니다.

## Oracle 변경 정책

- reference version, DB, timezone, locale 변경은 별도 diff로 검토합니다.
- Django oracle 출력은 같은 환경에서 반복 생성 시 byte 단위로 같아야 합니다.
- oracle 변화는 자동 승인하지 않습니다.
- mismatch는 `GoDj bug`, `scenario bug`, `intentional deviation`, `Django bug`, `environment drift` 중 하나로 분류합니다.
- 정렬로 차이를 숨기지 않습니다. 순서가 계약이면 원래 순서를 비교합니다.

## 출처와 라이선스

Django 문서와 테스트에서 파생한 시나리오는 upstream version, 파일 경로, 원래 테스트 이름을 기록합니다. Django의 BSD 3-Clause 조건에 따라 복사·번역한 코드에는 필요한 저작권 고지와 라이선스 정보를 보존합니다. 동작을 독립적으로 기술한 계약과 upstream 코드를 번역한 파생물을 구분합니다.

공식 기준 링크와 로컬 검증 정보는 [SOURCES.md](SOURCES.md), 구체적인 파생물 정책은
[LICENSING.md](LICENSING.md)에 있습니다.

## Historical MIG-075..086 decision compatibility boundary

이 절은 GDJ-0036 이전 GDJ-0035 Phase A~D의 artifact와 evidence를 보존합니다. 아래 dual profile/digest/state,
context handoff와 optional relation backend 설명은 [ADR-0035](adr/0035-pre-release-current-only-format-and-generated-publication.md)에
의해 현재 제품 계약에서 대체됐으며 MIG-075..086 publication은 retire됐습니다.

[GDJ-0035](../work/0035-relation-capable-migration-definition-state-and-sqlite-lifecycle.md)는 MIG-075..086 exact
12 reference-only contract ID를 고정했습니다. Exact 16-document activation head `52f9bcb7...`는
[EVID-084](status/TEST_EVIDENCE.md#evid-20260812-084--gdj-0035-activation-documentation-head-exact-26-job-ci)에서
hosted-verified됐습니다. Phase A는
[EVID-085](status/TEST_EVIDENCE.md#evid-20260813-085--gdj-0035-phase-a-reference-only-artifacts-and-local-validation)에서
12-contract reference-only set을 로컬 고정했습니다. Reference는 exact 13 sets/139 unique contracts+scenarios/
156 ordered cross-bindings=`122 passing + 5 deviation + 12 oracle_locked`입니다. Product는 exact
12/127=`122 passing + 5 deviation + 0 oracle_locked`, relation 12/12로 불변입니다. Phase A exact-head
hosted proof는 EVID-086/run `31625898551`에서 통과했습니다. Phase B test-only feasibility는 EVID-087/088,
Phase C exact 8-test-only decision proof head `7d36502...`는
[EVID-089](status/TEST_EVIDENCE.md#evid-20260819-089--gdj-0035-phase-c-test-only-decision-proof-local-validation)와
[EVID-090](status/TEST_EVIDENCE.md#evid-20260819-090--gdj-0035-phase-c-test-only-decision-proof-exact-head-hosted-ci) /
[run 32174259324](https://github.com/progresshans/godj/actions/runs/32174259324)에서 local/hosted 검증됐습니다.
Proposed decision-freeze docs head `5bdf013...`도 EVID-091/run `32183309328`의 별도 local/hosted proof를
통과했고 그 성공을 근거로 ADR-0034 bounded design이 Accepted됐습니다. Product status는 그
acceptance boundary에서 바뀌지 않았습니다. Later Phase D1/D2/D3a bounded product slices는
[EVID-093](status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification)에서
각각 Implemented/Verified됐고 D3b loaded relation core integration은
[EVID-094](status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification)에서
별도 Implemented/Verified됐습니다. D4 test-only head `424ec4d...`는
[EVID-095](status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification)에서
기존 product path의 bounded file-backed restart observation만 Verified했습니다. 이 bounded 증거들은
MIG-075..086을 `passing`으로 전환하지 않습니다. EVID-096 exact-six documentation head `62df9b2...`는
run `32260744096`에서 고유하게 닫혔고, D4d final head `dd83362...`는
[EVID-097](status/TEST_EVIDENCE.md#evid-20260820-097--gdj-0035-d4d-bounded-nullable-foreignkey-add-local-and-hosted-verification) /
run `32271361724`에서 bounded nullable ForeignKey Add를 Implemented/Verified했습니다. Reference artifact와
MIG status는 바뀌지 않았습니다. EVID-097 docs head `c59669c...`는 run `32278555810`에서 닫혔고 D4e final head
`1d86f6e...`는 [EVID-098](status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification) /
run `32282269755`에서 bounded required-empty Add를 Implemented/Verified했습니다. Reference artifact와 MIG
status는 계속 불변입니다. EVID-098 docs head `85f9270...`는 CI #94/run `32288383027`에서 닫혔고 D4f final
head `9d5b894...`는 [EVID-099](status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification) /
CI #95/run `32294983953`에서 bounded ForeignKey reverse/remove table remake를 Implemented/Verified했습니다.
Reference artifact와 MIG status는 계속 불변입니다.

Compatibility decision은 [Accepted ADR-0034](adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)에
다음과 같이 분리합니다.

| ID | 당시 Accepted decision/reference observation | 당시 분류 |
|---|---|---|
| MIG-075 | Legacy tuple `(1,1,1,2)`, digest v1, scalar state v1과 lifecycle ABI 보존 | `oracle_locked` / Accepted-decision reference |
| MIG-076 | Additive public relation constants `(loader ABI, codec, IR)=(2,2,3)`, one `Load`, per-document dispatch와 hybrid rejection | `oracle_locked` / Accepted-decision reference |
| MIG-077 | Per-document profile을 포함한 relation-only/mixed digest domain v2; legacy-only v1 byte preservation | `oracle_locked` / Accepted-decision reference |
| MIG-078 | `RelationStateFormatVersion=2`, whole-step scalar v1↔relation v2 promote/demote; helpers unexported | `oracle_locked` / Accepted-decision reference |
| MIG-079 | Wire `target_field` 없이 historical exact AutoField derivation과 static/history-plan/physical three-stage preflight | `oracle_locked` / Accepted-decision reference |
| MIG-080 | Relation CreateModel apply/unapply/reapply | `oracle_locked` / Django observed |
| MIG-081 | Populated nullable AddField, empty required support와 populated required rejection | `oracle_locked` / observed + Accepted-decision separation |
| MIG-082 | FK remove/remake row/sequence preservation | `oracle_locked` / Django observed |
| MIG-083 | Exact pinned connection FK-on, physical `NO ACTION`와 existing revision-fence reuse | `oracle_locked` / observed + Accepted-decision separation |
| MIG-084 | File-backed restart observation; bounded epoch/fingerprint/DAG/`StateReconstructor` product-path scenario Verified, actual MIG adapter/general restart blocked | `oracle_locked` / Django observed |
| MIG-085 | Three-stage preflight, one existing fenced transaction, rollback/cause policy | `oracle_locked` / observed + Accepted-decision separation |
| MIG-086 | Commit three outcomes, no retry | `oracle_locked` / Accepted-decision reference |

Pinned Django 6.1/SQLite external observation과 GoDj-owned decision/reference provenance는 계속 구분합니다.
Physical `NO ACTION`, mixed digest/state format처럼 Django가 정의하지 않는 GoDj-owned payload는 later
acceptance 뒤에도 historical artifact의 `kind=proposal`, decision ID `GDJ-0035`, `derived=false`를
소급 변경하지 않고 Django exact parity로 표현하지 않습니다. Django BSD source/test provenance는 실제 관찰한
부분에만 붙이며 source, fixture, comment 또는 assertion 구조를 복사·번역하지 않습니다. Q-010/Q-012/Q-013은
`Partial`, Q-017/Q-019는 P1/open을 유지합니다.

Artifact lock은 manifest 7,792 bytes/`dfe021c22931de3383b44068cf5f6e0ecbc86aa5f8ed96cb017c60171dcb569b`,
oracle 125,248/`c742f91abee12708ef635c540578c6757470e34270e6594ad8a618f9b1afde27`, ordered NI
1,846/`f9bd9c47b5ab3f91e3bb2b0ca5bf4fc88c1d612caf8d6051236af6738eef9e24`, 13-line checksum
1,245/`5022a23094702463861f32270f373ba1287b609e5b3f8cb5723b74db8d69cf4f`입니다. MIG-085의
Django 관찰은 recorder fault 전 schema-editor DDL이 이미 commit되어 schema는 남고 record는 없는 경계입니다.
Pre-DDL fault만 완전 rollback됐습니다. 이 관찰을 GoDj same-transaction atomic proposal의 parity로 표현하지
않습니다. Accepted product design은 `migrations/backend`의 additive `RelationRevisionFencedBackend`/
`RelationRevisionFencedSession`, exact four capabilities와 existing `RevisionFencedTransaction` 하나입니다.
Relation support는 implemented D3b에서도 `definition.Load`/`Set.Migrate`/`Executor.Migrate` normal path만 소유하며 direct
legacy Apply/Unapply/ExecutePlan은 relation-bearing input을 capability error로 거부합니다. Loader profile/
provenance/digest authority는 implemented `migrations/internal/definitionhandoff.Handoff`에 raw bytes/alias 없이 담고,
relation/mixed `Set.Migrate`만 fresh context carrier로 existing Executor에 넘깁니다. Executor는 context precedence
뒤, backend/session/I/O 전에 visible definition clone과 exact per-definition/full-graph seals를 검증합니다.
Existing public signature/entrypoint modification은 0개이고 legacy/empty set과 raw legacy `Executor.Migrate`, public
`NewStateReconstructor` scalar behavior는 보존합니다. Carrier 없는 raw relation Migrate/Definitions copy/direct
legacy execution은 pre-Begin `CategoryCapability`/`CodeUnsupported` feature `relation_migration`, public
reconstructor raw relation은 existing `CategoryState`/`CodeInvalidState`입니다. Internal exported identifiers는
consumer API가 아닙니다. D1 internal handoff, D2 private relation state/reconstructor/readiness와 D3a direct
optional SQLite Create/Delete port는 EVID-093의 각 product/correction head에서 구현·검증됐습니다. D3b는
static authority/readiness 뒤 exact-one fenced history로 actual Planner를 실행하고 whole actual plan을
dry-validate한 뒤에만 relation capability를 every begin/mutation 전에 검증하도록 구현했습니다. Scalar/no-op
plan은 relation call 0이고 unsupported mixed plan은 scalar prefix를 commit하지 않습니다. Normal loaded
relation-bearing CreateModel은 SQLite에서 apply/unapply/reapply합니다. D4d/D4e는 no-default, non-PK
ForeignKey append를 지원하고 D4f는 그 exact appended relation의 bounded backward/unapply를 지원하되 public
changed-target 하나가 source의 모든 기존
ForeignKey와 exact same symbolic target을 나타내고 sealed target model이 relation-free인 경우로 닫았습니다.
Source model당 migration step의 nullable/required relation mutation을 합쳐 하나입니다. Nullable Add는 empty/populated
source를 허용하고 required Add는 `PROTECT`와 empty source만 허용합니다. Existing source emptiness는 exact pinned
connection의 `BEGIN IMMEDIATE` 뒤 physical preflight에서 revision claim 전에 확인하며 same-intent created source는
statically empty입니다. Loaded core authority/resource closure는
pre-capability/pre-Begin이고 missing capability는 selection 중 pre-Begin 실패합니다. SQLite independent static
seal은 remaining invalid/direct shapes를 새 pinned relation connection과 SQL `BEGIN` 전에 거부합니다. Physical
target-outgoing cycle/pre-existing drift는 `BEGIN IMMEDIATE` 뒤 physical preflight에서 검사하지만 revision
claim/mutation 전 rollback합니다. D4f Remove는 exact unique field-order/prefix candidate, deterministic temporary
name, retained-column PK-order copy, affected/stored row counts, exact `sqlite_sequence`, final canonical/FK와
`foreign_key_check`를 같은 fenced transaction에서 검증합니다. Remake source를 참조하는 inbound FK,
remake source 위 non-PK user index, touched/control table을 소유하거나 참조하는 trigger/view, relevant
declared table의 generated/hidden column 또는 unsupported option, relevant sequence의 invalid/case-variant/
noninteger/negative form, namespace/deterministic-temp/control collision은 claim 전에 fail-closed합니다. 이
범위 밖 unrelated harmless object는 허용합니다. Capability tuple은 `{true,true,true,true}`입니다. D4 `424ec4d...`는 file
close/reopen마다 backend와 loaded set을 새로 만들고 full/branch/full history 및
revision-fingerprint ABA, canonical schema/rows와 physical FK snapshot을 비교했습니다. Raw SQLite file bytes,
general restart와 actual adapter는 검증하지 않았습니다. Populated required Add/reapply와 arbitrary/general
remake는 unsupported입니다.
EVID-091/092는 각 docs head만,
EVID-093은 D1/D2/D3a bounded slices만, EVID-094는 D3b product/correction head만, EVID-095는 D4 exact
test-only verification head만 증명합니다. EVID-097/EVID-098/EVID-099는 각각 final D4d/D4e/D4f head까지의
bounded product evidence이며 MIG-081/MIG-082 status 전환이나 actual adapter 증거가 아닙니다.
