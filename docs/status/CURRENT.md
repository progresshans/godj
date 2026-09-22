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

공통 ORM collector·transitive generated fingerprint와 TicketLabel의 Form/Admin/API/client 소비자는 아직 남아 있다.
현재 generated project deleter와 ORM은 CASCADE를 명시적으로 거부한다. Native 기반 구현을 전체 CASCADE 지원으로 세지 않는다.
지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.
위 Hosted full은 복합 고유성·Label의 명시한 source 결과이며 이후 CASCADE 변경의 검증으로 옮기지 않는다.

## 다음 행동

전체 CASCADE 행을 중복 없이 수집하고 모든 PROTECT 검사 뒤 SET_NULL·삭제를 한 transaction에서 실행하는 공통 ORM을 구현한다.
같은 변경 묶음에서 descendant 정책 변경을 감지하는 generated fingerprint와 실패·취소·caller/cache 보존을 검증한다.
그 뒤 TicketLabel 소비자로 연결한다. 현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
