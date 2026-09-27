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

고정 Unicode 16의 NFKC·소문자·문자 판정을 연결하고, credential/CLI 1,024바이트와 User IR 256자 한도를 구분했다.
관리 생성의 150자 정책과 긴 기존 이름을 보존하는 편집을 구현했다. 새 bootstrap은 NFKC, legacy adoption은 기존 바이트를 보존한다.
고정 Django의 네 내장 password validator와 API의 명시적 정책 선택을 구현했다. Article은 하나의 설정을 Admin/API에 함께 적용한다.
현재 인가·hash 전/최종 fence 검사, 원문 보존·session/audit와 실패 경계를 기존 소비자에 연결해 영향 normal/race/CGO=0·양 DB를 통과했다.
이는 전체 UserCreationForm이나 GDJ-0100의 모든 lifecycle 완료를 뜻하지 않는다.
[Credential·관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[Password 정책/출처](../../identity/PASSWORD_VALIDATION.md), [구현 현황](IMPLEMENTATION_MATRIX.md)에 지원 범위를 기록한다.

## 다음 행동

관리 소비자와 Unicode/password 입력 경계가 정리된 `5c2045f4`에서
[Hosted full](https://github.com/progresshans/godj/actions/runs/36300136646)을 실행했고, 통합 소비자의 이전 migration/config 기대와 참조 잠금 누락을 확인했다.
현재 migration 집합·User/권한/전환 기록의 재시작·scalar backfill SQL·실제 외부 컴파일 판정·출처/byte lock을 함께 수정하고 영향 통합 검증을 통과했다.
이 수정 source의 Hosted 전체를 확인한 뒤 다음 lifecycle을 이어간다.
그 다음은 사용 불가능한 password의 credential/session 표현과 last_login을 연결하고, self-service/reset을 구현한다.
이전 Hosted 전체 성공을 이후 identity 변경의 검증으로 전이하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
