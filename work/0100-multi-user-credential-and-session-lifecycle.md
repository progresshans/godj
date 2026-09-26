---
id: GDJ-0100
status: active
updated: 2026-09-27
baseline_commit: "1036bcd079e96260dc5230dab1172e0228f34ce5"
integration_owner: "root"
---

# 다중 사용자와 credential/session lifecycle

ManyToMany 기반을 User·Group·Permission에 사용하고, 단일 operator에서 실제 다중 사용자 관리로 확장한다.
모델·migration·현재 권한 평가와 세션 폐기, 관리 UI/API·실패 경로를 함께 연결한다.
새로운 저장 모델을 만들기 전에 로그인한 credential과 세션의 결합부터 명시한다.
GDJ-0099와 credential/session 기반의 Hosted 통합은 완료했다. 이후 다중 사용자 구현은 새 source에서 검증한다.

## 구현 조건

- [ ] 고정 Django의 사용자·권한·credential/session 외부 동작을 독립 관찰하고 차이와 소유권을 채택
- [x] 불변 credential snapshot과 세션 결합을 기존 인증·실제 HTTP 소비자·실패 경로에 연결
- [x] Schema IR의 User/Group/Permission과 관계·생성 model·historical migration을 연결하고 기존 operator 데이터를 보존
- [x] 현재 사용자·그룹 권한의 합집합과 active/staff/superuser 의미를 durable 조회·실제 Admin/API admission에 연결
- [ ] 사용자·credential 관리의 권한·변경 transaction·세션 폐기·감사·동시성·unknown outcome을 연결
- [ ] 실제 Form/Admin/API·독립 client에서 생성·편집·비밀번호 변경·비활성·재시작을 검증
- [ ] 영향 normal/race/CGO0·양 DB/process와 선택한 통합 milestone의 source/범위를 기록

## 현재와 다음

[Credential snapshot 결정](../docs/adr/0076-credential-snapshots-and-session-binding.md)을 구현하고 영향 checkpoint를 통과했다.
Authenticate/Resolve의 현재 credential과 권한 snapshot을 한 번에 반환하고, ID와 credential stamp를 서버 세션에 함께 저장한다.
비밀번호 교체·재해싱과 권한·username 변경의 서로 다른 세션 결과를 독립 Django 기준과 실제 HTTP에서 검증한다.
이는 다중 사용자 저장이나 password maintenance endpoint의 구현 완료가 아니다.
재사용 가능한 사용자 앱에 필요한 [외부 app 소유권](../docs/adr/0077-reusable-app-models-and-host-relation-ownership.md)을 구현하고 영향 checkpoint를 통과했다.
User·Group·Permission 선언과 초기 migration, 별도 호스트의 generated relation 소비자를 연결했다.
호스트가 라이브러리 파일을 수정하지 않고 전체 FK·CASCADE·PROTECT를 소유하는 경계를 양 DB·normal/race/CGO=0과 실제 generated 소비자에서 검증했다.
Directory가 사용자와 직접·그룹 권한을 한 native read snapshot에서 읽고 불변 Account를 반환한다.
Profile/권한 반환의 복사 소유권과 진단/JSON 경계, 잘못된 저장값·권한 한도·실패한 종료의 부분 게시 거부를 양 DB에서 검증했다.
고정 Django의 grant union·객체별 snapshot·그룹 삭제를 비교하고 role 조합을 독립 관찰했다.
저장 인증과 role admission을 소비자에 통합했다. `identity.NewAuthenticator`는 비밀번호 작업 전 snapshot을
종료하고 성공 뒤 ID·username·credential stamp가 같은지 다시 읽는다. 최신 permission/role을 반환하며 재검증·재시도하지 않는다.
Inactive는 인증/권한을 거부하고 superuser는 canonical permission을 허용한다. Admin은 active staff를 요구하며
기존 site-access permission만으로 진입하지 않는다. 추가 AccessPermission과 Authorizer의 deny overlay는 별도로 적용한다.
기존 operator 저장 형식에 없는 role을 startup 설정으로 임의 부여하지 않도록 거부한다.

저장 인증·staff admission에 이어 system schema의 명시적 identity 전환과 operator adoption을 구현해 게시했다.
PrincipalID·encoded password·기존 grant·session binding·audit 행을 보존하고 User/권한/기록/legacy 비활성 표시를 원자적으로 쓴다.
Unknown commit은 성공 receipt를 게시하지 않고 명시적 조회로 조정한다. 새 bootstrap도 옛 provisioning을 재활성화하지 않는다.
Article/Helpdesk·createsuperuser/runserver·별도 프로세스 소비자는 OpenIdentity로 연결했으며,
Article의 명시적 adoptoperator/inspect 명령은 기존 세션의 실제 Admin 진입을 검증한다.
Credential/Account/인증기/Directory/receipt의 잘못된 fmt verb 노출을 불투명한 내부 상태와 Formatter로 막았다.
영향 normal/race/CGO=0, 양 DB와 플랫폼별 process/capture·30개 system-state 소비자 관찰을 완료했다.
전체 플랫폼 milestone은 아래 사용자 관리 소비자 통합 뒤 실행한다.

관리자 비밀번호 교체 service를 구현해 게시했다. 현재 권한·revision·credential을 재확인하고 한 번의 hash 작업 뒤
User 갱신·대상 세션만의 폐기·값 없는 audit를 같은 transaction으로 처리한다. 양 DB의 실제 session/HTTP·재접속과 경쟁·실패 경계,
영향 normal/race/CGO=0·negative control을 검증했다.
다음은 사용자·그룹·권한의 나머지 관리 service와 실제 Form/Admin/API·독립 client다. Self-service password/reset도 별도 미완료 범위다.
기존 operator에 staff를 자동 추론하는 호환 분기나 in-memory role 부여는 사용하지 않는다.
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md) 한 곳에 기록한다.
