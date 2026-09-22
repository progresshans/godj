# 현재 상태

- 갱신: 2026-09-22
- 활성 구현: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료: [GDJ-0098 CASCADE와 TicketLabel 연결](../../work/0098-cascade-and-ticket-label-links.md)
- 최근 전체 검증: [CASCADE·TicketLabel Hosted full](https://github.com/progresshans/godj/actions/runs/35689549739), source `93e77bd9c19d6e7b137de3a068c40a403970e73d`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

CASCADE·명시적 TicketLabel 소비자의 Hosted full을 완료했다. 위 source 이후 GDJ-0099를 시작했다.
고정 Django의 독립 ManyToMany 기준과 실제 의미 변경 negative control을 보관했다.
[Storage·변경 소유권](../adr/0075-many-to-many-storage-and-mutation-ownership.md)에 따라 명시한 non-null unique tuple의 native 삽입을
공통 AST·양 DB·transaction session에 연결하고 영향 normal/race/CGO=0을 통과했다.
Columnless 선언·Schema IR·자동 storage projection·생성 metadata/binding·bounded project wire를 연결했다.
영향 normal/race/CGO=0과 다섯 기존 project의 generated drift를 통과했다.
명시적 through의 Add/Remove/Rename·reverse·자동 계획을 연결했다. 양 DB의 기존 행·payload·sequence·catalog를 보존하며
선택한 두 FK·dependency ancestry와 전체 관련 모델을 검증한다. 최신 영향 normal/race/CGO=0과 다섯 기존 project의 generated drift를 통과했다.
자동 intermediary의 Create/Add/Remove/Rename·reverse·자동 계획과 raw CreateModel의 columnless 선언도 연결했다.
Rename은 연결 PK와 시퀀스·물리 테이블 identity를 보존하며, 생성·제거는 소유한 intermediary만 변경한다.
Transient table·중첩 storage 의존성·SQL projection·실패 rollback을 포함해 영향 normal/race/CGO=0과 generated drift를 통과했다.
지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

## 다음 행동

Generated manager의 add/remove/clear/set를 공통 runtime·native conflict insert·소유 transaction에 연결한다.
자동/명시적 through와 nullable·중복 허용·payload·자기 관계의 차이를 실제 저장 제약에 맞게 처리한다.
이후 같은 Query AST의 컬렉션 조회·prefetch와 Ticket 라벨 컬렉션 편집으로 이어간다.
동시 중복 add, set의 retained link 보존·실패 rollback과 cache 소유권을 같은 구현에서 검증한다.
명시적 연결 모델의 CRUD가 일반 ManyToMany 구현을 대신한 것으로 세지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
