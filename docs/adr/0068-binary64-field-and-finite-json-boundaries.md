# ADR-0068: Binary64 모델과 finite JSON 경계

- 상태: Proposed
- 날짜: 2026-09-20
- 관련 작업: [GDJ-0090](../../work/0090-floating-point-models.md)

이 결정은 독립 관찰을 바탕으로 한 구현 준비다. 제품 코드의 채택·실행 완료는 CURRENT와 TEST_EVIDENCE에서 별도로 확인한다.

## 값과 저장의 경계

FloatField의 Go 모델 타입은 float64, nullable은 *float64를 사용한다. Zero는 실제 값이며 nil과 구분한다.
Finite binary64의 전체 범위와 subnormal, ±Infinity와 NaN의 모델 의미를 유지한다. NaN의 payload/sign은 모델의 별도 값이 아니며
IR·query·scanner에서는 하나의 quiet NaN으로 정규화한다. ±0의 sign은 값 표현에 보존하되 DB의 숫자 equality와 구분한다.

IR default는 binary64 bits의 고정 길이 lowercase hex 표현을 사용한다. JSON에 NaN/Infinity token을 넣거나 일반 JSON 숫자로
바꾸어 정밀도·zero sign을 잃지 않는다. 선언 builder는 값을 정규화하고 raw IR/definition은 canonical 표현과 한도를 엄격히 검사한다.
생성된 default는 bit pattern에서 float64를 복원한다. Query AST도 float arm을 구분하며 typed/dynamic 경로와 backend compiler가 이를 공유한다.
Integer·Duration·Date/Time의 묵시적 ORM coercion을 추가하지 않는다.

SQLite는 REAL, PostgreSQL은 DOUBLE PRECISION을 사용한다. 고정 Django SQLite는 NaN을 NULL로 바꾸지만 GoDj는 NaN을 쓰거나
비교하는 statement를 backend에서 I/O 전에 명시적으로 거부한다. Nullable의 NULL 의미와 사용자 값의 구분을 보존하기 위해서다.
SQLite의 -0 → +0 저장 정규화는 명시적으로 관찰한다. PostgreSQL은 signed zero·subnormal·NaN·Infinity를 저장할 수 있으며
NaN=NaN, NaN>Infinity의 native comparison을 유지한다. SQLite의 제약을 전체 모델 범위로 확장하지 않는다.

## Form·JSON·실제 소비자

Form은 NumberInput(step=any)과 유한한 binary64 값을 사용한다. 빈 입력은 null/required로 구분한다.
Python float 문자열의 decimal/exponent·자리 사이 underscore·Unicode decimal digits와 외부 whitespace를 지원하되
hex float 등 다른 grammar를 묵시적으로 허용하지 않는다. 변경 감지는 Django Form의 숫자 equality를 따라 ±0을 같게 취급한다.

공통 JSON parser는 원문 Number를 보존한다. Float serializer가 integer·decimal/exponent·boolean·string을 필드 입력으로 변환하며
binary64 rounding은 이 경계에서 발생한다. 큰 JSON integer의 conversion overflow와 문자열의 길이 한도도 분리한다.
Serializer는 NaN/Infinity를 미리 invalid로 거부한다. DRF는 이를 validated_data에 넣은 뒤 JSONRenderer에서 실패할 수 있으므로
이 선제 거부를 의도적 차이로 기록하고 정확한 raw selector와 오류를 검증해야 한다. Typed model의 non-finite 출력도 명시적인 오류다.
숫자의 문법·byte 한도·중복 key·UTF-8/NUL·문서/깊이 한도는 공통 JSON 계층이 계속 소유한다.

Helpdesk nullable effort의 create/PUT/PATCH·Admin·OpenAPI/독립 client를 함께 연결한다. 생략은 보존, null은 비우며
유한한 값의 숫자 equality가 같으면 no-op이다. 따라서 ±0 변경만으로 UPDATE를 발생시키지 않는다. 새로운 NaN 허용 model API와
finite인 앱 입력을 혼동하지 않고 validation-before-transaction, 권한/CSRF, rollback·reopen과 실제 저장 결과를 확인한다.

OpenAPI canonical domain은 number/double과 finite min/max·nullable이다. 실제 client의 float64와 외부 HTTP JSON을 사용해
subnormal·정밀도 경계·±0·overflow/invalid·생략/null을 확인한다. Serializer의 넓은 coercion grammar가 client 입력 타입이라는 뜻은 아니다.

## 출처와 남은 검증

고정 Django 6.1 model/form FloatField, DRF 3.18.0 FloatField와 JSONRenderer의 public API를 독립 실행했다(BSD-3-Clause).
[독립 runner](../../conformance/runners/django/float_reference.py)와 [raw 관찰](../../internal/floattest/testdata/django61.json)은
model 89·Form 136·serializer 360·JSON number 16, 실제 SQLite lifecycle/query/relation과 특수값의 원래 동작을 보존한다.
Python 네 버전의 재생과 PostgreSQL의 별도 binary storage probe는 제품 지원을 대신하지 않는다.
구현 과정에서 위 제안과 다른 경계가 필요하면 이 결정을 갱신하고 실제 실패 경로를 다시 검증한다.
