# 현재 상태

- 갱신: 2026-09-27
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 전체 검증: [ManyToMany·credential/session Hosted full](https://github.com/progresshans/godj/actions/runs/36253381368), source `01b67211a083c507d5e69c6be26702439aada559`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

불변 Credential과 서버 세션의 stamp를 연결하고 누적 ManyToMany·Ticket 소비자와 Hosted 통합을 완료했다.
이후 User·Group·Permission 선언·초기 migration과 재사용 앱의 모델·파일 소유권을 구현했다.
외부 앱의 schema/ABI·소스 변경을 확인하고 호스트가 전체 관계·CASCADE·PROTECT를 소유한다.
대소문자 경로 겹침도 거부하며 양 DB 및 생성·CLI 소비자의 영향 checkpoint를 실행했다.
현재 계정과 직접·그룹 권한을 한 DB snapshot으로 읽는 Directory를 구현했다.
Account의 비밀 표현·복사 소유권, 권한 초과·잘못된 저장값·취소·종료 실패의 부분 결과 거부를 영향 checkpoint에서 검증했다.
[Credential 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[외부 app 결정](../adr/0077-reusable-app-models-and-host-relation-ownership.md),
[구현 현황](IMPLEMENTATION_MATRIX.md)이 현재 지원 범위를 설명한다.
새 identity/생성 변경에 이전 Hosted 전체 성공을 전이하지 않는다.

저장 인증·active/staff/superuser와 명시적 identity 전환을 연결해 Draft PR #1에 게시했다.
새 system migration, operator adoption/첫 계정 생성, 원자적 소유권 기록과 옛 credential 비활성 표시를 구현했다.
Article/Helpdesk·createsuperuser/runserver 및 기존 Article 세션을 이전하는 명령을 연결했다.
잘못된 fmt 형식의 credential 진단 노출도 발견해 불투명한 내부 상태로 수정했다.
영향 normal·race·CGO=0, 양 DB와 Linux의 별도 프로세스/capture·system-state 소비자 검증을 완료했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36270067564)는 `610ebe18`의 실제 Go 검사를 통과했다.
현재 변경의 Hosted 전체 검증은 관리 소비자 통합 milestone이 소유한다.
실행 상태·실패와 보정 근거는 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록한다.

## 다음 행동

전환·로그인·세션·재시작이 연결된 source를 기준으로 사용자/그룹/권한 관리의 revision·transaction·감사·credential 변경 서비스와
실제 Form/Admin/API·client를 구현한다. 이 전환 경로를 다중 사용자 관리 전체의 완료로 표시하지 않는다.
그 관리 소비자 통합 뒤 GDJ-0100의 새 source로 전체 milestone을 실행한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
