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
[Credential 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[외부 app 결정](../adr/0077-reusable-app-models-and-host-relation-ownership.md),
[구현 현황](IMPLEMENTATION_MATRIX.md)이 현재 지원 범위를 설명한다.
새 identity/생성 변경에 이전 Hosted 전체 성공을 전이하지 않는다.

## 다음 행동

저장 모델의 비밀 표현 경계와 현재 사용자·그룹 권한 합집합, active/staff/superuser admission을 연결한다.
기존 operator 데이터의 명시적 migration·credential 변경·감사·동시성을 보존하고 실제 관리 Form/Admin/API·client로 이어간다.
그 소비자 통합 뒤 GDJ-0100의 새 source로 전체 milestone을 실행한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
