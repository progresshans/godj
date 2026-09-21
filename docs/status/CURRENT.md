# 현재 상태

- 갱신: 2026-09-22
- 활성 작업: [GDJ-0097 모델 복합 고유성과 Category 라벨](../../work/0097-composite-uniqueness-and-labels.md)
- 최근 완료: [GDJ-0096 일대일 관계와 티켓 작업 보고서](../../work/0096-one-to-one-service-reports.md)
- 최근 전체 검증: [ServiceReport 연결 Hosted full](https://github.com/progresshans/godj/actions/runs/35652494345), source `4f92d68869d5491c4b56e83da40b79a4c7866bb7`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

OneToOne과 ServiceReport의 migration·Form/Admin/API/OpenAPI·독립 client 연결 및 Hosted 전체 통합 검증을 완료했다.
관계 선택의 권한·Category 범위, 실제 FK+UNIQUE·티켓 PROTECT와 coordinated transaction의 실패 의미를 함께 검증했다.

GDJ-0097의 복합 고유성은 선언·생성 metadata·project wire와 CreateModel/AddConstraint/RemoveConstraint 이력에 반영했다.
제약 교체·역방향·순환 FK 의존성과 중단 뒤 동일한 계획 재개를 연결하고 독립 Django의 해당 변경 관찰과 대조했다.
양 DB의 named constraint native 적용·모든 key의 catalog 검증·독립 이름 소유권과 실패 rollback을 연결했다.
SQLite remake의 남은 제약·행·sequence 보존과 PostgreSQL의 중복 제약 병합 방지도 검증했다. ORM 사전 검증과 Label 소비자는 미완료다.
Form이 제외한 Category도 저장 시 전체 조합에 포함해야 한다.

지원 범위와 제약은 [구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md),
[일대일 관계 ADR](../adr/0073-one-to-one-cardinality-and-reverse-objects.md)이 소유한다.
위 Hosted 결과는 명시한 source의 OneToOne/ServiceReport 검증이며 이후 복합 고유성의 제품 검증으로 옮기지 않는다.

## 다음 행동

복합 제약의 ORM 사전 검증을 전체 저장 candidate와 self exclusion에 연결하고,
Category별 Label의 Form/Admin/API/client로 연결한다. 현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
