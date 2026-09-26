# 현재 상태

- 갱신: 2026-09-26
- 활성 구현: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료: [GDJ-0098 CASCADE와 TicketLabel 연결](../../work/0098-cascade-and-ticket-label-links.md)
- 최근 전체 검증: [CASCADE·TicketLabel Hosted full](https://github.com/progresshans/godj/actions/runs/35689549739), source `93e77bd9c19d6e7b137de3a068c40a403970e73d`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

ManyToMany의 Schema IR·자동/명시적 through migration·공통 mutation runtime·generated model/session facade와
같은 typed/dynamic Query AST의 mixed 관계 조건을 연결했다.
직접 컬렉션 prefetch를 기존 through query·target eager projection과 generated typed/path selector에 연결했다.
여러 관계·양방향/self·nullable duplicate와 독립 cache를 처리하며 전체 조회가 성공한 뒤에만 결과를 반환한다.
Custom target filter의 연결 행 scope와 owner 귀속을 보존하는 Query AST·양 DB compiler 기반을 연결했다.
ManyToMany 중첩 typed/path 선택과 하위 collection cache를 공통 model materialization에 연결했다.
Target Filter·OrderBy·Distinct와 명시한 하위 prefetch 설정을 generated typed/path 구성에 연결했다.
Manager 변경 뒤 기본 조회 복귀와 held query의 조건·cache 보존을 구분한다.
설정된 target query의 eager·추가 prefetch에도 기존 하위 설정을 전달한다.
단일 FK/역방향 OneToOne prefetch와 하위 컬렉션을 연결하고, root eager와 양쪽 호출 순서로 조합해 이미 읽은 부모를 재사용한다.
Reverse FK collection과 ManyToMany target eager 구성을 같은 graph와 model 접근자에 연결했다.
명시한 target eager는 파생 조회에 유지하며 역방향 manager 초기화는 기본 scope로 돌아간다.
Owner별 slice를 공통 runtime과 generated Snapshot·Limit/Offset·Read에 연결했다.
Named snapshot은 일반 manager와 분리되고 하위 관계·eager graph·세션 수명을 보존한다.
Custom ManyToMany 조회는 전체 owner 집합을 유지하며 큰 integer membership은 양 DB에서 compact parameter로 읽는다.
단일 관계 target의 Filter·OrderBy·Distinct·eager와 하위 설정을 연결했다.
조회로 생긴 부재는 명시적 관계 해제와 구분하며 모델 파생·저장에서도 기존 FK를 보존한다.
지원 범위와 남은 제한은 [구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md),
[관계 소유권 결정](../adr/0075-many-to-many-storage-and-mutation-ownership.md)을 따른다.

## 다음 행동

남은 prefetch materialization 계약을 완성하고 Ticket 라벨 컬렉션 편집으로 이어간다.
Ticket 저장 transaction에서 권한·양쪽 Category·전체 원하는 집합을 다시 검증하고 Form/Admin/API/OpenAPI·독립 client까지 완성한다.
이 소비자 통합 뒤 GDJ-0099 Hosted 전체 milestone을 검증한다. 명시적 연결 CRUD나 root manager만으로 전체 소비자를 완료로 세지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증을 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
