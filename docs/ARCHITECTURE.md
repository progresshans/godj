# GoDj 아키텍처

- 상태: 핵심 방향 Accepted, 세부 API Proposed
- 마지막 검토: 2026-08-10

이 문서는 안정적인 계층과 책임을 정의합니다. 코드 예시가 있더라도 개별 공개 API는 compile prototype, contract test, Accepted ADR 없이 확정된 것이 아닙니다.

## 전체 흐름

```text
사용자 Schema DSL
        ↓ validate / normalize
정규화된 Schema IR
   ├── codegen ──→ generated model / fields / descriptor / codec binding
   ├── migration snapshot / project state
   ├── runtime metadata
   └── documentation / introspection
        ↓
Generic Core: Manager[M], QuerySet[M], Predicate[M], Field[M,V]
        ↓
불변 Query AST / execution plan
        ↓
Backend compiler + schema editor + adapters
        ↓
SQLite / PostgreSQL / MySQL / MariaDB / Oracle
```

## 계층별 책임

| 계층 | 책임 | 하지 않는 일 |
|---|---|---|
| Schema DSL | 사람이 모델 의미를 선언하는 API | DB 실행, 생성 코드 형식 결정 |
| Schema IR | 모든 소비자가 공유하는 정규화·직렬화 가능한 의미 | ORM이나 Admin 구현 import |
| Codegen | 모델별 Go 타입, typed fields, descriptor와 codec 연결 생성 | 런타임 쿼리 해석, DB별 SQL 생성 |
| Generic Core | 모델 공통 동작 재사용, 컴파일 시 타입 보존 | 문자열로 새 struct/field 이름 생성 |
| Runtime Metadata | 동적 lookup, Admin, Historical Model, introspection | 두 번째 schema 원본 역할 |
| Query AST | typed/dynamic API가 공유하는 DB 독립 쿼리 의미 | SQL dialect 문자열 보유 |
| Backend | AST compilation, DDL, introspection, value adaptation, capability | 상위 Admin/API 정책 알기 |
| 상위 모듈 | Form, Admin, API, Realtime, GIS 등 제품 경험 | 하위 계층의 독립성을 역전하기 |

## 단일 원본 규칙

Schema DSL은 입력 형식이고 Schema IR이 정규화된 의미의 단일 원본입니다. Codegen, migration state, runtime metadata, Form/Admin/API schema가 각자 DSL을 해석하거나 별도 모델 정의를 만들면 안 됩니다.

Schema IR은 최소한 다음 성질을 가집니다.

- schema version이 명시됨
- 결정적 serialization과 hash가 가능함
- 선언 순서처럼 의미 있는 순서와 canonical ordering을 구분함
- closure 대신 안정적인 identifier로 default/validator를 참조함
- 현재 모델과 historical model이 같은 의미 구조를 공유할 수 있음
- backward/forward compatibility 정책을 테스트할 수 있음

## Codegen, Generics, Metadata의 역할

```text
Codegen: Post, PostFieldSet, PostDescriptor처럼 모델마다 다른 형태
Generics: Manager[Post], QuerySet[Post]처럼 모든 모델에 공통인 동작
Metadata: author__email__icontains처럼 실행 중 결정되는 경로
```

Go에서는 메서드가 receiver에 없는 새 type parameter를 선언할 수 없습니다. 예를 들어 `QuerySet[M].SelectInto[R]` 형태는 사용할 수 없으므로, 추가 결과 타입이 필요한 연산은 `SelectInto[M, R](...)` 같은 최상위 함수 또는 별도 generic builder로 설계합니다.

