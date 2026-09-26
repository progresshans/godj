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
선언/metadata와 migration의 지원을 일반 collection manager의 완료로 합치지 않는다.

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
Clear는 해당 owner의 연결 행을 삭제 root로 선택한다. 연결에서 endpoint로 나가는 FK를 따라 endpoint를 삭제하지 않는다.
연결 행을 참조하는 별도 incoming FK의 CASCADE·PROTECT·SET_NULL은 기존 collector가 전체 root 집합에 적용한다.
명시적 through의 추가 필드 제약은 정상 오류로 남는다. 대칭 자기 관계는 반대 방향 연결도 같은
변경에 포함하며, 조회 multiplicity는 명시적 Distinct 없이 축약하지 않는다.

## Collection runtime과 generated manager

`BindCollections()`는 project의 forward/reverse `orm.ManyToMany[Owner, Target, Through]` factory를 생성한다.
`From(backend, owner)`는 PK presence를 확인하고 독립 `ManyCollection`을 만든다. PK 0도 저장된 값으로 구분한다.
Columnless 선언, 선택한 두 FK, 같은 project snapshot의 양 endpoint/through descriptor와 incoming delete graph fingerprint를
함께 검증한다. 타입별 생성 코드는 키·payload/defaults의 직접 접근을 소유하고 transaction·집합 변경·cache는 공통 runtime이 소유한다.

`Add(ctx, targets, throughDefaults...)`와 `AddKeys`는 필요한 연결만 추가한다. Generated through Create input은
선택한 endpoint를 manager가 채우고 나머지 필드의 기존 default/nullable/validation을 적용한다. 신규 link를 만드는 defaults에 endpoint를
따로 지정하면 거부한다. 이미 있는 연결의 payload나 ID를 바꾸지 않으며 no-op에는 새 create의 required 값을 요구하지 않는다.
정확한 pair unique가 있으면 nullable column에도 실제 non-NULL assignment와 같은 metadata를 유지한 native conflict insert를 사용한다.
Pair unique가 없는 explicit through는 기존 duplicate를 보존하고 사전 조회에서 없는 pair만 일반 insert한다.
DB가 허용하는 동시 duplicate를 숨은 constraint나 retry로 바꾸지 않는다.

`Remove`, `RemoveKeys`, `Clear`, `Set`, `SetKeys`는 하나의 `AtomicRelation`을 소유한다. `Set`은 기본적으로 retained link를
보존하고 `ManyToManySetOptions{Clear:true}`는 기존 link를 지운 뒤 다시 만든다. 모든 새 create input을 검사한 뒤 삭제·삽입하고,
여러 삭제 root와 공유 CASCADE row를 model+PK로 합쳐 모든 PROTECT 검사를 먼저 수행한다. Nullable through의 NULL endpoint는
컬렉션 구성원이 아니므로 delta set과 특정 대상 remove에서 유지한다. Clear는 owner와 일치하는 NULL 연결도 삭제한다.
대칭 자기 관계의 mirror와 self link는 같은 변경에서 중복 없이 처리한다. 최종 membership을 다시 확인해 trigger의 0행 결과를
성공으로 오해하지 않는다. Concurrent set 전체를 serializable이라고 주장하지 않으며 더 강한 consumer fence는 별도 composition이 소유한다.

빌린 transaction session 안에서 성공한 insert는 provisional이다. 현재 root manager는 callback 횟수·동기 완료·원인 오류 보존을
확인하고 확인된 outer commit 이후에만 성공을 반환한다. Commit/rollback uncertainty를 그대로 전달하며 자동 재시도하지 않는다.
확인된 commit 뒤 늦은 context 취소로 결과를 실패로 바꾸지 않는다. 외부 transaction과의 명시적 composition은 아래의 session binding을 따른다.

`Query()`는 동일 Query AST의 physical reverse join으로 target을 읽는다. 선택한 nullable FK의 metadata도 유지하며,
명시적 through의 duplicate는 `Distinct()` 요청 전까지 보존한다. 일반 traversal 조건과 일괄 조회는 아래의 공통 관계 조회·prefetch를 따른다.
Collection handle의 query cache는 mutex 아래 교체한다. Mutation 시작과 종료 모두 무효화하므로 실패·취소·unknown outcome이나
이전 in-flight 조회가 현재 handle에 오래된 cache를 남기지 않는다. 이미 반환한 QuerySet, 다른 owner materialization과 Fresh는
독립 snapshot을 소유하며 pointer handle의 zero/value-copy는 오류다.

독립 Django 관찰은 nullable/nonunique through의 set/remove/clear/reverse clear와 연결 행의 incoming 정책까지 확장했다.
통합 facade/session composition과 직접 관계 prefetch, 남은 중첩 prefetch·Ticket 소비자·signal 완료를 구분한다.

## 공통 관계 조회와 필터별 연결 행

2026-09-23, forward/reverse FK·OneToOne·ManyToMany 조건을 `orm.QueryRelation[S,T]`와
`ChainRelations`로 통합한다. Generated `BindRelations()`의 같은 그룹에서 scalar field와 lazy traversal를 제공하고,
`ParseDynamicRelations`도 같은 sealed project snapshot과 경로를 사용한다. `BindForward`/`BindReverse`는 방향을 제한하는
constructor다. 별도 reverse query adapter·parser와 이전 query type의 호환 별칭은 유지하지 않는다. Reverse object와 prefetch의
객체 소유권은 그대로 해당 runtime이 담당한다. Query companion ABI는 v3, reverse companion ABI는 v5다.

