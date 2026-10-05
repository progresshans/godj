# 현재 상태

- 갱신: 2026-10-06
- 현재 작업: [GDJ-0112 그룹 집계와 티켓 업무 요약](../../work/0112-grouped-aggregation-and-ticket-summary.md)
- 통합 대기: [GDJ-0109 Native bulk 생성](../../work/0109-bulk-creation-and-ticket-import.md), [GDJ-0110 Native bulk update](../../work/0110-bulk-update-and-ticket-editing.md), [GDJ-0111 QuerySet 갱신](../../work/0111-query-update-and-writable-expressions.md)
- 진행 중인 Hosted full: [37365281161](https://github.com/progresshans/godj/actions/runs/37365281161), source `720c9be6211f00a146a39d00957a81ce69ed294f`
- 최근 완료: [GDJ-0108 행 잠금과 조회 후 생성 또는 갱신](../../work/0108-row-locking-and-update-or-create.md)
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

QuerySet native 갱신·쓰기 표현식과 Helpdesk 우선순위 명령의 Hosted에서 남은 외부 compile fixture와
생성 소비자의 package 시간 제한을 확인했다. 보정을 영향 검증했고 runner 배정에 실패한 실행의 재시도를 진행한다.
그룹 집계의 고정 Django 기준·공통 AST·양 backend·typed/dynamic 생성 소비자와 영향 세 mode를 확인했다.
현재 Category의 우선순위별 업무 요약을 HTML/API와 독립 client로 연결하고 영향 세 mode·브라우저·정적 검사·DB 정리를 완료했다.
후속 구현은 진행 중인 Hosted source와 분리하며 그 실행을 새 기능의 검증으로 계산하지 않는다.

## 다음 행동

업무 요약을 기록·발행하고 typed API 응답의 출력 검증·OpenAPI 중복 선언을 줄이는 다음 기반으로 확장한다. 선행 full의 runner 실패를
분리하며 필수 owner·실제 실행·새 capture의 source 결합/소비·최종 집계를 확인한다. Codec 특성/storage provider·custom user model·인증/mail provider와 나머지 카탈로그 기능도
의존 순서에 따라 계속 구현한다. 현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다.
전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. 장기 목표는 [헌장](../CHARTER.md)과
[기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
