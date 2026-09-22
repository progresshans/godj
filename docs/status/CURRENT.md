# 현재 상태

- 갱신: 2026-09-22
- 현재: GDJ-0098 완료, 일반 ManyToMany 선언과 Ticket 라벨 편집 작업을 준비 중
- 최근 완료: [GDJ-0098 CASCADE와 TicketLabel 연결](../../work/0098-cascade-and-ticket-label-links.md)
- 최근 전체 검증: [CASCADE·TicketLabel Hosted full](https://github.com/progresshans/godj/actions/runs/35689549739), source `93e77bd9c19d6e7b137de3a068c40a403970e73d`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

CASCADE의 선언·생성 metadata·historical migration·양 DB native FK와 공통 재귀 삭제기를 연결했다.
TicketLabel의 migration·scoped Form/Admin/API/OpenAPI·독립 client와 두 endpoint의 Category·권한·고유성·CSRF·실패 경로를 구현했다.
[재귀 삭제 설계](../adr/0074-cascade-delete-graph-and-constraint-timing.md)에 따른 PROTECT 우선과 링크 CASCADE·반대 endpoint 보존까지 검증했다.
위 source의 Hosted full을 완료했으며 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

## 다음 행동

고정 Django의 독립 ManyToMany 관찰을 기준으로 columnless 선언·자동/명시적 intermediary·generated manager와 Ticket 라벨 편집을 연결한다.
동시 중복 add, set의 retained link 보존·실패 rollback, cache 소유권과 기존 TicketLabel 데이터 보존을 먼저 명시한다.
명시적 연결 모델의 CRUD가 일반 ManyToMany 구현을 대신한 것으로 세지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
