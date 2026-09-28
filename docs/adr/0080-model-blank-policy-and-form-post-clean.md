# ADR-0080 — 모델의 빈 입력 정책과 Form 후처리

- 상태: accepted, 구현 중
- 날짜: 2026-09-28
- 관련 작업: [GDJ-0102](../../work/0102-model-blank-policy-and-post-clean.md)

`Nullable`은 DB NULL과 Go 모델 값의 표현을 소유한다. 별도 `Blank`는 모델 입력에서 빈 값이 허용되는지를 소유한다.
둘은 Schema IR에 독립적으로 남기며 기본 Blank는 false다. Scalar/FK와 columnless ManyToMany 모두 이 정책을 가진다.
Form projection은 Blank에서 기본 required를 정하며 Boolean의 checkbox/null widget 규칙과 명시적 override는 따로 적용한다.
폼의 정리 결과가 Null이라는 사실은 nonnullable 모델에 저장할 권한을 주지 않는다.
고정 Django의 model.clean_fields는 Blank가 허용한 empty를 Field.clean보다 먼저 건너뛴다. 숫자의 None도
이 대상이므로 pure ModelForm 검증에서 이를 임의의 null 오류로 강화하지 않는다. Advisory uniqueness도 NULL을
조회하지 않는다. Generated write의 타입/nullable 검사와 DB의 NOT NULL은 별도로 계속 적용된다.

Blank만 바꾸는 migration은 기존 행·column·constraint·자동 intermediary·관계 link를 다시 만들지 않는다.
Before/after의 다른 속성이 같은지 확인하고 historical state·digest·capability·revision fence를 통과하는 metadata 변경으로
정의한다. 일반 컬럼은 AlterField, ManyToMany는 명시적인 AlterManyToMany로 변경하며 임의 rename/binding/storage 변경을
blank 변경에 섞어 허용하지 않는다. 원본 migration을 덮어쓰지 않고 새 이력과 reverse에 정책을 보존한다.

입력 field cleaning과 모델 검증은 다른 단계다. Field 오류가 있는 값과 명시적으로 제외한 모델 field는 후처리 대상에서
제외한다. Form에서 required를 완화한 빈 값의 제외와 모델 Blank가 허용한 빈 값의 unique 참여를 구분한다.
선택지에 포함된 Email도 모델 이메일 문법을 다시 검사한다. 모델 clean/unique/constraint의 순서와 오류가 있는 member의
제외는 고정 Django의 외부 결과로 대조한다. DB 검사는 context/error와 현재 인가를 갖는 read-only scope가 소유하고,
성공한 사전 검사도 최종 write fence·DB 제약·transaction·unknown outcome 처리의 대체물이 아니다.

`forms/model.BoundForm`은 원래 Form과 전체 모델 candidate를 분리해 소유한다. 오류가 난 field는 cleaned data에서
빠지지만 모델 clean은 해당 field의 기존/default 후보를 볼 수 있다. Pure model validator는 Form cross-validator와
다른 인터페이스다. 기존/error 값이 없는 unsaved field의 초기 상태와 제외한 field는 저장 입력으로 자동 노출하지 않는다.
`ValidationValues`는 현재 오류·제외 규칙을 반영한 scalar snapshot이며 단계 사이에서 다시 계산한다.
ORM의 `ValidateUniqueFields`와 `ValidateUniqueConstraints`는 이를 별도로 읽고, 전체 mutation을 요구하는 기존
`ValidateUniqueCreate/Update`와 역할을 구분한다. 인가된 현재 row의 명시적 PK 상태로 자신을 제외하며 0도 유효한 PK다.
I/O 또는 취소 실패는 그 호출의 부분 진단을 버린다. 소비자는 여러 단계를 같은 인가된 read scope에 연결해야 한다.

`BoundForm.CheckDatabase`는 `DatabaseChecks.UniqueFields`와 `Constraints`를 순서대로 호출한다. 두 callback을 모두
명시하며 해당 제약이 없는 모델은 빈 진단을 반환한다. 이미 실패한 입력이 있어도 다른 유효 field의 검사는 진행한다.
첫 단계의 오류를 내부 Form 복사본에 적용한 뒤 두 번째 단계의 candidate/exclusion을 다시 계산한다. 반환값은 새 진단만
포함하므로 기존 Form 오류를 중복 적용하지 않는다. Callback 오류·취소는 앞 단계의 진단도 버리고 실행 오류로 유지한다.
Callback이 오류 반환값으로 input rejection을 잘못 보내도 renderable rejection으로 내보내지 않는다. 실행 오류의 원인은
errors.Is/As로 보존하지만 일반 formatting으로 raw DB 오류를 노출하지 않는다. Scope cleanup까지 성공해야 진단을 공개한다.

