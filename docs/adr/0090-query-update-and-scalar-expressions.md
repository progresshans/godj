# ADR-0090: QuerySet 갱신과 현재 행의 scalar 표현식

- 상태: Accepted design — 공통 기반 구현; 환경별 검증과 업무 연결은 TEST_EVIDENCE/활성 work가 소유
- 날짜: 2026-10-06
- 작업: [GDJ-0111](../../work/0111-query-update-and-writable-expressions.md)

## 입력과 공개 API

`QuerySet.Update(ctx, assignments...)`는 원 query의 조건에 맞는 행에 같은 assignment 집합을 한 native UPDATE로
적용하고 matched-row count를 반환한다. 이미 같은 값을 가진 행도 센다. 없는 행을 생성하거나 행별 Save/Clean을
호출하지 않으며, choices·Form 검증·application audit는 consumer가 소유한다. Primary key도 명시적으로 할당할 수
있지만 실제 고유성/FK 제약을 통과해야 한다. 실패 결과에는 부분 count를 게시하지 않는다.

`Assign(field, value)`, `AssignExpression(field, F(field))`, `AssignNull(nullableField)`가 typed 입력이다.
`Value`, `NullValue`, `Add/Subtract/Multiply/Divide`와 두 operand를 받는 `...Expressions`, 정수 `Remainder`,
`Negate`로 현재 행의 값을 조합한다. Model과 값 타입은 sealed generic capability로 연결한다. FK의
`ForeignKeyField`/`NullableForeignKeyField`도 원 모델의 concrete 정수 key 열을 나타내며 관계 join이나 관련 객체가 아니다.
생성 `ModelFields.<FK GoName>`은 같은 metadata의 읽기·field reference·명시적 write mask를 제공한다.

`UpdateDynamic(ctx, DynamicUpdateInput{Field: "amount", Value: DynamicF("amount").Add(int64(1))})`도 같은
Schema IR metadata와 scalar AST로 준비한다. 동적 이름은 schema의 concrete field name이다. Go 필드 이름이나
관계 traversal을 SQL 문자열로 해석하지 않는다. `DynamicValue`는 닫힌 scalar 입력을 즉시 소유하며 임의 객체,
aggregate, mutable raw container와 알 수 없는 이름은 I/O 전에 거부한다.

Manager와 생성 root/eager/prefetch facade는 같은 연산을 제공한다. 기존의 한 모델·PatchInput 저장 메서드는
`Manager.Patch`로 이름을 분리한다. 미배포 내부 API이므로 이전 Update signature를 유지하는 overload/호환 layer를
만들지 않는다. 모델 Save/update_fields와 BulkUpdate에 expression을 넣는 별도 표면은 아직 제공하지 않는다.

## 불변 AST와 수치 의미

`ScalarExpression`은 literal, source field, binary arithmetic, negate의 private 불변 tree다. Boolean 조건 AST와
별도 값 표현식이지만 typed/dynamic 경로마다 두 구현을 만들지 않는다. `ScalarAssignment`가 결과 kind를 target에
결합하고 `QueryUpdatePlan`이 원 predicate·primary key·전체 source metadata와 assignment를 소유한다.
깊이 64, assignment를 합친 전체 scalar node 1024, concrete source metadata와 backend parameter 예산을 검증한다.
중복 target, 다른 모델/열 metadata, 빈 expression과 slice/window는 빈 assignment·빈 query에서도 거부한다.

상수와 같은 kind의 field 복사는 현재 scalar codec 전체에 적용한다. 숫자 연산은 같은 kind의 int64 또는 float64다.
문자열·Boolean·정수/float 사이의 암묵 변환을 하지 않는다. Integer 나눗셈은 native truncate-toward-zero,
나머지는 왼쪽 값의 부호를 따른다. NULL은 전파되고 untyped NULL은 상대 operand의 숫자 kind를 따른다.
Non-null target의 NULL literal은 preflight에서 거부하지만 nullable field/expression을 대입할 때 실제 NULL인 행은
DB 제약으로 전체 statement가 실패한다. Float의 특수값은 [ADR-0068](0068-binary64-field-and-finite-json-boundaries.md)의 모델 범위를 따른다.

SQLite는 overflow한 정수 연산을 REAL로 승격할 수 있다. 각 integer field/연산 결과를 statement 내부의
stateless `godj_int64` 함수로 검사하여 중간값 손실과 affinity의 재반올림을 거부한다. 이 함수는 행을 Go로 읽어
계산하는 대체 구현이 아니며 SQL native 연산 뒤 storage class를 검사한다. 오류는 UPDATE 전체를 중단한다.
PostgreSQL은 native bigint overflow/zero-division 오류와 원 SQLSTATE를 보존한다. SQLite의 zero-division NULL은
nullable 여부에 따라 저장되거나 NOT NULL 오류가 된다. Backend별로 관찰한 차이를 공통 성공으로 합치지 않는다.

