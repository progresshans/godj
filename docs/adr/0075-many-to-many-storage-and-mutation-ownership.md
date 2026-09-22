# ADR-0075: ManyToMany storage와 변경 소유권

상태: Accepted boundary — 제품 구현과 환경별 검증은 [GDJ-0099](../../work/0099-many-to-many-and-ticket-label-collections.md)가 구분한다.

## 요구

Ticket에 labels를 선언하고 기존 TicketLabel을 통해 컬렉션을 편집한다. Columnless 관계의 의미를 Schema IR에 한 번 정규화하고,
타입별 generated facade·공통 runtime·DB AST/compiler로 연결한다. 기존 명시적 연결 행·endpoint·Category와 권한 경계를 보존한다.
자동 intermediary와 부가 필드가 있는 명시적 intermediary, cross-app·자기 관계에서도 같은 의미를 사용한다.

고정 Django의 [독립 관찰](../../conformance/runners/django/many_to_many_reference.py)은 선언·자동 through·동시 중복 add,
set의 retained identity/payload·늦은 실패 rollback·self symmetry·query multiplicity·cache snapshot·실제 migration·signal을 기록한다.
이는 독립 작성한 public 입력(`derived=false`, Django 6.1, BSD-3-Clause)이며 제품 구현이나 전체 호환성을 뜻하지 않는다.

## 선택

ManyToMany는 owner table의 scalar/FK column이 아니다. 정규화된 relation identity·target·reverse namespace와 자동/명시적
through ownership·양 endpoint field 선택을 소유한다. 자동 storage는 deterministic하게 유도하고 명시적 through는 기존 모델을 참조한다.
Generated code와 historical state가 각자 별도의 관계 원본을 만들지 않으며 잘못되거나 오래된 binding은 I/O 전에 거부한다.
자동 through의 Add/Remove/Rename과 역방향은 endpoint 행을 보존한다. Rename은 retained link identity를 보존하고,
기존 TicketLabel에 선언만 연결하는 작업은 행을 재작성하지 않는다.

Columnless 선언은 `Model.ManyToMany`의 `ManyToManyField`에 둔다. `Fields`에는 실제 저장 컬럼만 남긴다.
`ThroughModel`은 모델 identity와 두 logical FK field를 명시해 같은 모델에 다른 FK가 추가돼도 선택이 바뀌지 않게 한다.
명시적 through는 nullable FK나 pair uniqueness의 부재를 선언 단계에서 다른 storage로 바꾸지 않는다. 실제 모델의 제약을 유지한다.
자동 storage는 별도 원본으로 저장하지 않고 `StorageSchema`로 유도한다. 이름은 `<owner>_<field>`, Go 타입은
`<Owner><Field>Link`, table은 `<owner table>_<field>`이며 source/target 두 CASCADE FK와 `relation_pair` ordered unique를 갖는다.
이 물리 필드 명명은 GoDj의 선택이며 Django의 Python intermediary 객체 이름과 같다는 주장이 아니다.
생성기와 ORM binding은 이 같은 projection을 사용한다. 생성된 schema companion은 logical 선언만 반환하고,
automatic model descriptor·공통 삭제 graph·프로젝트 모델 binding은 파생 storage를 포함한다.
선언과 storage projection을 혼합한 입력의 충돌을 거부하며 snapshot hash·clone·model equality가 columnless 의미도 소유한다.

기본 자기 관계는 symmetrical이며 별도 reverse를 만들지 않는다. Directed 자기 관계는 별도 reverse를 갖는다.
현재 Go 선언은 symmetrical과 named reverse의 동시 지정을 모순으로 거부한다. 이는 Django의 ignored-option warning과 다른
입력 정책이며 이 선언 subset의 명시적 한계다. Through field 선택의 자동 추론과 broader declaration options도 후속 범위다.
이 단계의 선언/metadata 지원을 일반 collection manager나 migration의 완료로 합치지 않는다.

자동 through의 pair 추가에는 사전 존재 조회 이후 일반 INSERT만으로는 충분하지 않다. PostgreSQL의 동시 unique 오류는
이미 transaction을 실패 상태로 만들 수 있으므로 error를 나중에 정상 중복으로 바꿔서 계속 쓰지 않는다.
DB 독립 `ConflictInsertPlan`은 명시한 non-null unique tuple만 conflict target으로 삼고 모든 target field의 동일한 assignment를 요구한다.
관계 runtime은 자동 through 또는 실제 pair unique가 있는 명시적 through에서 해당 owned constraint를 target으로 만든다.
Pair unique가 없는 명시적 through의 변경 전략은 그 모델의 중복 허용 의미와 transaction 소유권을 따로 연결한다.
Backend는 `ON CONFLICT (columns) DO NOTHING`을 사용하고
다른 unique·FK·NOT NULL·CHECK·driver 오류를 보존한다. 무조건 모든 충돌을 무시하는 SQL이나 숨긴 재시도를 쓰지 않는다.

`db.ConflictInserter`는 일반 generated-key Insert와 다른 capability다. Root backend와 빌린 transaction session에서 지원하고,
없는 backend는 명시적으로 거부한다. 결과는 native affected row가 정확히 1이면 `inserted=true`, 0이면 false이며 오류면 false다.
삽입하지 않은 경우의 생성 PK를 합성하지 않고 SQLite의 이전 `LastInsertId`도 읽지 않는다. 0행은 존재 증명은 아니므로
trigger가 삽입을 생략했거나 다른 데이터가 개입할 수 있는 caller는 최종 membership을 별도로 확인한다. 일반 Insert의 1행 요구는 유지한다.
이 primitive의 true도 session 안에서는 provisional이다. 소유 transaction의 확인된 commit 이후에만 caller/cache에 성공을 게시한다.
Commit/rollback 불확실성과 SQLite 연결 격리·session 만료·취소의 기존 소유권을 그대로 사용한다.

관계 manager의 add/remove/clear/set는 같은 transaction에서 전체 후보를 검사하고 retained link identity와 payload를 보존한다.
Clear 요청은 링크만 삭제한다. 명시적 through의 추가 필드 제약은 정상 오류로 남는다. 대칭 자기 관계는 반대 방향 연결도 같은
변경에 포함하며, 조회 multiplicity는 명시적 Distinct 없이 축약하지 않는다.

## 후속 구현 경계

현재 native conflict-insert primitive의 구현과 전체 ManyToMany declaration/manager는 다른 단계다.
Relation cache의 실패 시 무효화는 Django 관찰과 Go의 기존 publication 계약을 비교해 manager 구현에서 명시적으로 결정한다.
독립 materialization·평가한 QuerySet·Fresh의 소유권을 공유 cache로 합치지 않는다. Signal callback은 이후 rollback되는 변경도
관찰할 수 있으므로 durable commit 영수증으로 취급하지 않는다. 미구현 signal 범위는 카탈로그에 남긴다.
Ticket의 컬렉션 입력은 권한·CSRF를 body/DB 전에 검사하고 저장 transaction에서 양쪽 Category와 전체 원하는 집합을 다시 확인한다.
실행 source와 환경, 아직 구현하지 않은 선언·생성·migration·query·소비자는 [CURRENT](../status/CURRENT.md)와
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 구분한다.