Admin의 `ValidateCreate`와 `ValidateChange`는 immutable BoundForm을 받는 명시적 읽기 검증 port다. CSRF·현재 admission과
선택 목록 권한 뒤에 호출하고, change는 observed revision도 먼저 확인한다. Invalid Form도 callback에 전달한다.
Callback은 같은 read snapshot에서 필요한 현재 권한·row·관계와 DB 제약을 확인하며 직접적인 validation.Reject만 입력
오류로 전달한다. Site의 호출 순서는 Manager의 최종 저장 검사를 대신하지 않는다.

기본 User 수정의 CheckUserChange는 User 모델과 후보의 ID/revision 결합을 먼저 확인한다. 현재 저장된 actor의 change_user
권한과 대상 row/revision, unique/constraint 조회는 하나의 management snapshot을 사용한다. 기본 User creation의 별도
대소문자 무시 username 검사와 password profile 순서는 기존 UserCreationForm 계약을 유지한다.
Group/Permission create/change도 올바른 모델과 unsaved/current ID·revision을 I/O 전에 확인하며 같은 management snapshot의
현재 actor·row/revision에서 검사한다. Group의 유효한 permission 선택은 현재 존재를 다시 확인한다. 입력 진단과 관계 I/O 오류는
분리하며 최종 저장의 grant 한도·관계 변경·revision·audit는 기존 write transaction이 계속 소유한다.
Helpdesk는 SnapshotReader를 명시적으로 요구하며 parent category와 수정 row를 같은 read snapshot에서 확인한다.
TicketLabel은 projection 이후 달라질 수 있는 양 endpoint의 category 소속도 다시 확인하고, 거부된 endpoint는 tuple 검사에서
제외한다. 범위 밖 관계의 존재를 unique_together 오류로 공개하지 않는다. 저장 transaction의 최종 검사는 별도로 유지한다.
ServiceReport도 Ticket 소속을 먼저 확인하여 거부된 관계의 OneToOne unique를 조회하지 않는다. Label은 서버가 고정한
category를 constraint 후보에 명시적으로 공급해 제품의 scoped name을 확인한다. Excluded initial이나 client가 category를
소유하지 않으며 일반 ModelForm의 제외 field를 암묵적으로 다시 포함하는 동작으로 확장하지 않는다.

모델 default, Form initial, 제출 생략과 JSON omission default를 같은 것으로 처리하지 않는다.
일반 ORM 저장은 모델 full_clean이나 email 문법을 암묵적으로 호출하지 않는다. 기존 데이터의 문법상 잘못된 값을
정리하거나 조회에서 숨기지 않는다. JSON API의 명시적 노출·required/empty/partial 정책은 유지한다.
모델 default가 있는 field는 제출이 생략되고 정리 값도 empty인 경우 기존/default 후보를 유지한다. 명시적 empty는
그 후보를 덮어쓴다. Checkbox와 select-multiple의 생략은 실제 false/빈 목록이다. `Form.Submitted`가 원래 presence와
반복 값을 보존하고 값 getter는 복사본을 반환한다. 원래 password도 Form 수명 동안 보존되므로 일반 formatting과
password widget은 이를 계속 감춘다. 직접 field를 선택해 읽는 것은 caller의 명시적 접근이다.

Admin은 초기 제출과 저장 직전 재검증 모두 원래 제출값을 사용한다. Cleaned 문자열을 다시 직렬화해 normalizer를
중복 적용하지 않는다. 수정의 후보 initial은 현재 인가된 Get의 row에서 얻고 observed revision을 재확인한다.
호출자가 넘긴 Form.Initial은 기존 값의 권한 근거가 아니다. Pure model validator가 등록된 수정 폼은 제외된 scalar도
포함한 현재 모델 snapshot을 Initial에 제공해야 하며, registry가 PK와 구성된 revision field를 검증된 Snapshot에서 소유한다.
이 두 값은 Initial에서 생략할 수 있고 명시했다면 Snapshot과 일치해야 한다. 이 추가 값은 모델 검증에만 쓰고
rendered initial·typed mutation 입력에 포함하지 않는다. 저장 직전 재조회도 최종 transaction의 revision fence를 대체하지 않는다.

