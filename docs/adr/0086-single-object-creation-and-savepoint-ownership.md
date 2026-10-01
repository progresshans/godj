# ADR-0086: 단건 조회·조회 후 생성과 savepoint 소유권

- 상태: Accepted — ORM/savepoint·업무 흐름 구현과 고정 source의 Hosted 통합 완료
- 날짜: 2026-10-01
- 구현: [GDJ-0107](../../work/0107-single-object-creation-and-savepoints.md), 완료

## 채택한 의미

`QuerySet.Get`은 원 query의 full-result cache를 읽거나 변경하지 않고 새 평가를 수행한다.
정확히 한 객체는 독립 복사로 반환하고, 0개와 여러 개는 서로 다른 안정된 오류 code로 구분한다.
슬라이스 없는 조회는 정렬을 제거하고, 명시한 limit/offset은 보존한다. 결과 판정은 최대 21행으로
제한한다. 빈 조건도 context·query·session 검증을 먼저 수행한다. typed/dynamic 조건은 같은 AST를 쓴다.
선택한 관계와 prefetch의 소유권은 기존 materialization 경계와 함께 유지한다.

Savepoint는 이미 열린 transaction의 borrowed session에서 수행한다. 생성·rollback·해제와
callback 동안의 자원은 그 scope가 소유하며, child session은 callback 종료 뒤 사용할 수 없다.
중첩 child가 활성인 동안 parent handle의 I/O를 거부하여 child의 rollback 범위를 유지한다.
열린 cursor와 동시 진입·부모/자식 수명도 명시적으로 검사한다. savepoint의 rollback/해제를
확인할 수 없으면 부모 transaction을 정상 commit하거나 생성 경쟁을 자동 재시도하지 않는다.
부모 transaction의 commit·rollback과 SQLite quarantine의 기존 소유권은 유지한다.

`GetOrCreate`는 먼저 fresh 단건 조회를 하고, 부재일 때만 명시적 생성 입력을 준비한다.
생성은 owned transaction 또는 borrowed session의 savepoint 안에서 한 번만 시도한다.
실제 INSERT의 unique 오류 뒤 rollback을 확인한 경우에만 원 조건을 한 번 다시 조회한다. 여전히 부재라면 원래
생성 오류를 보존한다. 다중 일치·취소·다른 DB 오류·unknown outcome을 정상 반환으로 바꾸지 않는다.
실제 unique 제약 없는 임의 조건의 중복 생성을 막는 보장은 하지 않는다.
Query 조건을 생성 입력에 암묵적으로 복사하거나 인가 정책으로 취급하지 않는다. 생성 값과 조회 조건의
일치는 호출 앱이 명시하고, 기본값과 필드 의미는 원 Manager의 정규화 metadata snapshot을 따른다.

Helpdesk는 category/name의 실제 복합 unique와 현재 category 접근·생성/조회 권한을 함께 검사한다.
이미 존재하는 Label은 기존 값을 반환하고, 새 행과 감사 기록은 같은 부모 transaction에 속한다.
일반 create endpoint의 중복 거부와 별개의 명시적 업무 동작으로 제공한다.

## 단건 조회와 생성 API

공개 API는 `QuerySet.Get(ctx) (M, error)`와 `GetOrCreate(ctx, CreateInput[M]) (M, bool, error)`다.
반환 bool은 이번 호출이 생성했는지 나타낸다. `RelatedSelectQuery`·`PrefetchQuery`와 생성된 model query의
일반/eager/prefetch 표면에도 같은 terminal을 제공한다. `Get`의 오류 code는 `does_not_exist`와
`multiple_objects_returned`이며, 하위 backend·scanner·관계 loader가 반환한 오류를 실제 0행과 구분한다.

`Manager`의 준비된 metadata snapshot은 파생 QuerySet에 공유하고, 관계 query는 검증된 `BoundModel`의
immutable snapshot을 사용한다. Terminal에서 descriptor의 변경 가능한 `Metadata()`를 다시 읽지 않는다.
`CreateInput.BuildCreate`는 실제 부재를 확인한 뒤 transaction/savepoint 안에서 한 번 호출한다.
기존 객체가 있으면 nil·유효하지 않은 입력도 평가하지 않는다. 입력 준비·검증이 unique code를 반환해도
실제 Insert의 경쟁으로 간주하지 않는다. QuerySet에서 파생한 컬렉션 조회의 생성도 조회 조건을 입력으로
복사하거나 관계의 Add로 해석하지 않는다.