Schema IR의 logical collection은 선택한 through source FK의 reverse hop과 target FK의 forward hop으로 확장한다.
`query.NewRelationChain`은 root를 포함한 모든 방문 model의 row key를 소유한다. Model/table/key의 일관성·연결성·terminal scope를
검증하며 backend가 intermediary의 PK 이름을 추측하지 않는다. 경로 한도는 물리 hop 64개이므로 ManyToMany 한 단계는 두 hop을 쓴다.

한 번의 `Filter(a,b)`는 같은 collection 연결 행을 검사한다. 연속 `Filter(a).Filter(b)`는 각 호출의 독립 join을 사용한다.
단일 관계 prefix는 공유하지만 첫 collection 이후의 join identity에는 filter scope를 포함한다. 원래 predicate나 이전 QuerySet은
변하지 않는다. 중복 연결 행과 서로 다른 scope의 곱은 명시적 `Distinct` 전까지 유지하며 cold Count도 그 결과를 센다.

Collection 부정은 correlated EXISTS로 준비하고 outer row를 늘리지 않는다. 같은 filter에서 앞서 준비한 positive collection이 있으면
그 첫 연결 행에, 없으면 root owner에 상관시킨다. 따라서 `And(a,Not(b))`, `And(Not(b),a)`, `Filter(a).Filter(Not(b))`는
서로 다른 결과가 가능하다. 이 순서는 고정 Django 6.1의 독립 관찰을 따른다. OR와 isnull은 필요한 LEFT JOIN·부재 행을 보존한다.
여러 단계의 mixed traversal·nullable through도 같은 조건/alias 계획을 사용한다. Scalar lookup의 타입·JSON path·backend capability는
기존 field 규칙을 유지한다. 관계를 넘는 F, collection value projection/ordering, 일반 관계 MIN/MAX를 이 변경으로 확장하지 않는다.

Collection manager의 core membership은 바로 다음 `Filter` 한 번에만 같은 through 행을 사용한다. 빈 Filter·scalar Filter도 이 기회를
소비하고, OrderBy/Distinct/Limit/Offset 또는 QuerySet.Fresh 같은 파생은 독립 다음 scope를 만든다. Collection handle의 Fresh는
새 manager를 만드는 별도 API다. 이 구분도 독립 related-manager 관찰과 비교한다.

`db/internal/queryplan`이 filter scope·join presence·상관 identity를 결정한다. 각 DB compiler는 identifier/schema quoting과
scalar SQL/parameter를 소유한다. SQLite는 outer SELECT와 각각의 EXISTS 안에서 root 포함 64-table 한도를 따로 확인하며,
LIMIT 0도 잘못된 provenance나 미지원 capability를 숨기지 않는다. 실행 source·DB별 결과·negative control은 TEST_EVIDENCE가 소유한다.

## Model facade와 빌린 session

Generated model의 forward/reverse collection 접근자는 typed view를 반환한다. 같은 owner materialization의 접근자는
공통 `ManyCollectionCache`를 통해 한 raw manager와 평가 cache를 공유한다. Query 결과와 파생 model은 독립 cell을 가지며,
`Fresh`는 별도 manager를 반환한다. View가 보관한 owner의 PK fence·pointer identity·facade origin도 매번 확인한다.
Typed target은 같은 origin의 model만 허용하며 다른 model 타입은 Go 컴파일에서 거부한다. 명시적 key 입력 경로는 별도로 유지한다.
컬렉션을 만들기 전에 owner가 저장돼 있어야 한다. Through payload/defaults는 동일한 generated Create input을 사용한다.

Root `Using(backend)`와 `UsingSession(session)`은 transaction 소유권이 다르다. 빌린 session을 root constructor로 넘기거나
root backend를 session constructor에 넘기면 거부한다. `ManyToMany.InSession(session, owner)`도 같은 명시적 경계를 가진다.
빌린 manager의 읽기는 `db.Session`과 lifetime capability를 요구한다. 변경은 실제 전달된 객체가 `db.RelationSession`을
제공할 때만 허용한다. 없는 경우 빈 add/remove도 명시 오류이며 root transaction으로 우회하지 않는다. PostgreSQL의 native
ordinary session도 relation capability를 제공하므로 callback 이름으로 쓰기 가능 여부를 추측하지 않는다.
빌린 manager는 전달된 relation session에서 기존 집합 변경 알고리즘을 직접 실행한다. BEGIN/COMMIT/ROLLBACK·재시도나
새로운 fence 획득을 하지 않는다. 이 경우 nil error는 provisional이다. Caller는 오류를 바깥 callback으로 전파하고,
확인된 outer commit 이후에만 결과를 게시해야 한다. Savepoint 또는 자체 부분 rollback으로 가장하지 않는다.
`CoordinatedAtomicRelation`의 session을 사용하면 그 기존 fence에 참여하며 일반 transaction으로 대체하지 않는다.

`db.SessionValidator.ValidateSession(ctx)`는 native session이 이미 소유한 active/lifetime/context 검사를 SQL 없이 노출한다.
SQLite ordinary/relation/coordinated와 PostgreSQL의 모든 transaction session이 이 capability를 구현한다. Root backend의
capability가 아니며, 빌린 collection/model binding은 없으면 명시적으로 거부한다. 임의 wrapper는 이 capability도 전달해야 한다.

