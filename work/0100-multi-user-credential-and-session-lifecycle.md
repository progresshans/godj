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

[Credential snapshot·관리 결정](../docs/adr/0076-credential-snapshots-and-session-binding.md)과
[외부 app·호스트 관계 소유권](../docs/adr/0077-reusable-app-models-and-host-relation-ownership.md)을 구현했다.
현재 credential·role·grant snapshot과 session stamp, User/Group/Permission 모델·migration,
Directory·저장 인증·staff admission·명시적 operator 전환을 Article/Helpdesk·CLI·durable 소비자에 연결했다.
관리자 비밀번호 교체는 현재 인가·revision·credential을 재확인하고 User·대상 session 폐기·감사를 원자적으로 쓴다.
이 기반은 게시와 영향 검증을 완료했다. 이전 Hosted 전체 결과는 이후 관리 변경의 전체 검증으로 전이하지 않는다.

사용자 생성·조회·편집·삭제 service를 구현하고 영향 checkpoint를 통과했다.
생성은 현재 add/change 권한을 모두 확인하고 hash 전 읽기를 종료한 뒤 transaction에서 재검사한다.
편집은 expected revision, scalar와 그룹/직접 grant의 전체 집합을 처리하며 no-op은 revision/audit를 늘리지 않는다.
비활성/삭제의 대상 session 폐기, 호스트 관계 정책과 감사 rollback, unknown 결과 미게시를 양 DB에서 검증했다.
독립 Django manager/UserAdmin 관찰과 Unicode 정규화, 실제 HTTP/session·runtime 재접속, 두 연결의 경쟁과 실패 경계를 포함한다.
전체 UserCreationForm validator와 전용 management endpoint가 구현됐다고 주장하지 않는다.

다음은 Group/Permission 자체의 관리 service와 실제 사용자·비밀번호·그룹·권한 Form/Admin/API·독립 client다.
Self-service password/reset·사용 불가능한 password lifecycle·last_login 갱신도 별도 미완료 범위다.
전체 플랫폼/process milestone은 관리 소비자 통합 뒤 새 source에서 실행한다.
기존 operator에 staff를 자동 추론하는 호환 분기나 in-memory role 부여는 사용하지 않는다.
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md) 한 곳에 기록한다.
