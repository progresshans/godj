# ADR-0087: 행 잠금과 조회 후 생성 또는 갱신

- 상태: Accepted — 구현·업무 소비·영향 검증과 고정 source의 Hosted 통합 완료
- 날짜: 2026-10-01
- 구현: [GDJ-0108](../../work/0108-row-locking-and-update-or-create.md), 완료

## 책임과 원자성

행 잠금은 DB 독립 Query AST의 일부다. typed/dynamic query가 같은 요청을 만들며 SQL·지원 capability와
native transaction의 확인은 backend가 소유한다. query 파생은 잠금 설정의 소유권을 보존하고 새 평가 상태를
가진다. 이미 채워진 일반 query의 cache를 잠금 획득으로 해석하지 않는다.

PostgreSQL의 일반 UPDATE 잠금과 NO KEY UPDATE, 대기·NOWAIT·SKIP LOCKED, 명시적인 대상 선택을 연결한다.
양립할 수 없는 옵션이나 잘못된 대상을 무시하지 않는다. 잠금 I/O는 같은 연결의 살아 있는 writable transaction에서
수행하며 root의 autocommit 조회나 read-only snapshot을 잠금 transaction으로 간주하지 않는다.
취소·cursor 정리·savepoint와 부모 수명은 [ADR-0086](0086-single-object-creation-and-savepoint-ownership.md)의 경계를 따른다.

SQLite는 행 단위 잠금을 제공하지 않으므로 명시적인 행 잠금 요청을 unsupported로 거부한다. SQLite의
update-or-create는 기존 native transaction/savepoint 안에서 읽기와 쓰기를 수행하고 실제 busy·경쟁 실패를
전파한다. 가상의 UPDATE나 application mutex로 잠금을 흉내 내거나 전체 작업을 자동 재시도하지 않는다.

## 대상과 query 변형

명시한 잠금 대상은 모델/관계의 정규화된 출처와 실행 SELECT의 범위에 결합한다. projection의 열 개수가 바뀌었다는
이유로 명시한 대상을 버리거나 다른 table까지 잠금을 넓히지 않는다. 요청을 표현할 수 없으면 명시적인 오류를 반환한다.
nullable outer join, aggregate·distinct·window 등 native 잠금의 제한은 backend의 capability/compiler와 실제 DB에서 확인한다.

Count와 aggregate는 집계 의미에 따라 잠금 요청을 제거한다. 객체·값의 조회, Exists·First·Get 및 iterator의 잠금은
실제 평가에서 처리한다. 빈 결과의 SQL 생략은 기존 context·metadata·session 검증을 우회하지 않는다.
PostgreSQL에서 실행 SELECT가 생략되는 빈 membership은 별도 transaction을 요구하지 않는다.
기존 scope의 만료·취소·metadata 검증과 SQLite의 명시적 unsupported 경계는 빈 결과에서도 유지한다.
기본 prefetch의 별도 query에 root 잠금을 암묵적으로 전파하지 않는다. 명시적인 target query의 설정과 해당 SQL의
지원 경계를 검증하며, 잠금이 필요한 관계를 cache가 대신한 것으로 간주하지 않는다.

고정 Django 6.1의 native 관찰에서는 root 열을 포함하지 않은 values projection이 `of=self`를 OF 없는 FOR UPDATE로
바꾸며, 실제로 root와 조인한 모델을 모두 잠갔다. GoDj는 이 대상 확대를 채택하지 않는다. 이는 명시적인 Go 대상
보존 의미이며 Django의 해당 관찰 결과와 구분해 검증한다. Python 내부 선택 열 구조를 복제하거나 알려진 동작을
숨기기 위한 compatibility layer를 두지 않는다.

## 현재 기반 API

Query AST의 `NewRowLock`은 강도·대기 정책과 `LockSelf`/`LockRelated` 대상을 불변 값으로 보관한다.
`Plan.WithRowLock`은 원 plan을 보존하며 `WithResultShape`의 aggregate 전환은 잠금을 제거한다.
명시적인 관련 대상은 실제 SELECT의 materialized join과 전체 관계 출처가 일치해야 하며, 대상 요청이 새 join을
만들지는 않는다. 따라서 기존 filter/value join의 명시적 선택도 표현할 수 있다. 이는 eager로 선택한 관계만
`of` 대상으로 허용하는 Django API 경계와 구분한 Go의 AST 계약이다.

