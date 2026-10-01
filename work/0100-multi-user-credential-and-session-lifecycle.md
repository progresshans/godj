---
id: GDJ-0100
status: completed
updated: 2026-09-28
baseline_commit: "1036bcd079e96260dc5230dab1172e0228f34ce5"
integration_owner: "root"
---

# 다중 사용자와 credential/session lifecycle

ManyToMany 기반을 User·Group·Permission에 사용하고 단일 operator에서 실제 다중 사용자 관리로 확장했다.
모델·migration·현재 권한 평가와 session 폐기, 관리 UI/API·실패 경로를 함께 연결했다.
이 작업의 완료는 아래 범위와 source에 한정하며 전체 identity 기능 카탈로그의 완료를 뜻하지 않는다.

## 구현 조건

- [x] 고정 Django의 사용자·권한·credential/session 외부 동작을 독립 관찰하고 차이와 소유권을 채택
- [x] 불변 credential snapshot과 세션 결합을 기존 인증·실제 HTTP 소비자·실패 경로에 연결
- [x] Schema IR의 User/Group/Permission과 관계·생성 model·historical migration을 연결하고 기존 operator 데이터를 보존
- [x] 현재 사용자·그룹 권한의 합집합과 active/staff/superuser 의미를 durable 조회·실제 Admin/API admission에 연결
- [x] 사용자·credential 관리의 권한·변경 transaction·세션 폐기·감사·동시성·unknown outcome을 연결
- [x] 실제 Form/Admin/API·독립 client에서 생성·편집·비밀번호 변경·비활성·재시작을 검증
- [x] 영향 normal/race/CGO0·양 DB/process와 선택한 통합 milestone의 source/범위를 기록

## 구현과 통합 범위

[Credential snapshot·session 결합](../docs/adr/0076-credential-snapshots-and-session-binding.md)과
[외부 app·호스트 관계 소유권](../docs/adr/0077-reusable-app-models-and-host-relation-ownership.md)을 구현했다.
Directory·저장 인증·staff admission·명시적 operator 전환과 User/Group/Permission 관리를 Article/Helpdesk·CLI에 연결했다.
현재 인가와 revision, 관계 집합·사용자 grant 한도를 최종 transaction에서 재검사하고 session 변경·감사를 원자적으로 쓴다.
권한 회수·동시 변경·PROTECT/CASCADE·rollback과 unknown 결과, 별도 process의 재시작도 검증했다.

기본 User의 일반/Admin 생성 Form은 공통 IR Definition과 Bind/Prepare/Commit을 재사용한다.
원래 Manager/actor에 결합한 후보의 복사본은 한 번의 저장 시도를 공유한다. 입력 정규화·password confirmation과
내장 password validator·unusable credential·현재 password 상태 표시를 실제 Admin/API와 독립 client에 연결했다.
저장 last_login·현재 credential의 session 수립, 일반 계정 login/logout·자기 password 변경과 session 회전/폐기를 구현했다.

Reset은 현재 credential/email/last_login/active·expiry를 재검사하고 password/session/audit를 원자적으로 변경한다.
같은 읽기 snapshot의 수신자·token·메일 준비, proof session·token을 숨기는 실제 Form/JSON 경로·CSRF·익명 공개 응답과
독립 client를 연결했다. SMTP 접수·거절·unknown·취소/no-retry는 [메일 소유권](../docs/adr/0078-mail-message-ownership-and-delivery.md)을 따른다.
고정 Django의 입력·저장·HTTP·session·메일 관찰과 Go의 transaction 소유권 차이를 구분한다.

EmailField와 선행 CI 보완을 포함한 source `ea2867f4323fb34e713fc1d10d8a62c05c785d37`의
[Hosted full 36374533286](https://github.com/progresshans/godj/actions/runs/36374533286)이 완료됐다.
62 jobs·8개 실행 owner와 해당 run의 새 capture·Git blob source 결합을 확인했다.
단계별 영향 검사와 최초 실패·수정·환경·source의 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md) 한 곳에 기록한다.
이전 구현 과정을 현재 단계와 섞지 않도록 작업 이력은 Git과 Evidence에서 확인한다.

## 후속 범위

이후 Blank·model candidate·DB Form 후처리는 [GDJ-0102](0102-model-blank-policy-and-post-clean.md)의 별도 source와 검증이다.
Custom user model·전체 ModelForm 저장/변환·다른 인증 provider·운영 mail provider와 기능 카탈로그의 남은 요구는 계속 남아 있다.
이 작업의 기본 User·관리/계정 소비자 구현과 Hosted 결과를 해당 미완료 범위에 전이하지 않는다.
