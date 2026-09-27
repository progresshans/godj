# 현재 상태

- 갱신: 2026-09-28
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료한 전체 검증: [계정 소비자·reset service·source 목록 보완 Hosted full](https://github.com/progresshans/godj/actions/runs/36328590201), source `fb817d6b58b53f147067d027624786e004df8dda`
- 최근 영향 CI: [Reset 소비자 Hosted Fast](https://github.com/progresshans/godj/actions/runs/36345090890), source `e3ec9a2a75f6983d8e71ea8842aa8f924609575a`, 실제 Fast Go feedback 성공
- Source·환경·scope·선행 실패와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 저장·관리 service·Form/Admin·JSON API·독립 client, 저장 인증·password policy와
사용 불가 password·last_login/session 원자 수립을 연결했다. 일반 계정 login/logout·password change의
Form·Session JSON/OpenAPI·독립 client도 연결했다. 위 `fb817d6b`의 Hosted 전체 결과를 이후 변경에 전이하지 않는다.

Reset mail·proof session·bounded string routing에 실제 email 요청·token entry·confirmation Form/JSON을 연결했다.
익명 CSRF admission과 별도 proof-cookie 계약, 유효 email의 동일 공개 응답·내부 오류 보고,
token-free redirect·privacy header와 현재 proof 재검사·원자 완료를 Article 공유 구성과 독립 generated client로 검증했다.
영향 normal/race/CGO=0·양 DB·실제 HTTP의 필수 실행과 실패/unknown·negative control이 통과했다.
이 consumer source의 [Hosted 전체](https://github.com/progresshans/godj/actions/runs/36345110007)에서 dependency checksum,
외부 CLI fixture의 의존성 준비, API 관찰 도구의 module lookup과 Python observer의 version 제한 오류를 확인했다.
현재 source에서 수정하고 관련 Go 세 모드·실제 PostgreSQL/CLI·Python 네 버전의 영향 검증을 통과했다.
해당 Hosted 실행은 전체 성공이 아니며 수정 source에 대해 새 전체 검증이 필요하다.

[구현 현황](IMPLEMENTATION_MATRIX.md), [인증 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[Account 사용법](../../identity/account/README.md), [Password 정책/출처](../../identity/PASSWORD_VALIDATION.md),
[메일 결정](../adr/0078-mail-message-ownership-and-delivery.md)을 따른다.

## 다음 행동

수정 source를 게시하고 credential lifecycle의 Hosted 전체 platform/cold-build 통합 milestone을 다시 수행한다.
새 PostgreSQL capture와 source/producer·필수 실행 owner를 확인하며 실패는 실제 원인과 해당 범위로 수정한다.
로컬 전체와 Hosted 전체를 관성적으로 중복 실행하지 않는다. 전체 UserCreationForm·다른 인증 provider와
운영 mail provider 검증은 남은 범위이며 이번 reset 소비자 완료로 대체하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