기존 nonnullable checkbox의 임의 문자열 거부와 정수의 TextInput/inputmode는 이번 변경에서 유지한다.
Native 160개 입력 대조의 checkbox 4개 의미 차이와 8개 integer profile의 widget 차이를 전체 동등성으로 합치지 않는다.
모델에서 제외한 field 이름을 ExtraFields가 다시 사용하는 경우도 명시적으로 거부한다. 고정 Django ModelForm은 이를
허용하지만 GoDj의 모델 입력과 추가 command 입력은 겹치지 않는 소유권을 가진다. DB 16개 사례 중 이 1개는
construction 단계의 shadows_model 오류로 검증하며 나머지 15개의 native DB 후처리 일치와 구분한다.
`PostClean`은 pure `Clean`의 명시적 변경 집합과 read-only `Validators`를 소유한다. Field cleaning 뒤 실행하고
field 오류가 있어도 모델 후보를 전달한다. Clean의 변경과 입력 오류는 함께 보존하며 필드 검증을 다시 실행하지 않는다.
후속 validator와 DB unique/constraint는 변환된 후보를 읽는다. Form.Cleaned와 원래 제출은 바꾸지 않으며,
exclusion은 Form 선택·오류에서 계속 계산하므로 제외 필드의 변경만으로 DB 사전 검사의 대상을 늘리지 않는다.

`PostClean.Fields`는 선택 여부와 별개로 변경할 수 있는 stored scalar의 명시적 목록이다. PK·ManyToMany·command input과
알 수 없는 field는 포함할 수 없고, 미선언 변경 또는 값 표현 불일치는 부분 결과 없이 construction/execution error가 된다.
모델 field의 email/choices/blank/max-length 검증을 clean 뒤 암묵적으로 반복하지 않는다. 최종 typed writer의 저장 표현
검사와 DB 제약은 그대로다. Python의 mutable instance와 무시되는 clean 반환값 대신 immutable 후보와 명시적 변경 집합을
사용한다. 이는 Go API 소유권의 차이이며 native 반환 프로토콜과 일치한다고 주장하지 않는다.

유효한 `BoundForm.Input`은 선택 입력과 command input 외에 **실제로 반환한 명시적 clean 변경**을 포함한다. 따라서
제외한 field도 서버가 선언한 파생 값을 typed adapter에 전달할 수 있다. 선언만 하고 변경하지 않은 제외 field를 추가하지 않는다.
Input/typed instance 준비는 I/O를 소유하지 않고 저장 권한도 부여하지 않는다. Consumer는 현재 인가된 typed row/default에
해당 입력을 적용하고 최종 transaction/revision·관계 인가·DB 제약을 유지한다. 일반 ORM Save에는 full_clean을 추가하지 않는다.

Admin은 일반/생성 전용 PostClean을 복사해 소유한다. 후처리가 있는 수정 폼은 모든 stored scalar의 현재 initial을 요구한다.
PK·revision은 registry 소유이며 revision의 clean 출력 선언도 거부한다. 명시적 출력 field는 typed 입력·Snapshot 구조 검사와
변경 field/audit reconciliation에 포함하지만 editable Form으로 만들지 않는다. HTTP의 숨긴 입력 위조는 기존대로 거부한다.
저장 직전에도 원래 제출과 최신 인가 row에서 bind하므로 한 요청에서 재검증이 있을 때 clean을 다시 호출하며, 각 bind에서는
한 번 실행한다. Callback은 pure하고 동시 실행에 안전해야 한다.
전체 ModelForm 저장 자동화·전체 constraint 종류·custom user model과 모든 제품 소비자 연결의 완료는 별도 작업이다.

고정 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`와 DRF 3.18.0을 참조하며
[Django license](../../LICENSE.django)와 [출처](../SOURCES.md)를 따른다. 구현·소비자 연결·환경별 검증은 별도로 기록한다.
