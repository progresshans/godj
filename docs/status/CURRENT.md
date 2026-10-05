# 현재 상태

- 갱신: 2026-10-06
- 현재 작업: [GDJ-0112 그룹 집계와 티켓 업무 요약](../../work/0112-grouped-aggregation-and-ticket-summary.md)
- 통합 대기: [GDJ-0109 Native bulk 생성](../../work/0109-bulk-creation-and-ticket-import.md), [GDJ-0110 Native bulk update](../../work/0110-bulk-update-and-ticket-editing.md), [GDJ-0111 QuerySet 갱신](../../work/0111-query-update-and-writable-expressions.md)
- 최근 Hosted full: [37359348832](https://github.com/progresshans/godj/actions/runs/37359348832), 외부 compile fixture·생성 소비자 package 시간 제한 실패; 보정 source 통합 준비
- 최근 완료: [GDJ-0108 행 잠금과 조회 후 생성 또는 갱신](../../work/0108-row-locking-and-update-or-create.md)
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

QuerySet native 갱신·쓰기 표현식과 Helpdesk 우선순위 명령의 Hosted에서 남은 외부 compile fixture와
생성 소비자의 package 시간 제한을 확인했다. 보정을 영향 검증했고 새 Hosted 통합을 준비한다.
다음 수직 단면으로 그룹 집계와 Helpdesk 업무 요약을 선택했다. 고정 Django의 NULL·조건부 집계·HAVING·
정렬/페이지를 관찰하고 공통 AST·양 backend·typed/dynamic 소비자와 HTML/API로 연결한다.
후속 구현은 진행 중인 Hosted source와 분리하며 그 실행을 새 기능의 검증으로 계산하지 않는다.

## 다음 행동

그룹 집계의 고정 기준 관찰과 API/AST 구현을 진행하고, 새 full의 필수 owner·실제 실행·새 capture의
source 결합/소비·최종 집계를 확인한다. Codec 특성/storage provider·custom user model·인증/mail provider와 나머지 카탈로그 기능도
의존 순서에 따라 계속 구현한다. 현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다.
전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. 장기 목표는 [헌장](../CHARTER.md)과
[기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
