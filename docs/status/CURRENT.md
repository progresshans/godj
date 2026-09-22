# 현재 상태

- 갱신: 2026-09-22
- 활성 구현: [GDJ-0098 CASCADE와 TicketLabel 연결](../../work/0098-cascade-and-ticket-label-links.md)
- 최근 완료: [GDJ-0097 모델 복합 고유성과 Category 라벨](../../work/0097-composite-uniqueness-and-labels.md)
- 최근 전체 검증: [복합 고유성·Label Hosted full](https://github.com/progresshans/godj/actions/runs/35678713385), source `231260c5116bb7cfe157cab54ceb404e05c8ba43`
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
첫 Hosted full에서 누락된 외부 migration backend fixture의 새 제약 메서드를 찾아 수정했다.
외부 소비자의 정상/오용 compile 검증을 다시 통과했고 수정 source의 Hosted full도 최종 전체 판정까지 완료했다.

CASCADE의 독립 기준은 보호된 후손·중복 경로·숨긴 역관계·required/nullable 순환과 늦은 실패 rollback을 포함한다.
GDJ-0098의 선언·historical 정책 변경과 양 DB FK 검사 시점을 구현 중이며 전체 삭제 엔진과 TicketLabel 소비자는 아직 남아 있다.

지원 범위와 제약은 [구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md),
[일대일 관계 ADR](../adr/0073-one-to-one-cardinality-and-reverse-objects.md)이 소유한다.
위 Hosted 결과는 명시한 source의 복합 고유성·Label 검증이며 이후 CASCADE 변경의 검증으로 옮기지 않는다.

## 다음 행동

GDJ-0098은 CASCADE의 native FK 검사 시점·historical 변경과 공통 ORM의 재귀 삭제 그래프를 함께 구현한다.
두 작업의 source와 검증 범위를 구분하며 현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
