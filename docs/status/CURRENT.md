# 현재 상태

- 갱신: 2026-09-27
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 전체 검증: [ManyToMany·credential/session Hosted full](https://github.com/progresshans/godj/actions/runs/36253381368), source `01b67211a083c507d5e69c6be26702439aada559`
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

고정 Django의 양 DB credential/session 관찰을 기준으로 불변 Credential 결과와 서버 세션의 stamp를 연결했다.
비밀번호 교체·재해싱, 권한·username 변경과 실패 시 세션 폐기를 실제 HTTP 소비자·양 DB 영향 checkpoint에서 검증했다. 다음은
모델 기반 사용자·그룹·권한 저장과 기존 operator 데이터의 migration으로 이어간다.

GDJ-0099와 credential/session 변경을 포함한 Hosted 전체의 필수 실행·최종 gate·같은 실행의 capture 검증을 완료했다.
이후 구현하는 다중 사용자 저장과 외부 앱 생성 경계는 새 source에서 별도로 검증한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증을 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
