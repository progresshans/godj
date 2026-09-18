# ADR-0060: TextField와 Form widget·빈 입력의 의미

- 상태: Accepted
- 날짜: 2026-09-19
- 관련 작업: [GDJ-0073](../../work/0073-text-field-and-multiline-model-forms.md)
- 보완: [Form](0043-safe-template-and-model-form-validation.md), [모델 serializer](0046-json-serializer-and-session-authenticated-article-api.md), [OpenAPI](0058-model-derived-openapi-and-operation-ownership.md)

## 모델과 저장

긴 본문을 길이가 제한된 CharField로 표현하지 않도록 `schema.TextField`를 추가한다. Schema IR의 kind는 `text`이며
Go model은 string 또는 nullable `*string`이다. 현재 저장 길이 제약은 없고 문자열 application default를 지원한다.
SQLite·PostgreSQL은 TEXT를 사용한다. PostgreSQL catalog는 text type·typmod·nullability를 검사한다.
Application default를 영속 SQL DEFAULT로 바꾸지 않으며 기존 필드의 Char→Text 변경은 현재 autodetector 범위 밖이다.

Typed/dynamic query·CRUD·F·projection·Min/Max는 공통 string Query AST와 runtime을 사용한다. 생성 descriptor에는
Text kind를 보존하고 nullable pointer는 model/cache 경계에서 복사한다. 관계 terminal은 기존 nonnullable implicit-exact
범위에 Text를 추가한다. Nullable terminal이나 새로운 관계 lookup을 지원한 것으로 표현하지 않는다.

## Form의 값과 표시

Form의 cleaned value type과 widget은 독립이다. `forms.Widget`의 TextInput/Textarea/Checkbox 중 제출 표현이 구현된 조합만
시작 시 허용한다. String Form은 TextInput 또는 Textarea, integer는 TextInput, boolean은 Checkbox를 사용한다.
`forms/model`의 TextField는 Char Form과 Textarea로 투영한다. `WithWidget`으로 표시를 바꿔도 값의 cleaning은 바뀌지 않는다.

고정 Django 6.1의 model field form을 참조한다. Optional nullable Char의 빈 입력은 Null이지만 nullable Text는 빈 문자열이다.
이를 widget에서 추론하지 않고 `forms.WithEmptyValue`로 명시한다. 빈 문자열 또는 nullable field의 Null만 허용한다.
Text Form은 nullable 여부와 관계없이 빈 문자열 정책을 선택하며 required 입력의 빈 값은 계속 오류다. 앞뒤 공백은 기존
Char Form처럼 제거하고 내부 줄바꿈은 보존한다. 입력 default는 제출한 빈 값을 대체하지 않는다.

초기 model 값은 별도로 주입한다. 명시적으로 저장된 NULL과 제출한 빈 문자열은 서로 다른 값이며 기존 Changed 판정도 이를
구분한다. 이 결정은 Django의 모든 `has_changed`·ModelForm 저장 정책에 대한 호환 주장이 아니다.

Admin은 같은 widget metadata를 사용한다. Textarea 내용도 일반 template escaping을 거치며 trusted HTML로 바꾸지 않는다.
HTML parser가 시작 직후의 newline 하나를 제거하므로 태그와 값 사이에 newline 하나를 항상 넣는다.
HTTP body, serializer parser와 template의 기존 자원 제한은 TextField 저장 길이와 별개로 유지한다.

## API와 모델 성장

모델 serializer는 기존 string normalization·full/partial·null/생략·default 정책을 재사용한다. String default의 앞뒤 공백도
기존 serializer 정책에 따라 정리된다. 저장용 default를 그대로 쓰는 ORM Create와 구분한다. API 노출은 application allowlist가
정하고 OpenAPI에는 string/null을 투영한다. 저장 길이 제약이 없다는 이유로 HTTP 입력 한도를 없애지 않는다.

Helpdesk의 nullable resolution은 새 `0003_ticket_resolution` migration으로 추가한다. 기존 0001·0002는 보존하고 기존 행은
NULL로 남긴다. API 생략/null은 NULL, 명시적 빈 문자열은 빈 값이며 Admin의 빈 Text 제출은 빈 문자열로 저장한다.
실제 DB의 적용·reverse·재적용과 외부 ogen client를 통해 이 차이를 검증한다. 실행 환경과 결과는
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.

## 출처와 제한

저장소 lock의 Django 6.1 `django/db/models/fields/__init__.py`의 CharField/TextField, `django/forms/fields.py`의 CharField와
`django/forms/templates/django/forms/widgets/textarea.html`을 참조했다. BSD-3-Clause이며 Python 내부 구조를 복제하지 않는다.
[Reference runner](../../conformance/runners/django/text_field_reference.py)와 [고정 관찰값](../../forms/model/testdata/text-django61.json)은
독립 작성한 입력 roster(`derived=false`)의 문자열 cleaning·오류 code·widget만 비교한다. 임의 collation·모든 Text 옵션·범용 widget·relation input은
별도 미완료 범위다.
