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
TicketLabel의 Form/Admin/API/client 소비자와 해당 전체 통합 검증은 아직 남아 있다.
지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.
위 Hosted full은 복합 고유성·Label의 명시한 source 결과이며 이후 CASCADE 변경의 검증으로 옮기지 않는다.

## 다음 행동

TicketLabel의 모델·migration·scoped Form/Admin/API/OpenAPI/client를 연결한다. Ticket/Label 삭제 시 연결 행을 정리하고,
기존 ServiceReport PROTECT·두 관계의 권한·Category 범위·중복·실패 rollback을 함께 검증한다. 현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
