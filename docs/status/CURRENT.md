# 현재 상태

- 갱신: 2026-09-28
- 현재 통합 작업: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 구현·영향 검증 완료: [GDJ-0101 이메일 필드와 모델 입력 검증](../../work/0101-email-fields-and-model-input-validation.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료한 전체 검증: [계정 소비자·reset service·source 목록 보완 Hosted full](https://github.com/progresshans/godj/actions/runs/36328590201), source `fb817d6b58b53f147067d027624786e004df8dda`
- 선행 전체 검증: [재사용 Form·외부 CLI 수정 Hosted full](https://github.com/progresshans/godj/actions/runs/36353329053), source `563aac29`, exact macOS 시간 제한 취소·최종 집계 실패
- 최근 Hosted 실패: [EmailField Fast](https://github.com/progresshans/godj/actions/runs/36359488366)·[full](https://github.com/progresshans/godj/actions/runs/36359485025), source `2b612968`, capability·Article 이력 기대값 누락으로 실패 후 full 대체 취소
- 최근 Hosted 종료: [수정 full](https://github.com/progresshans/godj/actions/runs/36364531786), source `b6bff156224a205bd4247b42e72be46254c49828`, 60 성공·Intel race package 시간 초과·최종 집계 실패; 같은 source의 [Fast](https://github.com/progresshans/godj/actions/runs/36364530446)는 실제 Go 검사까지 성공
- 진행 중: [시간 예산 보완 full](https://github.com/progresshans/godj/actions/runs/36369062484), source `bb9eae3c7e50df29cb19e15a8407d9e703026d0c`
- Source·환경·scope·선행 실패와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

EmailField를 별도 IR kind, 문자열 query·저장, Form/serializer·EmailInput에 연결했다.
기본 User의 email migration과 Admin/API·독립 client, 계정 reset의 공통 검증을 구현했다.
기존 데이터·credential/session/audit·호스트 관계를 보존하며 canonical migration 이력을 확인한다.
고정 Django/DRF와 양 DB, 영향 normal/race/CGO=0·실제 migrate/runserver/재시작 검증을 완료했다.
Native와 다른 NUL transport 경계, 입력 default·기존 출력의 의미는 [ADR-0079](../adr/0079-email-fields-and-input-semantics.md)를 따른다.

User·Group·Permission 관리, 저장 인증과 일반 계정 login/logout·password change/reset,
일반/Admin 사용자 생성 Form의 재사용과 prepare/commit 소유권도 구현했다.
상세 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md), [Account 사용법](../../identity/account/README.md),
[사용자 생성 Form](../../identity/USER_CREATION.md)을 따른다.

## 다음 행동

누락된 backend capability·Article 이력 기대값 네 테스트를 수정하고 영향 세 모드 검증을 완료했다.
Intel race의 aggregate package 예산을 70분·job 예산을 90분으로 조정한 새 Hosted 전체를 확인한다.
제품·테스트 선택·필수 실행 검사는 동일하다. Exact macOS의 native 전체 검사는 선행 실행에서 성공했다.
필수 owner·최종 집계·새 capture의 source 결합이 모두 확인되어야 전체 통합 완료로 기록한다.
Custom user model·전체 ModelForm 후처리·다른 인증 provider와 운영 mail provider 검증은 남아 있다.
로컬 전체와 Hosted 전체를 관성적으로 중복 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
