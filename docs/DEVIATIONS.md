# 의도적 호환 차이 원장

- 상태: Active ledger
- 마지막 갱신: 2026-08-31
- 현재 검증된 deviation: DEV-0001..DEV-0010 열 건 / contract 스물다섯 개
- 현재 구현됐으나 terminal 검증 전인 deviation: 없음
- Proposed이며 아직 aggregate에 포함하지 않는 후보: 없음

이 문서는 Django reference contract와 다른 GoDj 동작을 의도적으로 수용한 경우의 정본입니다. 단순 mismatch, 미구현, bug, 환경 drift를 deviation으로 바꾸어 테스트를 녹색으로 만들면 안 됩니다.

## 승인 절차

1. Differential mismatch를 `GoDj bug`, `scenario bug`, `Django bug`, `environment drift`, `deviation candidate`로 먼저 분류합니다.
2. Candidate에는 사용자에게 보이는 차이, 대안, migration 비용, backend 영향, 보안/성능 영향과 contract ID를 기록합니다.
3. public behavior, data, error, transaction 의미를 바꾸면 Proposed ADR을 연결합니다.
4. review와 필요한 사용자 결정을 거쳐 상태를 `Accepted`로 바꿉니다.
5. manifest의 contract 상태를 `deviation`으로 바꾸고 GoDj 고유 expected test를 추가합니다.
6. 대체되면 원래 기록을 지우지 않고 `Superseded`와 새 ID를 연결합니다.

## 상태

- `Proposed`: 차이를 검토 중이며 compatibility pass로 계산하지 않음
- `Accepted`: 의도적인 제품 결정으로 승인됨
- `Implemented`: 현재 checkout에 다른 동작이 구현됨
- `Verified`: deviation 전용 expected test가 기록된 환경에서 통과함
- `Superseded`: 새 deviation 또는 원래 Django 동작으로 대체됨

## 기록 형식

```markdown
## DEV-NNNN — 제목

- Status: Proposed
- Date:
- Contracts:
- Reference profile/backend:
- Related ADR/work/evidence:

### Django의 관찰 가능 동작
### GoDj에서 제안/채택한 동작
### 이유와 고려한 대안
### 사용자·데이터·migration 영향
### backend/concurrency/security 영향
### 구현과 검증 조건
### 복귀 또는 supersede 조건
```

## 원장

## DEV-0001 — 역방향 migration의 schema와 recorder를 같은 transaction으로 처리