M1에서는 `orm`이 `ModelDescriptor[M]` interface를 소유하고 codegen이 상태 없는
concrete descriptor를 생성합니다. `Metadata()`는 독립 복사를 반환하고 `Scan` 반환
타입이 `M`을 보존합니다. Runtime freeze/registry 없이 생성·compile 시점부터 frozen인
경계는 [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md)에
고정했습니다. Relation binding은 completed GDJ-0023과 Accepted ADR-0023에서 제품 변경 전에 별도
검증했습니다. 채택한 불변점은 Go package/type pointer가 아닌 stable symbolic model/field identity,
all-app project binder가 소유하는 target/reverse resolution, generated app package 사이 direct import
금지와 project bridge ownership, typed/dynamic relation path의 같은 immutable AST 수렴입니다. Explicit
IR vNext는 field-union relation arm 방향을 채택하지만 exact format version/tag/encoding과 public
DSL/descriptor/loader/bridge API는 후속 product contract가 고정합니다. Current Schema IR v2와 definition
tuple `(1,1,1,2)`는 relation을 계속 거부합니다.

GDJ-0008의 `godj-codegen-m2-v3`는 `ModelDescriptor[M].CloneModel(M) M` 구현을 생성합니다.
Nullable pointer를 포함한 model별 deep clone으로 QuerySet canonical cache와 caller 값을
격리하고, 기존 `CloneWriteModel`은 같은 clone 구현에 위임합니다. 이 descriptor ABI 변경은
generator version, golden/compile test와 generated drift gate로 함께 고정합니다.

## Query 경로

일반 애플리케이션은 typed predicate를 기본으로 사용하고, HTTP query, Admin, Historical Model은 allowlist와 coercion이 있는 dynamic lookup을 사용합니다. 둘은 같은 AST node와 오류 taxonomy로 수렴해야 합니다.

```text
typed field predicate ─┐
                      ├─→ validated expression ─→ Query AST ─→ backend compiler
dynamic lookup ────────┘
```

QuerySet 체이닝은 기존 plan을 변경하지 않고 새 plan을 만듭니다. M1 dynamic API는
`ParseDynamic`에서 construction 오류를 즉시 반환하고, 성공한 `Predicate[M]`를 typed
`Filter`에 넘깁니다. GDJ-0008부터 immutable plan과 pointer evaluation state를 분리합니다.
Direct Go value copy는 state를 공유하지만 `Filter`/`OrderBy`/성공한 `Limit`와 `Fresh`는
새 state를 받습니다. 성공한 full `All`만 cache하고 같은 state의 동시 `All`은
singleflight하며, 실패/cancellation은 cache하지 않습니다. Owner와 waiter context를
격리하고 generated deep clone을 통해 canonical cache를 caller에게 직접 노출하지
않습니다. Cold `Count`/`Exists`/`At`/`First`는 full cache를 채우지 않고 warm terminal은
cache를 재사용하며, `Iterate`는 cache를 우회·보존합니다. 이 경계는
[ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)에 고정합니다.

## Model과 Migration

생성 모델은 가능한 한 데이터 타입으로 유지하고, row scan, value encode, metadata, state 접근은 생성 descriptor/codec 경계에 둡니다.

Migration은 현재 생성 타입을 과거 migration state에 사용하지 않습니다. GDJ-0004는
Schema IR 기반 `ProjectState`, typed operation, executor와 같은 SQLite transaction의
schema editor/recorder를 [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md)에
따라 검증했습니다. [ADR-0013](adr/0013-immutable-migration-planner.md)은 historical
`ProjectState`와 applied migration history를 분리하고, operation/backend를 보관하지 않는
immutable identity graph가 caller-supplied AppliedState와 target으로 zero-I/O plan을
계산하도록 결정했습니다. GDJ-0010은 이 immutable Planner와 structured graph/history/
target error를 구현하고 MIG-005..016 actual adapter로 검증했습니다. Planning의 logical
state와 zero-I/O metrics는 실제 database probe가 아니라 backend를 import하지 않는 pure
structural 경계에서 산출합니다. [ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md)는
`ExecutePlan`이 전체 plan/state를 backend I/O 전에 검증하고 기존 `Apply`/`Unapply`를
migration별 transaction으로 실행하도록 결정했습니다. 첫 실패는 뒤 step을 시작하지 않고
마지막 durable `ProjectState`를 반환하며, reverse schema와 recorder는 DEV-0001에 따라 같은
transaction에 둡니다. Public migration file 형식, loader/CLI, data callback, locking과 crash
recovery는 GDJ-0012 당시 Q-012의 후속 결정이었습니다.

