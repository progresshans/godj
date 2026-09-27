# 현재 상태

- 갱신: 2026-09-27
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 전체 검증: [저장 로그인·세션 Hosted full](https://github.com/progresshans/godj/actions/runs/36315320971), source `63b07213ecaaea37a2270b33d8c806a982817269`
- 최근 영향 CI: [로그인 오류 경계 Hosted Fast](https://github.com/progresshans/godj/actions/runs/36316637831), source `94d0229b`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 관리 service·Form/Admin·JSON API·독립 client와 저장 인증을 연결했다.
내장 password policy·사용 불가 password·현재 상태 표시·last_login과 세션 원자 수립을 구현했다.
source `63b07213`의 Hosted 전체 platform/process 통합이 완료됐다. 이후 오류 수정과 새 기능의 검증으로 전이하지 않는다.

자기 비밀번호 확인과 교체의 service·session persistence·Web runtime을 구현하고 영향 검증을 통과했다.
현재 credential/세션 재검사, 현재 세션 회전·다른 세션 폐기·값 없는 감사의 원자 저장,
현재 profile/revision/last_login 보존과 rollback/unknown, 충돌 재시도·준비 값 소유권을 양 DB에서 검증했다.
제품 Form·JSON/OpenAPI·독립 client 연결은 아직 남아 있다.
[Credential·관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[Password 정책/출처](../../identity/PASSWORD_VALIDATION.md), [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

## 다음 행동

자기 비밀번호 변경의 제품 소비자를 이어서 구현한다.
모델 권한이 필요 없는 본인 인증 API/OpenAPI 계약과 제품 Form·JSON 소비자를 함께 연결한 뒤 reset으로 이어간다.
전체 UserCreationForm과 다른 인증 provider도 미완료 요구로 유지한다.
다음 전체 platform/cold-build 검증은 소비자까지 연결된 credential lifecycle 통합 milestone이 소유하며
로컬 영향 검증과 Hosted 전체를 관성적으로 중복 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
