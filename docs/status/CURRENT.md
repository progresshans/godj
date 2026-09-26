# 현재 상태

- 갱신: 2026-09-27
- 활성 구현: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 전체 검증: [ManyToMany·credential/session Hosted full](https://github.com/progresshans/godj/actions/runs/36253381368), source `01b67211a083c507d5e69c6be26702439aada559`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

User·Group·Permission 저장, 현재 권한 snapshot, 저장 인증·role admission과 명시적 operator 전환을 구현했다.
Article/Helpdesk·CLI와 durable session 소비자를 연결했고 관리자 비밀번호 교체 service까지 게시·영향 검증했다.
[Credential·관리 결정](../adr/0076-credential-snapshots-and-session-binding.md),
[외부 app·호스트 관계 소유권](../adr/0077-reusable-app-models-and-host-relation-ownership.md),
[구현 현황](IMPLEMENTATION_MATRIX.md)이 지원 범위를 설명한다.

사용자 생성·조회·편집·삭제 service와 호스트 관계 삭제를 `48aefdb1`까지 게시하고 영향 검증했다.
Group/Permission 관리에 필요한 Permission revision을 추가하면서, 기존 행이 있는 테이블의 scalar-default AddField를
양 DB lifecycle·자동 계획·SQL 출력에 연결했다. 기존 identity migration은 보존하고 새 migration으로 revision 1을 채운다.
새 runtime은 이 migration 누락을 시작 시 거부하며 관계·credential·session·audit는 보존한다.

이 변경의 영향 normal/race/CGO=0·양 DB·migration process·독립 Django 비교와 변형 검사를 통과했다.
기본값 보존·DB default 제거·실패 rollback·sequence 상한과 잘못된 Permission revision 거부를 확인했다.
[Remake와 backfill 결정](../adr/0064-historical-relation-graphs-and-sqlite-remakes.md),
[독립 관찰의 sequence 차이](../DEVIATIONS.md#dev-0013--sqlite-migration-remake에서-삭제된-id의-sequence-상한을-보존),
현재 source·scope와 실행 근거는 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록한다.
Group/Permission service·전용 관리 Form/Admin/API/client와 GDJ-0100 전체 검증은 미완료다.

## 다음 행동

Group/Permission 자체의 생성·조회·편집·삭제와 revision·transaction·감사를 구현하고,
사용자·비밀번호·그룹·권한 관리의 실제 Form/Admin/API·독립 client를 연결한다.
Self-service/reset과 나머지 credential lifecycle도 미완료다. 관리 소비자 통합 뒤 GDJ-0100의 새 source로 전체 milestone을 실행한다.
이전 Hosted 전체 성공을 이후 identity 변경의 검증으로 전이하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