GDJ-0013은 recorder table absent/empty, durable record/unrecord, fresh applied-prefix plan과
history preflight를 MIG-027..036 reference로 잠갔습니다. GDJ-0014와 Accepted
[ADR-0015](adr/0015-recorder-backed-applied-state.md)는 `AtomicBackend`/`Transaction`과
별도인 backend raw applied-migration read port, core `LoadAppliedState` validation과 explicit
`Planner.CheckHistory`를 구현했습니다. SQLite reader는 recorder table을 직접 SELECT하고
정확한 missing-table만 empty로 정규화하므로 read가 table을 만들지 않습니다. Fresh
file-backed backend를 다시 열어 record/unrecord, database isolation, applied tail과 restart
tail을 검증했고 MIG-027..036은 10개 모두 `passing`입니다.

이 read/check/plan 경계는 `ExecutePlan`과 한 API가 아니며 snapshot과 이후 실행 사이의
lock·revision binding도 제공하지 않습니다. Recorder identity만으로 historical
`ProjectState`를 재구성할 수도 없습니다. 완료된
[GDJ-0015](../work/0015-historical-project-state-reconstruction-compatibility-contracts.md)는
MIG-037..046에서 explicit empty와 omitted latest, target before/after, dependency closure와
durable applied-history projection의 외부 의미를 여덟 번째 exact reference set으로
고정했습니다. Logical state는 loaded migration definition에서 나오고 deliberately divergent
live database는 capture 전후 그대로 남아야 합니다. 이 10개는 `oracle_locked`이며 제품
adapter가 없어 GDJ-0015 완료 시 제품 분류는 `73 passing + 4 deviation`이었습니다.

완료된
[GDJ-0016](../work/0016-historical-project-state-reconstruction-product-slice.md)과 Accepted
[ADR-0016](adr/0016-historical-project-state-reconstruction.md)은 full loaded definition을
deep-copy하는 별도 immutable `StateReconstructor`와 explicit empty/latest/before/after/applied
request를 구현했습니다. Existing Planner graph/order kernel과 operation state transition만
사용하므로 core에는 DB handle, backend/SQLite/SQL import나 I/O가 없습니다. Applied live
adapter는 real SQLite recorder를 read-only로 읽어 `LoadAppliedState`를 거치며 MIG-037..046은
10 `passing`, GDJ-0016 완료 당시 제품 분류는 `83 passing + 4 deviation`입니다. 이 경계는 recorder
identity만으로 definition을 발명하지 않으며 read/reconstruct/plan/execute가 하나의 atomic
lifecycle이라는 뜻도 아닙니다.

완료된 [GDJ-0017](../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md)은
MIG-047..056으로 read/check/reconstruct/plan/execute lifecycle의 fresh/target/failure/restart
외부 의미를 아홉 번째 exact set에 고정했습니다. GDJ-0017 완료 당시 10개는
`oracle_locked`였고 제품 adapter나 public lifecycle API는 없었습니다. Accepted
[ADR-0017](adr/0017-revision-fenced-migration-lifecycle.md)은 제품 승격 시 recorder identities와
opaque freshness revision을 같은 snapshot으로 읽고 각 migration transaction의 첫 DDL/write
전에 expected token을 검증하도록 결정합니다. SQLite feasibility harness는 persistent epoch와
monotonic revision을 후보로 사용했고 fingerprint는 direct non-ABA drift를 잡는 보조 gate로만
검증했지만, 당시 제품 storage와 token encoding은 결정하지 않았습니다. Harness는 per-step commit,
last-durable state, no retry와 unsupported fail-closed를 검증했지만 product package를 변경하지
않았습니다. Cutover 전 non-cooperating ABA, recorder 밖 schema drift와 crash repair는 계속
Q-012 후속입니다.

