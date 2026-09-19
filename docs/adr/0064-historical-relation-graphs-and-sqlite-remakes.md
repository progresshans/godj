# ADR-0064: Historical relation graph와 SQLite remake의 연결 소유권

- 상태: Accepted
- 날짜: 2026-09-20
- 관련 work: [GDJ-0084](../../work/0084-relation-migration-graphs.md)
- 선행 결정: [ADR-0018](0018-revision-fenced-migration-lifecycle-product-shape.md), [ADR-0057](0057-sqlite-retained-connection-terminal-quarantine.md)

## 맥락

기존 source와 한 개의 scalar-only target으로는 self/cyclic Create/Add/Remove와 nested target을 표현할 수 없다.
단순히 constructor의 cycle 거부를 없애면 historical metadata가 부족해지고, SQLite DROP/rename 중 FK 값 또는
commit 의미가 손상될 수 있다. 모델 graph의 cycle과 migration dependency DAG는 다른 제약이다.

## 결정

`internal/migrationgraph`가 Schema IR 위의 순수 historical metadata와 검증을 소유한다. State reconstructor와 backend가
같은 type/validator를 사용하되 DB/session/durability는 backend에 남긴다. 하나의 operation은 관계를 가진 source 경계,
field 순서의 전체 direct binding, source/direct 외의 정렬된 transitive model inventory를 가진다. Self target은 해당 source와
정확히 같은 snapshot이다. 각 identity는 graph에 한 번 저장하고 visited traversal로 cycle을 종료한다.

누락·불연속·충돌·미도달 metadata, 잘못된 key/reverse 이름과 실제 creator보다 앞선 target은 거부한다. Before/After와
전체 initial/final forest를 검증한다. Operation/field/string/aggregate node·byte 한도는 복제 전에 검사하고 target binding의
확장량도 제한한다. Transition에서 파생한 forest의 동일 app 문자열은 공유된 값으로 계산한다. 반환 IR은 깊게 복사한다.
Migration dependencies는 DAG를 유지한다. Self Create는 같은 operation에서 visible하며 상호참조는 이미 존재하는 model에
AddField를 적용하는 역사 순서로 표현한다. 일반 cyclic model 선언의 자동 분할·재정렬은 이 결정의 구현 범위가 아니다.

SQLite는 FK Remove의 remake와 self table Delete에서만 private pinned connection의 FK enforcement를 BEGIN 전에 끈다.
기존 context-aware raw admission을 먼저 얻고 ON 확인 → OFF 확인 → BEGIN IMMEDIATE → physical graph/fence 검사 → DDL →
전체 foreign_key_check → recorder/revision → COMMIT/ROLLBACK 순서로 실행한다. 이미 존재하는 inbound/self 관계 값과
row/null/default/PK/sequence를 보존한다. Index/trigger 등 검증하지 않은 물리 구조를 임의로 재작성하지 않는다.

Confirmed terminal 뒤에는 취소와 분리된 bounded context로 ON을 복원하고 readback 1을 확인한 다음에만 pool로 반환한다.
설정 변경/BEGIN/종료가 불확실하거나 복원이 실패하면 물리 discard를 시도한다. Discard도 확인하지 못하면 admission을 통해
connection 하나를 retain하고 기존 terminal quarantine을 게시한다. Confirmed commit 뒤 cleanup 실패는 committed를 유지하며
commit/rollback 불확실성은 unknown을 유지한다. 자동 재시도·implicit reopen은 하지 않는다. 이 한정된 migration 경로가
ADR-0057의 admission에 참여하며 모든 migration에 blanket quarantine을 추가하지는 않는다.

PostgreSQL은 전체 historical graph의 실제 catalog와 constraint, 관련 table lock을 확인한다. PK만 확인하고 nested target의
나머지 schema를 신뢰하지 않는다. Native DDL과 PostgreSQL 고유 identifier/storage 제한은 해당 backend가 소유한다.

## 대안과 관찰

SQLite `defer_foreign_keys`만 켜는 안은 채택하지 않는다. 실제 NO ACTION self/inbound remake에서 foreign_key_check가 비어도
COMMIT이 실패했다. FK enforcement를 켜 둔 DROP은 SET NULL 같은 action으로 외부 값을 바꿀 수도 있다.
근거는 [SQLite FK와 DROP](https://www.sqlite.org/foreignkeys.html#fk_schemacommands),
[일반 ALTER 절차](https://www.sqlite.org/lang_altertable.html#otheralter)와 GDJ-0084의 물리 probe/회귀 실행이다.

고정 Django 6.1 SQLite 관찰에서 remake는 삭제된 ID의 sequence 상한을 잃었다. GoDj는 기존 상한 보존 요구를 유지한다.
이 차이는 [DEV-0013](../DEVIATIONS.md#dev-0013--sqlite-migration-remake에서-삭제된-id의-sequence-상한을-보존)에 한정해 기록하며,
raw Django oracle을 수정하지 않는다. 정상 행·FK·historical field/choices·recorder의 나머지 비교는 그대로 적용한다.

## 검증 경계

Loaded definition, SQL projection, 실제 SQLite/PostgreSQL lifecycle와 generated 소비자가 같은 authority를 사용한다.
Self/cycle·cross-app back edge의 apply/unapply/reopen, populated inbound remake, FK 복원·취소·commit 불확실성·discard 실패를
검증한다. 실행한 source/환경과 통과 여부는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유하며 이 ADR의 채택 상태와 구분한다.
