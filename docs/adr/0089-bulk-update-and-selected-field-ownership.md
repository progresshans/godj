# ADR-0089: bulk update와 선택 필드의 소유권

- 상태: Accepted — selected-field AST/backend·ORM/생성 API와 업무 소비 구현; 환경별 검증은 TEST_EVIDENCE가 소유
- 날짜: 2026-10-02
- 구현: [GDJ-0110](../../work/0110-bulk-update-and-ticket-editing.md)

## 입력과 결과

`BulkUpdate`는 명시적 primary-key 상태를 가진 모델 slice와 하나의 nonempty field mask를 받는다.
Typed `BulkUpdateFields`와 동적 `BulkUpdateFieldNames`는 같은 frozen Schema IR의 writable 필드로 해석하고
중복 이름을 schema 순서로 정규화한다. Primary key·columnless relation·다른 모델의 필드는 거부한다.
Batch 크기는 양수 상한이며 한 번만 지정한다. 빈 입력도 설정·context·scope·capability를 검증하고
transaction이나 UPDATE를 열지 않은 채 0을 반환한다.

전체 입력의 key와 선택한 값을 clone하고 검증한 뒤 첫 UPDATE를 실행한다. 선택하지 않은 값은 저장하지 않는다.
원 입력, 이전에 읽은 객체와 query cache는 변경하지 않는다. 모델 Save/Clean과 audit를 자동으로 호출하지 않고
없는 행을 생성하지 않는다. 기본값·Python 내부 객체 상태·객체 필드에 임의 expression을 대입하는 문법은 복제하지 않는다.
Raw nullable FK는 관련 객체 대신 key 또는 SQL NULL을 표현한다.

성공 결과는 native matched-row count의 합이다. 같은 값을 저장해도 matched row에 포함된다. 같은 batch에서
반복된 key는 첫 입력 값을 적용하고 한 번만 센다. 다른 batch의 같은 key는 다시 수정되어 여러 번 셀 수 있다.
필터 밖 또는 없는 key는 count를 줄인다. Caller가 모든 입력의 존재나 고유성을 요구하면 application이 확인한다.
Backend의 음수 count 또는 batch 안의 서로 다른 key 수를 넘는 count는 오류다. 일부 batch의 count를 실패 결과로 게시하지 않는다.

Manager·QuerySet·생성 root/eager/prefetch facade는 같은 generic runtime과 DB 독립 AST를 사용한다.
Relation filter의 별도 collection scope identity를 보존하며 여러 Filter를 하나로 합쳐 의미를 바꾸지 않는다.
Ordering·distinct·읽기 lock·eager/prefetch 결과 모양은 쓰기를 제한하지 않는다. 비어 있지 않은 입력의 slice/window는
명시적으로 거부한다. 빈 입력은 predicate를 평가하지 않으며 이 경우의 slice도 실행하지 않는다.

## Native statement와 transaction

`BulkUpdateSpec`은 현재 predicate·root primary key·선택 열을 결합하고, 불변 `BulkUpdatePlan`은 한 native statement의
key와 값 행렬을 소유한다. Compiler가 실제 식별자·codec·predicate parameter와 native parameter 한도를 검증한다.
CASE의 key parameter를 대상 membership에 재사용한다. 총 scalar 예산은 key를 포함해 65,535개, 행 상한도
65,535개이며 실제 batch는 SQLite 32,766/PostgreSQL 65,535 parameter에서 predicate가 사용한 수를 뺀 한도와
caller 상한 중 작은 값이다. 한 행도 표현할 수 없으면 실행 전에 거부한다.

SQLite는 한 statement의 대상 선택 CTE와 CASE UPDATE를 사용한다. CTE 이름이 root/관계의 물리 table 이름을
가리지 않도록 compiler가 선택한다. PostgreSQL은 실제 join이 없는 predicate를 UPDATE의 WHERE에 유지한다.
잠금 대기 중 다른 transaction이 행을 바꾸면 native 조건 재평가가 현재 행에 적용되어, 더 이상 조건에 맞지 않는
행은 수정하지 않는다. 단순 FK key로 줄인 lookup과 NOT EXISTS도 실제 join 유무로 판정한다.
실제 join을 사용하는 조건은 statement snapshot의 membership을 보존한다. 모든 조건을 materialized key 목록으로
바꾸면 root predicate의 잠금 대기 의미가 달라지므로 사용하지 않는다.

모든 batch는 하나의 owned transaction 또는 borrowed savepoint에 속한다. 전체 준비/UPDATE가 성공하고 owned
commit이 확인된 뒤 count를 반환한다. Borrowed 결과는 부모 commit 전까지 provisional이다. 뒤쪽 batch·취소·callback·
cleanup 실패이면 확인 가능한 범위에서 전체 작업을 rollback하고 오류를 보존한다. Commit/rollback 결과 불확실성은
성공이나 자동 재시도 가능 상태로 축소하지 않는다. Commit 확인 뒤 도착한 취소가 이미 확정한 결과를 지우지 않는다.

Root·ordinary/coordinated/relation session과 pinned root cursor의 같은 연결에 capability를 연결한다.
Read-only snapshot·종료된 scope·child가 사용하는 동안의 parent는 limits 조회도 허용하지 않는다.

## Helpdesk의 더 강한 업무 조건