Decimal 상수는 선언된 precision/scale에 정확히 맞아야 한다. Decimal field 복사는 target이 source의 선언 범위를
좁히지 않을 때만 허용한다. SQLite exact BLOB과 PostgreSQL NUMERIC에 공통으로 적용할 연산·결과 precision·rounding
계약을 정하기 전에는 Decimal arithmetic을 명시적으로 거부한다. Float로 바꾸는 구현이나 native DB의 임의 반올림을
정확한 Decimal 지원으로 계산하지 않는다. Temporal/function/subquery/aggregate expression도 남은 기능이다.

## 선택, 실행과 동시성

한 statement의 모든 RHS는 수정 전 같은 행을 참조한다. 여러 assignment의 입력 순서로 갱신 후 값을 다시 읽지
않는다. 원 조건·관계/collection scope identity를 보존하고 ordering·distinct·eager·prefetch·읽기 row-lock 모양은
갱신 범위를 제한하지 않는다. Slice는 assignment가 비어도 거부한다. 빈 assignment나 정적으로 빈 query는
context·구성·session·capability를 검증한 뒤 I/O 없이 0을 반환한다.

SQLite는 갱신 전 membership을 materialized CTE로 고정하고 이름 충돌을 피한다. PostgreSQL의 실제 join 없는
predicate는 직접 UPDATE WHERE에 둔다. 잠금 대기 뒤 바뀐 행을 native 조건으로 재평가하고 RHS도 그 현재 행을
사용한다. Trimmed FK와 correlated NOT EXISTS도 같은 경계다. 실제 join의 membership은 statement snapshot을
따른다. 모든 조건을 애플리케이션의 미리 읽은 key 목록으로 바꾸지 않는다.

Owned transaction 또는 borrowed savepoint 안에서 실행한다. 부모의 source/session 결합, callback 한 번의 수명,
취소·child 사용 중인 parent·종료한 handle·read-only snapshot의 거부를 유지한다. Commit 확인 뒤 count를 게시하고
borrowed 결과는 부모 commit 전까지 provisional이다. 뒤쪽 실패와 확인 가능한 rollback은 원 행을 복원한다.
Literal COMMIT/rollback의 불확실성은 성공이나 자동 retry로 바꾸지 않는다. 단건 Patch와 같은 기존 scope guard를 쓴다.

## Helpdesk의 명령과 ORM 의미 구분

선택 티켓 우선순위 명령은 `POST /api/tickets/raise-priority/`와 Admin의 `raise-priority` 작업을 같은 writer에
연결한다. Category의 현재 선택 행을 읽어 변경 예정 집합을 정하고 Low/Normal에 `F(priority) + 1`을 한 statement로
적용한다. NULL은 별도 statement에서 Normal로 설정한다. 원 우선순위와 Category 조건을 함께 남겨 모든 예정 행이
실제로 일치하는지 확인한다. Urgent와 기존 choices 밖의 값은 그대로 두며 수정하지 않은 열·라벨도 보존한다.

전체 입력은 1..40개의 서로 다른 양수 int64 key다. 인증·CSRF·ChangeTicket·ViewLabel을 파싱 전에 확인하며 Admin은
ViewTicket도 요구한다. 모든 선택 행·현재 라벨 무결성·변경 행의 digest·전체 응답·실제 변경 audit가 하나의 relation
transaction에 속한다. Unchanged 행은 digest를 복구하거나 audit를 추가하지 않는다. API는 모든 선택 행을 입력
순서로 반환하고 Admin은 실제 변경 수를 표시한다. 뒤쪽 실패·취소·불확실한 결과를 부분 성공이나 재시도로 바꾸지 않는다.
NULL을 Normal로 올리는 업무 정책은 일반 scalar AST의 NULL 전파와 별개다.

## Cache와 검증 경계

성공한 UPDATE는 0건/빈 assignment도 호출 query 자신의 cache를 비운다. 같은 query의 복사본은 이 상태를
공유하며 derived query와 이미 반환한 model은 독립 snapshot이다. 보통의 실패는 기존 cache를 유지하고
commit/rollback unknown은 오래된 성공 cache를 게시하지 않도록 비운다. Borrowed update 후 부모 rollback이
이미 반환한 model을 되돌리거나 다른 query cache를 전역 무효화한다고 주장하지 않는다.

평가 generation이 바뀌면 이전 in-flight 읽기의 값/관계 graph를 새 cache로 게시하지 않고, 그 오류도 새 generation의
읽기로 전파하지 않는다. 이미 진행 중인
호출 자체의 결과와 이후 query cache의 소유권은 구별한다. Eager/prefetch는 원 parent query의 cache를 함께 지우지 않는다.

독립 [Django observer](../../conformance/runners/django/query_update_reference.py)와
[서버 잠금 observer](../../conformance/runners/django/query_update_concurrency_reference.py)는 Go source·expected를 읽지
않는다. 생성 소비자는 실제 양 DB의 행/count·오류/SQLSTATE·SQL 횟수·cache·부모 결과를 대조하고 명시적인
[Go 차이](../DEVIATIONS.md#dev-0021--query-update의-정확한-정수와-명시적-변환)를 별도 assertion으로 검증한다.
미지원 union·distinct-fields·projection update와 Decimal 연산을 Django parity로 계산하지 않는다.
구현·현재 source의 실제 실행·전체 플랫폼·Helpdesk 업무 완료는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에서 구분한다.
