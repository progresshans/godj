# 현재 상태

- 갱신: 2026-09-22
- 활성 구현: [GDJ-0098 CASCADE와 TicketLabel 연결](../../work/0098-cascade-and-ticket-label-links.md)
- 최근 완료: [GDJ-0097 모델 복합 고유성과 Category 라벨](../../work/0097-composite-uniqueness-and-labels.md)
- 최근 전체 검증: [복합 고유성·Label Hosted full](https://github.com/progresshans/godj/actions/runs/35678713385), source `231260c5116bb7cfe157cab54ceb404e05c8ba43`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

CASCADE의 독립 Django 기준과 [재귀 삭제 설계](../adr/0074-cascade-delete-graph-and-constraint-timing.md)를 채택했다.
선언·생성 metadata·project wire와 historical Create/Add/정책 Alter/reverse·자동 계획·durable prefix 재개를 연결했다.
양 DB의 native deferred FK·catalog timing 검증과 정책 변경/역방향을 구현하고 영향 범위의 normal/race/CGO=0을 통과했다.
SQLite remake의 행·sequence·다른 제약 보존, required 순환과 실패 rollback/연결 정리도 확인했다.

공통 ORM collector와 v2 transitive generated fingerprint를 연결했다. 생성한 모델의 양 DB 삭제 결과가 독립 Django의 13개 관찰과 일치한다.
중첩 조회/cleanup 실패·취소·native 결과 불확실성의 caller 보존과 기존 generated 소비자의 회귀를 함께 검증한다.
TicketLabel의 migration·scoped Form/Admin/API/OpenAPI·독립 client를 연결하고, 양쪽 Category·권한·중복·CSRF·PROTECT·CASCADE 보존과 실패 경로의 로컬 checkpoint를 통과했다.
API의 복수 권한 검사와 검색 없는 Admin 등록도 연결했다. 새 source의 Hosted 전체 통합 검증은 남아 있다.
지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.
위 Hosted full은 복합 고유성·Label의 명시한 source 결과이며 이후 CASCADE 변경의 검증으로 옮기지 않는다.

## 다음 행동

CASCADE 기반과 TicketLabel 소비자를 합친 고정 source의 Hosted full 통합을 실행한다.
완료 inventory·실제 checkout·환경별 결과를 확인하고 GDJ-0098을 정리한다. 현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
