# 현재 상태

- 갱신: 2026-09-28
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료한 전체 검증: [계정 소비자·reset service·source 목록 보완 Hosted full](https://github.com/progresshans/godj/actions/runs/36328590201), source `fb817d6b58b53f147067d027624786e004df8dda`
- 선행 검증 종료(전체 미통과): [일반 계정 소비자 Hosted full](https://github.com/progresshans/godj/actions/runs/36324864466), source `b995c8c6`의 Python 시간 초과와 전체 완료 gate 실패
- 최근 영향 CI: [Reset transaction 구성 Hosted Fast](https://github.com/progresshans/godj/actions/runs/36337317864), source `507eb473`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 관리 service·Form/Admin·JSON API·독립 client와 저장 인증을 연결했다.
내장 password policy·사용 불가 password·현재 상태 표시·last_login과 세션 원자 수립을 구현했다.
source `fb817d6b`의 계정 소비자·reset service·source 목록 보완까지 Hosted 전체 platform/process 통합을 완료했다.
이후 공통 mail과 현재 reset request 변경의 검증으로 전이하지 않는다.

자기 비밀번호 확인과 교체의 service·session persistence·Web runtime을 구현하고 영향 검증을 통과했다.
현재 credential/세션 재검사, 현재 세션 회전·다른 세션 폐기·값 없는 감사의 원자 저장,
현재 profile/revision/last_login 보존과 rollback/unknown, 충돌 재시도·준비 값 소유권을 양 DB에서 검증했다.
일반 계정 로그인/logout·비밀번호 Form과 Session JSON/OpenAPI·독립 generated client를 연결했다.
관리 권한 없는 명시적 API admission과 세션 touch/cleanup 없는 preflight를 구현했고 Article의 공유 구성도 연결했다.
제품 Form·JSON/OpenAPI·독립 client와 양 DB의 normal/race/CGO=0 영향 검증을 완료했다.
선행 `b995c8c6`의 Hosted 실행은 Python 시간 초과로 전체 성공 조건을 충족하지 못했다.
실제 Go 의존성 대조에서 attestation 목록의 누락을 찾아 보완했고 독립 누락 검사도 추가했다.
수정된 `fb817d6b`의 새 capture/source binding과 전체 milestone을 확인했다.
Reset token·Form·메일의 독립 Django 양 DB 기준을 확보했다. Go reset token/key ring·현재 상태 재검사와
password/session 폐기/audit 원자 저장 service를 구현하고 영향 normal/race/CGO=0·양 DB 검증을 완료했다.
공통 mail의 불변 message·MIME·SMTP/Memory 전달과 명시적 접수 결과를 구현하고 영향 normal/race/CGO=0 검증을 완료했다.
의존성 변경의 PostgreSQL 연결·저장 인증 회귀도 세 모드에서 확인했다.
Reset 수신자 선택과 같은 snapshot의 token·메일 연결, 고정 Unicode full casefold와 공통 email 문법을 구현했다.
메일 발급 뒤 실제 password/session/audit 처리, snapshot 경합·전송 실패·취소와 source 목록을 영향 normal/race/CGO=0·양 DB에서 확인했다.
Native HTTP reset view·CSRF·DB session의 양 DB 기준을 확보했다. Token 숨김, 익명/본인/다른 계정 session과
session 저장 실패 뒤 native password가 남는 동작을 관찰했다. Prepare/ApplyIn과 빌린 scope의 token 검사를 구현해
이후 proof 저장까지 같은 transaction에 결합할 수 있게 했다. 후속 오류의 전체 rollback과 준비 값 소유권·최신 상태 재검사를
영향 normal/race/CGO=0·양 DB에서 확인했다.
Reset proof session persistence와 Web runtime을 연결했다. Entry의 ID 회전, 최종 session/token/인증 binding과 만료 재검사,
현재 proof 정리와 password/revocation/audit의 원자 저장, anonymous/본인/다른 계정 상태를 영향 normal/race/CGO=0·양 DB·실제 HTTP probe에서 확인했다.
최신 payload 보존과 교체된 proof 거부, rollback/unknown·취소·collision과 manager 한도도 확인했다.
실제 재설정 Form/API·독립 client와 공개 응답의 연결은 남아 있다.
[Credential·관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[Password 정책/출처](../../identity/PASSWORD_VALIDATION.md), [메일 결정](../adr/0078-mail-message-ownership-and-delivery.md), [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

## 다음 행동

`fb817d6b`의 Hosted milestone을 종료 확인했다. 새 capture/source binding과 선행 run의 미통과 결과를 Evidence에 보존했다.
다음 구현은 실제 reset route·Form/API·독립 client 연결이다. Token/principal을 위한 bounded path 입력과
기존 router/OpenAPI의 표현 범위를 먼저 확인하고 필요한 기반을 함께 연결한다.
확보한 native HTTP 기준에 따라 CSRF·token을 숨긴 confirmation·동일한 공개 응답/내부 오류 보고·기존 session 상태를 처리한다.
구현한 proof session/Web runtime을 사용해 최신 상태 재검사와 성공 후 정리를 같은 transaction에 유지한다.
전체 UserCreationForm과 다른 인증 provider도 미완료 요구로 유지한다.
다음 전체 platform/cold-build 검증은 소비자까지 연결된 credential lifecycle 통합 milestone이 소유하며
로컬 영향 검증과 Hosted 전체를 관성적으로 중복 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
