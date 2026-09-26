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
GDJ-0099의 Hosted 통합은 아직 열려 있으며 다음 전체 milestone에서 누적 변경과 함께 검증한다.

## 구현 조건

- [ ] 고정 Django의 사용자·권한·credential/session 외부 동작을 독립 관찰하고 차이와 소유권을 채택
- [x] 불변 credential snapshot과 세션 결합을 기존 인증·실제 HTTP 소비자·실패 경로에 연결
- [ ] Schema IR의 User/Group/Permission과 관계·생성 model·historical migration을 연결하고 기존 operator 데이터를 보존
- [ ] 현재 사용자·그룹 권한의 합집합과 active/staff/superuser 의미를 durable 조회·실제 Admin/API admission에 연결
- [ ] 사용자·credential 관리의 권한·변경 transaction·세션 폐기·감사·동시성·unknown outcome을 연결
- [ ] 실제 Form/Admin/API·독립 client에서 생성·편집·비밀번호 변경·비활성·재시작을 검증
- [ ] 영향 normal/race/CGO0·양 DB/process와 선택한 통합 milestone의 source/범위를 기록

## 현재와 다음

[Credential snapshot 결정](../docs/adr/0076-credential-snapshots-and-session-binding.md)을 구현하고 영향 checkpoint를 통과했다.
Authenticate/Resolve의 현재 credential과 권한 snapshot을 한 번에 반환하고, ID와 credential stamp를 서버 세션에 함께 저장한다.
비밀번호 교체·재해싱과 권한·username 변경의 서로 다른 세션 결과를 독립 Django 기준과 실제 HTTP에서 검증한다.
이는 다중 사용자 저장이나 password maintenance endpoint의 구현 완료가 아니다.
다음은 모델 기반 사용자·권한 저장과 기존 operator 상태를 연결하는 migration/권한 경계다.
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md) 한 곳에 기록한다.
