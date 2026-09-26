# 현재 상태

- 갱신: 2026-09-27
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 전체 검증: [ManyToMany·credential/session Hosted full](https://github.com/progresshans/godj/actions/runs/36253381368), source `01b67211a083c507d5e69c6be26702439aada559`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 저장, 현재 권한 snapshot, 저장 인증·role admission과 명시적 operator 전환을 구현했다.
Article/Helpdesk·CLI와 durable session 소비자를 연결했고 관리자 비밀번호 교체 service까지 게시·영향 검증했다.
[Credential·관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[외부 app·호스트 관계 소유권](../adr/0077-reusable-app-models-and-host-relation-ownership.md),
[구현 현황](IMPLEMENTATION_MATRIX.md)이 지원 범위를 설명한다.

사용자 생성·조회·편집·삭제 service를 추가했다. 현재 인가와 revision을 확인하고 profile·role·그룹/직접 권한·감사를
원자적으로 변경한다. 비활성/삭제는 대상 session을 폐기하며 삭제에는 호스트의 전체 관계 정책을 사용한다.
양 DB·영향 normal/race/CGO=0, 독립 Django 비교와 실패·동시성·negative control을 통과했다.
`48aefdb1`으로 게시했고 [Hosted Fast](https://github.com/progresshans/godj/actions/runs/36275314844)의 실제 Go 검사도 성공했다.
현재 source·scope와 실패 보정 근거는 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 고정했다.
이 변경의 전용 관리 Form/Admin/API/client와 별도 process·Hosted 전체 검증은 아직 수행하지 않았다.

## 다음 행동

Group/Permission 자체의 생성·조회·편집·삭제와 revision·transaction·감사를 구현하고,
사용자·비밀번호·그룹·권한 관리의 실제 Form/Admin/API·독립 client를 연결한다.
Self-service/reset과 나머지 credential lifecycle도 미완료다. 관리 소비자 통합 뒤 GDJ-0100의 새 source로 전체 milestone을 실행한다.
이전 Hosted 전체 성공을 이후 identity 변경의 검증으로 전이하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