PostgreSQL compiler는 DISTINCT·windowed prefetch·잠글 대상의 nullable outer join을 unsupported로 거부한다.
Outer join이 있어도 root만 명시한 요청은 허용한다. 일반 root 및 root cursor의 대여 연결은 transaction을
대신하지 않으며 `transaction_required`로 거부한다. 실제 writable session과 그 savepoint/cursor가 잠금 수명을
소유한다. SQLite의 명시적인 요청은 빈 query에서도 unsupported이고 Count 전환 후에는 잠금 요청이 없다.
ORM의 `SelectForUpdate(RowLockOptions, ...RowLockTarget[M])`는 `NoWait`·`SkipLocked`·`NoKey` 옵션과
source 모델 타입을 유지하는 대상을 받는다. Query의 `LockTarget`은 self, typed `QueryRelation.LockTarget`은
해당 관계의 canonical route를 만든다. `ParseRowLockTargets`와 생성 facade의 `SelectForUpdatePaths`는 같은
IR에서 `self` 및 명시적인 관계 경로를 해석하며 SQL alias를 입력으로 받지 않는다.

일반/eager/prefetch query의 파생은 새 cache를 가진다. 명시적인 prefetch target 잠금은 기존 eager 값·부재 cache도
다시 평가하며 결과 graph의 소유권을 유지한다. 이미 선택한 중첩 관계는 잠금을 얻은 부모의 새 FK를 기준으로
별도 조회에서 복원한다. 이 복원이 부모의 잠금 SELECT에 JOIN을 추가하거나 하위 관계로 잠금을 전파하지 않는다.
하위 조회가 실패하면 부분 graph를 게시하지 않으며 원 query의 반환 snapshot도 바꾸지 않는다.
기본 prefetch는 root의 잠금을 상속하지 않는다. 일반/eager/prefetch 생성 소비자와 양 DB의 영향 검증을 완료했다.

## 생성 또는 갱신

update-or-create는 전체 조회·선택한 입력의 평가·저장을 하나의 owned transaction 또는 borrowed savepoint에 둔다.
PostgreSQL에서는 갱신할 기존 행을 잠근다. 실제 부재에는 생성 입력만, 기존 행에는 갱신 입력만 평가한다.
조회 조건을 생성 입력이나 인가 정책으로 암묵적으로 복사하지 않는다. 준비된 원 Manager metadata와 모델 필드의
검증·codec·default를 사용하고, 빈 갱신은 불필요한 UPDATE를 만들지 않는다.

동시 생성의 실제 unique 실패는 내부 생성 범위의 rollback을 확인한 뒤 한 번 재조회하여 갱신 경로로 연결한다.
조회/입력/일반 갱신 오류·부재·다중 일치·취소·정리 실패·unknown outcome을 이 복구로 처리하지 않는다.
unique 제약이 없는 임의 조건에 중복 방지를 보장하지 않는다. 입력 callback의 단 한 번 실행, 완료 뒤 진입 거부,
오류를 삼키는 owner의 거부는 기존 write scope와 같은 수준으로 검증한다.

반환할 모델과 관계 graph는 저장 scope의 성공 전에 준비하며 호출자의 원 backend/부모 수명에 결합한다.
만료될 내부 scope를 반환하지 않고, 확인된 commit 뒤 도착한 취소를 실패나 재시도 신호로 바꾸지 않는다.
borrowed savepoint의 해제는 부모 commit을 뜻하지 않는다. 생성 여부·입력 형식과 관계 결과의 형태는 아래 API와
생성 facade가 표현한다. Durable 결과와 불확실한 결과를 구분한다.

API는 `UpdateOrCreate(ctx, CreateInput[M], PatchInput[M]) (result, created, error)`다.
Go에서는 생성과 갱신 입력을 명시적으로 구분하며 사용하지 않는 분기의 nil 입력을 평가하지 않는다.
`BuildPatch`는 잠금을 얻은 현재 모델의 소유된 복사본을 받는다. Mutation IR은 빈 patch의 모델·table도 보존해
omitted field·표현·기본 키를 검증하고, 일반 `Manager.Update`와 그 unique 검증은 여전히 empty-patch 오류를 낸다.
임의 builder가 돌려준 empty-patch 오류를 성공으로 변환하지 않는다.

