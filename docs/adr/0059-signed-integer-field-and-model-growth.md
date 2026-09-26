# ADR-0059: 일반 정수 필드와 기존 모델의 성장

- 상태: Accepted
- 날짜: 2026-09-19
- 관련 작업: [GDJ-0072](../../work/0072-integer-field-model-growth.md)
- 보완: [typed scalar](0041-typed-scalar-comparisons-and-field-references.md), [Form](0043-safe-template-and-model-form-validation.md), [OpenAPI](0058-model-derived-openapi-and-operation-ownership.md)

## 맥락과 결정

자동 ID와 FK 저장값 외에는 정수를 선언할 수 없어 Helpdesk priority 같은 업무 필드를 같은 모델 흐름에 연결할 수 없었다.
`schema.IntegerField`는 플랫폼에 독립적인 signed int64다. IR kind는 `integer`이며 nullable과 명시적 정수 application default를
지원한다. Python 객체 구조나 Go의 플랫폼 크기 `int`를 저장 의미로 사용하지 않는다. Django 6.1의 `BigIntegerField`가 범위와
저장 표현의 참조이며 Django의 32-bit `IntegerField`를 같은 범위로 구현했다는 주장은 하지 않는다.

## 저장과 typed capability

SQLite와 PostgreSQL에서 일반 정수는 `BIGINT`이며 nullability를 보존한다. 일반 정수에 자동 증가·identity·PK 의미를 부여하지
않고 application default를 영속 SQL DEFAULT로 바꾸지 않는다. PostgreSQL의 실제 catalog 검사도 int8·nullability·identity를 확인한다.
IR·historical definition·생성 model·serializer가 같은 default를 소비한다. 생성 코드는 literal을 `int64(...)`로 표현해
nullable default의 포인터 타입과 32-bit compile에서도 범위를 보존한다.

`orm.AutoField[M]`는 읽기만 가능한 ID selector다. `orm.IntegerField[M]`와 `orm.NullableIntegerField[M]`는 수정 가능한 selector이며
typed Save mask에 사용할 수 있다. 둘의 비교와 정렬·F reference는 int64를 사용하고 nullable projection은 `*int64`, Min/Max는
`Optional[int64]`를 반환한다. 반환 포인터와 cache snapshot은 공유하지 않는다. 미배포 생성 ABI를 함께 갱신하며 옛 ID 타입을
유지하기 위한 alias를 두지 않는다. 동적 PK 쓰기도 기존 runtime 검증으로 거부한다.

일반 정수의 typed/dynamic query는 기존 Query AST를 쓴다. 관계의 terminal 정수는 기존 nonnullable implicit-exact 범위에
추가하며 nullable terminal·관계 비교 연산의 확대는 별도 작업이다. 미지원 경로를 null의 잘못된 해석으로 처리하지 않는다.

## Form/Admin과 API

정수 Form은 float를 거치지 않고 signed decimal을 읽는다. 고정된 Django 6.1 `BigIntegerField.formfield()`의 문자열 입력을
관찰해 부호·선행 0·정수 사이 underscore·0뿐인 소수부·범위 오류 코드를 비교한다. Unicode decimal digit은 Go의 Unicode
table을 사용한다. 이 비교는 명시한 입력 roster의 실제 관찰이며 모든 Unicode 버전의 동등성이나 Python 객체 입력을 주장하지 않는다.

정수 0은 값이다. optional empty는 nullable일 때 Null이며 optional nonnullable Form 설정은 시작 시 거부한다.
required 정수의 기본 초기 화면은 빈 값이고 명시적 default가 있으면 그대로 표시한다. 제출한 빈 값을 default로 대체하지 않는다.
Admin은 int64를 정확히 표시·재검증하며 숫자 입력은 decimal text와 `inputmode="numeric"`을 사용한다.

Integer serializer는 canonical int64 Value만 받는다. 공통 JSON parser는 [ADR-0067](0067-duration-model-range-and-number-input.md)에 따라
다른 숫자의 원문도 보존하지만 이를 Integer 필드에 암묵적으로 변환하지 않는다. Form에서 허용하는 `+000.0`은 JSON 숫자 문법이 아니다.
serializer의 full/partial, 생략/null/0/default와 read-only 규칙을 재사용하고 OpenAPI·ogen client도 같은 모델 필드에서 갱신한다.
API 노출 필드는 application의 allowlist가 계속 소유한다.

## 기존 데이터의 성장

Helpdesk의 `0001_initial`은 보존한다. `makemigrations`가 작성하는 `0002_ticket_priority`는 nullable/no-default 필드를 추가해 기존
행을 NULL로 남긴다. 별도 generated consumer는 신규 모델의 required/default/full-range CRUD를 검증한다. 기존 행에 대한
nonnullable/default backfill, 임의 AlterField나 data migration을 이 기능에 포함했다고 표현하지 않는다.

실행 검증은 실제 이전 schema에 입력한 데이터의 적용·reverse·재적용, 양 DB의 Admin/API, generated consumer와 외부 client,
고정 Django 관찰값·생성물 drift·자동 ID 쓰기 거부를 소유한다. 환경별 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 기록한다.

## 출처

참조 소스는 저장소 lock의 Django 6.1 `django/db/models/fields/__init__.py`의 `BigIntegerField`와
`django/forms/fields.py`의 `IntegerField`이며 BSD-3-Clause다. 입력과 관찰값은
[reference runner](../../conformance/runners/django/integer_field_reference.py)와 [fixture](../../forms/testdata/integer-django61.json)에 있다.
공식 설명: [BigIntegerField](https://docs.djangoproject.com/en/6.1/ref/models/fields/#bigintegerfield),
[Integer form field](https://docs.djangoproject.com/en/6.1/ref/forms/fields/#integerfield).