- Status: Verified
- Date: 2026-08-08
- Contracts: MIG-018, MIG-020, MIG-022, MIG-024
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj SQLite 3.53.3
- Related ADR/work/evidence:
  [ADR-0014](adr/0014-migration-plan-execution-atomic-reverse.md),
  [GDJ-0011](../work/0011-migration-plan-execution-compatibility-contracts.md),
  [GDJ-0012](../work/0012-migration-plan-execution-orchestrator.md),
  [EVID-20260808-010](status/TEST_EVIDENCE.md#evid-20260808-010--gdj-0011-migration-plan-execution-compatibility-contracts),
  [EVID-20260808-011](status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)

### Django의 관찰 가능 동작

Django 6.1의 backward migration은 schema transaction을 먼저 commit한 뒤 recorder row를
삭제합니다. MIG-018/020/022의 정상 또는 앞선 성공 backward step은 compact metrics에서
`transaction_model=schema_then_record`입니다. MIG-024는 A3 unapply를 완료한 뒤 A2 schema
reverse까지 commit하고, A2 recorder의 실제 write 전에 주입한 fault로 삭제가 실패합니다.
최종 schema는 A1만 남고 recorder rows는 A1/A2이며 phase는 `commit`입니다.

### GoDj에서 채택한 동작

기존 `Executor.Unapply`처럼 reverse schema operation과 recorder 삭제를 같은 transaction에서
처리합니다. Recorder 실패면 실패 migration의 schema와 recorder를 함께 rollback하고 앞선
migration commit만 보존합니다. 정상 backward 결과는 같지만 transaction model은
`schema_and_record`입니다. MIG-024형 실패에서는 A2 schema도 남고 A2 recorder도 남으므로
Django의 DB state와 phase가 달라집니다.

MIG-024의 구체적 product expectation은 phase `rollback`, A3 unapply만 durable commit,
최종 schema/records 모두 A1/A2입니다. A2 step은 `status=rolled_back`,
`schema_outcome=rolled_back`, `recorder_outcome=retained`,
`transaction_model=schema_and_record`, `fault_point=before_record_write`입니다.

### 이유와 고려한 대안

채택 이유는 schema와 applied history가 durable하게 불일치하는 실패 모드를 기본 제품
동작으로 만들지 않고, MIG-001..004에서 이미 검증한 한 migration 원자성을 유지하기
위함입니다. Django 경계를 그대로 구현하는 안, plan 전체를 한 transaction으로 묶는 안과
backend별 선택을 검토합니다. 상세 trade-off는 ADR-0014에 기록합니다.

### 사용자·데이터·migration 영향

정상 reverse 뒤 최종 schema/record는 동일합니다. 다만 recorder 장애가 발생하면 Django
운영자는 schema/history reconciliation이 필요할 수 있지만 GoDj는 실패 migration을 함께
rollback합니다. Django DB와 GoDj recorder를 직접 교환하거나 장애 복구 절차를 공유하는
경우 이 차이를 진단 출력과 문서에서 명시해야 합니다.

### backend/concurrency/security 영향

SQLite의 기존 pinned transaction과 backend interface를 유지할 수 있습니다. 다른 backend의
DDL transaction capability는 아직 검증하지 않았으므로 이 결정이 모든 backend에 자동으로
적용된다고 주장하지 않습니다. Process lock/crash recovery는 별도 Q-012 범위입니다.

### 구현과 검증 조건

- GDJ-0012의 `ExecutePlan`이 기존 same-transaction `Unapply`를 step primitive로 재사용
- MIG-018/020/022의 `transaction_model` 차이와 MIG-024의 DB state/phase 차이를 live
  product observation에서 재현
- Reference oracle/Django phase나 core comparator를 변경하지 않는 별도
  `godj-migration-execution-deviation-expected.json`
- Existing 57 differential, full/race/CGO=0/vet와 cancellation/failure gate 통과
- Manifest는 이 결정이 적용되는 네 계약만 `deviation`으로 분류하고 정확히 하나의
  `decision=DEV-0001`, `derived=false` provenance를 가짐
- Sparse product expectation의 등록되지 않은 selector/status/provenance 변경은 actual을
  쓰기 전에 exit 2로 실패

제품 구현 commit `3bcd25ce557cfddc2d73652f9154b6db0fd0b065`에서 네 계약을
전용 expected와 live SQLite actual로 검증했습니다. GDJ-0012 완료 당시 제품 분류는
`63 passing + 4 deviation`이었으며 67 exact passing으로 표현하지 않았습니다. 이후
recorder-restart, historical-state reconstruction과 lifecycle 제품 set이 추가된 현재 분류는
`92 passing + 5 deviation`이고, DEV-0001 네 계약은 그대로 유지됩니다. 검증 명령과 artifact hash는
[EVID-20260808-011](status/TEST_EVIDENCE.md#evid-20260808-011--gdj-0012-migration-plan-execution-orchestrator-and-atomic-reverse)에
기록하며 현재 aggregate는
[EVID-20260809-017](status/TEST_EVIDENCE.md#evid-20260809-017--gdj-0018-revision-fenced-migration-lifecycle-product-slice)에
기록합니다.

### 복귀 또는 supersede 조건

Django와의 exact backward transaction 호환이 schema/history 원자성보다 우선이라는 근거가
생기거나, backend별 recovery protocol이 partial commit을 안전하게 복구하면 새 ADR로 이
결정을 Rejected/Superseded할 수 있습니다.

## DEV-0002 — App zero의 incomparable sibling은 GoDj canonical order를 유지

- Status: Verified
- Date: 2026-08-09
- Extended: 2026-08-30 (MIG-122 target-plan product publication)
- Contracts: MIG-052, MIG-122
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj SQLite 3.53.3 and bounded
  PostgreSQL 17.10 target-plan actual
- Related ADR/work/evidence:
  [ADR-0013](adr/0013-immutable-migration-planner.md),
  [ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md),
  [GDJ-0017](../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md),
  [GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md),
  [EVID-20260809-017](status/TEST_EVIDENCE.md#evid-20260809-017--gdj-0018-revision-fenced-migration-lifecycle-product-slice),
  [ADR-0054](adr/0054-project-linked-targeted-migration-plan-and-reverse-safety.md),
  [GDJ-0052](../work/0052-project-linked-targeted-migrate-plan-and-bounded-reverse.md),
  [EVID-164](status/TEST_EVIDENCE.md#evid-20260830-164--gdj-0052-phase-d-postgresql-product-publication-and-ownership-hardening),
  [EVID-165](status/TEST_EVIDENCE.md#evid-20260830-165--gdj-0052-first-hosted-diagnostic-ci-isolation-and-frozen-local-final),
  [EVID-166](status/TEST_EVIDENCE.md#evid-20260830-166--gdj-0052-second-hosted-timing-diagnostic-and-corrected-source-refreeze),
  [EVID-167](status/TEST_EVIDENCE.md#evid-20260831-167--gdj-0052-corrected-exact-head-hosted-completion)

### Django의 관찰 가능 동작

MIG-052는 A1/A2/A3과 unrelated B1이 applied인 상태에서 alpha app을 zero target으로
내립니다. Django public orchestration의 plan과 committed step 순서는
`B1←A3←A2←A1`입니다. B1과 A3은 서로 dependency가 없는 incomparable reverse sibling이지만
Django의 private traversal에서는 B1이 먼저 선택됩니다.
MIG-122는 같은 app-zero graph의 public target-plan 결과를 별도 contract로 관찰하며 locked Django plan도
`B1←A3←A2←A1`입니다.

### GoDj에서 채택한 동작

Accepted ADR-0013의 canonical ascending planner policy를 그대로 사용해
`A3←A2←B1←A1`로 실행합니다. MIG-052 deviation scope는 locked Django lifecycle observation을 복사한
product expectation에서 다음 여섯 selector의 value를 replace하는 것으로 제한합니다.

- `result.plan[0]`, `result.plan[1]`, `result.plan[2]`
- `metrics.steps[0]`, `metrics.steps[1]`, `metrics.steps[2]`

MIG-122는 별도 target-plan expectation에서 `result.plan[0]`, `result.plan[1]`, `result.plan[2]` 세 selector만
replace합니다. 두 fixture와 code-owned policy는 manifest contract set으로 분리되며 한 surface의 selector가 다른
surface를 승인하지 않습니다. 그 밖의 `result`, resulting logical state, managed DB schema, recorder history,
phase와 metrics는 이 deviation으로 바꿀 수 없습니다. MIG-052의 phase는 두 구현 모두 `commit`입니다.

### 이유와 고려한 대안

Django 순서를 복제하려면 public 결과보다 private traversal detail을 기준으로 deterministic
planner order를 바꿔야 합니다. 이는 기존 MIG-005..016 planner contract와 다른 app/target의
순서를 넓게 흔들 수 있습니다. B1/A3은 dependency로 순서가 강제되지 않고 final state/DB/phase가
같으므로, 기존 canonical order의 안정성과 재현성을 유지하고 좁은 ordered payload만 deviation으로
승인했습니다.

검토한 대안은 Django traversal을 lifecycle adapter에서 특수 처리하는 안, app-zero에 별도
planner mode를 추가하는 안, plan/metrics order를 comparator에서 무시하는 안입니다. 첫 두 안은
public Planner와 lifecycle plan이 갈라지고, 마지막 안은 실제 ordered contract를 약화해 false
green을 만들므로 채택하지 않았습니다.

### 사용자·데이터·migration 영향

사용자는 app zero의 로그/plan/step 표시에서 incomparable B1과 A3의 순서 차이를 볼 수 있습니다.
두 migration이 dependency로 연결되지 않았다는 전제에서 최종 logical state, DB schema와 recorder
history는 동일합니다. B1 또는 A3에 외부 side effect가 생기는 future data migration은 이
deviation의 현재 범위에 자동 포함되지 않으며 새 contract/결정이 필요합니다.

### backend/concurrency/security 영향

두 순서 모두 migration별 fenced transaction과 revision successor를 사용합니다. Stale,
contention, integrity, rollback/unknown durability와 last-durable state 의미는 달라지지 않습니다.
이 결정은 SQLite/PostgreSQL lock 순서, retry, privilege 또는 security boundary를 완화하지 않습니다.
MIG-122의 PostgreSQL 17.10 actual은 이 bounded target-plan surface만 검증하며 다른 backend나
non-cooperative writer를 승인하지 않습니다.

### 구현과 검증 조건

- Lifecycle manifest에서 MIG-052만 `deviation`, 나머지 MIG-047..051/053..056은 `passing`
- Target-plan manifest에서 MIG-122만 `deviation`, MIG-119..121/123..128은 `passing`
- MIG-052와 MIG-122 provenance는 각각 정확히 하나의 `kind=decision`, `reference=DEV-0002`, `derived=false`
- `godj-migration-lifecycle-deviation-expected.json`은 위 여섯 replace selector만 소유
- `godj-migration-target-plan-deviation-expected.json`은 MIG-122의 세 plan replace selector만 소유
- Code-owned DEV-0002 policy는 exact MIG-047..056/MIG-119..128 manifest set을 구분하고 ambiguous/neither/partial/
  duplicate manifest, selector/status/provenance의 누락·추가·중복과 unknown decision을 actual 생성 전에 fail-closed
- Lifecycle adapter는 public `Executor.Migrate`와 live SQLite DB를 사용하며, target-plan adapter는 public
  `Executor.Plan`/`Executor.Migrate`와 실제 project process ownership 경계를 oracle-blind backend로 관찰함
- 두 adapter 모두 contract ID/oracle/static dispatch를 하지 않으며 별도 external product flow가 실제
  SQLite/PostgreSQL 17.10을 검증
- Target/definition/history/fault propagation, source guard와 semantic mutation gate 통과
- 두 독립 actual이 byte-identical하고 reviewed expectation과 10 contract/0-diff
- Locked lifecycle oracle, not-implemented static fixture, `SHA256SUMS`와
  `conformance/lifecyclefence/**` byte 불변

Machine/conformance commit `fd49d5147beefead640f43ae6fd5c83860a17a06`에서 검증했습니다.
Lifecycle manifest는 13,735 bytes, SHA-256
`5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0`, sparse expectation은
6,769 bytes, SHA-256 `58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b`입니다.
두 actual은 각각 98,304 bytes, SHA-256
`a32e768323dae33a312267d5f8041818570d55f1fd887b29580cf8d4c5b3064b`로 byte-identical했고,
9 exact + DEV-0002 expectation에서 10/0-diff였습니다. Aggregate 제품 분류는
`92 passing + 5 deviation`입니다.

위 값은 MIG-052 lifecycle publication의 역사적 fixture/hash와 당시 aggregate이며 소급 변경하지 않습니다.
GDJ-0052 Phase D source `a92efb5f09eb4dcf3094fddf84a21ff65fa604f3`, tree
`06f90a90eb61de13c234dfc2356b6b4ed085f087`은 별도 target-plan manifest 6,796 bytes/SHA-256
`0636eb512d7de824b79d44d17373b3db4c2a6e6f7c712cc9e803480b33ce0496`와 MIG-122 sparse expectation
2,673 bytes/SHA-256 `7e0c04e21237da15ab979d9b4bfec41cf81063c37e7ba5dd753c2dc0bfceb317`을 게시했습니다.
PostgreSQL 17.10 normal/race/CGO0와 SQLite external product flow가 통과했고 current aggregate는 reference
26/291/650=`254 passing + 25 deviation + 12 oracle_locked`, product 25/279=`254 passing + 25 deviation`입니다.
Phase E predecessor full-local, corrected-source refreeze와 exact-head Hosted는 위 EVID-165/166/167이 각각 소유하며
ADR-0054/GDJ-0052는 Accepted/completed입니다.

### 복귀 또는 supersede 조건

Django와의 exact sibling execution order가 existing GoDj planner stability보다 우선해야 한다는
사용자 근거가 생기거나, dependency/side-effect contract가 B1/A3의 order를 의미 있게 만들면 새
ADR과 deviation으로 이 결정을 Superseded합니다. 단순히 comparator를 완화하거나 locked oracle을
수정해서 복귀하지 않습니다.

## DEV-0003 — Template은 closed value algebra만 해석하고 arbitrary attribute/callable을 실행하지 않음

- Status: Verified
- Date: 2026-08-24
- Contracts: WEB-022, WEB-027
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj closed template value runtime
- Related ADR/work/evidence:
  [ADR-0043](adr/0043-safe-template-and-model-form-validation.md),
  [GDJ-0043](../work/0043-safe-template-validation-session-auth-and-article-admin.md),
  [Local EVID-123](status/TEST_EVIDENCE.md#evid-20260824-123--gdj-0043-template-form-auth-admin-frozen-local-checkpoint),
  [Hosted EVID-124](status/TEST_EVIDENCE.md#evid-20260824-124--gdj-0043-exact-head-hosted-completion)

### Django의 관찰 가능 동작

Locked WEB-022 reference는 같은 probe에 class attribute `name=attribute-value`와 dictionary lookup
`name=dictionary-value`를 함께 두고 renderer가 dictionary lookup을 한 번 호출해 attribute fallback보다 우선했음을 관찰합니다.
따라서 `attribute_fallback_shadowed=true`, `object_dictionary_lookups=1`입니다. Locked WEB-027 reference는 dotted
lookup에서 zero-argument callable을 자동 호출해 `auto_called=true`,
`rendered_return_category=callable_return`, `callable_invocations=1`을 관찰합니다.

### GoDj에서 채택한 동작

`templates.Value`는 String/Boolean/Integer/List/Object/Null/SafeHTML의 closed algebra만 받습니다. Raw `any`,
reflection, Go struct attribute/property, application dictionary callback, function/method 값을 template resolver에
게시하지 않습니다. WEB-022는 closed Object member와 list-index 결과 자체는 렌더링하지만 경쟁 attribute fallback이나
application callback이 없으므로 `attribute_fallback_shadowed=false`, `object_dictionary_lookups=0`입니다. WEB-027은
`auto_called=false`, `rendered_return_category=closed_value`, `callable_invocations=0`입니다. DEV-0003은 이 다섯 selector만
교체합니다.

### 이유와 고려한 대안

Python callable auto-call을 복제하면 template text가 context/error 경계 없이 DB/network I/O나 mutation을 실행할 수 있습니다.
Reflection 기반 zero-argument method 호출, caller FuncMap, marker interface로 허용 callable을 구분하는 안을 검토했지만 첫 public
slice의 권한 표면과 실패 의미를 과도하게 넓혀 채택하지 않았습니다. Handler가 값을 명시적으로 계산해 closed context에 넣는 경계를
사용합니다.

### 사용자·데이터·migration 영향

Django template에서 attribute/property/callable처럼 보이거나 application `__getitem__`에 의존하던 값을 GoDj template으로 옮길 때
handler 또는 typed adapter가 먼저 평가해 closed Object/List 값으로 게시해야 합니다.
Template render 자체는 application I/O나 mutation을 유발하지 않습니다. Persisted data, Schema IR, migration과 generated ABI에는 변화가
없습니다.

### backend/concurrency/security 영향

Backend 영향은 없습니다. Immutable Engine과 closed Value는 concurrent render에서 application callable state를 공유하지 않습니다.
이 차이는 template injection이 임의 Go method/function 실행으로 확대되는 경로를 닫는 보안 경계입니다.

### 구현과 검증 조건

- WEB-022와 WEB-027만 이 set의 `deviation`이고 각각 정확히 하나의 `decision=DEV-0003`, `derived=false` provenance를 가짐
- Sparse expectation과 code-owned policy는 WEB-022 result/metrics 각 한 selector와 WEB-027 result 두 selector/metrics 한
  selector만 허용
- Exported template Value/Context 진입점에 function, raw `any`, empty interface 또는 reflection callable ingress가 없음을 source/API gate로 검증
- Oracle-blind actual은 expected/oracle/fixture를 읽지 않고 public Engine/Value 경계에서 closed result를 생성
- Local affected/full/386/external/audit는 EVID-123에서 통과했고 exact submitted-head EVID-124/CI #134도 통과해 `Verified`

### 복귀 또는 supersede 조건

Context/error/cancellation과 I/O 권한을 명시적으로 표현하는 안전한 typed callable capability가 별도 ADR로 설계되고 실제 사용자 요구가
확인되면 DEV-0003을 supersede할 수 있습니다. Reflection이나 comparator 완화만으로 Django auto-call을 복원하지 않습니다.

## DEV-0004 — Admin logout redirect와 session cookie lifetime을 명시적으로 고정

- Status: Verified
- Date: 2026-08-24
- Contracts: AUT-004, AUT-005
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj process-memory session store
- Related ADR/work/evidence:
  [ADR-0044](adr/0044-session-auth-csrf-and-bounded-article-admin.md),
  [GDJ-0043](../work/0043-safe-template-validation-session-auth-and-article-admin.md),
  [Local EVID-123](status/TEST_EVIDENCE.md#evid-20260824-123--gdj-0043-template-form-auth-admin-frozen-local-checkpoint),
  [Hosted EVID-124](status/TEST_EVIDENCE.md#evid-20260824-124--gdj-0043-exact-head-hosted-completion)

### Django의 관찰 가능 동작

AUT-004의 Django Admin logout POST는 authenticated session을 flush한 뒤 redirect 없이 200 logout view를 반환합니다. AUT-005의
기본 login session cookie는 browser-session cookie라 `Expires`와 `Max-Age`가 없고, deletion cookie의 HttpOnly observation은
false입니다.

### GoDj에서 채택한 동작

실제 `admin.Site` logout POST는 session과 bearer cookies를 삭제한 뒤 `/admin/login/`으로 302 redirect합니다. Session cookie는
configured 2-hour lifetime을 wire `Expires`와 `Max-Age=7200`으로 명시하고, login과 deletion 모두 HttpOnly를 유지합니다. 따라서
DEV-0004의 exact scope는 다음 네 selector입니다.

- AUT-004 `result.redirect`: `none` → `admin_login`
- AUT-005 `result.delete.http_only`: `false` → `true`
- AUT-005 `result.login.expires_present`: `false` → `true`
- AUT-005 `result.login.max_age`: `null` → `7200`

Deletion cookie의 Go 내부 `MaxAge < 0`은 wire에서 `Max-Age=0`으로 직렬화되므로 reference와 같은 normalized 0이며 deviation이
아닙니다.

### 이유와 고려한 대안

별도 logged-out template 대신 좁은 static route set의 login으로 돌아가고, server-side absolute/idle expiry와 client expiry를
명시적으로 정렬합니다. Django browser-session cookie를 그대로 쓰는 안과 logout 200 view를 추가하는 안도 가능하지만 첫 bounded
Admin surface에 별도 template/state를 늘리지 않는 현재 구성을 채택했습니다. 명시적 2시간 cookie는 브라우저 재시작 뒤에도 expiry까지
남을 수 있으므로 무조건 더 안전하다고 주장하지 않습니다.

### 사용자·데이터·migration 영향

Logout 직후 사용자는 login 화면으로 이동합니다. 로그인 cookie는 브라우저 종료가 아니라 최대 2시간의 client expiry를 가집니다.
Server session flush, 이후 anonymous request, cookie path/SameSite/Secure와 deletion 의미는 reference expectation과 동일합니다.
Schema/migration/generated Article data에는 영향이 없습니다.

### backend/concurrency/security 영향

현재 session store는 process-memory only이며 restart/multi-process 공유를 지원하지 않습니다. HttpOnly는 script access를 줄이지만
explicit persistence는 브라우저 재시작 경계를 넓힙니다. TLS/non-loopback production cookie policy는 이 deviation이 승인하지 않습니다.

### 구현과 검증 조건

- AUT-004/AUT-005만 DEV-0004 `deviation`이고 각각 정확히 하나의 decision provenance를 가짐
- Sparse expectation과 code-owned policy는 위 네 selector만 exact order로 허용
- Actual은 실제 `admin.Site` login/logout HTTP route, Runtime cookie change와 server Store row를 관찰하며 surrogate response를 합성하지 않음
- Raw cookie/session/CSRF/password/hash 값은 actual, oracle, diagnostic에 직렬화하지 않음
- Local affected/full/386/external/audit는 EVID-123에서 통과했고 exact submitted-head EVID-124/CI #134도 통과해 `Verified`

### 복귀 또는 supersede 조건

Browser-session cookie 또는 logout confirmation view가 제품 요구가 되면 새 ADR에서 expiry/UX/CSRF tradeoff를 검토해 supersede할 수
있습니다. Wire deletion normalization 차이를 새 deviation으로 확대하지 않습니다.

## DEV-0005 — 첫 Admin은 Article 하나와 publish action 하나만 게시

- Status: Verified
- Date: 2026-08-24
- Contracts: ADM-002
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj SQLite 3.53.3 Article Admin
- Related ADR/work/evidence:
  [ADR-0044](adr/0044-session-auth-csrf-and-bounded-article-admin.md),
  [GDJ-0043](../work/0043-safe-template-validation-session-auth-and-article-admin.md),
  [Local EVID-123](status/TEST_EVIDENCE.md#evid-20260824-123--gdj-0043-template-form-auth-admin-frozen-local-checkpoint),
  [Hosted EVID-124](status/TEST_EVIDENCE.md#evid-20260824-124--gdj-0043-exact-head-hosted-completion)

### Django의 관찰 가능 동작

Locked ADM-002 Admin list에는 `delete_selected`와 fixture `publish_selected` 두 action이 있고, reference Admin registry에는 Article 외
auth 관련 모델까지 세 개가 등록됩니다.

### GoDj에서 채택한 동작

첫 immutable Registry는 generated Article adapter 하나만 등록하고 selected-row action은 bounded atomic `publish` 하나만 게시합니다.
ADM-002 product expectation은 `result.actions=[publish]`, `metrics.registered_models=1`이며 그 밖의 list column/order/page/row와
query metrics는 reference와 같아야 합니다.

### 이유와 고려한 대안

Generic bulk delete action은 confirmed single-object delete, permission/mutation-zero, audit와 rollback 의미보다 넓은 public surface를
추가합니다. Django auth/group model discovery도 current Schema IR/system migration을 우회합니다. 첫 slice에서는 실제 Article 사용자
흐름에 필요한 한 모델과 selected publish만 구현하고 multi-model discovery와 bulk delete를 후속 작업으로 둡니다.

### 사용자·데이터·migration 영향

Admin list에서 bulk delete와 auth 관련 모델은 보이지 않습니다. Article은 add/change/confirmed delete로 관리하고 선택 게시만 action으로
수행합니다. Existing Article schema/data와 migration 형식에는 변화가 없습니다.

### backend/concurrency/security 영향

Publish는 selected ID cap과 `db.Atomic`을 사용하며 SQLite/PostgreSQL capability 경계를 유지합니다. 권한 없는 사용자는 action을 볼 수
없고 실행할 수 없습니다. 이 결정은 general multi-model Admin, object permission 또는 durable audit를 승인하지 않습니다.

### 구현과 검증 조건

- ADM-002만 `deviation`이고 정확히 하나의 `decision=DEV-0005`, `derived=false` provenance를 가짐
- Sparse expectation과 code-owned policy는 `result.actions`와 `metrics.registered_models` 두 selector만 허용
- Oracle-blind actual은 실제 list HTML, immutable Registry descriptor, SQLite row state와 public Site request를 관찰
- List/search/page와 selected publish의 나머지 result/db_state/metrics는 locked reference와 exact match
- Local affected/full/386/external/audit는 EVID-123에서 통과했고 exact submitted-head EVID-124/CI #134도 통과해 `Verified`

### 복귀 또는 supersede 조건

Generic confirmed bulk delete 또는 auth/group model registration이 별도 system-state 설계와 함께 제품 범위가 되면 새 contract/ADR로
DEV-0005를 좁히거나 supersede합니다. DOM이나 action 이름 comparator를 완화해서 복귀하지 않습니다.

## DEV-0006 — Closed int64 route type and stricter numeric grammar

- Status: Verified
- Date: 2026-08-24
- Contracts: WEB-028, WEB-029
- Reference profile/backend: DRF 3.18.0 + Django 6.1 / CPython 3.14.3 / SQLite 3.50.4 exact profile;
  GoDj closed signed-64-bit route runtime
- Related ADR/work/evidence:
  [ADR-0045](adr/0045-closed-parameterized-routing-and-reverse.md),
  [GDJ-0044](../work/0044-session-authenticated-article-json-api-and-parameterized-routing.md),
  [Local EVID-125](status/TEST_EVIDENCE.md#evid-20260824-125--gdj-0044-article-api-frozen-local-checkpoint),
  [Hosted EVID-126](status/TEST_EVIDENCE.md#evid-20260824-126--gdj-0044-exact-head-hosted-completion)

### Django/DRF의 관찰 가능 동작

Locked parameter-routing reference는 Python `int`를 route parameter type으로 보고합니다. `<int64>` converter 자체는
`-1`, `01`, `9223372036854775808`, `x`를 거부하지만, 뒤의 `api/<path:_remaining>` JSON-404 fallback route가
resolver match를 소유하므로 observation의 `matched`는 `true`입니다. HTTP 결과는 모두 404입니다. Valid `0`, `1`,
`9223372036854775807`의 type은 모두 `int`입니다. 이 관찰은 DRF/Python 내부 객체 호환을 요구하지 않지만 exact
reference bytes에는 남습니다.

### GoDj에서 채택한 동작

GoDj는 public converter를 `<int64:name>` 하나로 닫고 `0|[1-9][0-9]*`의 canonical non-negative signed-64-bit
범위만 match합니다. 따라서 exact sparse policy는 다음 여덟 selector만 바꿉니다.

- WEB-028 `result.parameter.pk_type`: `int` → `int64`
- WEB-029 `result.invalid[0..3].matched`: 각각 `true` → `false` (`-1`, `01`, overflow, non-decimal)
- WEB-029 `result.valid[0..2].type`: 각각 `int` → `int64`

나머지 static precedence, reverse path, 404/405/Allow, ambiguity와 resource-cap 결과는 locked reference와 같아야 합니다.

### 이유와 고려한 대안

Go의 public type과 DB primary-key 경계는 signed 64-bit입니다. Reference의 JSON-404 catch-all fallback match를 제품
route match로 보존하면 converter 성공과 fallback representation ownership이 섞입니다. `int` 이름을 모방하거나 raw string
catch-all converter를 먼저 열 수도 있지만 type contract와 injection surface를 흐려 채택하지 않았습니다.

### 사용자·데이터·migration 영향

Resource ID URL은 leading zero, sign, overflow와 non-decimal segment를 404로 거부합니다. 정상 ID는 canonical decimal로
reverse됩니다. Existing static Web/Admin route와 Article DB/schema/migration/generated ABI는 바뀌지 않습니다.

### backend/concurrency/security 영향

DB별 영향은 없고 router는 immutable construction 뒤 concurrent read만 수행합니다. Strict grammar는 ambiguous normalization과
oversized integer가 application handler에 도달하는 경로를 닫습니다. Arbitrary regex/string/UUID/path converter를 승인하지 않습니다.

### 구현과 검증 조건

- WEB-028/029만 DEV-0006 `deviation`이고 각각 exact `decision=DEV-0006`, `derived=false` provenance를 가짐
- Sparse expectation과 code-owned policy는 위 여덟 selector의 exact order/type/value만 허용
- Oracle-blind actual은 expected/oracle/fixture를 읽지 않고 public `web.Route`, `ReverseWith`, `Int64Parameter`를 실행
- Root-list comparison도 selector/type/count를 fail-closed하게 검증하고 unexpected difference는 0이어야 함
- EVID-125 local full/386/external/audit와 EVID-126 exact submitted-head hosted matrix가 통과해 `Verified`

### 복귀 또는 supersede 조건

다른 typed converter나 arbitrary-precision ID가 실제 제품 요구가 되면 별도 ADR/contract로 converter algebra와 ambiguity를
검증해 supersede할 수 있습니다. Comparator 완화나 Python type 이름 모방만으로 복귀하지 않습니다.

## DEV-0007 — Article JSON API error taxonomy

- Status: Verified
- Date: 2026-08-24
- Contracts: API-001, API-003, API-010
- Reference profile/backend: DRF 3.18.0 + Django 6.1 / CPython 3.14.3 / SQLite 3.50.4 exact profile;
  GoDj JSON-only API on SQLite and PostgreSQL 17.10
- Related ADR/work/evidence:
  [ADR-0046](adr/0046-json-serializer-and-session-authenticated-article-api.md),
  [GDJ-0044](../work/0044-session-authenticated-article-json-api-and-parameterized-routing.md),
  [Local EVID-125](status/TEST_EVIDENCE.md#evid-20260824-125--gdj-0044-article-api-frozen-local-checkpoint),
  [Hosted EVID-126](status/TEST_EVIDENCE.md#evid-20260824-126--gdj-0044-exact-head-hosted-completion)

### Django/DRF의 관찰 가능 동작

Locked API-001 reference는 bounded probe의 oversized JSON body를 generic `parse_error`와 HTTP 400으로 분류합니다.
API-003의 authenticated unsafe-method CSRF 실패 세 건과 API-010의 missing-CSRF delete는 generic
`permission_denied` detail을 반환합니다.

### GoDj에서 채택한 동작

Body cap 초과는 transport/resource-limit 소유권을 보존해 `request_too_large`와 HTTP 413으로 반환합니다. Authenticated
unsafe request가 session/permission을 통과했지만 CSRF에 실패하면 `csrf_rejected`로 구분합니다. Exact sparse policy는 다음
여섯 selector만 바꿉니다.

- API-001 ordered observation `[10].response.error_codes.detail`: `parse_error` → `request_too_large`;
  `[10].response.status`: `400` → `413`
- API-003 `result.unsafe_attempts[0..2].response.error_codes.detail`: 각각 `permission_denied` → `csrf_rejected`
- API-010 `result.missing_csrf.error_codes.detail`: `permission_denied` → `csrf_rejected`

Status 403, response shape, authentication/permission order, headers와 Article mutation 0은 reference expectation과 동일합니다.

### 이유와 고려한 대안

Oversized input은 malformed JSON과 달리 server-declared resource cap을 초과했으므로 413이 retry/diagnostic ownership을 더 정확히
표현합니다. CSRF를 permission denial로 뭉개는 안도 가능하지만 GoDj의 stable machine-readable envelope에서는 실패 단계를
구분하면서도 credential/session/token bytes나 internal cause를 노출하지 않는 편을 채택했습니다.

### 사용자·데이터·migration 영향

JSON client는 oversized body에서 413/`request_too_large`, CSRF 실패에서 403/`csrf_rejected`를 받습니다. Invalid/denied
request의 Article row mutation은 0이고 schema/migration/generated ABI에는 영향이 없습니다.

### backend/concurrency/security 영향

SQLite/PostgreSQL 모두 handler persistence 전에 같은 parser/auth/CSRF 경계를 통과합니다. 구체적인 CSRF 실패 category는
노출하지만 secret, expected token, cookie/session ID나 permission internals는 직렬화하지 않습니다. Durable/distributed session이나
production proxy body-limit 정책을 승인하지 않습니다.

### 구현과 검증 조건

- API-001/003/010만 DEV-0007 `deviation`이고 각각 exact `decision=DEV-0007`, `derived=false` provenance를 가짐
- Sparse expectation과 code-owned policy는 위 여섯 selector의 exact order/type/value만 허용
- Oracle-blind actual은 expected/oracle/fixture를 읽지 않고 public JSON parser, API session wrapper와 Article adapter를 실행
- Denial/oversize에서 Article DB mutation 0, raw secret/cause serialization 0과 unexpected differential 0을 검증
- EVID-125 local full/386/external/audit와 EVID-126 exact submitted-head hosted matrix가 통과해 `Verified`

### 복귀 또는 supersede 조건

API-wide standardized problem details 또는 trusted proxy body-limit ownership이 별도 ADR로 채택되면 taxonomy를 재검토할 수
있습니다. DRF prose/code를 모방하거나 comparator를 완화하는 것만으로 복귀하지 않습니다.

## DEV-0008 — Restart 뒤 process-local CSRF key로 stale masked token을 거부

- Status: Verified
- Date: 2026-08-24
- Contracts: SYS-009
- Reference profile/backend: Django 6.1 / CPython 3.14.3 / SQLite 3.50.4 exact profile;
  GoDj SQLite and PostgreSQL 17.10 actual
- Related ADR/work/evidence:
  [ADR-0047](adr/0047-explicit-single-runtime-system-state.md),
  [GDJ-0045](../work/0045-durable-single-runtime-system-state-and-article-restart.md),
  [Local EVID-127](status/TEST_EVIDENCE.md#evid-20260825-127--gdj-0045-durable-system-state-frozen-local-checkpoint),
  [Corrected refreeze EVID-128](status/TEST_EVIDENCE.md#evid-20260825-128--gdj-0045-first-hosted-lock-failures-and-corrected-local-refreeze),
  [Hosted EVID-129](status/TEST_EVIDENCE.md#evid-20260825-129--gdj-0045-corrected-exact-head-hosted-completion),
  [ADR-0048](adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md),
  [GDJ-0046](../work/0046-database-coordinated-multi-runtime-system-state-and-shared-csrf-keys.md),
  [Phase E EVID-133](status/TEST_EVIDENCE.md#evid-20260826-133--gdj-0046-phase-e-frozen-source-and-corrected-local-final),
  [Hosted EVID-134](status/TEST_EVIDENCE.md#evid-20260826-134--gdj-0046-corrected-exact-head-hosted-completion)

### Django의 관찰 가능 동작

Django의 accepted CSRF token은 cookie secret과 masked token의 secret이 일치하는지 검증합니다. Process A에서 받은 같은 CSRF
cookie와 masked token을 process B에 전달해도 secret이 유지되면 unsafe request가 수용됩니다. SYS-009 independent reference가
이 public behavior를 separate process와 file-backed DB에서 잠급니다.

### GoDj에서 채택한 동작

Client가 보존한 독립 CSRF cookie secret은 restart 뒤에도 남지만 masked token은 process-local CSPRNG key로 서명됩니다. Restart 뒤
process A의 token은 process B에서 403과 Article mutation 0으로 거부합니다. 같은 authenticated cookie로 safe GET을 수행해
process B가 새로 만든 token은 unsafe mutation에 성공합니다.

Actual은 stale POST 직전에 process B cookie jar의 CSRF cookie가 process A의 cookie와 byte-equal함을 증명하고, safe GET 뒤에도
같은 cookie secret이 유지된 상태에서 masked token만 교체됐음을 확인해야 합니다. Cookie 누락·회전 또는 새 secret 발급으로 인한
403은 이 deviation의 증거로 인정하지 않습니다.

계획한 sparse selector는 SYS-009 stale-token lane의 다음 네 값뿐입니다.

- `result.pre_restart.accepted`: `true` → `false`
- `result.pre_restart.status`: success status → `403`
- `db_state.pre_restart.article_delta`: `1` → `0`
- `metrics.pre_restart_mutations`: `1` → `0`

Fresh-token lane과 auth/session/permission/DB state의 나머지 값은 locked reference와 같아야 합니다. Exact oracle/actual은 이
네 selector 외 unexpected difference 0을 검증했습니다.

### 이유와 고려한 대안

Signing key까지 durable하게 저장하거나 deployment-shared ring을 주입하면 restart 전 masked token을 수용할 수 있지만 key rotation,
multi-instance 배포와 secret lifecycle을 함께 결정해야 합니다. GDJ-0045의 첫 single-runtime durable state는 restart가 기존 masked
token의 trust boundary를 끊고 safe request에서 remask하는 좁은 zero-config 정책을 채택했습니다. 이후 Accepted ADR-0048/GDJ-0046은
explicit active/validation key ring을 별도 opt-in 경계로 구현했지만 zero-config 동작과 이 deviation을 제거하지 않았습니다.

### 사용자·데이터·migration 영향

Server restart 직후 열린 page가 이전 token으로 보낸 첫 unsafe request는 403이 될 수 있습니다. Session/cookie는 살아 있으므로
safe refresh로 새 token을 받은 뒤 작업을 재시도할 수 있습니다. Rejected request의 Article/audit mutation은 0입니다.

### backend/concurrency/security 영향

SQLite/PostgreSQL 차이는 없고 raw CSRF cookie/token/key는 DB audit, observation, error와 log에 포함하지 않습니다. 이 decision
자체는 zero-config one-runtime sequential restart만 다룹니다. Explicit shared ring을 구성한 cooperative multi-runtime deployment는
SYS-019에서 cross-Runtime/staged-rotation을 검증하지만, 구성하지 않은 Runtime은 계속 이 DEV-0008 의미를 따릅니다.

### 구현과 검증 조건

- SYS-009 independent Django reference와 oracle-blind Go actual을 distinct OS process로 생성
- Exact sparse policy가 stale-token 네 selector만 허용하고 fresh-token extra difference를 거부
- Stale token은 403/Article·audit mutation 0, fresh token은 expected mutation 1
- Raw cookie/token/key의 artifact, DB audit, stdout/stderr/temp leak 0
- ADR-0047 Accepted, EVID-127/128 local gates와 EVID-129 exact hosted matrix 통과로 `Verified`

### 복귀 또는 supersede 조건

Default configuration 자체를 persistent/shared key policy로 바꾸고 그 migration·rotation·secret-provider 경계를 별도 ADR에서
채택할 때만 Django behavior로 복귀하거나 새 deviation으로 supersede합니다. ADR-0048의 explicit opt-in ring만으로는 zero-config
SYS-009를 supersede하지 않습니다. Comparator를 완화하거나 stale-token case를 삭제해서 통과시키지 않습니다.

## DEV-0009 — Bearer 실패 challenge는 RFC 6750 error 의미를 명시

- Status: Verified
- Date: 2026-08-27
- Contracts: AUT-012, AUT-013, AUT-015
- Reference profile/backend: DRF 3.18.0 / Django 6.1 / CPython 3.14.3 / SQLite 3.50.4 exact profile;
  GoDj SQLite and digest-pinned PostgreSQL 17.10 actual
- Related ADR/work/evidence:
  [ADR-0049](adr/0049-first-party-bff-and-bearer-api-authentication.md),
  [GDJ-0047](../work/0047-api-authentication-profiles-and-bearer-article-api.md),
  [EVID-138](status/TEST_EVIDENCE.md#evid-20260827-138--gdj-0047-corrected-exact-head-hosted-completion)

### Django의 관찰 가능 동작

DRF 3.18 `TokenAuthentication`의 Bearer-keyword exact observation은 syntactically valid unknown/inactive token을
401로 거부하면서 response detail code를 `authentication_failed`, `WWW-Authenticate`를 parameter 없는 `Bearer`로
반환합니다. Authenticated principal의 permission 거부는 403 `permission_denied`이며 challenge가 없습니다. Session
cookie가 함께 있어도 invalid Bearer가 선택된 AUT-015 lane은 unknown token과 같은 401
`authentication_failed` + `Bearer` 결과입니다.

### GoDj에서 채택한 동작

GoDj strict Bearer resource-server profile은 HTTP status와 mutation 의미를 유지하면서 RFC 6750 error category를 fixed
challenge에 명시합니다. Unknown/inactive credential은 401 JSON `not_authenticated`와
`Bearer error="invalid_token"`, authenticated principal의 permission 부족은 403과
`Bearer error="insufficient_scope"`를 반환합니다. Session cookie가 있어도 invalid Bearer를 session profile로
fallback하지 않습니다.

Sparse expectation은 다음 일곱 `result` replacement만 허용합니다.

- AUT-012 `[0].response.error_codes.detail`:
  `authentication_failed` → `not_authenticated`
- AUT-012 `[0].response.www_authenticate`:
  `Bearer` → `Bearer error="invalid_token"`
- AUT-012 `[1].response.error_codes.detail`:
  `authentication_failed` → `not_authenticated`
- AUT-012 `[1].response.www_authenticate`:
  `Bearer` → `Bearer error="invalid_token"`
- AUT-013 `www_authenticate`: `null` → `Bearer error="insufficient_scope"`
- AUT-015 `[1].response.error_codes.detail`:
  `authentication_failed` → `not_authenticated`
- AUT-015 `[1].response.www_authenticate`:
  `Bearer` → `Bearer error="invalid_token"`

DB/token-table selector는 없으며 valid credential, missing/unsupported credential, Article mutation, verifier count와
나머지 result/metrics는 locked reference와 같아야 합니다.

### 이유와 고려한 대안

DRF의 generic authentication failure vocabulary를 그대로 복제하는 안보다 RFC 6750의 resource-server error category를
명시하면 client가 missing credential, invalid token과 insufficient scope를 status/challenge에서 구분할 수 있습니다. Dynamic
realm/scope/description/URI를 반사하거나 verifier error text를 노출하는 안은 credential·permission leakage surface를 넓히므로
채택하지 않았습니다. 한 endpoint에서 Session과 Bearer를 순서대로 시도하는 안도 invalid Bearer downgrade와 CSRF ownership을
모호하게 하므로 채택하지 않았습니다.

### 사용자·데이터·migration 영향

Client가 보는 401/403 status와 성공/실패의 Article durable state는 reference와 같습니다. 차이는 invalid token과 permission
denial response의 fixed JSON detail category/challenge입니다. Raw bearer, permission 이름과 verifier cause는 포함하지 않습니다.
Schema, migration, session row와 token table을 추가하거나 변경하지 않습니다.

### backend/concurrency/security 영향

이 차이는 persistence backend와 무관한 request authentication 경계입니다. SQLite Article E2E와 digest-pinned Linux/amd64
Go 1.26.5 + PostgreSQL 17.10 Article Bearer E2E normal/race/CGO0 및 two-process attestation이 통과했습니다. Bearer adapter는
cookie/query/body fallback과 CSRF를 사용하지 않고, redacted opaque token과 injected verifier만 소유합니다. JWT/opaque token
발급, refresh family, OAuth/OIDC, key rotation/revocation과 production BFF는 이 deviation의 범위가 아닙니다.

### 구현과 검증 조건

- Manifest는 AUT-012/013/015만 `deviation`이고 각각 exact `decision=DEV-0009`, `derived=false` provenance를 가짐
- `godj-api-authentication-deviation-expected.json`은 위 일곱 result replacement만 exact order/type/value로 소유
- Code-owned DEV-0009 policy는 selector/status/provenance의 누락·추가·중복과 unknown decision을 actual 생성 전에 fail-closed
- Oracle/expected/deviation fixture를 읽지 않는 GoDj actual이 exact 10/10, unexpected difference 0으로 로컬 통과
- SQLite와 PostgreSQL Article Bearer user-flow E2E, PostgreSQL two-process attestation 및 affected
  normal/race/CGO0/vet/generate와 `make godj-conformance` 통과
- EVID-136 initial local final 뒤 first hosted run `33044776835`가 26 jobs success/1 macOS Intel outer-timeout으로 종료됨
- EVID-137의 Intel-only 45-minute correction, attestation recapture와 corrected full `make ci`, Linux/386,
  1,077-file external archive 통과
- Corrected submitted head `5f97fa8...`, tree `2b53c031...`의 EVID-138/CI #155 run `33049861740`이 exact
  27/27 jobs·360/360 steps success, failure/cancel/skip/annotation 0으로 통과
- ADR-0049 Accepted와 terminal evidence를 근거로 `Verified`

### 복귀 또는 supersede 조건

DRF generic challenge parity가 RFC 6750 error category보다 우선해야 한다는 제품 근거가 생기거나 더 넓은 standardized problem
details/authentication profile이 채택되면 새 ADR/deviation으로 supersede합니다. Locked DRF oracle을 수정하거나 comparator에서
challenge/detail을 무시해 복귀하지 않습니다.

## DEV-0010 — GoDj migration writer의 current format과 안정 오류 taxonomy

- Status: Verified
- Date: 2026-08-30
- Contracts: MIG-103, MIG-104, MIG-105, MIG-106, MIG-107
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile의 MIG-103..106;
  GDJ-0050 Phase-A GoDj decision oracle의 MIG-107; GoDj SQLite와 PostgreSQL 17.10 actual
- Related ADR/work/evidence:
  [ADR-0052](adr/0052-project-linked-deterministic-makemigrations.md),
  [GDJ-0050](../work/0050-project-linked-deterministic-makemigrations.md),
  [EVID-150](status/TEST_EVIDENCE.md#evid-20260830-150--gdj-0050-phase-d-postgresql-product-publication-and-external-consumer-checkpoint),
  [EVID-151](status/TEST_EVIDENCE.md#evid-20260830-151--gdj-0050-first-hosted-diagnostic-and-frozen-local-final),
  [EVID-152](status/TEST_EVIDENCE.md#evid-20260830-152--gdj-0050-corrected-head-hosted-failure-and-test-harness-refreeze),
  [EVID-153](status/TEST_EVIDENCE.md#evid-20260830-153--gdj-0050-corrected-exact-head-hosted-completion)

### Django 또는 Phase-A decision oracle의 관찰 가능 동작

Django MIG-103은 관찰한 새 `ForeignKey`의 `on_delete`를 `CASCADE`로 기록합니다. MIG-104는 operation 이름을 이어 붙인
`0002_category_article_summary_article_category`를 사용합니다. MIG-105/106은 app-local Python migration directory의
`__init__.py`, `0001_initial.py` roster와 사람이 읽는 다중 행 command output을 관찰합니다.

MIG-107은 Django parity 계약이 아닙니다. Phase A에서 GoDj-owned fail-closed decision oracle이 unsupported scalar/remove/required
field를 `unsupported_delta`, relation cycle을 `relation_cycle`, noncanonical applied leaf를 `noncanonical_leaf`로 분류했습니다.
현재 제품 비교는 이 historical decision oracle을 입력으로 보존하되, production error taxonomy와 다른 값을 의도적으로
supersede합니다.

### GoDj에서 채택한 동작

GoDj current writer는 지원하는 relation policy를 명시적으로 `PROTECT`로 기록하고, semantic input digest에서
`0002_auto_7fb2bf122b7c` 같은 결정적 successor 이름을 만듭니다. Migration file은 project-owned flat root의
`<app>_<name>.godj.json`이고 read-only/clean 결과와 candidate roster는 bounded canonical JSON 한 줄로 출력합니다. Python
package marker나 `.py` source를 만들지 않습니다.

MIG-107 actual은 production API가 소유하는 안정된 code만 노출합니다. 다섯 unsupported delta와 noncanonical leaf는
`unsupported_change`, relation cycle은 `invalid_relation`입니다. Raw cause, path 또는 secret을 error code로 반사하지 않습니다.

Sparse expectation은 result dimension의 exact 열아홉 replacement만 허용합니다.

- MIG-103: 두 `on_delete` selector의 `CASCADE` → `PROTECT`
- MIG-104: migration name 한 selector의 semantic Django name → digest-derived GoDj name
- MIG-105: dry-run의 before/after flat roster와 canonical JSON output 세 selector
- MIG-106: clean/check 두 case의 before/after flat roster와 canonical JSON output 여섯 selector
- MIG-107: 일곱 case의 Phase-A decision-oracle code → stable production error code

Exit status, candidate/write/DB-open count, no-mutation, dependency/operation payload, interruption/resume와 나머지 result/metrics는
locked reference와 같아야 합니다.

### 이유와 고려한 대안

GoDj가 지원하지 않는 `CASCADE`를 generated definition에 적거나 Django Python file ABI를 복제하면 current Schema IR,
strict data-only Definition과 project-wide flat publication 경계를 거짓으로 표현하게 됩니다. Human-readable stdout을 별도 기본
format으로 추가하면 automation contract가 이중화됩니다. Phase-A 전용 세분 taxonomy를 public error로 유지하는 안도 internal
detector 구조를 외부 ABI로 고정하므로 채택하지 않았습니다.

### 사용자·데이터·migration 영향

Generated relation 삭제 의미는 Django default와 달리 `PROTECT`입니다. 이름과 file roster/output도 Django project와 byte-compatible하지
않지만 같은 desired state에서는 결정적이고 repeat clean입니다. 기존 migration을 rewrite하지 않으며 이번 bounded writer는
CreateModel/AddField만 지원합니다. Remove/alter/rename/custom/data operation은 stable `unsupported_change`로 fail-closed합니다.

### backend/concurrency/security 영향

Writer planning/publication은 DB-free이고 supported local filesystem의 retained root, directory-inode lock, fresh plan/CAS와
recoverable no-replace publication을 사용합니다. SQLite와 PostgreSQL 17.10 generated migrate/no-op/restart actual, PostgreSQL
normal/race/CGO-disabled, repository-external public module flow가 Phase D에서 통과했습니다. DSN/password, raw build input과
temporary protocol은 stdout/stderr/artifact에 남기지 않습니다.

### 구현과 검증 조건

- Manifest는 MIG-103..107만 `deviation`이고 각각 exact `decision=DEV-0010`, `derived=false` provenance를 가짐
- `godj-migration-writer-deviation-expected.json`은 위 열아홉 result replacement만 exact order/type/value로 소유
- Code-owned DEV-0010 policy가 selector/status/provenance의 누락·추가·중복과 unknown decision을 actual 전에 fail-closed
- Oracle/expected/deviation fixture를 읽지 않는 GoDj actual이 MIG-099..110 exact 12/12, unexpected difference 0으로 통과
- MIG-099/100/101/102/108/109/110은 `passing`, MIG-103..107은 이 Verified deviation으로 product aggregate에 포함
- PostgreSQL 17.10 normal/race/CGO-disabled actual과 repository-external public API/project runner flow 통과
- Predecessor full `make ci`, Linux/386 compile-only/relation/archive는 EVID-151에서, workflow correction 뒤 current
  source-bound attestation/focused refreeze는 EVID-152에서 통과
- Exact submitted tree `48994a0...`는 EVID-153/CI #171 run `33280434425`의 source 변경 없는 failed-job rerun 뒤
  effective 41/41 jobs·464/464 steps, failure/cancel/skip/annotation 0과 PostgreSQL 각 21/21/0으로 terminal 검증

### 복귀 또는 supersede 조건

GoDj가 `CASCADE`와 Django-style Python migration ABI/output을 current public format으로 채택하거나, migration name/error taxonomy를
새 versioned public policy로 바꿀 때 새 ADR/deviation으로 supersede합니다. MIG-107의 Phase-A decision oracle을 Django 관찰로
재분류하거나 comparator에서 file/output/error selector를 무시해 복귀하지 않습니다.
