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

## Helpdesk의 페이지 단위 저장

Helpdesk editor는 서버 Category의 현재 ID 순서 20행을 cohort로 삼는다. GET은 하나의 read snapshot을 사용하고 POST는
현재 cohort·관계·선택지를 outer `AtomicRelation`에서 다시 읽어 바인딩한다. 현재 권한과 선택 field 경계를 유지하며,
일반 ticket 저장의 session 내부 helper를 재사용한다. 삭제는 완전한 project relation deleter의 `DeleteInSession`을 사용한다.
존재하는 삭제를 먼저 처리한 뒤 active 행을 저장한다. Immediate DB unique 제약을 우회하는 값 교환 알고리즘을 추가하지 않는다.

각 변경/생성/삭제의 audit도 같은 session에 기록하고, 후속 행·관계·audit의 실패는 outer rollback으로 전체를 되돌린다.
모든 권한은 인증된 immutable principal과 deny overlay에서 얻는다. Form 데이터 진단은 확실히 rollback이 완료되어 직접 반환된
rejection일 때만 재표시하며, rollback 실패·commit 결과 불명·취소를 validation 또는 성공으로 축소하지 않는다. 자동 재시도하지 않는다.
저장된 기존 scalar를 새 입력으로 다시 검증하는 범위와 제외 field 보존은 기존 단일 Ticket Form의 의미를 따른다.
