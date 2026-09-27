# 현재 상태

- 갱신: 2026-09-28
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료한 전체 검증: [계정 소비자·reset service·source 목록 보완 Hosted full](https://github.com/progresshans/godj/actions/runs/36328590201), source `fb817d6b58b53f147067d027624786e004df8dda`
- 진행 중인 전체 검증: [Reset 소비자·실행 기반 수정 Hosted full](https://github.com/progresshans/godj/actions/runs/36346992368), source `1ae07db3644290df4cc2c319d6f7dc44f8f7fd57`
- 최근 영향 CI: [수정 source Hosted Fast](https://github.com/progresshans/godj/actions/runs/36346945448), source `1ae07db3`, 실제 Fast Go feedback 성공
- Source·환경·scope·선행 실패와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 관리와 저장 인증, 일반 계정 login/logout·password change,
메일 요청·proof session·confirmation의 reset Form/JSON/OpenAPI·독립 client를 연결했다.
Reset 소비자 Hosted에서 발견한 dependency 준비·module source lookup·Python runtime 오류를 수정했고,
수정 source의 Fast는 성공했다. 새 Hosted 전체와 두 capture의 검증 상태는 위 Evidence를 따른다.

Admin 생성에 read-only post-clean 검증을 연결했다. 이름 중복·문법·password confirmation/strength의 복합 오류를
고정 Django 양 DB 기준으로 대조하고, invalid 입력의 ID/hash/write 없음과 현재 인가·최종 write fence를
영향 normal/race/CGO=0·양 DB·실제 HTTP 및 부정 대조에서 검증했다. 이 후속 변경에는 진행 중인 이전 source의 전체 결과를 전이하지 않는다.

[구현 현황](IMPLEMENTATION_MATRIX.md), [인증 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[Account 사용법](../../identity/account/README.md), [Password 정책/출처](../../identity/PASSWORD_VALIDATION.md),
[메일 결정](../adr/0078-mail-message-ownership-and-delivery.md)을 따른다.

## 다음 행동

진행 중인 Hosted 전체의 필수 실행 owner와 최종 집계를 확인하며 실패는 실제 원인과 해당 범위로 수정한다.
일반 재사용 UserCreationForm의 준비·저장 lifecycle과 custom user model, 다른 인증 provider 등
남은 요구를 현행 계약·독립 기준에 따라 이어서 구현한다. 운영 mail provider 검증도 남아 있다.
로컬 전체와 Hosted 전체를 관성적으로 중복 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
