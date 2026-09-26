---
id: GDJ-0099
status: active
updated: 2026-09-26
baseline_commit: "3eb403e718f511ac06dc457b41a5e50a41ec8890"
integration_owner: "root"
---

# ManyToMany 선언과 Ticket 라벨 컬렉션

Ticket에 labels 관계를 선언해 라벨 집합을 읽고 편집한다. 기존 TicketLabel의 행과 endpoint를 보존하면서
Form/Admin/API/OpenAPI·독립 client까지 연결한다. 자동 intermediary, payload가 있는 명시적 intermediary,
cross-app과 대칭/비대칭 자기 관계는 별도 generated fixture에서 같은 공개 API로 확인한다.
이는 [카탈로그](../docs/CAPABILITY_CATALOG.md)의 일반 ManyToMany 범위이며 명시적 연결 CRUD의 별칭을 추가하는 작업이 아니다.

## 구현 조건

- [x] 고정 Django의 독립 양 DB 관찰·결정성·실제 의미 변경 negative control을 보관하고 제품 지원과 구분
- [x] Columnless 관계의 Schema IR·선언·target/reverse/through ownership·endpoint field 선택과 deterministic 자동 storage를 연결
- [x] Generated metadata/project wire와 명시적 through의 historical Add/Remove/Rename·역방향·자동 계획에서 기존 행·endpoint·payload·sequence를 보존
- [x] 자동 intermediary의 historical Create/Add/Remove/Rename·역방향·자동 계획에서 endpoint·retained link ID·sequence·managed constraint ownership을 보존
- [x] 명시한 unique tuple에 대한 native conflict insert와 명확한 삽입 여부를 제공하고 동시 중복·다른 제약·오류·transaction 경계를 검증
- [x] 공통 runtime·generated forward/reverse root manager의 add/remove/clear/set, retained payload·self symmetry·취소·unknown outcome·cache 소유권을 연결
- [x] 통합 model facade와 빌린/coordinated transaction session의 명시적 collection composition을 연결
- [x] 같은 Query AST에서 관계 조회의 multiplicity·명시적 distinct·prefetch를 연결
- [x] Ticket 컬렉션 Form/Admin/API/OpenAPI·client에서 권한·CSRF·양쪽 Category·전체 후보·동시성·실패·durability를 확인
- [ ] 영향 compile/gofmt/drift·양 DB/race/CGO/process와 명시한 Hosted 통합 milestone의 source·환경·범위를 기록

## 현재와 다음

