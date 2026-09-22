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
SQLite remake의 남은 제약·행·sequence 보존과 PostgreSQL의 중복 제약 병합 방지도 검증했다.
ORM의 복합 사전 검증은 부분 수정의 생략 member·기본값을 포함하고 자기 행·SQL NULL을 구분한다.
Category별 Label의 모델·migration·Admin CRUD·API 검색/페이지/CRUD·OpenAPI·독립 client를 연결했다.
Form/API가 받지 않는 Category도 transaction에서 확인하고 전체 조합에 포함한다. 양 DB와 독립 client의 영향 범위 로컬 검증을 완료했다.
GDJ-0097 전체 통합·Hosted full 검증은 아직 남아 있다.

지원 범위와 제약은 [구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md),
[일대일 관계 ADR](../adr/0073-one-to-one-cardinality-and-reverse-objects.md)이 소유한다.
위 Hosted 결과는 명시한 source의 OneToOne/ServiceReport 검증이며 이후 복합 고유성의 제품 검증으로 옮기지 않는다.

## 다음 행동

구현 source를 고정해 GDJ-0097의 Hosted full 통합 milestone을 실행하고 필수 환경·owner·실패 경로의 완료를 확인한다.
그 결과를 현재 source에 귀속한 뒤 다음 카탈로그 요구를 선택한다. 현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
