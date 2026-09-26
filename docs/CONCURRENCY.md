# 동시성, 취소와 실패 소유권

성능은 실제 workload로 측정한다. Go나 goroutine을 사용한다는 사실만으로 처리량·안전성을 주장하지 않는다.
모든 I/O는 context와 error를 전달하고, pool·transaction·cache·process의 소유자를 구분한다.

`ConflictInserter`는 명시한 non-null unique tuple의 native 충돌만 no-op으로 처리한다. `inserted`는 실제 0/1행 결과이며
오류에는 false를 반환한다. False 자체가 membership 증명은 아니고, session에서 true를 받았어도 commit 전 성공을 게시하지 않는다.
기존 transaction의 취소·만료·unknown outcome·연결 격리와 재시도 부재를 유지한다.
[ManyToMany 변경 소유권](adr/0075-many-to-many-storage-and-mutation-ownership.md)이 선언/manager와의 연결을 정한다.

## 읽기 snapshot

`db.SnapshotReader`는 첫 관찰 이후 여러 SELECT가 같은 committed 상태를 보도록 읽기 범위를 연다.
Callback에는 Queryer와 session 유효성 검사만 노출하고 쓰기·transaction capability는 주지 않는다.
빌린 handle은 callback 반환 시 만료하며 rows를 포함해 그 범위 안에서 사용한다. 같은 backend에 별도 transaction을
중첩하면 현재 snapshot이 소유한 연결·admission을 기다릴 수 있으므로 callback은 빌린 Queryer만 사용한다.
취소·callback 오류·rows/종료 오류는 보존하고 자동 재시도하지 않는다. Panic/Goexit에도 만료와 종료를 수행한다.
Backend가 고정 연결을 정리하며 SQLite의 종료 미확인 연결에는 기존 retention/quarantine을 적용한다.
읽기 종료 실패는 write commit-unknown receipt가 아니다. 호출자는 전체 ReadSnapshot이 nil을 반환한 뒤에만 결과를 게시한다.
[Identity 조회](adr/0077-reusable-app-models-and-host-relation-ownership.md#현재-계정의-조회와-표현)는 이 계약으로 사용자·권한을 함께 읽는다.

## QuerySet 평가

Query plan은 불변이고 평가 cache는 별도 state다. Direct QuerySet value copy는 같은 평가 state를 공유할 수 있지만
Filter/OrderBy/Limit/Fresh에서 만들어진 query는 독립 평가 state를 갖는다. 같은 state의 concurrent All은 한 owner가
평가하고 waiter는 자신의 context로 기다린다. Waiter cancellation이 owner를 취소하지 않는다.
Manager는 생성 시 복사한 metadata와 기본 plan을 공유한다. `Using`은 descriptor.Metadata를 다시 호출하지 않고
독립 평가 state를 만든다. 쓰기 field index는 준비된 metadata를 빌리고 각 callback 직전에 해당 field를 복제하므로
callback mutation이 같은 쓰기의 다음 callback이나 이후 호출을 바꾸지 않는다.

성공한 전체 결과만 cache한다. Empty 결과도 성공이며 caller에게는 descriptor clone으로 분리한 model을 돌려준다.
Backend/scan/rows/close/context 실패는 성공 cache로 남기지 않는다. Cold Count/Exists/At/First는 full cache를 채우지 않으며,
warm terminal은 cache를 사용할 수 있다. Iterate는 full result cache를 사용하거나 채우지 않는다.
Nil/already-canceled context는 warm cache와 nullable no-I/O 경로에서도 확인한다.
Descriptor·callback panic도 열린 rows를 정리하고 평가 flight를 해제해 waiter가 다시 시도할 수 있게 한다.
Panic은 그대로 전파하며 partial result를 cache하지 않는다. 정상 반환의 rows·close·context 오류 결합은 유지한다.
Filter/OrderBy/Limit/Offset/Distinct 파생은 바뀌지 않은 private 불변 plan metadata를 공유하고, 입력·반환 mutable slice는 분리한다.
다수의 optional predicate를 미리 수집할 수 있으면 `Filter(predicates...)`로 한 번 전달해 기존 AND 자식의 반복 복사를 줄인다.
공개 `Condition.Values`는 복사본을 반환하며 PostgreSQL compiler는 검증 시 얻은 목록을 해당 컴파일의 준비된 leaf에만 보관한다.
출력은 같은 불변 tree의 DFS 순서로 그 목록을 읽고, 반환 SQL 인자와 다른 컴파일 사이에 mutable slice를 공유하지 않는다.
관계 JOIN의 필수 edge 집합·alias inventory는 각 compilation이 소유한다. AND/OR/NOT에서 만든 임시 map이나
nullable 보정 상태를 원래 AST 또는 다른 compilation과 공유하지 않는다. Source-key 존재 증명은 같은 edge의 정확한 metadata를 요구한다.
Cold Count는 현재 지원 관계 filter도 DB 집계로 실행하며 JOIN multiplicity와 Distinct·슬라이스를 보존한다.
Eager Count는 모든 eager target projection을 제외한 같은 source를 집계하고, warm eager All cache의 길이만 재사용한다.
Cold eager Count는 진행 중인 All을 기다리거나 eager·원래 QuerySet의 cache를 채우지 않는다.
Cold At는 표현 가능한 기존 offset과 index를 합친 OFFSET/LIMIT 1로 한 row만 소비한다. 원래 limit을 벗어나면 빈 plan을
사용하며 offset 범위를 넘는 index는 기존의 bounded row 소비 경로를 사용한다. DB 내부 offset 탐색 비용은 그대로다.

이 경계의 이유는 [ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)에 있다. QuerySet 자체의 안전성을 backend
session·사용자 callback·반환된 mutable model의 임의 공유 안전성으로 확대하지 않는다.

Forward query route는 동일 project snapshot의 declaration slice를 복사해 소유한다. Typed composition은 prefix를 바꾸지 않으며,
각 compilation이 route occurrence·alias·JOIN presence map을 따로 만든다. Generated generic field group은 immutable route와
project edge binding을 공유하고 lazy traversal 때 새 group을 만든다. Lookup policy는 복사한 field만 받는다.

## 관계 객체

- 같은 relation owner의 cache와 새 materialization/Fresh의 cache를 구분한다. 전역 identity map을 가정하지 않는다.
- Lazy required 관계의 missing/cardinality와 nullable absent는 서로 다른 결과다.
- OneToOne의 reverse는 자식이 없을 수 있으므로 `RelatedObject.Get`의 present=false를 성공 cache로 보존한다.
  Forward의 dangling target 오류와 구분하며 외부 insert 뒤에는 Fresh로 다시 읽는다. 일반 Unique FK는 reverse collection을 유지한다.
- OneToOne prefetch는 전체 batch의 단일 cardinality를 확인한 뒤 게시한다. 반복 owner는 독립 cache를 소유한다.
- ManyToMany direct prefetch는 모든 owner batch와 선택 관계가 성공한 뒤 source/cache를 함께 반환한다.
  동일 query의 동시 All은 하나의 immutable 평가를 공유하고 반환 model/collection은 독립 mutable handle을 가진다.
  한 handle의 변경은 그 handle만 무효화하며 원래 query·보유 중인 QuerySet·다른 materialization의 snapshot을 바꾸지 않는다.
  여러 SQL의 단일 시점 일관성과 빌린 session의 동시 사용은 caller의 transaction/backend 계약을 따른다.
- Named collection snapshot은 ordinary manager와 별도 alias에 보관한다. Read는 model·하위 graph·cache handle을 새로 복제하며,
  미선택과 선택된 빈 목록을 구분하고 fallback I/O를 하지 않는다. Manager 변경은 snapshot을 바꾸지 않으며 session 종료 뒤에는 읽을 수 없다.
  Custom ManyToMany의 grouping owner와 membership owner는 다를 수 있으므로 전체 owner 집합을 같은 SQL에 전달한다.
- OneToOne reverse의 JOIN 부재 가능성은 physical FK nullability와 별개다. 각 compilation이 AND/OR/NOT의 존재 조건을
  계산하며, 같은 FK의 forward/reverse가 OneToOne 선언 여부에 대해 충돌하면 거부한다. 다른 compile과 join map을 공유하지 않는다.
- Prefetch/eager All은 전체 scan·row close·cancel·cardinality 검증이 끝난 뒤 한 번에 결과를 게시한다. 실패 시 partial cache를 남기지 않는다.
- Forward/OneToOne reverse의 typed eager tree는 같은 scanner·evaluation owner를 쓴다. Reverse는 owner PK와 child FK를 대조하고,
  같은 occurrence/owner의 서로 다른 child 또는 presence를 거부한다. 동일 owner/child의 반복 행은 독립 반환 cache를 유지한다.
  Reverse ready cache는 부재에도 owner 기준의 plan을 보존하며 Fresh에서 외부 insert·재할당·교체를 읽는다.
  BindObjectsIn/BindReverseObjectsIn으로 한 project binding을 공유하며 다른 binding의 descendant는 I/O 전에 거부한다.
- Facade의 단일 reverse와 문자열 mixed path도 같은 selection/cache 경로를 사용한다. Unsaved owner의 cache 없는 reverse 접근은 I/O 전 오류이며
  명시적으로 할당한 child는 cache에서 읽는다. Owner Save가 PK를 얻으면 handle을 재바인딩하고 명시적 reverse cache를 유지한다.
  외부 child 저장은 warm missing cache를 자동 갱신하지 않는다.
  Forward FK 변경/assignment는 선택한 reverse 형제의 cache cell·target identity·subtree를 보존한다.
- 다른 filter JOIN으로 eager 결과에 같은 root 행이 중복돼도 각 반환 객체·FK pointer·선택한 관계 cache는 독립 소유한다.
  Warm All/First는 원본 query cache에서 다시 복제하며 한 결과의 수정을 다른 결과로 전파하지 않는다.
- Eager First는 cold query에서 최대 한 row를 읽고 All cache를 채우지 않는다. Warm All cache의 첫 결과도 독립 복제하며,
  관계 검증과 row close가 끝나기 전에는 결과를 반환하지 않는다. 기존 QuerySet.First처럼 명시적 정렬을 요구한다.
- Relation assignment는 FK 값과 cache를 함께 reconcile한다. Application memory를 DB rollback으로 자동 되돌리지 않는다.
  OneToOne reverse Set은 지정한 owner/child의 raw-field reconciliation·origin·self·PK·cache를 별도 candidate에 준비한 뒤 함께 게시한다.
  실패 시 두 wrapper에 부분 변경을 게시하지 않는다. Child Save 실패가 다른 wrapper의 cache를 자동 변경하지 않는다.
  Set(nil)은 warm child의 forward FK/cache만 지우며 cold 관계에서는 no-op이다. 두 wrapper의 mutation/access는 caller가 함께 serialize한다.
- 복수·중첩 selection의 immutable tree는 caller slice와 분리한다. 각 target adapter는 구체 Go type을 유지하고 source와 모든
  descendant를 한 번에 scan한다. 없는 ancestor 아래의 present/partial child도 오류로 처리하며 전체 검증 뒤 함께 게시한다.
  같은 target PK를 가리키는 다른 FK·행·terminal 반환의 하위 cache는 독립 소유한다. 한 facade 객체 안의 반복 접근은 기존
  relation target identity를 유지한다. `SelectedGraph`는 보유한 subtree의 독립 복제만 반환하고 lazy I/O를 일으키지 않는다.
  관련 작업의 검증 상태는 [GDJ-0083](../work/0083-nested-forward-eager-graphs.md)을 따른다.
- Eager materialization의 각 target source plan은 한 번 준비해 공유한다. 각 행의 key predicate와 ready cache는 독립적으로 만든다.
- Lazy/reverse 기본 plan도 project binding이 준비한다. Prefetch의 함수 내부 그룹은 `All`의 소유 model을 빌리고,
  storage callback 입력과 각 owner의 cache를 각각 복제한다. 중복 owner 사이에도 mutable model을 공유하지 않는다.
- Reverse object의 모델·관계·descriptor 형태·PK metadata 일치는 바인딩 시 검증한다. 게시한 private field snapshot은
  prefetch도 공유하며 매 호출의 깊은 비교나 같은 metadata의 별도 보관본을 만들지 않는다. Zero/nil handle과 backend,
  owner PK·storage callback의 현재 반환값 검사는 매 사용 시 유지한다. Zero-size descriptor가 callback의 순수성을 보장하지 않는다.
- 단일 prefetch의 custom target query는 이미 선택한 부모와 그 하위 graph를 다시 읽지 않는다. Custom query 안의 child와
  별도로 추가한 경로를 구분한다. `RelationLoadedAbsent`는 읽기 결과이며 FK 해제 할당이 아니다. 이 상태는 cache 복사와
  같은 key의 모델 파생·저장에 유지되고, 실제 FK가 바뀌면 다시 조회한다. Required 접근은 missing 오류, nullable/reverse는 정상 부재를 반환한다.
- 다른 query materialization에서 얻은 동일 PK의 객체가 같은 pointer일 필요는 없다.
- Transaction에서 만든 query의 사용 가능 범위는 transaction/session 계약을 따른다. Warm cache가 session 이후에도 값을 제공할 수 있다는 사실을
  session I/O가 계속 유효하다는 뜻으로 해석하지 않는다.
- Project-bound 삭제기는 root와 CASCADE 후손의 incoming 정책을 함께 고정한다. 행은 model identity+PK로 한 번 수집하며,
  모든 PROTECT 검사와 조회/row cleanup이 끝나야 SET_NULL·exact-key 삭제를 실행한다. 이미 수집된 행에 대한 PROTECT도 생략하지 않는다.
  Graph·생성 fingerprint는 바인딩이 소유하고, 방문 집합·보호된 행·변경 계획은 호출별로 분리한다.
  성공한 native commit 뒤에만 root PK를 지우고 root와 실제 삭제된 후손의 총수를 반환한다. 별도로 보유한 후손·observer와 그 cache는
  전역 수정하지 않는다. 취소·부분 실행 오류·불확실한 commit/rollback은 caller를 성공 상태로 바꾸거나 자동 재시도하지 않는다.

상세 이유: [forward cache](adr/0026-forward-foreign-key-object-cache-and-nullability.md),
[prefetch](adr/0028-reverse-foreign-key-prefetch.md), [eager](adr/0029-one-hop-forward-select-related.md),
[assignment](adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md),
[일대일 관계](adr/0073-one-to-one-cardinality-and-reverse-objects.md),
[CASCADE 그래프](adr/0074-cascade-delete-graph-and-constraint-timing.md).

## 배치 실행의 연결과 session 수명

`db.BatchQueryer`는 한 source query를 명시한 양수 크기로 읽는다. `scan`은 한 row를 caller 소유 buffer로 복사하고,
완성한 batch마다 `yield`에서 같은 borrowed session으로 하위 조회·광고한 capability의 쓰기를 할 수 있다.
두 callback은 동기 실행하며 decode 중 다른 I/O나 driver row/buffer 보유는 허용하지 않는다. 반환 bool=false는 정상 중단이다.
실패한 batch는 yield하지 않지만 이전 yield의 관찰을 되돌리지 않는다. Outer transaction의 commit·rollback과 full query cache를 소유하지 않는다.

SQLite는 같은 pinned/transaction connection의 source rowset을 유지한다. PostgreSQL은 `WITHOUT HOLD` cursor의
각 `FETCH`를 decode하고 rowset close 오류까지 확인한 다음 yield한다. 원본을 OFFSET으로 재실행하지 않는다.
취소·callback 오류·panic·Goexit에서도 소유 rows/cursor를 정리한다. Panic과 Goexit는 그대로 전파하고 cleanup 실패를 성공으로 바꾸지 않는다.
Raw SQLite transaction의 Goexit도 기존 rollback/discard/retention 경로를 사용하며 불확실한 연결을 pool에 돌려주지 않는다.
배치의 read affinity는 같은 session pointer이며 실행기가 기존에 광고하지 않은 relation mutation capability를 추가하지 않는다.
SQLite의 coordinated ordinary session은 관계 변경을 거부하고 PostgreSQL의 native transaction session은 기존 RelationSession capability를 유지한다.
Session 종료 뒤에는 빈 결과도 새 배치 조회를 허용하지 않는다.

Root `BatchQueryer`의 callback은 제공된 executor로 interleaved I/O를 수행한다. Root executor는 SessionValidator를
광고하지 않으며 스트리밍 종료 뒤 원래 backend로 돌아간다. 활성 범위는 한 연결을 사용하므로 caller가 작업을 직렬화하고,
직접 얻은 rowset은 다음 작업 전에 소비·close한다. Callback에 남은 직접 rowset은 다음 batch나 cursor cleanup 전에 닫는다.
원본과 중첩 iterator의 source rowset은 각 iterator가 소유하고, 전체 결과를 client에 보관하거나 source를 재조회하지 않는다.

SQLite root stream은 connection을 pin하기 전에 raw-transaction admission을 얻는다. 다른 writer가 admission을 잡고
같은 단일 연결을 기다리는 상태에서 callback이 admission을 다시 기다리는 순환을 방지한다. 범위 안의 ordinary write transaction은
`BEGIN`, coordinated/관계 transaction은 기존 `BEGIN IMMEDIATE`와 FK 검사를 사용한다. 같은 backend의 다른 raw writer는
stream 종료까지 admission을 기다린다. Unconfirmed cleanup의 retained lease는 stream의 lease와 별도로 남으며 pool을 봉인한
Backend.Close가 drain할 때만 물리 연결을 반환한다. Confirmed discard 전에 source/child rows를 닫아 database/sql close 대기를 해제한다.

PostgreSQL root stream은 WITH HOLD cursor를 소유한다. 범위 안의 transaction은 원래 caller context로 세션을 검증하고
종료 SQL을 동기 실행한다. database/sql의 취소 rollback이 진행 중인 연결을 재사용하지 않는다. 취소된 작업의 rollback/커서 정리는
분리한 bounded context를 사용한다. Literal COMMIT 오류는 outcome unknown을 유지하며 connection discard로 source를 중단한다.
개별 root write는 iterator 전체 transaction으로 묶이지 않는다. 명시적으로 호출한 atomic 작업만 자체 commit/rollback을 소유한다.


`MaterializeBatches`와 generated `Iterate(ctx, size, callback)`는 한 배치의 source·eager·하위 graph·wrapper를 모두 준비한 뒤
사용자 callback을 호출한다. 양수 size를 명시해야 하며 full evaluation cache를 읽거나 채우지 않는다. 실패한 배치의 일부를
공개하지 않고 이미 관찰한 이전 배치는 되돌리지 않는다. 조기 중단은 bool=false, callback 오류는 재시도 없이 전파한다.
기존 raw Iterate는 materialization 설정을 버리지 않도록 명시 오류를 유지한다.

Callback에 전달한 context로 interleaved ORM 작업을 수행한다. Context는 같은 original backend의 I/O만 pinned executor로
전달하며 model의 project origin·pointer identity·assignment/held cache를 바꾸지 않는다. 새 model과 이미 보유한 model의 Save·
CRUD·관계 atomic·lazy/collection query에도 적용한다. Direct backend 호출은 native BatchQueryer가 제공한 executor를 사용한다.
Context scope는 불변이며 nested stream은 해당 backend의 scope만 가린다. 서로 다른 goroutine의 context는 실행 연결을 공유하지
않지만 한 활성 callback context 안에서는 caller가 I/O를 직렬화해야 한다. 별도 Using origin의 assignment/selector 거부는 유지한다.
Original과 effective executor의 session lifetime을 함께 확인하고 부족한 쓰기 capability를 원래 root backend로 우회하지 않는다.
Backend identity는 비교 가능한 값이어야 한다. 상태를 가진 backend는 pointer로 제공하며 non-comparable 값은 source I/O 전에 거부한다.
Root graph는 원래 backend를 보관해 종료 뒤에도 사용할 수 있다. Borrowed graph는 context와 무관하게 원래 session 수명을 따른다.

현재 배치의 decoded model과 graph만 보관하지만 reverse OneToOne eager의 무결성 검사는 전체 stream의 route/owner/child 키를
유지한다. 이 ledger는 고유 owner 수에 비례하며 전체 모델 결과 cache와 다르다. 다른 배치에 나온 같은 owner의 상충 child도
오류로 처리한다. 사용자 callback이 별도로 보유한 모델과 custom target의 결과 크기는 사용자가 선택한 query 범위를 따른다.

## Migration의 durable 상태

Recorder identities와 opaque revision은 같은 DB snapshot에서 읽는다. Each-step transaction은 첫 DDL/recorder mutation 전에
expected revision을 검증한다. 성공한 schema·recorder·successor revision을 원자적으로 commit하고, 다음 step은 그 committed
successor만 사용한다. 다른 writer가 끼어들면 현재 step은 stale로 실패하고 앞선 commit은 durable하게 남는다.

Fingerprint는 history binding을 검증하지만 apply→unapply로 집합이 같아지는 ABA를 구별하지 못한다. Persistent generation과
revision이 협력 writer의 freshness를 소유한다. `BUSY`/`LOCKED`는 stale revision이 아니라 contention이다.
Automatic re-read/replan/retry는 caller의 의도를 바꿀 수 있으므로 caller가 명시적으로 새 lifecycle을 시작한다.

Legacy recorder의 최초 adoption은 old writer를 중지한 cutover가 필요하다. 비협력 writer의 완료된 ABA, recorder 밖에서 직접
수정한 schema, DB copy/restore의 운영 정책은 fence만으로 해결되지 않는다. SQLite PRAGMA의 data/schema/user version을
이 durable revision으로 대신 사용하지 않는다.

성공, 확정 rollback, outcome unknown을 구분한다. Commit error만으로 rollback을 주장하거나 자동 retry하지 않는다.
Caller에게 마지막 confirmed state를 돌려주더라도 DB가 그 state라고 확정한 것은 아닐 수 있다.
[ADR-0017](adr/0017-revision-fenced-migration-lifecycle.md)과 [ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md)에
이 선택의 이유가 있다.

## SQLite raw transaction과 quarantine

`database/sql`이 직접 추적하지 않는 raw BEGIN은 pinned connection에서 완료 여부를 확인해야 한다.
Cleanup은 취소된 caller context와 분리된 bounded context로 시도한다. Confirmed rollback은 release할 수 있고,
confirmed physical discard는 pool 재사용을 막는다. `Conn.Raw`가 nil을 반환했다는 사실만으로 discard를 확인했다고 하지 않는다.

Rollback과 discard를 모두 확인할 수 없으면 connection을 pool로 돌려주지 않는다. 같은 Backend의 retention-producing
`AtomicRelation`/`CoordinatedAtomic`과 FK suspension이 필요한 migration은 하나의 context-aware admission을 공유하며, 첫 retain은 terminal quarantine을 게시한다.
Retained physical connection은 최대 하나이며 이후 새 DB I/O는 connection acquire/callback/SQL 전에
`backend_error/backend_recovery_required`로 실패한다. 이미 admission된 작업을 소급 취소하지 않는다.

Explicit Backend.Close는 새 admission을 닫고 pool을 먼저 seal한 뒤 보관 handle을 drain한다. 늦게 retain한 이미-admitted
operation은 sealed pool의 handle을 스스로 닫는다. Error path에서 context 없는 implicit pool Close를 호출하지 않는다.
Concurrent Close loser는 winner의 전체 drain 완료를 기다리는 barrier가 아니다.

Callback panic은 cleanup 뒤 같은 panic value로 다시 발생시킨다. Unknown outcome cause를 보존하되 이것이 자동 retry나
현재 DB state를 추측할 권한이 되지는 않는다. 같은 SQLite 파일의 다른 Backend/process가 자동 quarantine되는 것도 아니다.
설계 이유는 [ADR-0057](adr/0057-sqlite-retained-connection-terminal-quarantine.md), 검증된 환경은 [Evidence](status/TEST_EVIDENCE.md)에서
각각 확인한다.

Runtime의 `AtomicRelation`도 동일한 process-local gate와 DB/schema coordination domain을 사용한다.
Backend의 `CoordinatedAtomicRelation`이 relation-capable session을 빌려주며 일반 `AtomicRelation`로 fallback하지 않는다.
SQLite는 pinned connection의 FK 상태를 BEGIN 전 확인하고 기존 cleanup/retention을 사용한다. PostgreSQL은 같은 advisory key를 쓴다.
Form의 관계 선택지는 request-local snapshot이며 provider의 반환값·getter slice를 복사한다. 여러 principal의 조회 결과를
공유 Spec에 게시하지 않는다. 저장 callback의 transaction 안에서 관계 membership을 다시 검사해야 하며 snapshot은 영구적인 인가가 아니다.

## 프로세스와 서비스

Principal·Session Record·Form/Serializer 결과·Validation 오류의 private 값은 불변이며 전달 시 공유한다. 외부 slice/map 입력과
mutable getter는 복사하고, 값 변경과 초기값 overlay는 바뀌는 container를 분리한다. Form validator가 보관한 cleaned 값은
후속 Bind가 바꾸지 않는다. 사용자 callback 자체의 동시 실행 안전성은 이 값 소유권과 별개다.

Template의 공개 List/Object/Context 생성은 caller 소유 container를 복사하고 private immutable child 값은 공유한다.
반복문의 변수와 forloop metadata는 render-local scope chain이 소유한다. 같은 동기 반복 안에서 frame을 재사용하며,
중첩 반복은 별도 frame을 갖고 include는 호출 동안만 현재 frame을 빌린다. 다른 Render나 바깥 binding과 공유하지 않는다.
준비된 serializer/Admin projector는 metadata를 공유하지만 사용자 reader에 전달하는 field는 복사한다. Reader와 model 자체의
동시 접근 안전성은 application 책임이며, 준비된 metadata가 사용자 callback을 동기화하지 않는다.
Password·CSRF entropy와 cookie clock의 직렬화 잠금은 callback panic에도 해제한다. 같은 panic은 기존 호출 경계로 전파한다.

CLI child의 input/output pipe, process group, signal과 reap은 실행자가 소유한다. Timeout이나 caller cancellation 뒤에도
child가 실행 중이거나 pipe writer가 남아 있는데 성공으로 반환하지 않는다. 큰 출력은 bounded protocol과 제한된 진단 buffer로
처리하고 원래 exit status와 실패 종류를 보존한다. Test fixture가 외부 process를 쓸 때도 같은 수명 규칙을 적용한다.

Web request의 context를 background job의 영구 상태로 보관하지 않는다. Server shutdown은 listener를 닫고 정해진 범위에서
in-flight request를 drain한다. Streaming이나 장시간 작업을 추가할 때는 backpressure와 취소 소유권을 별도로 정한다.

System-state coordination은 credential/session/audit와 application mutation의 원자성이 필요한 경계를 묶는다.
같은 schema를 사용하는 협력 runtime은 같은 hash profile·session 한도·CSRF policy를 사용해야 한다.
Identity runtime은 현재 User/Group/Permission을 native read snapshot에서 관찰하고 password work 전 읽기 범위를 종료한다.
Adoption은 source policy 검증·User/권한/receipt·source 비활성 표시를 같은 DB fence 아래 원자적으로 수행한다. Session rotation/logout/revocation 뒤의
거부와 application permission은 실제 DB·HTTP 흐름에서 검증한다. Process-local lock만으로 여러 runtime을 안전하다고 하지 않는다.

`identity.Manager.SetPassword`는 password work 전 native snapshot을 종료하고 write fence 안에서 현재 인가·revision·이전 credential을
다시 확인한다. Revision 증가·대상 session만의 폐기·감사 저장은 하나의 transaction이다. 다른 runtime의 동일 revision 요청은
한 번만 성공하며, 실패나 unknown commit 뒤 자동 재시도하지 않는다. 모든 협력 identity writer가 같은 fence/revision을 따라야 한다.

Identity로 이전하기 전 legacy operator 권한 변경은 `UpdateOperatorPermissions`에 expected current policy를 명시하여 요청한다.
Cooperative transaction에서 policy 비교·권한 변경·session 폐기를 함께 수행한다. Authenticate/Resolve는 현재 policy를 확인하므로
변경 전 runtime은 새 인증을 거부하고 새 policy로 다시 열어야 한다. 이미 허용된 in-flight 작업까지 소급 취소하지 않는다.