일반 QuerySet과 eager/select query는 terminal 진입과 결과 publication에서 이 수명을 검사한다. Warm All/Count/Exists/At/First,
빈 결과, 사용자 clone·projection/aggregate decoder도 우회 경로가 되지 않는다. Generated session model/view 역시 유효한
수명 안에서만 동작한다. 원래 root query의 snapshot/cache 의미는 그대로 유지한다.
이는 이미 반환한 raw Go 값의 관찰을 막거나 rollback 때 사용자 메모리를 되돌린다는 뜻이 아니다. 그런 값 역시 provisional이며
commit 확인과 외부 publication은 outer owner의 책임이다. 독립 root materialization의 cache를 session 변경으로 전역 갱신하지 않는다.
Root cache를 새로 읽어야 하면 Fresh 또는 명시적 Invalidate를 사용한다.

## 직접 컬렉션 prefetch

2026-09-23, bound `ManyToMany.Prefetch`와 `PrefetchInSession`은 저장된 owner snapshot의 순서와 중복을 유지하며
각 owner의 독립 collection handle을 반환한다. 정렬한 고유 owner key를 999개씩 묶고 기존 through QuerySet에 owner IN과
non-null target 조건을 적용한다. 선택한 target FK의 기존 eager projection으로 target을 같은 SQL에서 읽는다.
새 SQL dialect 경로를 만들지 않으며 원래 through의 서로 다른 행이 같은 target을 가리켜도 duplicate를 보존한다.

전체 owner의 key와 descriptor를 I/O 전에 검증한다. 반환 through 행의 owner는 현재 batch에 속해야 하며 동일 through PK의
재출현은 오류다. 모든 batch·선택 관계·context·session 검사를 마친 뒤에만 source와 cache를 함께 반환한다.
늦은 batch 실패나 취소에서 prefix를 반환하지 않는다. 빈 owner 입력은 SQL 없이 같은 binding/lifetime 검사를 수행한다.

공통 `PrefetchRelated`는 owner query와 여러 collection selection tree의 평가 cache를 소유한다. Generated facade의 typed
`Prefetch` selector와 `PrefetchRelatedPaths`는 같은 runtime으로 연결하며 다른 origin/model이나 미지원 경로를 거부한다.
Facade ABI는 v16이며 collection binding ABI는 relation-reverse v6다. 같은 selection은 하위 선택을 합쳐 한 번만 읽고 owner query의 multiplicity·Distinct·정렬·슬라이스를 보존한다.
Cold Count는 owner 행만 세고 cold First는 명시적 정렬 아래 owner 한 행으로 제한한다. 성공한 All 뒤에는 같은 평가를 공유한다.
실패한 평가는 source까지 다시 읽고, 파생 query와 Fresh는 새 평가를 소유한다.

Cache 안의 target 값은 immutable snapshot이며 terminal마다 복제한다. 반환 model/handle을 변경해도 다른 materialization,
원래 query의 cache, 이미 보유한 QuerySet은 바뀌지 않는다. 변경한 handle만 cache를 무효화하며 다음 조회에서 새 membership을 읽는다.
빌린 session의 warm/empty cache도 session 종료 뒤에는 사용할 수 없다. 세션 없는 여러 SQL 읽기를 단일 시점 snapshot이라고
주장하지 않으며 더 강한 읽기 일관성은 caller의 transaction이 소유한다.

ManyToMany의 양방향·nullable/nonunique through·대칭/비대칭 self와 여러 direct/nested selection이 현재 구현 범위다.
`ManyPrefetch.WithChildren`의 child 타입은 target model로 제한한다. Generated typed selector는 facade origin도 검사하며
문자열 경로는 같은 native tree로 변환한다. 깊이 64·중복 포함 1024 node를 준비 단계에서 제한하고 잘못된 하위 경로·cycle은 I/O 전에 거부한다.
각 단계에서 전체 parent batch의 target을 모아 다음 단계를 한 번씩 읽는다. 모든 하위 조회가 성공하기 전에는 외부 cache를 공개하지 않는다.

`RelatedSelected`는 source 값과 single-valued/collection cache를 함께 담는다. Generated 일반 조회도 `Materialize`/`MaterializeFirst`를
거쳐 target query의 immutable 평가에 붙은 graph를 복제한다. Objects와 Collections는 같은 project binding을 사용한다.
반환된 각 model/manager는 독립 mutable handle을 가지며 held query는 이전 graph를 유지한다. 기본 중첩 prefetch의 target query에
Filter 등 refinement를 적용하면 평가와 하위 graph를 함께 버린다. 일반·prefetch·eager 결과 표현의 통합을 eager/prefetch 구성 API 완료로 세지 않는다.
필터를 지정한 child prefetch와 eager 조합은 아래 계약을 따른다. Owner별 slice는 아래 named snapshot 계약을 따른다.
고정 Django의 독립 관찰과 Go-native 소유권·실패 경로의 실행 근거는 TEST_EVIDENCE에 기록한다.

### 구성 가능한 prefetch의 조회 기반

고정 Django에서 custom target query의 관계 조건은 prefetch membership과 연결 행을 공유한다.
예를 들어 Label을 `owners.name=first`와 `owners.name=second`로 연속 Filter하면 두 through join이 생긴다.
Owner batch 조건은 가장 최근의 일치하는 join을 재사용하지만 결과를 owner에 묶는 column은 첫 join에서 읽는다.
중첩 조회·필터·정렬·캐시 변경·eager 조합·owner별 slice와 이 scope 차이를 포함한 독립 관찰은 custom single·streaming 사례를 포함한 51개이며, 제품 완료와 구분한다.

