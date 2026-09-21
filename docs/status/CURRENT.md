# 현재 상태

- 갱신: 2026-09-21
- 활성 구현: [GDJ-0096 일대일 관계와 티켓 작업 보고서](../../work/0096-one-to-one-service-reports.md)
- 최근 완료: [GDJ-0095 모델 고유성과 외부 참조 중복 방지](../../work/0095-model-uniqueness.md)
- 최근 전체 검증: [고유성 수직 연결 Hosted full](https://github.com/progresshans/godj/actions/runs/35607632806), source `42ae95d3b1a891e6a0692fb0399968e483f4d907`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Column uniqueness를 Schema IR·생성 모델·migration·SQLite/PostgreSQL·ORM 사전 검증에서 Form/Admin/API와
실제 Helpdesk/client까지 연결하고 통합 검증했다. 지원 범위와 제약은
[구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md), [고유성 소유권](../adr/0072-column-uniqueness-and-constraint-ownership.md)이 소유한다.

다음 모델 기능은 명시적 일대일 관계다. 티켓별 작업 보고서를 0..1건 연결하는 실제 흐름을 선택했다.
양 DB의 독립 Django 관찰에서 단일 역방향 객체·없는 관계·cache·unique FK와의 차이·입력 검증·저장 실패·FK 변경을 확인했다.
현재는 기준 관찰을 고정한 단계이며 GoDj의 OneToOne 선언·생성 ORM·소비자는 아직 구현되지 않았다.

## 다음 행동

일대일 cardinality를 IR·historical state·migration과 생성 metadata에 연결하고 양 DB가 실제 FK와 고유성을 함께 보장하게 한다.
단일 역방향 조회·eager/prefetch·cache·assignment/delete를 typed/dynamic 경로에 일관되게 연결한다.
이를 작업 보고서의 Form/Admin/API·권한·실패 복구까지 이어간다. 구체적인 완료 조건은 활성 work가 소유한다.
현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
