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

User/password 관리와 scalar-default backfill·Permission revision migration까지 게시·영향 검증했다.
Group/Permission 관리 service도 게시하고 [Hosted Fast](https://github.com/progresshans/godj/actions/runs/36281969809)를 통과했다.
현재 권한과 revision을 확인하고 그룹 권한 합집합을 전체 사용자에 대해 검사한다.
호스트 관계 삭제와 직접 소유자의 revision 증가·감사를 원자적으로 반영하며 credential·session bytes를 보존한다.
조회에서 인가 처리 오류를 대체 권한으로 우회하지 않는 공통 경계도 보완했다.

영향 normal/race/CGO=0·양 DB와 독립 Django 비교·negative control을 통과했다.
설계 의미는 [관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
실행한 source·환경·범위와 실패 근거는 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록한다.
실제 관리 JSON API·OpenAPI를 Article의 인증된 composition에 연결했다.
관리 API의 영향 normal/race/CGO=0·양 DB와 기존 generated client 호환성, negative control을 통과했다.
전용 관리 Form/Admin·새 API의 독립 client와 GDJ-0100 전체 검증은 미완료다.

## 다음 행동

새 관리 API의 독립 generated client와 사용자·비밀번호·그룹·권한 관리 Form/Admin·관련 선택 목록을 연결한다.
Self-service/reset과 나머지 credential lifecycle도 미완료다. 관리 소비자 통합 뒤 GDJ-0100의 새 source로 전체 milestone을 실행한다.
이전 Hosted 전체 성공을 이후 identity 변경의 검증으로 전이하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
