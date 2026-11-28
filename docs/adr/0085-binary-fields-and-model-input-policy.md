# ADR-0085: Binary 필드와 모델 입력 정책

- 상태: Accepted
- 날짜: 2026-10-01
- 구현: [GDJ-0106](../../work/0106-binary-fields-and-model-input-policy.md), 완료

## 결정

`schema.BinaryField`는 임의 바이트를 저장한다. `binaryvalue.Value`는 불변 Go 문자열에 원래 바이트를
보관하며, 외부 `[]byte` 입력과 출력의 소유권을 분리한다. 값 복사·query·생성 모델 사이에 변경 가능한
버퍼를 공유하지 않는다. 영값은 빈 바이트이며 nullable 포인터의 nil만 SQL NULL이다. UTF-8 문자열,
base64 텍스트와 파일 저장 이름은 별도 타입/경계다. 값의 구현 상한은 1 MiB다.

IR의 `binary` scalar payload와 JSON 출력은 표준 padded base64다. 입력은 알파벳·padding·완전한
소비를 검사하며 URL-safe 알파벳, 내부 공백, 누락/추가 padding과 뒤따르는 데이터를 거부한다.
고정 Django처럼 0이 아닌 잔여 pad bits는 받아서 바이트로 정규화한다. Form은 일반 문자열의
공백 제거와 NUL 검사를 먼저 적용하고, JSON 입력은 문자열 또는 nullable null만 받는다.
Python의 임의 객체 변환이나 예외 누출을 재현하지 않고 타입 오류를 명시적인 검증 오류로 반환한다.

`MaxLength`는 디코딩된 바이트 수의 입력 검증이다. 0은 선언한 길이 제한이 없음을 뜻한다.
SQLite BLOB와 PostgreSQL bytea는 같은 바이트를 저장한다. 일반 ORM은 입력 validator를 자동 실행하지
않으며 기존의 선언 길이를 넘는 값을 읽고 출력할 수 있다. 값 자체의 구현 상한은 모든 경계에서 유지한다.
문자열 lookup·텍스트 암묵 변환은 지원하지 않는다. exact/in/isnull·바이트 순서 비교·F 비교·투영·
Count/Min/Max는 공통 Query AST를 사용한다. PostgreSQL의 bytea Min/Max 부재는 compiler가 hex의 C 순서
집계 후 bytea로 복원하여 처리한다. 이는 고정 Django의 PostgreSQL 집계 오류에 대한 명시적 확장이다.
일반 index와 unique는 기존 소유권 검사를 사용하며 backend의 실제 index 크기 오류를 숨기지 않는다.

`schema.Editable(bool)`은 모델에서 투영되는 입력 정책이다. 정규화 IR의 `NonEditable`이 그 원본이며
BinaryField는 기본 true, 다른 일반 scalar는 기본 false다. Auto key와 이미지 소유 크기의 기존
비편집 규칙은 유지된다. 일반 ORM의 서버 측 할당을 금지하는 권한이나 DB 제약은 아니다.
ModelForm의 자동 선택은 비편집 필드를 제외하고 명시적 선택/override/사용자 정의 binding은 거부한다.
Model serializer가 노출한 비편집 필드는 자동 read-only이며, GoDj의 기존 정책대로 제출하면
`read_only` 오류를 반환한다. DRF의 무시 정책과 이 차이를 구분한다. Admin 입력은 같은 모델 정책을 따른다.
편집 정책과 Binary 입력 길이만의 변경은 역사 상태와 revision을 갱신하되 DB 내용을 재작성하지 않는다.

Helpdesk의 `external_payload_digest`는 nullable BinaryField다. 클라이언트는 값을 할당할 수 없고,
허용된 쓰기는 실제 저장 후 다시 읽은 JSON의 canonical bytes에서 SHA-256을 계산한다. PostgreSQL의
JSONB 숫자 정규화 전 입력을 hash하지 않는다. payload·지문·관련 할당·응답 검증은 같은 transaction에
속하며 실패 시 함께 rollback한다. NULL payload와 JSON null은 별개다. 새 nullable 열은 기존 행을
계산하지 않고 NULL로 보존한다. 앱이 실제 Ticket scalar를 저장하여 행 잠금을 얻은 경우에만 저장값을
다시 읽고 지문을 초기화/갱신한다. 읽기·동일값 제출·관계만의 변경은 숨은 Ticket 쓰기를 만들지 않는다.
일반 서버 ORM 쓰기에 적용되는 DB 제약이나 자동 hook은 아니며 해당 호출자는 파생값을 소유한다.
지문은 데이터 관찰용 값이며 인증 서명이나 비밀 값이 아니다.
정규형은 객체 key 정렬·HTML escaping·숫자 token 보존을 포함한다. HTTP renderer의 JSON 문자열 escaping과
저장 정규형이 다를 수 있으므로 응답의 원문 bytes를 그대로 hash한 결과와 동일하다고 보장하지 않는다.

## 근거와 검증 경계

Django 6.1/DRF 3.18.0의 독립 관찰은 실제 SQLite와 PostgreSQL 17.10에서 각각 두 번 수행했다.
7개 입력 profile, 39개 합성 입력, DB의 빈 바이트/NULL/비 UTF-8·조회·길이/입력 정책/index/unique 변경과
역방향·deferred save·rollback을 다룬다. upstream 버전과 모듈 source digest를 결과에 포함한다.
출처와 BSD-3-Clause 표기는 [SOURCES](../SOURCES.md)를 따른다. 이 설계 관찰을 Go 제품 검증으로 전이하지 않는다.
구현 후 테스트는 실제 byte 저장 클래스, 가변 버퍼 소유권, 생성/동적 경로, history/revision 실패,
서버 필드 입력 우회, 실제 DB 정규화와 응답 실패 rollback까지 확인한다.
