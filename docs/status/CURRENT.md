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
공통 ModelMultipleChoice와 Ticket.labels의 실제 Form/Admin·API/OpenAPI·독립 생성 client를 연결했다.
기존 TicketLabel 행·키를 보존하며 scalar 변경과 전체 라벨 집합 교체를 같은 relation transaction에서 처리한다.
권한·CSRF·양쪽 Category, 생략/빈 배열, 실패·취소·동시성·재시작을 양 DB 영향 범위에서 검증했다.
지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md),
[관계 소유권 결정](../adr/0075-many-to-many-storage-and-mutation-ownership.md)을 따른다.

## 다음 행동

통합 source를 게시하고 GDJ-0099 Hosted 전체 milestone을 실행한다.
실제 full gate·필수 실행·동일 source의 capture를 확인한 뒤 해당 통합을 마무리한다.
영향 범위의 로컬 성공을 전체 플랫폼 검증이나 프레임워크 전체 완성으로 세지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증을 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