Root는 `db.Atomic`을, borrowed handle은 `db.Savepointer`를 요구한다. Borrowed adapter에 savepoint가 없으면
명시적 unsupported 오류를 반환하며 root transaction으로 우회하지 않는다. Callback의 0회/복수/반환 뒤 진입과
오류를 삼키는 owner를 거부한다. Join되지 않은 늦은 builder가 끝나도 만료된 callback context로 Insert하지 않는다.
정상 unique 복구는 확인된 rollback 뒤 원 backend/부모 session에서 한 번만 조회한다. 취소·rollback-required·
unknown outcome·backend recovery와 별도의 cleanup 오류가 있으면 복구 조회를 하지 않는다.

기존 객체의 eager/prefetch graph는 새 조회의 독립 복사다. 새 객체는 생성 scope가 끝나기 전에 값 소유권을 확보하고
원 root/부모 backend에 일반 lazy 관계를 연결한다. 종료한 생성용 child session을 반환하지 않는다.
생성된 facade는 root commit 또는 child release가 확인된 뒤 도착한 호출 context 취소만으로 생성 성공을 번복하지 않는다.
Child release의 성공은 여전히 provisional이며, 부모 rollback·수명 만료는 객체와 cache에도 적용된다.
반환 뒤 추가 facade 변환 오류가 있으면 이미 확인된 created bool을 보존한다. 오류가 있으면 객체를 사용하지 않으며
그 bool만을 durable commit의 증거로 삼지 않는다.

## 구현한 savepoint 경계

공개 진입점은 `db.WithSavepoint(ctx, session, callback)`과 선택 capability `db.Savepointer`다.
공통 `db/internal/txscope`가 중첩 handle·I/O·cursor 수명을 소유하고, 양 backend는 같은 native transaction/connection을
사용하는 child를 제공한다. ordinary/coordinated/relation owner와 root batch의 pinned transaction에 같은 경계를 적용한다.
Child는 parent가 제공하던 conflict insert·batch·relation capability를 보존한다. SQLite의 일반 쓰기 wrapper는
relation mutation을 노출하지 않으며, read snapshot reader는 쓰기·savepoint capability를 노출하지 않는다.

Borrowed session의 호출은 호출자가 직렬화하고 callback 반환 전에 해당 작업을 join한다. Parent cursor나 batch stream이
열려 있으면 savepoint 진입을 거부한다. Child 실행 중 parent의 조회·쓰기·cache 검증을 거부하고, scope 종료 뒤 handle과
실제·synthetic rowset의 사용은 공통 `backend_error/invalid_plan`으로 거부한다. Scope는 반환된 cursor의 정리를 소유하며
callback이 끝난 뒤 남은 child나 I/O가 있으면 이를 취소·무효화하고 root commit을 차단한다.

실패한 `SAVEPOINT`·`ROLLBACK TO SAVEPOINT`·`RELEASE SAVEPOINT`를 재시도하지 않는다. 제어 결과가 불확실하면
`transaction_rollback_required`를 남겨 호출자가 오류를 무시해도 root owner가 rollback한다. Root rollback도 확인할 수
없을 때의 `transaction_outcome_unknown`, literal commit 실패의 `commit_outcome_unknown`과 SQLite raw connection의
quarantine은 기존 native owner가 계속 소유한다. SQLite 일반 `sql.Tx`의 실제 rollback 실패도 unknown으로 분류한다.

개별 조회의 오류·취소와 cursor Close 실패를 구분한다. 호출자가 처리한 개별 query 취소는 부모 transaction의 정리 실패가
되지 않는다. 실제 Close 실패는 scope 종료 결과에 보존한다. Child callback 오류·취소·panic·Goexit는 cursor 정리 후
독립된 제한 시간의 cleanup context로 rollback/release하며, panic·Goexit를 일반 오류로 변환하지 않는다.

