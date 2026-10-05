# 현재 상태

- 갱신: 2026-10-06
- 현재 작업: [GDJ-0111 QuerySet 갱신과 쓰기 표현식](../../work/0111-query-update-and-writable-expressions.md)
- 통합 대기: [GDJ-0109 Native bulk 생성](../../work/0109-bulk-creation-and-ticket-import.md), [GDJ-0110 Native bulk update](../../work/0110-bulk-update-and-ticket-editing.md)
- 진행 중인 Hosted full: [37335450948](https://github.com/progresshans/godj/actions/runs/37335450948), source `19dd8178ae6f4a3d853ab6879543efa333cffb8a`
- 최근 완료: [GDJ-0108 행 잠금과 조회 후 생성 또는 갱신](../../work/0108-row-locking-and-update-or-create.md)
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

QuerySet의 native 갱신과 현재 행의 값을 사용하는 scalar expression을 조사한다.
고정 Django/실제 DB의 기준을 먼저 확인하고 공통 AST·ORM·생성 facade와 Helpdesk 우선순위 명령으로 연결한다.
선행 bulk 변경과 Hosted 회귀 수정은 영향 세 mode·브라우저를 통과했으며 별도의 고정 source로 통합 중이다.

## 다음 행동

표현식의 타입/NULL·원래 행의 참조·필터/관계 범위·cache·실패/부모/동시성 의미를 실제 관찰하고 Go 기반을 구현한다.
선행 Hosted의 필수 owner·실제 실행·새 capture의 source 결합/소비·최종 집계도 확인한다.
확장 query, codec 특성/storage provider·custom user model·인증/mail provider와 나머지 카탈로그 기능도
의존 순서에 따라 계속 구현한다. 현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다.
전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. 장기 목표는 [헌장](../CHARTER.md)과
[기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
