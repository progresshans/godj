# ADR-0063: 모델 선택값과 메타데이터 변경 이력

- 상태: Accepted
- 날짜: 2026-09-19
- 관련 작업: [GDJ-0076](../../work/0076-model-choices-and-metadata-migrations.md)
- 보완: [Form](0043-safe-template-and-model-form-validation.md), [migration writer](0052-project-linked-deterministic-makemigrations.md), [SQL 출력](0055-project-linked-deterministic-migration-sql-projection.md), [OpenAPI](0058-model-derived-openapi-and-operation-ownership.md)

## 선언과 소유권

Char/Text의 string과 일반 Integer의 int64에 순서 있는 value/label 선택값을 선언한다.
`schema.Choices(schema.Choice(value, label), ...)`의 원본은 `ir.Field.Choices`다. 기본값과 선택값은 같은 `ir.Scalar`를 사용한다.
선택값이 없으면 nil이며 명시적 빈 목록, 중복 값, 종류 불일치, 잘못된 텍스트와 Char 길이 초과는 선언 오류다.
표시명은 빈 문자열을 허용하며 선택 순서·표시명·값은 모두 schema identity에 포함한다.
옵션·정규화·historical snapshot·generated descriptor와 소비자 accessor는 호출자에게 변경 가능한 내부 slice를 넘기지 않는다.

선택값은 DB CHECK가 아니다. 일반 Create/Update/Save와 query는 기존 scalar 저장 영역을 유지한다.
선택 목록에서 제거된 기존 값과 목록 밖 기본값이 있을 수 있다. 표시명 lookup은 값이 없으면 실패를 돌려주고,
Admin 목록·편집 화면과 serializer 출력은 원래 저장 값을 보존한다. 모델 전체의 `full_clean` API를 구현했다고 주장하지 않는다.

## Form과 Admin

모델 choices는 기본 Select widget으로 투영한다. Form 입력은 scalar 변환·trim 전에 제출 문자열을 정확히 비교한다.
정수 0의 HTML 값은 `"0"`이며 `"01"`, `"+0"`, `" 0 "`는 같은 선택값으로 해석하지 않는다.
필수 빈 입력은 required, 목록 밖 입력은 invalid_choice다. 선택된 값에도 application validator를 적용한다.
선택값이 있는 nullable Text의 빈 Form 제출은 null이고, 선택값 없는 Text의 기존 빈 문자열 정책은 유지한다.

Admin은 value와 label을 HTML escape하고 scalar 입력과 Select를 중복 렌더링하지 않는다.
목록 밖 초기값·거부된 제출값은 선택된 임시 option으로 보존해 첫 허용 값으로 바뀌지 않게 한다.
이를 다시 제출하면 정상 선택값 검증을 거친다. 표시명은 화면에만 사용하고 저장·권한·audit snapshot을 대체하지 않는다.

## JSON과 OpenAPI

선택값 입력은 기존 strict JSON scalar type을 유지한다. Integer에 JSON 문자열 `"0"`나 bool을 coercion하지 않는다.
이 부분은 DRF ChoiceField와 [의도한 차이](../DEVIATIONS.md#dev-0012--choice-json-입력도-scalar-type을-유지)다.
String choices는 trim하지 않는다. 명시적 빈 문자열 choice 또는 `WithAllowEmpty`는 빈 문자열을 허용하고 nullability와 별도로 처리한다.
Full의 생략 기본값과 Partial의 생략 무변경 의미는 유지하며 기본값에는 입력 membership 검증을 다시 적용하지 않는다.

Request schema의 enum은 정확히 허용하는 string/int64 값이다. Nullable은 enum을 가진 non-null branch와 null branch를 나눈다.
외부 생성기가 enum을 보존하는지 고정 ogen client의 생성 결과로 검사한다. Response는 기존 행을 읽을 수 있도록 enum으로 좁히지 않는다.
양쪽의 `x-godj-choices`는 순서 있는 value/label 표시 정보다. 입력 enum 밖의 생략 기본값은
`x-godj-omission-default`로 알리고, 생성 client가 잘못된 값을 제출하도록 standard default에 넣지 않는다.
권한, CSRF, parser 제한과 실제 입력 검증은 계속 서버가 수행한다. 생성된 enum type을 명시적으로 cast해도 서버 검증을 통과하지 못한다.

## Metadata-only AlterField

choices 변경은 `AlterField`의 전체 Before/After field로 새 migration에 기록한다. 기존 파일을 고치거나 현재 모델에서 과거 값을 추측하지 않는다.
이 operation은 choices만 바뀌는 실제 차이에 한정한다. column·kind·nullability·default·relation·이름의 변경은 명시적으로 거부한다.
전진·역방향 state는 source field와 정확히 일치해야 하며, 변경 전 operation view는 별도 field slice로 보존한다.
label·order·목록 추가/제거도 autodetector가 작성하고 같은 desired state를 반복하면 clean이어야 한다.

SQLite와 PostgreSQL은 revision-fenced capability `AlterFieldChoices`에서 이 operation을 처리한다.
실제 DDL을 실행하지 않아도 정확한 intent·operation 순서·물리 catalog·revision/recorder의 transaction 검증을 수행한다.
같은 migration의 relation target 변경과 뒤따르는 source model 변경, 물리 AddField와의 혼합도 각각의 정확한 경계를 갖는다.
순수 SQL 출력은 이 operation에 0개 문장을 반환하고, 혼합 migration에서는 물리 operation의 SQL만 반환한다.
직접 raw SQLite migration 경로는 choices 변경을 capability error로 거부한다.

## 출처와 남은 범위

고정 Django 6.1의 Field.validate/formfield, BaseDatabaseSchemaEditor, MigrationAutodetector와 DRF 3.18.0 ChoiceField를 참조했다.
라이선스는 모두 BSD-3-Clause다. [독립 runner](../../conformance/runners/django/choices_reference.py)는 GoDj를 import하지 않고
[원본 관찰](../../schema/testdata/choices-django61.json)에 19개 입력의 Form·serializer·model clean, 일반 저장과 metadata SQL 결과를 기록한다.
GoDj의 직접 비교는 Form/serializer와 명시한 JSON type 차이를 소유한다. Python model clean 관찰을 GoDj 구현 PASS로 세지 않는다.

Callable/grouped choices, 다른 scalar choices, Python enum 내부 ABI, 일반 physical AlterField와 전체 모델 validation은 남은 범위다.
완성 목표의 범위를 줄이지 않으며 환경별 실제 실행과 미검증 범위는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 기록한다.
