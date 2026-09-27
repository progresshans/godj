# 현재 상태

- 갱신: 2026-09-28
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 진행 중인 전체 검증: [계정 소비자·reset service·source 목록 보완 Hosted full](https://github.com/progresshans/godj/actions/runs/36328590201), source `fb817d6b58b53f147067d027624786e004df8dda`
- 선행 검증 종료(전체 미통과): [일반 계정 소비자 Hosted full](https://github.com/progresshans/godj/actions/runs/36324864466), source `b995c8c6`의 Python 시간 초과와 전체 완료 gate 실패
- 최근 완료한 전체 검증: [저장 로그인·세션 Hosted full](https://github.com/progresshans/godj/actions/runs/36315320971), source `63b07213ecaaea37a2270b33d8c806a982817269`
- 최근 영향 CI: [Reset service Hosted Fast](https://github.com/progresshans/godj/actions/runs/36328406907), source `fb817d6b`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 관리 service·Form/Admin·JSON API·독립 client와 저장 인증을 연결했다.
내장 password policy·사용 불가 password·현재 상태 표시·last_login과 세션 원자 수립을 구현했다.
source `63b07213`의 Hosted 전체 platform/process 통합이 완료됐다. 이후 오류 수정과 새 기능의 검증으로 전이하지 않는다.

자기 비밀번호 확인과 교체의 service·session persistence·Web runtime을 구현하고 영향 검증을 통과했다.
현재 credential/세션 재검사, 현재 세션 회전·다른 세션 폐기·값 없는 감사의 원자 저장,
현재 profile/revision/last_login 보존과 rollback/unknown, 충돌 재시도·준비 값 소유권을 양 DB에서 검증했다.
일반 계정 로그인/logout·비밀번호 Form과 Session JSON/OpenAPI·독립 generated client를 연결했다.
관리 권한 없는 명시적 API admission과 세션 touch/cleanup 없는 preflight를 구현했고 Article의 공유 구성도 연결했다.
제품 Form·JSON/OpenAPI·독립 client와 양 DB의 normal/race/CGO=0 영향 검증을 완료했다.
선행 `b995c8c6`의 Hosted 실행은 Python 시간 초과로 전체 성공 조건을 충족하지 못했다.
실제 Go 의존성 대조에서 attestation 목록의 누락을 찾아 보완했고 독립 누락 검사도 추가했다.
수정된 `fb817d6b`의 새 capture와 전체 milestone 실행을 시작했다. 결과 확인 전이다.
Reset token·Form·메일의 독립 Django 양 DB 기준을 확보했다. Go reset token/key ring·현재 상태 재검사와
password/session 폐기/audit 원자 저장 service를 구현하고 영향 normal/race/CGO=0·양 DB 검증을 완료했다.
메일 수신자 선택·전달과 실제 재설정 Form/API는 아직 연결 전이다.
[Credential·관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[Password 정책/출처](../../identity/PASSWORD_VALIDATION.md), [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

## 다음 행동

`fb817d6b`의 Hosted milestone·실제 필수 실행과 새 capture/source binding을 확인한다. 선행 run의 미통과 결과는 Evidence에 보존했다.
다음 구현은 reset의 명시적 메일 전달·수신자 선택과 실제 Form/API·독립 client다. Native HTTP reset view도 별도로 관찰한다.
전체 UserCreationForm과 다른 인증 provider도 미완료 요구로 유지한다.
다음 전체 platform/cold-build 검증은 소비자까지 연결된 credential lifecycle 통합 milestone이 소유하며
로컬 영향 검증과 Hosted 전체를 관성적으로 중복 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