Native session의 선택적 `ReadModifyWriteSession`이 행 잠금 또는 transaction의 읽기/쓰기 충돌 정책을 선언한다.
알 수 없는 adapter·read-only snapshot은 이를 대신할 수 없다. PostgreSQL의 기본 lookup은 root를 명시해 잠근다.
사용자가 선택한 NOWAIT/NO KEY와 root를 포함한 대상은 유지하지만, SKIP LOCKED와 root를 제외한 대상은 거부한다.
고정 Django의 update_or_create가 미리 지정한 잠금 옵션을 기본 FOR UPDATE로 초기화하는 관찰과 구분한다.
SQLite에서 명시한 행 잠금을 버리고 upsert를 실행하지 않는다. 기본 upsert에는 native conflict 정책을 적용한다.

저장 뒤의 선택 graph는 바뀐 FK에서 다시 준비한다. 최초 lookup의 eager JOIN과 명시한 잠금 대상은 보존하되,
반환 graph의 재구성이 그 잠금을 다른 모델로 확대하지 않는다. 명시적 prefetch target 잠금은 해당 target query가
실행한다. 모든 중첩 collection의 cache·lazy backend를 내부 scope가 끝나기 전에 caller 수명으로 복사한다.
따라서 새 값이 원래 filter를 벗어나도 반환을 위해 그 filter로 다시 조회하지 않으며, graph 준비 실패는 전체 저장을
실패시킨다. 구현·생성 표면과 업무 소비자의 영향 검증은 완료했으며, 고정 source의 Hosted 통합은 별도로 기록한다.

## 업무 소비와 검증

Helpdesk의 티켓별 ServiceReport 저장은 실제 OneToOne 제약·현재 Category/Ticket 범위·필수 권한과 연결한다.
생성/갱신과 감사 기록의 원자성을 지키고, 일반 CRUD의 별도 의미와 인증·CSRF·출력 실패 처리를 보존한다.
Form/Admin/API와 생성된 독립 client가 같은 업무 입력·출력 계약을 사용해야 한다.

PUT `/api/tickets/<id>/service-report/`는 summary와 completed만 받아 생성 201·갱신/무변경 200의
`{report, created}` 결과를 제공한다. 생성·변경·조회 권한을 모두 먼저 확인하며, ticket/id/category의 본문 지정은
허용하지 않는다. Completed 생략은 false다. 기존 report의 ID와 ticket을 유지한다. 바뀐 필드만 patch에 넣어
동일 입력은 UPDATE와 audit를 추가하지 않으며, add/change 감사 이벤트와 인코딩한 결과를 같은 commit에 둔다.
PostgreSQL에서는 보고서보다 먼저 현재 범위의 Ticket을 NO KEY UPDATE로 잠가 Category 소속을 commit까지
유지한다. 이 잠금은 보고서 FK 참조를 허용한다. SQLite는 같은 native 읽기/쓰기 transaction의 충돌을 전파한다.

Admin의 collection command는 독립 Form의 명시적 RelatedChoices를 지원한다. Target 권한까지 모두 확인한 뒤
요청별 선택 목록을 읽고, writer 진입 직전에 새 목록으로 원 제출을 다시 검증한다. Writer가 자기 transaction에서
현재 key/scope를 확인할 책임은 유지한다. `CollectionCommandResult`는 생성·변경·무변경 결과를 구분하며 생성과
변경 flag의 동시 설정을 거부한다. Report 저장 Form은 ViewTicket을 추가로 요구하며 같은 Category의 티켓만
표시한다. API의 명시적 relation key는 Ticket subject를 공개하지 않아 별도 ViewTicket을 요구하지 않는다.
Label 확보와 Report 저장은 한 번 완료한 callback의 확인된 결과만 게시하는 같은 application transaction guard를 쓴다.
고정 Django의 source와 두 DB의 새 프로세스 관찰을 기준으로 입력 분기·실제 행/unique/FK 경쟁을 확인한다.
Go의 AST/compiler·native session·ORM·생성 module과 업무 소비자를 함께 검증한다. SQL 문자열 자체나 Python
내부 구조의 동일성은 목표가 아니다. 출처와 라이선스는 [SOURCES](../SOURCES.md), 실행 source·환경·실패와 남은
범위는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다. 이 문서의 채택은 구현이나 환경별 PASS를 뜻하지 않는다.