[독립 Django runner](../conformance/runners/django/many_to_many_reference.py)의 51개 관찰을 양 DB에서 확보했다.
Columnless 선언·자동 through, 중복 add·실제 두 연결의 동시 add, set의 retained identity·payload·늦은 오류 rollback,
자기 관계·조회 중복·cache snapshot·실제 historical migration과 signal을 관찰한다. GoDj의 구현 증거로 세지 않는다.
Native conflict insert를 공통 AST·양 DB·ordinary/relation/coordinated session에 연결하고 영향 normal/race/CGO=0을 통과했다.
Columnless 선언·정규화 IR과 자동 storage projection, generated descriptor/schema companion·프로젝트 binding을 연결했다.
Project wire의 closed shape·정확한 escaped bytes·사전 resource budget과 model clone/hash/equality에 관계 의미가 남는다.
실제 별도 generated module에서 cross-app 자동/명시적 through·symmetrical/directed self·stale descriptor 거부를 확인했다.
명시적 through의 AddManyToMany/RemoveManyToMany/RenameManyToMany를 closed definition·digest·bounded 재구성·자동 계획과 양 DB에 연결했다.
기존 FK 모델을 참조하는 선언 변경은 DDL 없이 전체 관련 catalog와 history/revision을 검증한다. Nullable·중복 허용·payload·self와
cross-app 모델의 기존 행을 보존하며, 최초 생성은 모델·선택한 FK가 준비된 뒤 선언을 추가하고 모든 durable prefix에서 재개한다.
자동 intermediary의 historical Create/Add/Remove/Rename·reverse·자동 계획·forward SQL projection과 raw CreateModel의 columnless 선언을 연결했다.
연결 PK 0·empty/high-water sequence와 물리 identity를 보존하는 rename, 혼합 선언·중간 table·다른 automatic storage 참조의 순서를 검증한다.
관리 밖의 FK/view·catalog drift·이름 충돌·늦은 unique 실패·취소에서는 이력과 저장을 보존한다. 실행 범위는 TEST_EVIDENCE를 따른다.
공통 root collection runtime과 generated BindCollections의 forward/reverse add/remove/clear/set를 연결했다.
Columnless 관계를 같은 AST의 physical join으로 읽고 explicit duplicate·Distinct와 nullable through를 처리한다.
Generated through Create input, native pair conflict, incoming delete graph의 전체 root 수집과 cache 무효화를 사용한다.
Generated model의 forward/reverse collection 접근자와 명시적 UsingSession/InSession을 연결했다.
Native session의 lifetime 검사를 공통 QuerySet·eager·projection/aggregate와 model/view에 적용하고 outer commit 소유권을 유지한다.
일반 mixed 관계 조건을 같은 typed/dynamic AST에 연결했다. 한 Filter/연속 Filter의 alias·부정 EXISTS·nullable multiplicity와
manager core filter를 고정 reference에 비교한다. QueryRelation/ChainRelations/BindRelations로 통합했고 reverse query adapter를 제거했다.
직접 ManyToMany prefetch와 generated typed/path selector를 연결했다. 기존 through query·target eager projection으로 묶어서 읽으며
nullable duplicate·양방향/self·owner multiplicity·전체 성공 후 cache publication과 materialization별 소유권을 유지한다.
일반 빌린 session에서도 읽을 수 있고 relation capability 없는 변경은 빈 입력도 거부한다.
Custom target query의 기존 조건·정렬·DISTINCT와 연결 행 scope를 유지하는 owner projection을 Query AST·양 DB에 연결했다.
Nested/filtered/eager prefetch의 독립 결과와 cache/query-count 기준을 확보했다.
Eager selected graph와 collection cache의 결과 표현을 통합하고 기본 중첩 ManyToMany를 generated model 접근자까지 연결했다.
Typed child와 문자열 경로는 같은 bounded tree를 사용하며 같은 관계의 하위 선택을 합쳐 일괄 조회한다.
중첩 결과는 query의 immutable 평가와 반환 model의 mutable cache를 구분하며, custom query의 Filter/정렬은 held QuerySet에 남고
manager 변경 뒤에는 기본 관계 조회로 돌아간다. Target Filter·OrderBy·Distinct와 명시한 하위 설정의 파생 조회,
lookup 재정의 거부·typed/path 조합을 실제 generated 소비자에 연결했다. 설정된 target query의 eager·추가 prefetch가
기존 하위 설정과 공유 node budget을 유지하도록 연결했다. 단일 FK/역방향 OneToOne prefetch와 하위 컬렉션을
root eager에 양쪽 호출 순서로 연결하고, 이미 읽은 부모를 재사용한다. 공통 cache 전달·NULL 관계의 session 수명을 반영했다.
단일 관계·eager 조합 checkpoint를 통과했다. Reverse FK collection과 ManyToMany의 target eager·하위 prefetch를
공통 graph와 generated model 접근자에 연결했다. 명시한 eager 설정은 held query의 파생 조회에 유지하며
역방향 manager Fresh/Invalidate는 기본 scope로 돌아간다. Facade ABI v16과 실제 generated 소비자의 통합 checkpoint를 통과했다.
Owner별 slice의 named snapshot·일반 manager 오류·관계 scope·중복·eager 기준을 독립 관찰에 추가했다.
Query AST의 owner별 window를 양 DB compiler에 연결했다. 첫 join의 grouping owner와 마지막 join의 membership partition을
구분하고 필요한 owner 귀속 필터는 순번 계산 후에 적용한다. Named Snapshot·Limit/Offset·Read를 공통 runtime과
generated facade에 연결했다. 일반 manager·서로 다른 snapshot·중복 owner의 cache는 독립이며 nested/eager graph와
실패 시 전체 publication·세션 수명을 유지한다. Custom ManyToMany는 grouping/membership owner의 전체 집합을 한 query에서
유지하고, 큰 integer IN은 양 DB의 compact parameter로 조회한다. Facade ABI v17과 생성물을 갱신했다.
SinglePrefetch의 Filter·OrderBy·Distinct·target eager와 custom/명시적 child 구분을 연결했다.
이미 eager로 읽은 부모를 재사용하고, 필터로 사라진 required target은 전체 목록을 실패시키지 않고 관계 접근에서 missing 오류를 반환한다.
조회 부재를 해제 할당과 나눈 cache 상태는 파생·Save에서 FK와 cache를 보존한다. Facade v18·relation object v6과 생성물을 갱신했다.
Streaming의 배치 크기·중단·warm cache 우회·owner scope에 대한 독립 관찰을 추가했다.
`db.BatchQueryer`를 양 DB의 ordinary/relation/coordinated session에 연결했다. Scan과 batch yield를 나누고
PostgreSQL FETCH rowset을 닫은 뒤 같은 세션에서 하위 조회·쓰기를 실행한다. SQLite raw transaction의 Goexit 정리 누락도 보완했다.
일반 backend의 배치 조회와 pinned 실행 범위를 추가했다. 반환 executor는 종료 뒤 원래 backend로 복귀하며 borrowed session 수명과 구분한다.
고정 연결의 raw transaction은 종료 SQL을 직접 소유하고, SQLite retention과 PostgreSQL cursor/불명 outcome 정리를 유지한다.
공통 model 배치 materialization과 generated plain/eager/prefetch Iterate를 연결했다. Callback context가 같은 backend의 ORM I/O를
해당 실행 연결로 전달하며 model origin·identity·기존 cache는 유지한다. 양 DB 영향 checkpoint와 nil 행 방어의 후속 검증을 마쳤다.
기존 raw Iterate는 graph 없는 callback으로 설정을 버리지 않도록 계속 명시 오류로 거부한다. Generated Iterate는 명시한 양수 배치 크기를 요구한다.
공통 ModelMultipleChoice Form의 immutable int64 집합·정확한 membership·raw-input 변경 감지를 연결했다.
Admin은 명시한 후보 권한과 전체 선택 집합을 재검증하며, 여러 초기값·거부된 원문을 escape해 보존한다.
Ticket.labels를 기존 TicketLabel through와 연결하는 선언·generated accessor·0020 historical migration을 추가했다.
선언의 적용/역적용·재시작은 기존 행·link ID·삭제된 ID 이후의 sequence 상한을 보존한다.
Ticket의 실제 Form/Admin·API/OpenAPI·독립 생성 client에 라벨 집합 편집을 연결했다.
일반 integer-list serializer는 I/O 없이 정확한 키·생략/빈 배열을 보존하고, 순수 model encoder는 이미 읽은 관계만 투영한다.
Ticket 저장은 현재 owner·전체 target scope와 admitted 권한을 확인하고 scalar·Set·응답 재조회를 같은 relation transaction에서 수행한다.
고정 Django/DRF의 양 DB 48개 관찰을 비교하고, 엄격한 JSON 타입·진단과 저장 시 재검증 차이는 따로 기록했다.
같은 source의 영향 normal/race/CGO=0에서 양 DB HTTP·실패/취소·독립 runtime 경쟁·재시작과 생성 client를 검증했다.
GDJ-0099 Hosted 전체 milestone은 다음 행동이며, 로컬 영향 검증으로 마지막 통합 조건을 완료 표시하지 않는다.
[Storage·변경 소유권](../docs/adr/0075-many-to-many-storage-and-mutation-ownership.md)을 채택했다.
동시 add를 사전 존재 조회와 일반 INSERT로 구현하지 않으며, 삽입하지 않은 결과에 생성 PK를 합성하지 않는다.
각 신규 기능의 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.

Signal은 DB rollback 이후에도 이미 관찰될 수 있으므로 durable commit 영수증과 구분한다.
Go의 cache/error publication·signal 지원 범위는 명시적으로 정하고, 미구현 동작을 전체 Django 동등성으로 합치지 않는다.