완료된 [GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md)과 Accepted
[ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md)은 이 조각들을 public
`Executor.Migrate(ctx, definitions, request)`로 조립합니다. Tagged latest/targeted request는
already-loaded definition만 받고, backend-owned session은 recorder identities와 private
freshness token을 정확히 한 atomic snapshot에서 읽은 뒤 call 사이 physical connection을
pin하지 않습니다. Core는 session을 반드시 닫고 unsupported optional port에 legacy execution으로
fallback하지 않습니다.

SQLite session은 persistent 128-bit epoch, monotonic revision과 sorted full-history fingerprint를
opaque token으로 결속합니다. 각 step은 새 pinned connection의 literal `BEGIN IMMEDIATE` 안에서
expected token을 첫 mutation 전에 검사하고 schema, declared recorder transition과 successor
token을 원자적으로 commit합니다. Metadata와 recorder가 모두 absent인 fresh database만
bootstrap하며 existing recorder는 empty여도 adoption-required이고 자동 adoption 경로는 없습니다.
`CommitRolledBack`은 core의 confirmed state와 session token을 advance하지 않으며, SQLite는 실패한
transaction 뒤 session을 poison해 같은 lifecycle에서 retry하지 않습니다. Unknown durability도
poison/no-retry이고 last confirmed pre-step state만 반환합니다.

MIG-047의 A2를 위해 SQLite `AddField`는 table이 empty일 때만 logical default를
`ProjectState`에 보존하면서 physical persistent default 없는 column을 추가합니다. Nonempty
table backfill/rebuild는 계속 explicit unsupported입니다. MIG-047..056의 아홉 product adapter
중 MIG-052만 DEV-0002이며 GDJ-0018 완료 당시 전체 분류는
`92 passing + 5 deviation`이었습니다. File/source loader, version handshake, CLI, public
adoption/repair와 crash reconciliation은 당시 다음 계약 범위였습니다.

완료된 [GDJ-0019](../work/0019-migration-definition-source-compatibility-contracts.md)과 Accepted
[ADR-0019](adr/0019-versioned-migration-definition-source.md)는 explicit caller-provided bytes를
strict data-only JSON v1과 tuple `(1,1,1,2)`로 검증하고, fully normalized Schema IR v2의
closed `CreateModel`/non-PK `char`·`boolean` `AddField` operation만 loader-owned snapshot에
게시하는 contract를 MIG-057..064로 고정했습니다. Canonical definition-set digest와 실패
precedence, duplicate/existing graph validation, all-or-nothing publication을 포함하며 Django
Python file discovery나 실행 의미를 가져오지 않습니다.

이 여덟 contract는 Accepted ADR decision oracle이고, 실제 Django 공통 동작을 관찰하는
MIG-057/MIG-064만 pinned Django provenance를 별도로 가집니다. MIG-064는 public Django
graph/executor와 existing Go `Executor.Migrate` 사이의 reference handoff observation일 뿐입니다.
`conformance/definitionload/**`는 실제 `migrations.NewPlanner`와 `Executor.Migrate`를 호출하는
`*_test.go` feasibility proof로 시작했으며, 제품 package가 이를 import하지 않는 독립 비교
경계로 유지됩니다.

완료된 [GDJ-0020](../work/0020-migration-definition-loader-product-slice.md)과 Accepted
[ADR-0020](adr/0020-migration-definition-loader-product-shape.md)은 import 방향이
`migrations/definition → migrations + schema/ir`인 새 leaf package를 구현했습니다. Caller가 I/O를
끝낸 뒤 `Source{SourceID, Document}`를 넘기며, `Load`는 tuple `(1,1,1,2)`, closed codec,
fully-normalized IR과 graph를 검증한 뒤에만 loader-owned immutable `Set`을 원자적으로
publish합니다. Zero `Set`은 canonical empty set이고 raw document를 보존하지 않으며, accessor와
failure/report graph mapping은 매번 fresh deep copy입니다. `Set.Migrate`는 fresh definition
snapshot과 immutable request value를 existing `Executor.Migrate`에 정확히 한 번 전달합니다.

