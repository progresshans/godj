# 현재 상태

- 갱신: 2026-09-22
- 활성 작업: [GDJ-0097 모델 복합 고유성과 Category 라벨](../../work/0097-composite-uniqueness-and-labels.md)
- 최근 완료: [GDJ-0096 일대일 관계와 티켓 작업 보고서](../../work/0096-one-to-one-service-reports.md)
- 최근 전체 검증: [ServiceReport 연결 Hosted full](https://github.com/progresshans/godj/actions/runs/35652494345), source `4f92d68869d5491c4b56e83da40b79a4c7866bb7`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

OneToOne의 명시적 cardinality·migration·양 DB FK+UNIQUE·단일 reverse 조회/prefetch·조건·mixed eager tree와
facade·명시적 assignment를 구현했다. Helpdesk의 ServiceReport migration·Form/Admin/API/OpenAPI와 독립 생성 client도 연결했다.
관계 선택은 요청별 권한·Category 범위의 불변 snapshot을 사용하고 저장 transaction에서 범위를 다시 확인한다.
보고서의 정상 부재·재할당·고유성·삭제 후 부모 보존과 티켓 PROTECT, 취소·rollback 오류의 실행 경계를 유지한다.
Runtime의 관계 삭제도 일반 쓰기와 같은 DB coordination fence를 사용한다.

지원 범위와 제약은 [구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md),
[일대일 관계 ADR](../adr/0073-one-to-one-cardinality-and-reverse-objects.md)이 소유한다.
OneToOne과 위 소비자까지 포함한 source의 Hosted 전체 통합 검증을 완료했다. 아직 모델 복합 고유성이나 Label은 구현하지 않았다.

## 다음 행동

Category별 Label 이름의 복합 고유성을 다음 소비자로 선택했다. 고정 Django의 실제 제약·진단·migration 관찰부터 확인하고,
IR·historical wire·양 DB ownership·Form/Admin/API/client를 연결한다. 현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
