---
id: GDJ-0099
status: active
updated: 2026-09-22
baseline_commit: "3eb403e718f511ac06dc457b41a5e50a41ec8890"
integration_owner: "root"
---

# ManyToMany 선언과 Ticket 라벨 컬렉션

Ticket에 선언한 labels 관계로 라벨 집합을 읽고 편집한다. 기존 TicketLabel의 행과 endpoint를 보존하면서
Form/Admin/API/OpenAPI·독립 client까지 연결한다. 자동 intermediary, payload가 있는 명시적 intermediary,
cross-app과 대칭/비대칭 자기 관계는 별도 generated fixture에서 같은 공개 API로 확인한다.
이는 [카탈로그](../docs/CAPABILITY_CATALOG.md)의 일반 ManyToMany 범위이며 명시적 연결 CRUD의 별칭을 추가하는 작업이 아니다.

## 구현 조건

- [x] 고정 Django의 독립 양 DB 관찰·결정성·실제 의미 변경 negative control을 보관하고 제품 지원과 구분
- [x] Columnless 관계의 Schema IR·선언·target/reverse/through ownership·endpoint field 선택과 deterministic 자동 storage를 연결
- [x] Generated metadata/project wire와 명시적 through의 historical Add/Remove/Rename·역방향·자동 계획에서 기존 행·endpoint·payload·sequence를 보존
- [x] 자동 intermediary의 historical Create/Add/Remove/Rename·역방향·자동 계획에서 endpoint·retained link ID·sequence·managed constraint ownership을 보존
- [x] 명시한 unique tuple에 대한 native conflict insert와 명확한 삽입 여부를 제공하고 동시 중복·다른 제약·오류·transaction 경계를 검증
- [ ] 공통 runtime·generated forward/reverse manager의 add/remove/clear/set, retained payload·self symmetry·취소·unknown outcome·cache 소유권을 연결
- [ ] 같은 Query AST에서 관계 조회의 multiplicity·명시적 distinct·prefetch를 연결
- [ ] Ticket 컬렉션 Form/Admin/API/OpenAPI·client에서 권한·CSRF·양쪽 Category·전체 후보·동시성·실패·durability를 확인
- [ ] 영향 compile/gofmt/drift·양 DB/race/CGO/process와 명시한 Hosted 통합 milestone의 source·환경·범위를 기록

## 현재와 다음

[독립 Django runner](../conformance/runners/django/many_to_many_reference.py)의 29개 관찰을 양 DB에서 확보했다.
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
다음은 공통 collection runtime·generated manager의 변경, 조회·prefetch와 Ticket 컬렉션 소비자다.
[Storage·변경 소유권](../docs/adr/0075-many-to-many-storage-and-mutation-ownership.md)을 채택했다.
동시 add를 사전 존재 조회와 일반 INSERT로 구현하지 않으며, 삽입하지 않은 결과에 생성 PK를 합성하지 않는다.
각 신규 기능의 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.

Signal은 DB rollback 이후에도 이미 관찰될 수 있으므로 durable commit 영수증과 구분한다.
Go의 cache/error publication·signal 지원 범위는 명시적으로 정하고, 미구현 동작을 전체 Django 동등성으로 합치지 않는다.
