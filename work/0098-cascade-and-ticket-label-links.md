---
id: GDJ-0098
status: completed
updated: 2026-09-22
baseline_commit: "911740f8bc68469160dd6c2ebe7ca94104bdf536"
integration_owner: "root"
---

# CASCADE 관계 삭제와 TicketLabel 연결

## 결과와 선택 이유

Category의 라벨을 Ticket에 연결하는 `TicketLabel` 모델을 두고 `(ticket, label)` 조합을 고유하게 저장한다.
Ticket 또는 Label을 삭제하면 그 연결 행을 같은 transaction에서 삭제하고 다른 endpoint·연결은 보존한다.
기존 ServiceReport 등의 PROTECT는 유지하며, 보호된 후손이 있으면 연결 행도 삭제하지 않는다.
Admin/API/client에서 두 관계의 권한과 서버가 배정한 Category를 확인한다.

이 흐름은 [카탈로그](../docs/CAPABILITY_CATALOG.md)의 CASCADE와 다대다 관계에 필요한 연결 모델을 사용하는 실제 소비자다.
GDJ-0097의 named 복합 고유성을 사용하고, 현재 한 단계 PROTECT/SET_NULL 삭제기를 재귀 관계 그래프로 확장한다.
일반 ManyToMany 선언·generated 관계 관리자·add/remove/set와 조회는 계속 남은 카탈로그 범위로 관리한다.
이번 명시적 연결 모델을 그 전체 기능의 완료로 세지 않는다. 선행 GDJ-0097의 전체 통합은 완료했으며 새 CASCADE 변경은 별도로 검증한다.

## 구현 조건

- [x] 고정 Django 6.1의 독립 관찰: recursive CASCADE·SET_NULL·PROTECT 우선, 중복 경로·숨긴 역관계·OneToOne, nullable·required 순환, endpoint/연결 보존, 실제 실패 rollback
- [x] Schema IR의 CASCADE 선언·생성 metadata·project wire와 historical Create/Add/Alter/reverse·자동 계획을 연결하고 기존 데이터와 durable prefix를 보존
- [x] 양 DB의 native FK 검사 시점·catalog ownership·migration/역방향·실패 복구를 구현하고 순환 삭제의 실제 결과로 검증
- [x] 공통 ORM에서 전체 도달 그래프를 고정하고 모든 보호 검사·SET_NULL·중복 없는 삭제를 한 coordinated transaction에서 처리
- [x] Generated policy fingerprint가 transitive descendant 변경도 I/O 전에 거부하며 cache·caller publication·오류·취소·unknown outcome의 소유권을 유지
- [x] TicketLabel 모델·migration·scoped Form/Admin/API/OpenAPI/독립 client를 연결하고 권한·CSRF·중복·다른 Category·기존 ServiceReport PROTECT를 검증
- [x] 필요한 generated drift·양 DB·관련 race/CGO/process와 소비자 통합을 실행하고 source·환경·미완료 범위를 기록

## 현재와 다음

[독립 runner](../conformance/runners/django/cascade_reference.py)의 실제 양 DB 관찰을 저장했다.
PROTECT는 CASCADE로 도달한 객체에도 적용되며, 늦은 삭제 오류는 앞선 삭제와 SET_NULL을 함께 rollback한다.
자동 ManyToMany intermediary는 두 CASCADE FK와 ordered pair 고유성을 가진다.
관계 삭제는 숨겨진 역관계·중복 경로·자기 참조·서로 다른 앱의 required 순환에서도 각 행을 한 번 삭제한다.

[채택한 설계](../docs/adr/0074-cascade-delete-graph-and-constraint-timing.md)에 따라 CASCADE의 native FK를 양 DB에서 NO ACTION·DEFERRABLE INITIALLY DEFERRED로 생성한다.
정책 변경은 PostgreSQL constraint timing ALTER와 SQLite의 sealed table remake로 적용하고 역방향·catalog drift·실패 복구를 검증했다.
SQLite는 다른 unique/FK·행·sequence를 보존하고, 양 DB의 required 순환 생성·삭제와 deferred COMMIT 실패 후 연결 재사용도 확인했다.
Schema/생성 metadata/project wire·strict history·자동 계획과 저장된 prefix 이후 재개를 연결했다.

공통 ORM collector와 v2 transitive generated fingerprint를 연결했다. 실제 생성한 16개 모델의 양 DB 삭제 결과가 독립 Django의
13개 관찰과 일치하며, 중첩 조회/cleanup 실패·취소와 native commit/rollback 불확실성에서 부분 성공을 게시하지 않는다.
별도 모듈의 generated 소비자와 기존 예제의 회귀를 함께 검증한다. 일반 ManyToMany manager를 대신 구현한 것으로 보지 않는다.

TicketLabel 모델·migration·scoped Form/Admin/API/OpenAPI/독립 client를 연결했다. 두 endpoint의 Category·view 권한, 조합 중복과 생략,
CSRF·실패/unknown outcome을 검증했다. Label 삭제도 관계 삭제기를 사용하며 ServiceReport PROTECT는 링크 삭제보다 먼저 적용한다.
영향 normal/race/CGO=0과 generated drift를 통과했다. CASCADE 기반과 소비자를 합친 source `93e77bd9c19d6e7b137de3a068c40a403970e73d`의
[Hosted full 통합](https://github.com/progresshans/godj/actions/runs/35689549739)이 62/62 success와 `full_platform_verified=true`로 완료됐다.
현재 work의 CASCADE·명시적 연결 모델 소비자 범위를 완료했다. 다음은 일반 ManyToMany 선언과 관계 관리자를 통한 Ticket 라벨 편집이다.
실행 상세와 source·환경별 한계는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.
