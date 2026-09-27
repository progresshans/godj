# 현재 상태

- 갱신: 2026-09-27
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 전체 검증: [ManyToMany·credential/session Hosted full](https://github.com/progresshans/godj/actions/runs/36253381368), source `01b67211a083c507d5e69c6be26702439aada559`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 저장과 현재 권한 snapshot, 저장 인증·staff admission·operator 전환을 연결했다.
관리 service·JSON API/OpenAPI·독립 Session/Bearer generated client와 실제 Identity Form/Admin을 구현했다.
Article의 Admin은 사용자·그룹·권한 CRUD, revision 조건, 별도 password command를 제공한다.
현재 action 권한의 관련 선택 목록과 인가·감사를 같은 read snapshot에서 읽으며, 호스트의 전체 삭제 정책을 사용한다.
비밀번호 확인·공백 보존·비공개 입력과 host password 정책을 hash 전/마지막 fence 검사에 연결했다.
View-only 상세에서는 편집 가능한 선택 목록을 읽지 않고, 변경 POST는 데이터 접근 전에 거부한다.

Identity Admin·기존 Form/Admin/API·Article/Helpdesk와 양 DB의 영향 normal/race/CGO=0, 독립 Django 입력 subset,
negative control을 통과했다. 구현 `9fe12534`를 게시했고
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36295406909)의 실제 Go feedback도 성공했다.
이는 전체 UserCreationForm이나 GDJ-0100 전체 platform/process 검증의 완료가 아니다.
[Credential·관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[호스트 관계 소유권](../adr/0077-reusable-app-models-and-host-relation-ownership.md),
[구현 현황](IMPLEMENTATION_MATRIX.md)에 지원 범위를 기록한다.

## 다음 행동

고정 Python의 Unicode 16과 Go/x/text의 Unicode 15 사이 username 정규화·문자 판정 차이를 닫는다.
Form의 150자 입력과 기존 credential의 256-byte 제한을 함께 정리하고 내장 password strength validator를 구현한다.
세 Unicode version probe는 실제 차이로 기록했으며 일반 입력 subset PASS로 덮지 않는다.
Self-service/reset·사용 불가능한 password·last_login lifecycle도 남아 있다.
GDJ-0100의 다음 전체 platform/process milestone은 관리 소비자와 남은 입력 경계를 정리한 새 source에서 실행한다.
이전 Hosted 전체 성공을 이후 identity 변경의 검증으로 전이하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
