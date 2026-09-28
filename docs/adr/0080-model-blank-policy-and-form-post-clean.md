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
포함한 현재 모델 snapshot을 Initial에 제공해야 하며, registry가 PK를 소유한다. 이 추가 값은 모델 검증에만 쓰고
rendered initial·typed mutation 입력에 포함하지 않는다. 저장 직전 재조회도 최종 transaction의 revision fence를 대체하지 않는다.

기존 nonnullable checkbox의 임의 문자열 거부와 정수의 TextInput/inputmode는 이번 변경에서 유지한다.
Native 160개 입력 대조의 checkbox 4개 의미 차이와 8개 integer profile의 widget 차이를 전체 동등성으로 합치지 않는다.
일반 model clean의 값 변환, 전체 constraint 종류·custom user model과 모든 제품 소비자 연결의 완료는 별도 작업이다.

고정 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`와 DRF 3.18.0을 참조하며
[Django license](../../LICENSE.django)와 [출처](../SOURCES.md)를 따른다. 구현·소비자 연결·환경별 검증은 별도로 기록한다.