Pre-construction resource boundary는 source 2,048, SourceID 1,024 bytes, document 1 MiB, batch
16 MiB, JSON depth 64, document JSON values 65,536, aggregate JSON values 262,144, migration별
dependencies 2,047, operations 2,048, `CreateModel` fields 2,048의 exact cap입니다. Strict scanner는
any-depth duplicate, invalid UTF-8/surrogate/numeric lexeme와 canonical RFC 6901 failure order를
bounded lazy path로 처리합니다. Source/document/compatibility/codec/IR failure만 9개
`migration_definition_source_error` code가 소유하고, resource breach는 새 code가 아니라 stable
limit context를 사용합니다. Graph validation은 raw `*migrations.PlanningError`, lifecycle은 기존
raw error를 wrap/reclassify/retry하지 않습니다.

MIG-057..064의 열 번째 actual adapter까지 연결됐을 때 제품 분류는 10 adapter/105 contract의
`100 passing + 5 deviation`이었습니다. 여덟 contract는 Django parity가 아닌 decision-reference
`passing`이며 성공 검증도 `locked reference oracle`로 표현합니다. File/directory/module/remote
discovery, public CLI, writer/upgrade/cache, executable/custom/data/raw-SQL operation, adoption/repair,
crash reconciliation과 non-SQLite migration backend는 이 loader package 자체가 지원하지 않습니다.

완료된 [GDJ-0021](../work/0021-migration-project-check-compatibility-contracts.md)과 Accepted
[ADR-0021](adr/0021-project-linked-migration-check.md)은 이 제품 경계를 바꾸지 않고
`godj.toml` 선택, project-linked build/runner protocol, flat no-follow source discovery와
`definition.Load` exactly-once handoff를 `conformance/projectcheck/**`의 test-only proof로
검증했습니다. Global orchestration proof는 제품 loader를 import하지 않고, linked runner fixture만
기존 `migrations/definition`을 사용합니다. Production package는 이 harness를 import하지 않으며
전역 `godj` CLI, project package와 filesystem discovery를 구현한 것으로 세지 않습니다.

Completed [GDJ-0022](../work/0022-migration-project-check-product-slice.md)와 Accepted
[ADR-0022](adr/0022-project-runtime-and-global-migration-check.md)는 test-only proof와 독립인
`cmd/godj`, public `project`, `internal/projectcheck` global/linked/protocol kernel과 열한 번째 actual
adapter를 구현했습니다. Product code는 conformance proof를 import/read하지 않고 linked kernel만 actual
`definition.Load`를 exactly once 호출합니다. MIG-065..074 exact 10 status는 `passing`이며 제품 분류는
11 adapter/115 contract의 `110 passing + 5 deviation`입니다. Protocol gate는 reference
11 set/115 unique contract/110 ordered cross-binding과 static fixture exit 1/ordered mismatch 10을
계속 고정합니다.