이 경계는 [PostgreSQL SAVEPOINT](https://www.postgresql.org/docs/17/sql-savepoint.html),
[ROLLBACK TO의 cursor 의미](https://www.postgresql.org/docs/17/sql-rollback-to.html)와
[SQLite savepoint](https://www.sqlite.org/lang_savepoint.html)의 transaction 의미를 따른다.
특히 기존 cursor 위치는 rollback으로 되돌아가지 않으므로 열린 cursor를 child에 넘기는 동작을 허용하지 않는다.
환경별 실행 결과와 미완료 범위는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 기록한다.

## Helpdesk의 입력과 부모 transaction

Label의 명시적 확보 동작을 일반 create와 분리한다. API는 POST `/api/labels/ensure/`의 name 입력을 받고,
Admin은 `CollectionCommandConfig`의 목록 링크와 GET/POST 입력 Form을 사용한다. 빈 목록에서도 사용해야 하므로
기존 object command의 선택 ID·revision 계약을 완화하거나 가상의 객체를 만들지 않는다. Collection command의
Form·필수 권한 집합은 등록 시 소유권을 확보하며, 읽기 전용 모델에는 등록하지 않는다. 각 callback은 자신의
transaction과 감사 기록을 소유하고 확정된 ID·생성 여부만 반환한다.

Helpdesk는 두 표면에서 AddLabel·ViewLabel을 함께 요구한다. Admin staff/site 접근·deny overlay·CSRF와 API의
인증/CSRF를 기존 경계에서 수행하고, writer는 승인된 불변 principal을 부모 transaction 안에서도 확인한다.
현재 Category는 DB에서 다시 확인하며 client가 category/id를 선택하지 않는다. Form은 Label metadata의 name을
사용하되 일반 create의 중복 사전 검사를 적용하지 않는다. 실제 `(category, name)` 제약과 `GetOrCreate`가 저장 경쟁을 소유한다.

`APIConfig.AppendAudit`와 `AdminConfig.AppendAudit`는 제공된 session만 사용하고 모든 오류를 전파해야 한다.
감사 의존 Admin 동작은 `AdminRegistry`가 등록하며 기존 단독 `Registry`를 변경하지 않는다. Runtime을 backend로
제공하면 그 부모 transaction이 기존 DB/schema coordination 영역에 참여한다. 이 callback 설정을 모든 기존 API CRUD의
자동 감사 정책으로 확대하지 않는다.

GetOrCreate의 child release 뒤에도 Label과 반환할 JSON의 전체 출력 준비, 새 Label의 add event 저장은 부모 안에 있다.
새 행과 감사가 함께 commit되어야 201/created=true를 반환한다. 기존 행은 200/false이며 별도 event를 만들지 않는다.
Admin도 서명된 알림으로 생성/재사용을 구분한다. 출력·audit·scope·취소 실패와 확인되지 않은 transaction 결과는
정상 응답/redirect나 자동 재시도로 바꾸지 않는다. callback 누락·반복·반환 뒤 호출·오류 은폐도 성공으로 게시하지 않는다.

## 기준과 남은 범위

고정 Django 6.1의 `QuerySet.get`, `get_or_create`, `transaction.Atomic` source와 양 DB 실제 관찰을
기준으로 한다. Python 내부 객체/예외 구현을 복제하지 않고 Go의 context·error·명시적 session 수명으로
외부 결과를 표현한다. 출처는 [SOURCES](../SOURCES.md)의 고정 Django와 BSD-3-Clause 표기를 따른다.
최종 [observer](../../conformance/runners/django/single_object_reference.py)와 양 DB fixture에 observer/실행 모듈의
source hash를 보존하며, 새 프로세스 재생과 별도 생성 Go module의 결과 대조를 구분해 기록한다.
SQL 문자열·원 예외 문구의 차이와 Go의 명시적 typed 입력은 [비교 범위](../SOURCES.md)에 명시한다.

Helpdesk의 현재 인가·CSRF·audit·입력 표면과 독립 client를 연결하고 영향 검증 및 source `8f8831ac`의 Hosted 전체 통합을 완료했다.
row locking·update-or-create·bulk의 넓은 요구는 같은 완료로 주장하지 않는다.
