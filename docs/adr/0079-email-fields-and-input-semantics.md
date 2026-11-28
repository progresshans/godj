# ADR-0079 — 이메일 필드와 입력 의미

- 상태: accepted, 기본 EmailField와 Identity 소비자 구현·영향 검증 완료
- 날짜: 2026-09-28
- 관련 작업: [GDJ-0101](../../work/0101-email-fields-and-model-input-validation.md)

## 의미와 소유권

EmailField는 Schema IR의 별도 field kind로 보존하며 Go 값과 Query AST에서는 문자열을 사용한다.
모델의 기본 최대 길이는 254자이며 nullable·default·choice·unique와 명시적 길이를 같은 IR에서 가져온다.
SQL 저장은 같은 길이의 CharField와 동일하다. Typed/dynamic·관계 query가 동일한 문자열 AST를 사용한다.
이메일 주소를 lowercase하거나 IDNA로 바꾸는 저장 정규화를 자동 추가하지 않는다.

Form의 EmailField는 기본 320자, model projection은 IR 길이를 사용한다. Unicode 공백 정리 뒤 required·이메일 문법·
길이·NUL의 오류 순서를 고정 Django와 비교한다. EmailInput은 화면 표현이며 별도의 서버 검증을 대신하지 않는다.
JSON 입력은 고정 DRF의 email field를 기준으로 required/null/blank·trim·길이·문법 의미를 구분한다.
Go의 공통 JSON value 경계는 NUL을 field 검증 전에 거부한다. Native DRF의 해당 field 오류 순서와 같다고 집계하지 않는다.
OpenAPI는 정리 전 원문에 `format: email`을 강제하지 않고 `x-godj-email`과 문자열 정규화 정책으로 검증 시점을 설명한다.
응답에는 이메일 문법 제약을 붙이지 않는다. 이는 기존의 문법상 잘못된 저장 값을 generated client에서도 읽을 수 있게 한다.
생략 시 적용하는 serializer default도 입력 정리·문법 검증을 다시 수행하지 않는다. 정적 구성의 타입·nullability·blank·길이 검사는 기존 serializer 계약을 유지한다. OpenAPI의 `x-godj-omission-default`는
서버가 생략에 적용할 값을 기록하며, client에 그 값을 입력으로 전송하도록 지시하는 표준 default로 바꾸지 않는다.
출력과 일반 ORM 저장은 이메일 문법을 암묵적으로 재검증하지 않는다. 기존 데이터의 잘못된 주소를 숨기거나 고쳐 저장하지 않는다.
Model EmailField에 choices가 있으면 기존 문자열 선택 field로 projection한다. 여기서 비교하는 Form 기준은
`field.formfield().clean()`이며 Python `Model.full_clean()`·전체 ModelForm의 후처리를 구현했다는 뜻은 아니다.

CharField↔EmailField는 길이·nullability·default·choice·unique·column 등 다른 속성이 모두 같을 때만 별도의
metadata 변경으로 분류한다. 현재 migration capability·revision·catalog·history의 기존 fence를 통과하며
SQL 스키마가 같은 변환에 데이터 재작성이나 문법상의 기존 값 정리를 붙이지 않는다. 변환과 reverse는 historical kind를 보존한다.
고정 Django SQLite는 이 변환에 table remake를 사용하지만 결과 데이터가 같다. Go의 metadata-only 경로는
SQL text의 동일성을 약속하지 않는 명시적 저장 구현 선택이다. 다른 속성 변경을 이 분류에 섞어 허용하지 않는다.

## 독립 기준과 완료 범위

[Observer](../../conformance/runners/django/email_field_reference.py)는 공유된 synthetic 입력만 읽고
고정 Django 6.1 / DRF 3.18의 model/form/serializer와 실제 DB를 실행한다. Native source hash를 기록하며
Go 출력이나 기대 fixture에서 관측 결과를 만들지 않는다. [Django license](../../LICENSE.django)와
[고정 DRF 출처](../SOURCES.md)를 적용한다.

단계별 구현·생성물·현재 환경의 실행 근거는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 별도로 기록한다.
EmailField와 기본 Identity 소비자의 구현·영향 검증을 완료했다. 전체 플랫폼 검증이나 custom user model의 완료를 뜻하지 않는다.
Custom user model은 이후 별도 모델 역할과 소유권 계약을 요구한다.
