# 현재 상태

- 갱신: 2026-09-26
- 활성 구현: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료: [GDJ-0098 CASCADE와 TicketLabel 연결](../../work/0098-cascade-and-ticket-label-links.md)
- 최근 전체 검증: [CASCADE·TicketLabel Hosted full](https://github.com/progresshans/godj/actions/runs/35689549739), source `93e77bd9c19d6e7b137de3a068c40a403970e73d`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

ManyToMany의 Schema IR·자동/명시적 through migration·공통 mutation runtime·generated model/session facade를 연결했다.
Typed/dynamic Query AST의 mixed 관계 조건, direct/nested/filtered/eager prefetch와 owner별 named slice,
배치 model graph와 generated Iterate를 구현했다. 실행 source·환경별 검증은 TEST_EVIDENCE를 따른다.
공통 ModelMultipleChoice Form과 Admin의 전체 집합 재검증·다중 선택 표시를 연결했다.
Helpdesk의 Ticket.labels 선언과 historical migration은 기존 TicketLabel 행·키 할당을 보존한다.
Ticket의 실제 편집·저장·API에 labels를 공개하는 통합은 아직 남아 있다.
지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md),
[관계 소유권 결정](../adr/0075-many-to-many-storage-and-mutation-ownership.md)을 따른다.

## 다음 행동

Ticket 저장 transaction에서 권한·양쪽 Category·전체 원하는 집합을 다시 검증하고,
공통 다중 선택을 실제 Ticket Form/Admin·API/OpenAPI·독립 client에 연결한다.
동시성·실패·durability를 검증한 뒤 GDJ-0099 Hosted 전체 milestone을 실행한다.
명시적 연결 CRUD나 공통 Form/Admin 기반만으로 전체 소비자를 완료로 세지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증을 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