`query.Plan.ForPrefetchOwners`는 원래 target query의 WHERE·정렬·DISTINCT를 유지한다.
새 `ResultPrefetch`는 source model과 eager target들의 기존 scan 순서 뒤에 owner key 한 cell을 추가한다.
필수 integer key를 갖는 단일 reverse intermediary 경로와 동일 source를 검증하고, positive WHERE의 첫/마지막 일치 occurrence를
구분한다. NOT EXISTS 내부의 경로는 외부 join 재사용 대상으로 세지 않는다.
Slice가 없고 첫 grouping occurrence와 마지막 membership occurrence가 다르면 두 occurrence 모두에 요청 owner IN 조건을 적용한다.
이는 Django가 grouping에서 버리는 batch 밖 행을 SQL에서 제외하며 반환되는 모든 owner key의 소속을 보장한다.
Owner projection만 다른 query에 옮겨 필수 membership anchor를 잃으면 거부한다.

Custom target의 Filter·OrderBy·Distinct는 이 owner projection으로 읽는다. Normalized target projection scan과 owner key를
같은 행에서 해석하고 요청 batch 밖·NULL owner, 잘못된 model presence/PK와 clone의 PK 변경을 거부한다.
같은 target을 가리키는 서로 다른 through 행은 유지하며 DISTINCT를 명시한 때만 target+owner 전체 행을 중복 제거한다.
기본 prefetch의 동일 through PK 중복 거부를 custom query의 정당한 join multiplicity에 적용하지 않는다.

`ManyPrefetch`와 generated typed selector의 Filter·OrderBy·Distinct는 명시적 target query를 구성한다.
`PrefetchPath`는 같은 origin에 속한 path selector를 만들며 typed custom selector와 같은 순서로 조합할 수 있다.
이미 나타난 경로에 뒤늦게 custom query를 지정하거나 custom query를 다시 지정하면 I/O 전에 오류다.
Custom query가 먼저 나타나고 뒤의 경로가 하위 선택을 더하는 것은 허용한다.

명시적 target query에 원래 지정한 하위 prefetch는 immutable query 설정에 남는다. 파생 Filter/OrderBy/Distinct/Fresh와
cold First/At은 새 평가에서 그 설정을 실행하며 model 값과 graph를 한 번에 publish한다. Count는 하위 조회를 실행하지 않는다.
설정된 target query에 eager selection이나 추가 prefetch를 적용해도 기존 하위 설정을 보존한다. Eager의 SQL rowset을
닫고 검증한 뒤 collection batch를 읽으며 모두 성공해야 source와 두 cache를 함께 공개한다. 추가 prefetch는 기존 선택 뒤에
병합한다. 두 경로 모두 기존 설정의 project binding과 node budget을 함께 검사한다.
나중에 별도 path로 추가한 하위 선택은 원래 평가에만 붙이며 custom query의 영구 설정으로 합치지 않는다.
Raw streaming Iterate는 child query를 실행할 materialized batch 계약 없이 설정을 버리지 않고 명시 오류로 거부한다.

각 collection handle은 기본 membership plan을 따로 소유한다. Mutation·Invalidate·manager Fresh는 이 plan으로 돌아가며
기존 held QuerySet의 custom 조건·정렬·설정과 성공한 snapshot은 유지한다. No-op·실패·불명 outcome도 같은 무효화 규칙이다.
고정 Django의 중첩/filtered cache·lookup 순서·owner filter scope를 실제 generated 소비자와 비교한다.
Target query의 slice는 아래의 owner별 window 계획을 따른다. 일반 manager와 분리된 named snapshot으로 읽는다.


### Streaming의 배치와 실행 소유권

고정 Django의 iterator는 prefetch가 있으면 양수 chunk size를 요구한다. 각 chunk의 전체 owner 집합으로 하위 graph를
읽은 뒤 값을 돌려주며, 전체 QuerySet cache를 사용하거나 바꾸지 않는다. 중간 중단·실패 뒤 기존 전체 cache는 유지한다.
연속 관계 filter·owner별 slice에서 chunk 경계는 membership owner의 조회 범위에 영향을 준다. 이를 임의의 숨은 크기로 다시 나누지 않는다.

GoDj의 `db.BatchQueryer`는 row decode와 완성 batch의 yield를 구분하는 선택적 DB 실행 경계다.
Source plan의 순서·중복·slice를 한 번의 원본 조회에서 유지한다. Scan callback은 caller 소유 비공개 buffer만 구성하고,
yield는 반환된 executor의 기존 query/write capability를 사용한다. 양수 크기·callback·전체 plan을 SQL 전에 검증한다.
이전 batch의 관찰은 이후 실패로 회수하지 않으며 실패한 batch는 yield하지 않는다. bool=false로 정상 중단한다.

