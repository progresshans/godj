# 의도적 호환 차이 원장

- 상태: Active ledger
- 마지막 갱신: 2026-08-24
- 현재 구현된 deviation: DEV-0001..DEV-0005 다섯 건 / contract 열 개

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
- Contracts: MIG-052
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj SQLite 3.53.3
- Related ADR/work/evidence:
  [ADR-0013](adr/0013-immutable-migration-planner.md),
  [ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md),
  [GDJ-0017](../work/0017-migration-lifecycle-compatibility-contracts-and-revision-fence-spike.md),
  [GDJ-0018](../work/0018-revision-fenced-migration-lifecycle-product-slice.md),
  [EVID-20260809-017](status/TEST_EVIDENCE.md#evid-20260809-017--gdj-0018-revision-fenced-migration-lifecycle-product-slice)

### Django의 관찰 가능 동작

MIG-052는 A1/A2/A3과 unrelated B1이 applied인 상태에서 alpha app을 zero target으로
내립니다. Django public orchestration의 plan과 committed step 순서는
`B1←A3←A2←A1`입니다. B1과 A3은 서로 dependency가 없는 incomparable reverse sibling이지만
Django의 private traversal에서는 B1이 먼저 선택됩니다.

### GoDj에서 채택한 동작

Accepted ADR-0013의 canonical ascending planner policy를 그대로 사용해
`A3←A2←B1←A1`로 실행합니다. Deviation scope는 locked Django lifecycle observation을 복사한
product expectation에서 다음 여섯 selector의 value를 replace하는 것으로 제한합니다.

- `result.plan[0]`, `result.plan[1]`, `result.plan[2]`
- `metrics.steps[0]`, `metrics.steps[1]`, `metrics.steps[2]`

그 밖의 `result`, resulting logical state, managed DB schema, recorder history, phase와 metrics는
Django reference와 동일해야 합니다. MIG-052의 phase는 두 구현 모두 `commit`입니다.

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
이 결정은 SQLite lock 순서, retry, privilege 또는 security boundary를 완화하지 않으며
non-SQLite backend 동작을 승인하지 않습니다.

### 구현과 검증 조건

- Lifecycle manifest에서 MIG-052만 `deviation`, 나머지 MIG-047..051/053..056은 `passing`
- MIG-052 provenance는 정확히 하나의 `kind=decision`, `reference=DEV-0002`, `derived=false`
- `godj-migration-lifecycle-deviation-expected.json`은 위 여섯 replace selector만 소유
- Code-owned DEV-0002 policy는 selector/status/provenance의 누락·추가·중복과 unknown decision을
  actual 생성 전에 fail-closed
- Live adapter는 public `Executor.Migrate`와 SQLite DB를 사용하고 contract ID/oracle/static
  dispatch를 하지 않음
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

### 복귀 또는 supersede 조건

Django와의 exact sibling execution order가 existing GoDj planner stability보다 우선해야 한다는
사용자 근거가 생기거나, dependency/side-effect contract가 B1/A3의 order를 의미 있게 만들면 새
ADR과 deviation으로 이 결정을 Superseded합니다. 단순히 comparator를 완화하거나 locked oracle을
수정해서 복귀하지 않습니다.

## DEV-0003 — Template은 closed value algebra만 해석하고 arbitrary attribute/callable을 실행하지 않음

- Status: Implemented
- Date: 2026-08-24
- Contracts: WEB-022, WEB-027
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj closed template value runtime
- Related ADR/work/evidence:
  [ADR-0043](adr/0043-safe-template-and-model-form-validation.md),
  [GDJ-0043](../work/0043-safe-template-validation-session-auth-and-article-admin.md),
  [Local EVID-123](status/TEST_EVIDENCE.md#evid-20260824-123--gdj-0043-template-form-auth-admin-frozen-local-checkpoint)

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
- Local affected/full/386/external/audit는 EVID-123에서 통과; exact submitted-head hosted matrix 뒤에만 `Verified`로 승격

### 복귀 또는 supersede 조건

Context/error/cancellation과 I/O 권한을 명시적으로 표현하는 안전한 typed callable capability가 별도 ADR로 설계되고 실제 사용자 요구가
확인되면 DEV-0003을 supersede할 수 있습니다. Reflection이나 comparator 완화만으로 Django auto-call을 복원하지 않습니다.

## DEV-0004 — Admin logout redirect와 session cookie lifetime을 명시적으로 고정

- Status: Implemented
- Date: 2026-08-24
- Contracts: AUT-004, AUT-005
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj process-memory session store
- Related ADR/work/evidence:
  [ADR-0044](adr/0044-session-auth-csrf-and-bounded-article-admin.md),
  [GDJ-0043](../work/0043-safe-template-validation-session-auth-and-article-admin.md),
  [Local EVID-123](status/TEST_EVIDENCE.md#evid-20260824-123--gdj-0043-template-form-auth-admin-frozen-local-checkpoint)

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
- Local affected/full/386/external/audit는 EVID-123에서 통과; exact submitted-head hosted matrix 뒤에만 `Verified`로 승격

### 복귀 또는 supersede 조건

Browser-session cookie 또는 logout confirmation view가 제품 요구가 되면 새 ADR에서 expiry/UX/CSRF tradeoff를 검토해 supersede할 수
있습니다. Wire deletion normalization 차이를 새 deviation으로 확대하지 않습니다.

## DEV-0005 — 첫 Admin은 Article 하나와 publish action 하나만 게시

- Status: Implemented
- Date: 2026-08-24
- Contracts: ADM-002
- Reference profile/backend: Django 6.1 / SQLite 3.50.4 exact profile; GoDj SQLite 3.53.3 Article Admin
- Related ADR/work/evidence:
  [ADR-0044](adr/0044-session-auth-csrf-and-bounded-article-admin.md),
  [GDJ-0043](../work/0043-safe-template-validation-session-auth-and-article-admin.md),
  [Local EVID-123](status/TEST_EVIDENCE.md#evid-20260824-123--gdj-0043-template-form-auth-admin-frozen-local-checkpoint)

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
- Local affected/full/386/external/audit는 EVID-123에서 통과; exact submitted-head hosted matrix 뒤에만 `Verified`로 승격

### 복귀 또는 supersede 조건

Generic confirmed bulk delete 또는 auth/group model registration이 별도 system-state 설계와 함께 제품 범위가 되면 새 contract/ADR로
DEV-0005를 좁히거나 supersede합니다. DOM이나 action 이름 comparator를 완화해서 복귀하지 않습니다.
