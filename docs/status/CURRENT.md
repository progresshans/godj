# 현재 상태

- 갱신: 2026-09-28
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료한 전체 검증: [계정 소비자·reset service·source 목록 보완 Hosted full](https://github.com/progresshans/godj/actions/runs/36328590201), source `fb817d6b58b53f147067d027624786e004df8dda`
- 최근 영향 CI: [Reset proof session/Web runtime Hosted Fast](https://github.com/progresshans/godj/actions/runs/36340235834), source `cffb6e10a84ea030d5eb41b93594df1707adbba3`, 실제 Fast Go feedback 성공
- Source·환경·scope·선행 실패와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 저장·관리 service·Form/Admin·JSON API·독립 client, 저장 인증·password policy와
사용 불가 password·last_login/session 원자 수립을 연결했다. 일반 계정 login/logout·password change의
Form·Session JSON/OpenAPI·독립 client도 연결했다. 위 `fb817d6b`의 Hosted 전체 검증 이후 변경에는 그 결과를 전이하지 않는다.

공통 mail·동일 snapshot의 reset 수신자/token/메일 구성, reset service의 Prepare/ApplyIn과 proof session/Web runtime을
구현하고 영향 normal/race/CGO=0·양 DB·실제 HTTP probe를 검증했다. Entry의 ID 회전과 완료 시 현재 proof·인증 binding·만료를
재검사하며 password/revision·대상 session 폐기·proof 정리·audit를 원자 저장한다. 새 로그인은 만들지 않는다.
Native Django의 session save 실패 뒤 부분 변경과 Go의 전체 rollback 차이는 [ADR-0076](../adr/0076-credential-snapshots-and-session-binding.md)에 있다.

실제 reset URL에 필요한 bounded `<str:name>` 경로·typed reverse/accessor·OpenAPI 투영을 구현했다.
문자열/정수 충돌, static 우선순위, escaping·byte 한도와 값 없는 오류 route 진단을 영향 세 모드에서 검증했다.
고정 Django URL 기준, 기존 생성 client 회귀와 독립 ogen HTTP probe도 확인했다.
이 라우팅 변경의 Hosted 전체는 실행하지 않았다. 공개 reset Form/API·독립 제품 client는 아직 연결하지 않았다.

[구현 현황](IMPLEMENTATION_MATRIX.md), [인증 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[Password 정책/출처](../../identity/PASSWORD_VALIDATION.md), [메일 결정](../adr/0078-mail-message-ownership-and-delivery.md),
[경로 결정](../adr/0045-closed-parameterized-routing-and-reverse.md)을 따른다.

## 다음 행동

기존 authenticated-only API와 별개로, 로그인 전 요청도 CSRF를 검사하는 명시적 API admission/OpenAPI 계약을 연결한다.
이를 실제 reset email 요청·token entry·confirmation Form/JSON, 독립 generated client와 Article 공유 구성에 사용한다.
유효한 email 제출의 동일 공개 응답과 별도 내부 오류 보고, token을 숨긴 redirect·no-store/no-referrer,
현재 proof 재검사와 성공 정리·기존 session 상태·실패/unknown 무재시도를 끝까지 검증한다.
전체 UserCreationForm과 다른 인증 provider도 남은 요구다.

다음 전체 platform/cold-build 검증은 소비자까지 연결된 credential lifecycle 통합 milestone이 소유한다.
로컬 영향 검증과 Hosted 전체를 관성적으로 중복 실행하지 않는다.
장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
