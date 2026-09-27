# 현재 상태

- 갱신: 2026-09-27
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 전체 검증: [관리 소비자·입력 경계 Hosted full](https://github.com/progresshans/godj/actions/runs/36305013585), source `f3264aeffce3c6a07ea07bb3a41097edf26ce15a`
- 실행 중 통합: [저장 로그인·세션 Hosted full](https://github.com/progresshans/godj/actions/runs/36315320971), source `63b07213ecaaea37a2270b33d8c806a982817269`
- 최근 영향 CI: [Password 상태 Hosted Fast](https://github.com/progresshans/godj/actions/runs/36310871626), source `8316b27a`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 저장과 현재 권한 snapshot, 저장 인증·staff admission·operator 전환을 연결했다.
관리 service·JSON API/OpenAPI·독립 Session/Bearer client와 실제 Identity Form/Admin을 구현했다.
현재 인가·revision·관계·session 폐기와 감사 transaction, host 삭제 정책·rollback/unknown 경계를 유지한다.
Unicode 16 정규화·입력 한도와 네 내장 password validator를 Admin/API의 명시적 공통 정책에 연결했다.

사용 불가능한 password의 credential/identity 표현, 관리 생성·설정·반복 설정·복구와 API null을 구현했다.
Admin의 확인 명령과 생성 선택도 연결했다. 새 범위의 영향 normal/race/CGO=0·양 DB·독립 참조와 소비자 검증을 통과했다.
현재 password 사용 상태를 같은 snapshot에서 계산하는 Admin/API·독립 client 표시도 구현하고 영향 검증을 통과했다.
last_login과 세션 수립을 같은 transaction에 연결하고 현재 credential/admission 재검사·계정 교체 데이터 분리를 구현했다.
Admin/API·독립 client와 재시작 소비자를 연결했고 영향 normal/race/CGO=0·양 DB 검증을 통과했다.
재시작 소비자의 실제 실행과 전체 platform 검증은 이번 Hosted 통합 milestone에서 확인한다.
직전 통합 source `f3264aef`의 Hosted full도 완료했으며, 이후 password 기능의 전체 검증으로 전이하지 않는다.
[Credential·관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[Password 정책/출처](../../identity/PASSWORD_VALIDATION.md), [구현 현황](IMPLEMENTATION_MATRIX.md)에 지원 범위를 기록한다.

## 다음 행동

source `63b07213`의 Hosted 전체 platform/process 실행 결과와 각 owner의 증거를 확인한다.
이어서 self-service password/change·reset을 구현한다. 전체 UserCreationForm과 다른 인증 provider도 남은 요구와 구분한다.
이번 저장 로그인·세션 수립 통합 milestone이 새 source의 전체 platform/cold-build 검증을 소유한다.
로컬에서는 영향 검증을 수행하며 Hosted 전체 실행과 중복한 로컬 전체 검증을 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
