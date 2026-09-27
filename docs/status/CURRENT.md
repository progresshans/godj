# 현재 상태

- 갱신: 2026-09-28
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료한 전체 검증: [계정 소비자·reset service·source 목록 보완 Hosted full](https://github.com/progresshans/godj/actions/runs/36328590201), source `fb817d6b58b53f147067d027624786e004df8dda`
- 진행 중인 전체 검증: [재사용 Form·외부 CLI 수정 Hosted full](https://github.com/progresshans/godj/actions/runs/36353329053), source `563aac29d611f08e0b943cc24bfb334881e72ba1`
- 선행 전체 검증: [Reset 소비자 Hosted full](https://github.com/progresshans/godj/actions/runs/36346992368), source `1ae07db3`, 외부 CLI 준비 실패와 후속 수정은 Evidence 참조
- 현재 영향 CI: [재사용 Form Hosted Fast](https://github.com/progresshans/godj/actions/runs/36353272560), source `563aac29`, 실제 Fast Go feedback 성공
- Source·환경·scope·선행 실패와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 관리와 저장 인증, 일반 계정 login/logout·password change,
메일 요청·proof session·confirmation의 reset Form/JSON/OpenAPI·독립 client를 연결했다.
기본 User의 일반/Admin 생성 Form을 같은 IR 정의로 재사용하고, 읽기 검증·credential 준비·최종 원자 저장을 분리했다.
준비한 후보는 원래 Manager·actor에 결합하고 복사본도 한 번의 저장 시도를 공유한다.
현재 권한·중복·관계·policy 재검사, 실패·unknown과 재사용 거부를 영향 세 모드·양 DB·독립 Django 기준으로 검증했다.

선행 Hosted 전체의 외부 CLI 준비 실패에 대해 검증된 checksum을 사용하는 offline 준비와 원인 진단을 수정했다.
실제 CLI의 영향 세 모드는 통과했으며 새 source의 Hosted 전체 성공으로 합치지 않는다.

[구현 현황](IMPLEMENTATION_MATRIX.md), [인증 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[사용자 생성 Form](../../identity/USER_CREATION.md), [Account 사용법](../../identity/account/README.md),
[Password 정책/출처](../../identity/PASSWORD_VALIDATION.md), [메일 결정](../adr/0078-mail-message-ownership-and-delivery.md)을 따른다.

## 다음 행동

진행 중인 Hosted 전체의 필수 실행 owner·최종 집계·새 capture의 source 결합을 확인한다.
Custom user model, 다른 인증 provider 등 남은 요구를 현행 계약·독립 기준에 따라 이어서 구현한다.
운영 mail provider 검증도 남아 있다. 로컬 전체와 Hosted 전체를 관성적으로 중복 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
