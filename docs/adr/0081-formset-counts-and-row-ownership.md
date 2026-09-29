# ADR-0081: Formset의 행 수와 소유권

- 상태: 채택, 구현/환경별 검증은 [현황](../status/CURRENT.md)과 [증거](../status/TEST_EVIDENCE.md) 참조
- 날짜: 2026-09-29
- 관련 작업: [GDJ-0103](../../work/0103-formsets-and-scoped-batch-editing.md)

Formset은 같은 불변 Form 명세의 여러 인스턴스와 요청 prefix를 묶는다. 관리 필드의 TOTAL_FORMS/INITIAL_FORMS,
빈 추가 행, optional ORDER/DELETE와 여러 행에 걸친 pure 검증을 소유한다. 모델 조회·identity·권한·저장·transaction은
ModelForm/애플리케이션 경계에서 별도로 연결한다. 숫자 순서와 삭제 표시가 권한을 만들지는 않는다.

고정 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`의 `django.forms.formsets`를 관찰한다.
[출처](../SOURCES.md)와 [Django license](../../LICENSE.django)를 따른다. Python 내부 객체/상속/renderer API는 복제하지 않는다.

## 관리 필드와 resource 경계

`SetConfig`는 서버 구성이다. DefaultSetConfig는 extra 1, min 0, max 1000, absolute max 2000을 제공한다.
Max는 기본 표시 수를 제한하며 ValidateMax가 켜졌을 때 제출 개수도 검사한다. AbsoluteMax는 제출과 unbound 구성의
폼 생성 상한이다. 클라이언트의 MIN_NUM_FORMS/MAX_NUM_FORMS는 정책을 변경할 수 없다. 잘못된 구성은 명시적 오류다.

서버 initial 수와 INITIAL_FORMS가 다르거나 count가 음수이거나 INITIAL_FORMS가 TOTAL_FORMS보다 크면 입력을 거부한다.
고정 Django는 관찰한 이 세 경우를 허용하지만, GoDj는 저장된 행을 추가 행으로 재분류하거나 생략해 검증을 피할 수 없게 한다.
이 차이를 native 일치로 세지 않는다. Int64 범위 밖 count와 단일 필드의 중복 제출도 기존 Go 입력 규칙대로 거부한다.
기존 Form의 Boolean/Choice 등 입력 규칙도 유지하며 formset 때문에 Python의 임의 truthiness/coercion을 추가하지 않는다.
그 밖의 prefix는 독립적으로 다루며 요청의 행 수 상한을 먼저 적용해 폼/validator 생성을 제한한다.

## 검증과 결과

초기 행은 일반 필드 검증을 수행한다. Min에 포함되지 않는 추가 행이 initial과 동일하면 field/cross validation을
실행하지 않고 빈 cleaned data를 유지한다. Delete로 지정한 행의 오류는 다른 행의 유효성을 막지 않지만, 삭제 대상의
실제 persistence는 서버가 소유한 identity에서 다시 결정한다. CanDeleteExtra가 꺼진 추가 행에는 삭제 필드를 만들지 않는다.

Order는 optional 정수다. 같은 값은 원래 행 순서를 유지하고 빈 Order는 뒤에 배치한다. 빈 추가 행과 삭제 행을 제외한다.
입력/초기값/cleaned/오류/관리 값은 불변 snapshot이며 일반 formatting은 내용을 공개하지 않는다. 원래 제출은 명시적
accessor로만 읽는다. 여러 행 validator도 pure·동시 실행 안전성을 caller가 보장하며 I/O를 숨기지 않는다.

행 오류와 non-form 오류는 분리한다. Invalid/미바인딩 결과로 저장용 active/deleted/ordered 선택을 얻으려 하면 명시적으로
실패한다. 이 선택이 유효하다는 사실은 현재 DB에서 쓰기 권한이나 원자성을 확인했다는 뜻이 아니다. 모델/제품 연결에서
현재 scope·identity·revision·제약을 확인하고 전체 요청이 요구하는 transaction/rollback 의미를 보장한다.

## 모델의 identity와 typed 준비

`forms/model.InstanceSet`은 caller가 미리 조회한 current 모델 집합을 소유한다. Auto/Integer PK의 presence를 요구하며,
기존 행을 제출 PK로 이 집합에 연결한다. 순서 변경은 허용하지만 누락·중복·외부 PK·추가 행의 PK 주입은 전체 요청 오류다.
새 행과 삭제된 행에도 같은 규칙을 적용한다. Hidden PK는 Form의 editable input과 분리한다. 고정 Django의 model formset은
관찰한 외부/중복/추가 PK 및 삭제와 결합한 일부 사례를 허용하지만 GoDj는 이 6개를 의도적으로 거부한다.
Native도 거부한 3개 identity 사례는 오류 위치와 후보 구성까지 동일하다고 주장하지 않는다.

일반 Form 바인딩이 끝난 뒤 pure processor가 같은 binding에 모델 후처리를 붙인다. 각 bound Form은 비공개 binding token을
갖고, 새 Bind 결과나 다른 행의 결과로 교체할 수 없다. 모델 후처리까지 끝난 후 count/set 검증을 수행한다. 단일/여러 행은
같은 내부 model candidate 경로를 사용하고 raw input을 보존한다. Optional 빈 추가 행은 모델 후처리도 실행하지 않는다.

Typed Prepare는 모든 active 후보와 기존 삭제 의도를 분리한다. 변경 없는 기존 행도 보존해 caller가 모델 후처리의 변경과
최종 쓰기 정책을 명시할 수 있게 한다. Django의 `save(commit=False)`가 반환하는 변경 행 목록과 이 API를 동일시하지 않는다.
삭제 Model은 원래 current snapshot이고, 새 행의 DELETE는 DB 삭제 의도가 되지 않는다. Selected ManyToMany는 미리 읽은
관계 값을 pure reader로 전달하고 준비된 collection intent를 별도로 유지한다. 이 계층은 query·쓰기·transaction을 실행하지 않는다.

후속 DB 데이터 오류는 immutable row 결과에 추가할 수 있다. 기존 callback/count 검증을 재실행하거나 invalid 결과를
valid로 바꾸지 않는다. DELETE가 데이터 오류를 무시할 수 있는 성질과 요청 admission은 별개다. Identity·권한·운영 실패는
전체 진단 또는 operation error로 전달하며 행 오류로 축소하지 않는다. 실제 제품의 최종 인가·원자 저장은 별도 소유자다.

## 여러 행의 고유값 검사

ModelFormSet은 행별 model clean 뒤 개수 제약을 확인하고, 사용자 여러 행 검증 전에 unique field·IR의 명시적 복합 unique를
검사한다. Django와 같이 candidate가 아닌 Form cleaned 입력을 비교한다. NULL·제외 field·invalid 행을 건너뛰고, 삭제 선택은
검사 직전 Set의 유효성에 따른다. 중복된 뒤쪽 행의 non-field 진단과 cleaned tuple 제거, 전체 진단을 함께 적용한다.
후속 DB 검사에는 갱신한 exclusion이 전달되며 후보·원래 입력·변경 목록은 유지한다. 자동 저장이나 DB 검증 완료를 뜻하지 않는다.

GoDj는 IR 순서와 검사 시작 snapshot의 완전한 튜플을 사용한다. 앞선 제약 오류로 일부 field가 cleaned data에서 제거되어도
다른 제약을 부분 튜플로 비교하지 않는다. 이는 Django 내부 set iteration/cleaned dict 수정 순서를 복제하지 않는 명시적 선택이다.
Django의 코드 없는 cross-row 메시지는 GoDj의 안정된 `unique` 진단으로 표현하며 입력값은 오류 parameter에 넣지 않는다.

Core SetProcessor의 Form/Clean 단계는 행과 전체의 private binding token을 보존한다. 같은 의미의 데이터를 새로 bind하거나 다른
바인딩 결과를 가져와 검증을 우회할 수 없다. Compound 진단이 제거할 cleaned field는 명시하고, 진단 없는 제거는 허용하지 않는다.

## Inline의 부모 소유권과 지연 key 연결

`InlineSpec`은 canonical project binding에서 명시한 자식 FK와 대상 부모를 확인한다. 양 typed manager의 전체 metadata가
project 모델과 같아야 하며 앱 이름을 추측하지 않는다. Current 자식은 같은 부모에 속해야 한다. 부모 PK가 없는 상태에서 기존
자식을 채택할 수 없다. 현재 Auto/Integer PK와 required/nullable FK·OneToOne을 지원하며 IR의 FK default 범위를 넓히지 않는다.

부모 FK는 서버가 정한 optional hidden field이며 제출값으로 reparent하지 않는다. 빈 값/생략은 서버 값을 사용하고 nonempty
입력은 canonical 숫자와 일치해야 한다. 부모 값은 Changed에서 제외하여 빈 extra 행을 활성화하지 않는다. Model clean 전에
서버 부모를 candidate에 넣고 PostClean의 변경 소유권에서도 제외한다. 고정 Django가 허용한 삭제 행/빈 추가 행의 다른 부모,
반복 부모 scalar, 새 부모의 Python `None` 문자열 네 사례를 GoDj는 의도적으로 전체 거부한다. Native도 거부한 다른 세 부모
사례는 오류 위치까지 같다고 주장하지 않는다. 저수준 field 검사 외에 InlineSet이 raw 입력의 전체 admission을 소유한다.

아직 key가 없는 부모의 자식 검증은 허용한다. 부모를 포함한 unique tuple은 실제 key 대신 같은 부모라는 상수로 형제 사이를
비교한다. Typed scalar로 준비할 때는 nullable FK여도 부모 key가 필요하다. 독립 행 Prepare로 이 조건을 우회할 수 없다.
`PrepareWithParent`는 caller가 저장한 부모의 key를 별도 후보에 적용하고 callback을 반복하지 않는다. 기존 부모 identity는
바꿀 수 없다. PK 0의 명시적 presence를 보존하며 원래 pending 후보·nullable pointer·동시 준비의 소유권을 분리한다.

Binding/준비는 I/O·인가·부모 존재 조회를 수행하지 않는다. Caller는 부모 Form 저장과 모든 자식 쓰기/삭제/감사 기록을 같은
transaction에서 처리한다. 뒤의 오류는 전체 scope를 벗어나 rollback해야 한다. Rollback 뒤 Go 값에 남은 parent key를 commit
증거로 쓰지 않는다. 이 API와 실제 양 DB 저장 검증은 새 부모 HTML 화면이나 Admin inline UI의 구현 완료를 뜻하지 않는다.

## Helpdesk의 페이지 단위 저장

Helpdesk editor는 서버 Category의 현재 ID 순서 20행을 cohort로 삼는다. GET은 하나의 read snapshot을 사용하고 POST는
현재 cohort·관계·선택지를 outer `AtomicRelation`에서 다시 읽어 바인딩한다. 현재 권한과 선택 field 경계를 유지하며,
공통 InlineSpec으로 Category FK와 hidden 부모 검사를 연결한다.
일반 ticket 저장의 session 내부 helper를 재사용한다. 삭제는 완전한 project relation deleter의 `DeleteInSession`을 사용한다.
존재하는 삭제를 먼저 처리한 뒤 active 행을 저장한다. Immediate DB unique 제약을 우회하는 값 교환 알고리즘을 추가하지 않는다.

각 변경/생성/삭제의 audit도 같은 session에 기록하고, 후속 행·관계·audit의 실패는 outer rollback으로 전체를 되돌린다.
모든 권한은 인증된 immutable principal과 deny overlay에서 얻는다. Form 데이터 진단은 확실히 rollback이 완료되어 직접 반환된
rejection일 때만 재표시하며, rollback 실패·commit 결과 불명·취소를 validation 또는 성공으로 축소하지 않는다. 자동 재시도하지 않는다.
저장된 기존 scalar를 새 입력으로 다시 검증하는 범위와 제외 field 보존은 기존 단일 Ticket Form의 의미를 따른다.

## 조회 전용 기존 행

Admin inline에서 Change 권한이 없는 기존 행은 일반 편집 입력을 POST하지 않는다. 고정 Django Admin도 그 행의 검증을
건너뛰고 서버 initial을 사용하며 Add 권한의 새 행은 검증한다. GoDj는 Python Form의 `_errors`/`cleaned_data`를 직접 바꾸는
방식 대신 `SetConfig.ReadOnlyInitial`과 결과의 `Form.ReadOnly()`로 이 의미를 명시한다. 일반 입력·field/cross/model clean은
기존 행을 바꾸지 못하고 선언된 ORDER/DELETE는 별도 제어로 검증한다. Typed 준비는 읽기 전용 행의 저장 입력 생성을 거부한다.
이를 전체 skip으로 구현하지 않아 identity·parent admission, management/count, 형제 고유성 및 전체 진단은 계속 적용한다.

Django는 삭제할 조회 전용 행에서 일반 Form/model clean을 실행한 뒤 그 오류를 무시한다. GoDj는 삭제 의도와 서버 모델을
분리하여 그 callback도 실행하지 않는다. 제어 필드의 변경은 Go Form의 Changed에 남을 수 있으며 native `has_changed`와
동일한 권한 표식으로 사용하지 않는다. 저장된 행을 삭제하고 같은 unique 값의 새 행을 만들 때 native Admin은 삭제 전 DB 검사로
새 행을 거부한다. Go의 순수 준비는 삭제/추가 의도를 기술하므로 이 DB 검사와 같다고 주장하지 않는다. 실제 Admin 소비자의
읽기 검증·현재 권한·원자 저장과 오류 경계를 별도로 연결해야 한다. 조회 전용 snapshot은 저장 권한이나 DB 유효성 증명이 아니다.

## Admin의 합성 저장

Admin Inline은 typed InlineSpec을 표시·인가 경계에 연결하는 type-erased adapter다. 공통 Site는 부모 등록과 canonical
FK/app identity를 결합하고 권한·count·PK/parent·optional child revision과 오류 재표시를 처리한다. 부모 Create/Update의
필수 InlineSubmission 인자로 자식의 원래 입력과 허용한 동작을 전달한다. 별도 inline writer나 개별 transaction callback을
두지 않고 하나의 application callback이 전체 저장을 소유한다. Site는 writer 직전 같은 자식을 추가로 재조회/재검증하지 않는다.
Writer가 transaction 안에서 현재 집합으로 다시 바인딩하고 final uniqueness/관계·삭제 정책과 감사 기록을 책임진다.

Child-only 변경은 inline prefix라는 semantic changed field로 표현하여 부모 revision/audit 정책에 연결한다. Prefix는 부모의
stored/form/audit field와 충돌할 수 없다. Direct RejectInline만 confirmed input rejection이며 wrapped/joined execution failure를
재표시로 낮추지 않는다. 삭제 불가·인가/집합 진단은 삭제 행의 ordinary errors에 숨기지 않는다.

Helpdesk의 opt-in AdminRegistry는 transactional audit callback을 명시적으로 받는다. 독립 Registry의 기존 저장 경로와
application을 바꾸지 않고 티켓/OneToOne 보고서의 새 부모 생성·원자 수정/삭제를 조합한다. Native가 확인한 readonly 정책과
삭제 전 DB 고유성 검사를 적용하며 모든 API/HTML 오류 문구나 동작을 Django 전체와 동일하다고 선언하지 않는다.
현재 UI는 선언한 추가 행의 서버 렌더링이며 동적 행 JavaScript/files와 일반 자동 persistence는 별도 미완료 범위다.