API `PATCH /api/tickets/bulk/`, Admin의 Close/Reopen 선택 작업, 기존 여러 티켓 편집기는 같은 bulk writer를 사용한다.
1..40개의 서로 다른 양수 ID를 현재 Category에서 모두 다시 읽는다. 하나라도 없거나 다른 범주에 있으면 전체
요청을 거부하며 어느 ID가 외부에 존재하는지 공개하지 않는다. 서버 소유 ID·Category·payload digest는 입력으로 바꾸지 못한다.
API는 항목별 partial patch, 편집기는 이미 clean한 bound form을 현재 모델에 적용한다. 현재 값과 달라진 scalar의
mask가 같은 행끼리 묶고 20행 batch의 생성 facade로 저장한다. 각 UPDATE에도 Category predicate를 유지하며
실제 count가 그룹 전체 수와 다르면 모든 변경을 되돌린다.

입력 집합과 DB의 UUID 고유성, 명시한 라벨의 현재 범위를 첫 UPDATE 전에 확인한다. 라벨 변경·저장된 JSON/digest·
전체 응답 인코딩과 실제 변경 행의 change audit도 같은 transaction에 포함한다. 원 입력 순서로 모든 결과를 반환하고
변경 없는 행은 scalar UPDATE와 audit를 생략한다. 편집기는 삭제·기존 행 bulk update·새 행 bulk create를 하나의 부모
transaction으로 묶는다. 별도 revision precondition은 제공하지 않는다. Runtime의 cooperative writer는 기존 DB/schema
fence를 공유하고, 이를 우회하는 외부 SQL을 인가하거나 직렬화한다고 주장하지 않는다.

공개 읽기는 권한 범위의 라벨만 보여준다. 쓰기 성공을 게시할 때는 필터 없는 실제 TicketLabel 전체 집합과 scoped
출력을 대조하고 모든 라벨의 현재 Category도 확인한다. 라벨 입력을 생략하거나 행이 무변경이어도 이 검사를 수행한다.
저장 중 범위를 벗어난 라벨을 출력 필터가 숨긴다는 이유로 성공 처리하지 않는다. 이미 저장된 잘못된 membership과
저장 중 변경은 입력 필드 오류로 추측하지 않고 실행 오류로 rollback한다.

Admin의 선택 작업은 주 permission과 `AdditionalPermissions`를 모두 요구한다. 등록된 immutable 목록은 registry의
principal admission, Site의 표시와 실행, Authorizer overlay에서 같은 조건으로 검사한다. 입력 오류는 직접 확인된
validation rejection에 한해서만 400으로 표시하며 감싼 결과 불확실성이나 정리 오류를 입력 오류로 축소하지 않는다.
서명된 성공 알림의 count는 실제 변경한 ID 수이며 반복 close/reopen은 0일 수 있다.

API는 인증·ChangeTicket/ViewLabel·필수 CSRF를 파싱 전에 검사한다. 1..40개의 `TicketBulkPatch` 항목마다 정확한
양수 int64 ID가 필수이고 나머지는 기존 partial field 정책을 사용한다. `ExtendObject`는 닫힌 inline 객체 schema에
새로운 필드를 중복 없이 추가하여 partial 입력의 annotation·requiredness를 보존한다. 다른 쓰기의 ID 입력은 계속 거부한다.
배열은 163,840 byte·깊이 16·65,536 values, 각 compact 객체는 4,096 byte, 전체 결과는 1 MiB의 공유 예산이다.
원 입력 index를 가진 진단과 native 충돌의 전체 진단을 구분하고 부분 성공·자동 재시도는 제공하지 않는다.

## 기준과 검증의 구별

고정 Django 6.1의 [독립 40개 observer](../../conformance/runners/django/bulk_update_reference.py)와
[PostgreSQL 잠금 대기 observer](../../conformance/runners/django/bulk_update_concurrency_reference.py)의 원 출력과
upstream hash를 보존한다. Go 비교가 native fixture의 expected를 다시 쓰지 않는다. BSD-3-Clause 출처는
[SOURCES](../SOURCES.md)에 남긴다. Python의 non-null 문자열에 None을 넣는 경우는 typed Go 입력으로 표현할 수 없으며,
malformed descriptor의 선택 필드 검증과 compile rejection을 따로 확인한다. 선택하지 않은 잘못된 값은 저장하지 않는다.
빈 IN의 Go zero-match UPDATE와 Django의 SQL 생략은 외부 count/행이 같아도 실행 횟수의 차이로 기록한다.

고정 Django의 부모 `atomic(savepoint=False)` 오류는 부모를 rollback-only로 만들 수 있다. Go는 명시적인 borrowed
savepoint의 rollback/release 확인 뒤 부모를 계속 쓴다. Literal COMMIT의 지연 FK 오류는 Go의 보수적인
CommitOutcomeUnknown과 native 원인을 유지한다. 두 차이는 [bulk 생성의 소유권](0088-bulk-creation-and-native-batch-ownership.md)과 같다.

설계 채택·코드 구현·환경별 검증은 구별한다. 실행 상세와 실패 이력은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 둔다.
일반 writable F/연산 expression과 QuerySet update는 후속 공통 AST의 요구이며 이 기능으로 완료 처리하지 않는다.