현재 ordinary/relation/coordinated transaction session에 구현했다. SQLite는 source rows와 하위 작업에 같은 연결을 사용한다.
PostgreSQL은 `NO SCROLL CURSOR WITHOUT HOLD`를 선언하고 각 bounded FETCH의 rows를 닫은 뒤 yield한다.
원본 rowset이 열린 채 다른 statement를 실행하면 pgx는 `conn busy`로 실패하므로 이 close 경계가 필요하다.
Native scalar adapter를 FETCH에도 적용하며 cursor 이름은 호출마다 독립이다. Cursor cleanup은 취소된 read context와
분리된 bounded context를 사용하고 기존 transaction 종료가 cursor를 정리한 경우만 종료된 세션 오류를 정규화한다.
Commit·rollback·재시도는 배치 executor가 수행하지 않는다. PostgreSQL의 cursor lifetime은
[DECLARE](https://www.postgresql.org/docs/17/sql-declare.html)와 [FETCH](https://www.postgresql.org/docs/17/sql-fetch.html)를 따른다.

Root backend에도 배치 executor를 연결했다. Callback에 전달한 executor는 활성 stream의 연결을 사용하고 종료 뒤 원래 backend로 복귀한다.
Root 범위에는 SessionValidator를 붙이지 않으며 borrowed transaction은 기존 만료 규칙을 유지한다. Callback은 전달된 executor의
capability로 후속 작업을 실행하고 직접 rowset은 다음 I/O 전에 닫는다. Callback 종료 시 남은 직접 rows를 닫고 nested/source rows는 각 iterator가 정리한다.
이는 모델 facade의 origin을 바꾸는 기능이 아니다. Model graph·generated iterator는 아래의 context scope로 origin과 I/O affinity를 구분한다.

SQLite는 root stream에서 raw admission을 먼저 얻은 다음 연결을 pin한다. 반대 순서는 단일 연결을 기다리는 외부 writer와
callback의 admission 대기를 순환시킬 수 있다. 범위 안의 원래 root executor로 별도 작업을 시작하지 않고 제공된 affinity를 쓴다.
Ordinary atomic의 BEGIN과 coordinated/관계 atomic의 BEGIN IMMEDIATE를 구분하고 기존 FK 검사·unknown outcome·retention 규칙을 유지한다.
하나의 물리 연결에 stream lease와 작업 lease를 나눈다. Raw 정리가 미확정이면 작업 lease는 backend quarantine에 남으며 stream 종료로
pool에 반환하지 않는다. Confirmed discard 전에 열린 rowset을 닫고, backend Close는 pool 봉인 뒤 retained lease를 해제한다.

PostgreSQL root source는 WITH HOLD cursor이며 autocommit에서 선언한 뒤 각 FETCH를 닫고 callback을 실행한다.
그 범위의 transaction은 BEGIN/COMMIT/ROLLBACK을 직접 소유하며 기존 transactionSession의 query·write·lifetime·오류 분류를 사용한다.
이는 sql.Tx의 cancellation rollback이 끝나기 전에 pinned 연결을 다시 사용하는 경합을 피한다. Begin/커서 정리 실패와
미확정 transaction 종료는 물리 연결을 discard하며 원본을 새 연결에서 재조회하지 않는다. Literal COMMIT 실패의 unknown 분류는 유지한다.
Source 중단이 이미 완료된 별도 root write를 rollback하지 않는다. 더 큰 write 원자성은 명시적 atomic callback이 소유한다.

Model graph·generated iterator 연결 전에는 기존 configured raw Iterate의 명시 오류를 유지한다.
전체 source를 client에 먼저 저장하거나 OFFSET 재조회로 대체하지 않는다.

### 단일 관계 prefetch와 eager 부모 재사용

2026-09-26, `SinglePrefetch`는 required/nullable FK와 역방향 OneToOne의 bound relation에서 구성한다.
Typed child는 target 모델로 제한하고 generated typed/path selector는 같은 origin·snapshot과 전체 64 hop/1,024 node 한도를 검사한다.
Root `SelectRelated`와 `PrefetchRelated`는 양쪽 호출 순서로 조합하며 eager와 prefetch의 모든 입력 node가 같은 한도를 소비한다.

단일 node는 이미 선택한 부모의 projection·binding·membership을 검증하고 그대로 사용한다. 없는 부모만 고유 integer key로
999개씩 나누어 읽고, batch 밖 행·복제 PK 불일치·단일 관계의 중복 행을 거부한다. Nullable NULL과 정상 부재도 cache로 보존한다.
부모의 기존 eager descendants와 새 collection descendants는 하나의 비공개 graph에서 결합하며 전체 조회 성공 뒤 publish한다.
반환 model·부모 wrapper·collection handle은 각각 독립이고, 한 객체의 변경은 다른 materialization이나 held query의 snapshot을 바꾸지 않는다.
객체 factory가 없는 collection-only target도 공통 model materialization으로 하위 cache를 전달한다.
NULL relation의 lazy/eager/prefetch handle도 backend의 session 수명을 유지하며 종료 뒤 Get/Fresh/SelectedGraph로 빠져나오지 않는다.

Cold First는 source를 하나만 decode한 뒤 그 graph를 구성하며 full evaluation cache를 채우지 않는다. Count는 child I/O를 하지 않는다.
Filter·정렬·Distinct·source slice·Fresh는 같은 준비된 관계 tree를 새 source 평가에 적용한다. 실패와 취소는 부분 graph를 공개하지 않는다.
`SinglePrefetch`는 target의 `Filter`·`OrderBy`·`Distinct`·`SelectRelated`를 같은 Query AST로 구성한다.
Custom filter join이 같은 target PK를 반복하면 첫 행을 사용한다. 같은 owner에 서로 다른 target PK가 나온 경우에는
단일 관계 cardinality 오류를 유지한다. Custom query가 없는 조회에서 중복 행을 허용하는 것으로 넓히지 않는다.
Target eager와 child graph는 모든 rowset을 닫고 검증한 뒤 함께 publish한다. Default target과 reverse owner key의 각 partition은
독립이므로 고유 key의 999개 batch를 유지한다. Slice는 단일 target query에서 지원하지 않는다.

Custom selector에 포함한 WithChildren은 target query의 설정이다. 이미 eager로 읽은 부모는 해당 target query 전체를
건너뛰므로 filter·target eager·그 안의 child를 재실행하지 않는다. Custom selector 뒤에 별도로 추가한 typed/path descendant는
이미 읽은 부모에도 적용한다. 앞서 선택한 lookup에 custom query를 다시 지정하면 I/O 전에 거부한다.

필터로 대상이 제외되어도 source model은 반환한다. Required accessor는 `related_object_missing` 오류를 반환하고 nullable와
reverse accessor는 기존 bool 부재 계약을 따른다. Generated facade의 eager cache 채움 단계는 required 부재를 목록 전체의
오류로 승격하지 않는다. `RelationLoadedAbsent`는 조회 결과이며 `RelationAssignedAbsent`의 해제 할당과 다르다.
읽기 cache를 복사하거나 같은 FK로 파생·Save해도 저장할 FK를 비우지 않으며, required source key 자체가 없으면 저장을 거부한다.
실제로 FK를 바꾸면 해당 cache를 버리고 기본 관계를 다시 읽는다. Low-level object의 Fresh는 custom filter/graph를 버리고
원래 관계를 읽으며 기존 객체의 부재 cache는 유지한다. Session 종료 뒤에는 부재 cache도 사용할 수 없다.

Prefetch 설정 query의 materialized streaming은 공통 배치 materialization 계약을 따른다.


### 역방향 컬렉션과 target eager 구성

2026-09-26, `ReverseObject.WithChildren`는 bound reverse FK에서 `ReverseCollectionPrefetch`를 구성한다.
Generated model은 같은 binding의 역방향 collection 접근자·typed/path selector를 제공한다. 일반 reverse FK는 collection이며
명시적 OneToOne만 단일 객체다. `ReverseCollectionPrefetch`와 `ManyPrefetch`의 Filter·OrderBy·Distinct·SelectRelated는
명시적 target query를 구성하며 이후 같은 lookup의 query 재정의는 거부한다. Implicit child 경로의 추가·병합은 허용한다.

역방향 batch는 정렬·중복 제거한 owner key를 999개씩 나누어 target FK의 IN 조건으로 읽는다. Eager target은 같은 SQL row에 있고,
모든 target rowset을 닫은 뒤 child prefetch를 전체 parent batch에 걸쳐 실행한다. 각 target의 PK·owner FK·clone과 batch 소속을
검증한다. Owner의 중복과 target query JOIN의 정당한 multiplicity는 보존하며 source와 전체 하위 graph의 성공 전에는 cache를 공개하지 않는다.
ManyToMany owner projection도 같은 projection scanner로 eager target 뒤의 owner key를 해석한다. NULL/batch 밖 owner와
projection key/model key 불일치는 전체 행을 공개하기 전에 거부한다.

명시적 eager와 child prefetch는 QuerySet의 immutable materialization 설정에 남는다. 파생 Filter·정렬·Fresh·indexed At/First는
새 평가에서 같은 graph를 읽으며 Count는 child I/O를 하지 않는다. 추가 root eager와 prefetch는 원래 설정과 project binding·node 한도를
함께 유지한다. Cold indexed 조회는 full cache를 채우지 않으며 큰 offset의 제한된 row-drain 경계도 유지한다.
일반 lookup 경로로만 추가한 descendants는 기존 평가에만 남는다.

역방향 `RelatedSet`은 기본 owner scope와 현재 query/cache를 구분한다. Query는 held snapshot을 반환하고 manager Fresh/Invalidate는
기본 scope·기본 정렬로 돌아간다. 이미 보유한 query·다른 materialization은 변하지 않는다. Query snapshot과 Invalidate의 교체는
같은 mutex가 소유하며 session 종료 후에는 cache도 읽을 수 없다. 이 collection 연결은 읽기·조회 cache 범위이며 역방향 FK 변경 API의 완성을 뜻하지 않는다.
Facade ABI는 v19, relation object ABI는 v6이다. 실제 facade 소비자는 reverse companion까지 포함하며 compiler 원인·stale binding·alias·COW 검증을 유지한다.

### Owner별 slice 조회 계획

2026-09-26, 고정 Django의 sliced Prefetch는 `to_attr`로 별도 목록에 담는다. 일반 relation manager에 같은 sliced query를
설치하면 owner filter 적용 중 TypeError가 발생한다. GoDj도 일반 manager의 조회 범위와 별도 snapshot을 구분한다.
공통 Query AST와 SQLite/PostgreSQL compiler를 runtime/generated named snapshot에 연결했다. 일반 manager에 slice를
설치하는 입력은 GoDj의 사전 검증에서 I/O 없이 거부한다. Limit 없는 Offset(0)은 slice 제한이 없으므로 일반 manager에도 허용한다.

`Plan.ForPrefetchOwners`는 먼저 구성한 target limit/offset을 membership owner별 `ROW_NUMBER` 범위로 해석한다.
첫 intermediary join은 출력의 grouping owner이고, 마지막 일치 join은 membership과 window partition을 소유한다.
두 join이 다를 때 요청 범위 밖 grouping 행도 순번 계산에는 참여한다. 이 owner 필터는 window 적용 이후의 바깥 query에서
검사하며 scanner에 반환되는 모든 owner는 요청 범위에 속한다. Unsliced owner projection을 만든 뒤 일반 limit를 붙이면 거부한다.
`ForPrefetchForeignKey`는 역방향 collection target의 integer FK를 partition으로 삼고 같은 조회 계획을 사용한다.

Window rank는 DISTINCT의 입력 cell에 포함한다. 비고유 through의 동일 target도 서로 다른 순번이면 남으므로,
먼저 target을 중복 제거하거나 최종 목록에 임의의 DISTINCT를 적용하지 않는다. Eager target·owner cell은 기존 scan 순서를 유지하고,
rank와 정렬용 cell은 외부 row에서 제거한다. JSON 정렬 key·decimal 결과 변환·매개변수 위치는 각 backend compiler가 소유한다.
Empty slice와 empty membership도 전체 plan 검증을 거친 뒤 I/O를 생략하며 overflow 없는 upper bound와 context 취소를 유지한다.

`ManyPrefetch`와 `ReverseCollectionPrefetch` 및 generated selector는 `Snapshot(name)`·`Limit`·`Offset`·`Read(ctx, owner)`를
제공한다. 이름은 단일 identifier이며 모델 field·declared relation과 충돌하지 않는다. 같은 이름의 query 재정의·다른 관계 충돌,
foreign binding/origin·copied owner·PK 변경·취소를 거부한다. 서로 다른 이름의 snapshot과 일반 manager 선택은 함께 사용할 수 있다.
Read는 selected 여부를 별도로 반환하여 미선택과 빈 목록을 구분하며 자동 조회하지 않는다. 매번 target model과 하위 graph의
독립 handle을 반환한다. 동일 selector에 WithChildren/SelectRelated를 지정하고 named child를 다시 Read할 수 있다.
Generated wrapper는 원본 graph를 보유하므로 root eager로 재사용한 부모나 collection-only target에서도 named child가 보존된다.
같은 owner의 With 관계 변경으로 파생한 모델과 그 모델의 Save도 명시한 snapshot을 유지한다. Private graph는 immutable하게
공유하고 각 Read는 독립 model/cache handle을 반환한다. Manager mutation/reset은 snapshot을 변경하지 않는다.
Snapshot은 영구 membership/인가 증명이 아니며 borrowed session 종료 뒤에는
warm 결과도 읽을 수 없다. 모든 target/child의 scan·close·membership 검증이 성공해야 source와 snapshot이 함께 공개된다.

Custom ManyToMany의 grouping owner와 membership owner가 서로 다른 batch에 있으면 999개씩 독립 조회하는 방식은 행과 순번을
바꾼다. 따라서 custom query는 전체 고유 owner 집합을 한 SQL에 전달한다. Integer IN이 999개를 넘으면 SQLite는 bound JSON
integer array의 json_each, PostgreSQL은 bound bigint array의 ANY를 사용한다. 표준 IN의 NULL/부정 의미와 int64 전체 정밀도를
유지하고 SQL 매개변수 개수를 owner 수만큼 늘리지 않는다. Window 뒤의 grouping owner 필터도 같은 표현을 사용한다.
기본 ManyToMany와 reverse FK처럼 owner partition이 독립인 조회는 기존 bounded batch를 유지한다. 이 연결은 일반 관계 query의
새로운 제한이나 전체 table을 읽고 Go에서 자르는 fallback을 도입하지 않는다.

## Historical 선언 변경

Columnless 변경은 `AddManyToMany`, `RemoveManyToMany`, `RenameManyToMany`라는 별도 typed operation으로 표현한다.
저장 컬럼의 AddField를 가장하지 않는다. Add/Remove는 선언 목록의 삽입 anchor와 완전한 field를 보관하고, Rename은 이름·Go 이름 외
binding 변경을 허용하지 않는다. 기존 scalar/FK/constraint delta도 retained ManyToMany 의미를 변경할 수 없다.
Definition의 closed nested shape·사전 resource admission·semantic digest와 일반/최적화 historical state가 같은 의미를 보존한다.

명시적 through는 기존 모델을 소유하지 않는다. 양 DB의 `ExplicitManyToMany` capability와 schema editor는 원본 owner·target·through와
transitive FK graph의 실제 catalog를 검증하고, DDL이나 행 재작성 없이 같은 revision-fenced transaction에 이력을 반영한다.
Columnless source에 직접 FK가 없어도 through와 두 endpoint의 authority는 생략하지 않는다. Retained binding을 검증하는 scalar 변경도
이 capability를 요구한다. 현재 SQL projection은 해당 operation의 빈 statement group만 허용한다.

자동 계획은 explicit-through 선언을 모델·선택한 FK 생성 뒤에 추가한다. Cross-app의 모델이 이미 있어도 필요한 FK가 아직 없는
중간 prefix라면 선언을 지연한다. Dependency ancestry와 모든 재개 prefix의 남은 bytes를 보존하며, 모호한 rename이나 retained 선언의
순서 변경·binding retarget을 추측하지 않는다. 자동 intermediary도 같은 계획에 포함하고, 아직 없는 derived target을 참조하는 FK는
다음 prefix로 지연한다. Raw CreateModel의 columnless 선언은 필요한 endpoint/through가 준비된 경우에 지원한다.

자동 intermediary의 물리 변경은 하나의 logical operation에서 유도한다. 외부 입력으로 별도의 storage 원본을 받거나 합성 CreateModel을
공개 이력에 추가하지 않는다. Sealed graph는 정확한 파생 모델과 target·명시적 alias의 authority를 요구하며, 사전 resource budget은
파생 이름과 전체 fan-out도 제한한다. 생성 전 부재·제거 뒤 부재·이름 변경의 두 경계를 포함해 step 중간에만 존재하는 table도 검증한다.
같은 CreateModel에 포함된 automatic model끼리의 FK는 의존성 순서로 생성하고 역순으로 제거한다. Owner의 저장 FK가 자신의
intermediary를 다시 참조하는 생성 순환은 모델 생성 후 AddField로 작성한다. 자동 계획은 이 FK를 준비된 prefix까지 지연한다.

양 DB의 `AutomaticManyToMany` capability는 retained binding에도 필요하다. DDL 전체·최종 catalog·revision/history를 같은 transaction이
소유하고 cursor는 logical operation당 한 번 진행한다. Rename은 drop/create나 row copy를 사용하지 않는다. SQLite native table rename은
PK·rootpage·sqlite_sequence 부재/값을 유지하고 pair index는 새 managed 이름으로 교체한다. PostgreSQL은 table·identity sequence와
PK/FK/unique constraint 이름을 바꾸며 기존 table/sequence OID·last_value/is_called를 유지한다. Go 이름만 바뀌면 DDL은 없다.
관리 밖의 inbound FK나 view를 rename에 따라 조용히 retarget하지 않는다. Logical alias/FK가 이전 derived identity를 참조하면 변경 전에
해제해야 한다. 명시적 through 선언은 자동 table을 참조하더라도 해당 table을 소유하지 않는다.

Remove는 소유 link table만 삭제하고 두 endpoint의 행을 보존한다. Reverse Remove나 재적용은 빈 intermediary를 생성하며,
이미 삭제한 링크를 복구했다고 게시하지 않는다. 실제 migration과 forward SQL projection은 같은 완전한 DDL group을 사용한다.
늦은 제약 오류·취소·이름 충돌과 catalog drift에서 부분 이력이나 변경 상태를 게시하지 않고 기존 rollback/unknown outcome 계약을 유지한다.

## 후속 구현 경계

Root collection manager·통합 facade·session composition과 전체 query/consumer 지원은 다른 단계다.
실패 시 collection cache 무효화와 성공 publication은 위의 소유권을 따른다.
독립 materialization·평가한 QuerySet·Fresh의 소유권을 공유 cache로 합치지 않는다. Signal callback은 이후 rollback되는 변경도
관찰할 수 있으므로 durable commit 영수증으로 취급하지 않는다. 미구현 signal 범위는 카탈로그에 남긴다.
Ticket의 컬렉션 입력은 권한·CSRF를 body/DB 전에 검사하고 저장 transaction에서 양쪽 Category와 전체 원하는 집합을 다시 확인한다.
실행 source와 환경, 아직 구현하지 않은 선언·생성·migration·query·소비자는 [CURRENT](../status/CURRENT.md)와
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 구분한다.


### 배치 모델 공개와 실행 context

2026-09-26, `orm.MaterializeBatches`와 eager/prefetch `IterateBatches`, generated
`Iterate(ctx, size, func(context.Context, *Model) (bool, error))`를 채택했다. 배치 크기는 명시한 양수이고
source의 순서·중복·slice를 한 실행에서 보존한다. 각 owner universe는 해당 배치이며 custom target의 owner별 window도
같은 집합에서 계산한다. Source decode·eager 검증·하위 prefetch·graph clone·generated wrapper 준비를 마친 배치만 공개한다.
오류가 난 배치를 부분 공개하지 않고 이전 callback의 효과는 보존한다. Full evaluation cache는 우회하고 보존한다.

Facade state를 바꿔 실행 연결을 전달하면 기존에 할당한 model pointer나 reciprocal cache가 원래 연결에 남는다.
따라서 origin/state는 그대로 두고 callback context의 불변 scope를 ORM I/O 경계에서 해석한다. 같은 original backend의
query·scalar mutation·relation atomic만 전달된 executor에서 실행하며 중첩 context는 해당 backend의 이전 scope를 가린다.
배치 밖에서 얻은 model의 identity·cache도 유지하고 다른 Using origin은 계속 거부한다. Executor의 capability를 확인하며
부족한 capability를 원래 root에서 실행하지 않는다. Root/borrowed lifetime 종류를 바꾸지 않고 양쪽 session 검사를 유지한다.
Comparable backend identity가 필요하며 non-comparable backend 값은 명시 오류다. 일반적인 상태 보유 backend는 pointer로 전달한다.

한 배치의 decoded graph만 유지한다. Reverse OneToOne eager에서 같은 owner의 서로 다른 child를 배치 경계로 숨기지 않도록
route/owner/child 키 ledger는 stream 전체에 유지한다. 키 저장량은 고유 owner 수에 비례하며 모델 전체 buffering이나 full cache는
아니다. Query/Save에 사용하는 callback context와 활성 연결의 직렬 실행 의무는 CONCURRENCY 계약을 따른다.
Facade v19 생성물을 같이 갱신하며 실행 근거·환경·남은 소비자 범위는 TEST_EVIDENCE와 CURRENT가 소유한다.