GDJ-0021 implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`은 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)에서 기존
Ubuntu 24.04 x64 full/macOS 15 arm64 exact 두 job, Linux/macOS x64/arm64 project-check 네 leg와
같은 좌표의 actual SQLite 네 leg, 총 `2 + 4 + 4 = 10` hosted execution을 모두 통과했습니다.
GDJ-0022 workflow는 actual product 네 leg와 Python 3.12.13/3.13.15/3.14.3/3.14.7 네 leg를 더한
exact 18 required execution으로 확장됐고 fix head
`3dfeff2a881a3313883729943519896798d92afc`의
[run 31329294154](https://github.com/progresshans/godj/actions/runs/31329294154)에서 18/18 성공했습니다.
Initial head의 네 Python pre-test uv assertion failure/cancel과 fix는 EVID-028에 기록했습니다.
Actual adapter가 없는 PostgreSQL/MySQL은 service만 띄우는 green job을 지원 증거로 세지 않습니다.
첫 backend job은 digest-pinned service image, health check, UTC timezone과 C locale 또는 명시적으로
승인된 collation, actual query/write/transaction/schema/migration/recorder/revision-lifecycle 및
durable restart/persistence contract를 모두 실행해야 합니다. Expected contract 수와 executed 수가
같고 `skipped=0`, `continue-on-error` 없음, final clean worktree도 필수입니다.

## CLI와 프로젝트 실행

전역 `godj` CLI의 첫 좁은 단면인 exact `godj migrations check`와
`godj migrations check --project <descriptor-file>`가 구현됐습니다. 그 밖의 향후 `version`,
`startproject`, `startapp`, broader 프로젝트 탐색과 orchestration은 아직 목표 책임입니다. 프로젝트
설정·앱·모델·사용자 command가 필요한 작업은 프로젝트 코드를 포함한 바이너리에서 실행하는 방향입니다.

```text
godj CLI
  ├─ 독립 명령
  └─ 프로젝트 탐색/빌드/실행
           ↓
프로젝트 바이너리
  ├─ serve
  ├─ migrate
  ├─ createsuperuser
  └─ custom commands
```

`manage.py` 파일은 복제하지 않지만 프로젝트 전용 실행기라는 역할은 보존합니다. `go generate`는 보조 진입점이고 공식 orchestration은 `godj generate`입니다.

## 의존 방향

화살표는 “왼쪽이 오른쪽을 import할 수 있음”을 의미합니다.

```text
schema DSL ─→ schema/ir
codegen ────→ schema/ir
migrations ─→ schema/ir, backend contracts
migrations/definition ─→ migrations, schema/ir
cmd/godj ───→ internal/projectcheck ─→ internal/projectcheck/protocol
project ────→ internal/projectcheck/linked ─→ protocol, migrations/definition
orm ────────→ query, schema/ir metadata, backend contracts
backends ───→ query, schema/ir, backend contracts
forms/auth/templates ─→ metadata와 제한된 ORM interface
admin/api/realtime ───→ 공개 하위 module interface
gis extension ────────→ schema/query/backend의 명시적 extension point
```

금지 예시는 `schema/ir → orm`, `query → admin`, `orm → admin`, `orm → api`, `forms → admin`,
`backend → 상위 제품 모듈`과 `migrations → migrations/definition`입니다. 거대한 범용 `core`
패키지는 만들지 않습니다. 실제 패키지가 생기면 dependency test로 검증하고 interface 소유
패키지를 명시합니다.

## Codegen bootstrap 경계

선언 package와 generated target을 분리하고 generator는 Schema IR만 의미 입력으로
사용합니다. Target 교체 전 candidate를 `gofmt`/parse하고 Go overlay로 실제 target
package를 compile하며, 실패하면 last-good bytes를 보존합니다. 이 결정과 M0
rename/delete/stale fixture는 [ADR-0006](adr/0006-codegen-input-package-boundary.md)에
기록합니다. Exact migration-check private protocol은 구현됐지만 full CLI/project library/generator
version handshake는 Q-010으로 남아 있고, generator runner는 여전히 `internal/cmd/m1generate`입니다.

## 목표 저장소 구조

최종적으로 `cmd/godj`, schema/IR, codegen, query, ORM, backends, migrations, forms, templates, admin, auth, API, realtime, GIS, i18n, contrib, testing/conformance, examples가 필요할 수 있습니다. 구현하지 않은 미래 패키지는 미리 만들지 않습니다.

현재 핵심 미결정 사항은 [OPEN_QUESTIONS.md](OPEN_QUESTIONS.md), 채택된 이유는 [adr/](adr/README.md)에 기록합니다.
