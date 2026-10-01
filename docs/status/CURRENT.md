# 현재 상태

- 갱신: 2026-10-02
- 현재 구현: [GDJ-0108 행 잠금과 조회 후 생성 또는 갱신](../../work/0108-row-locking-and-update-or-create.md)
- 최근 완료: [GDJ-0107 단건 조회와 savepoint 기반 조회 후 생성](../../work/0107-single-object-creation-and-savepoints.md)
- 최근 전체 검증: [Hosted full 36866445270](https://github.com/progresshans/godj/actions/runs/36866445270), source `8f8831ac8231a9c32049b6649e49f8638edd5117`; 필수 owner·집계·새 capture/Git source 결합과 소비 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

행 잠금 AST·PostgreSQL native transaction/cursor와 SQLite capability 경계를 구현하고 영향 세 mode를 확인했다.
Typed/dynamic ORM·일반/eager/prefetch 생성 facade를 연결했으며 양 DB 생성 소비자의 세 mode를 확인했다.
잠금 갱신 뒤 중첩 관계와 기존 snapshot의 보존도 확인했다. UpdateOrCreate와 생성 facade의 영향 세 mode도 통과했다.
정식 Django fixture와 Go 생성 소비자의 양 DB 직접 대조도 세 mode에서 확인했다. Helpdesk ServiceReport의 업무 저장·Admin 선택 입력·API/client 연결과 영향 세 mode·실제 브라우저·훼손 대조를 완료했다.
이 새 source는 아래 GDJ-0107의 Hosted 전체 검증에 포함하지 않는다. [행 잠금 의미](../adr/0087-row-locking-and-update-or-create.md)를 따른다.

Get/GetOrCreate와 양 DB savepoint의 중첩 session·cursor·실패 경계를 구현하고 일반/eager/prefetch 생성 소비자에 연결했다.
원 metadata snapshot과 cache 소유권을 보존하며 고정 Django 기준과 실제 양 DB에서 대조했다.
Helpdesk Label 확보의 현재 인가·Category 범위·원자 audit와 Form/Admin/API·독립 client,
영향 세 mode·브라우저 및 고정 source의 Hosted 전체 통합을 완료했다.
[채택한 의미](../adr/0086-single-object-creation-and-savepoint-ownership.md)를 따른다.

## 다음 행동

GDJ-0108의 고정 source로 Hosted 전체 통합을 실행하고 필수 owner·집계·새 capture 결합을 확인한다.
해당 작업의 새 source는 위 Hosted 통합에 포함되지 않는다. Bulk·확장 query, codec 특성/storage provider·
custom user model·인증/mail provider와 다른 카탈로그 기능도 의존 순서에 따라 계속 구현한다.
현재 외부 입력이 필요한 blocker는 없다. 환경별 검증은 [검증 문서](../TESTING.md)를 따른다.

공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다. 전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다.
장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
