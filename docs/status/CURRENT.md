# 현재 상태

- 갱신: 2026-10-06
- 현재 구현: [GDJ-0110 Native bulk update와 여러 티켓 수정](../../work/0110-bulk-update-and-ticket-editing.md)
- 통합 대기: [GDJ-0109 Native bulk 생성과 여러 티켓 생성](../../work/0109-bulk-creation-and-ticket-import.md)
- 회귀 수정 중인 Hosted full: [37329368105](https://github.com/progresshans/godj/actions/runs/37329368105), source `99491ff36c0eceeed6d1cec6eb74e7c5b2f49afc`
- 최근 완료: [GDJ-0108 행 잠금과 조회 후 생성 또는 갱신](../../work/0108-row-locking-and-update-or-create.md)
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Selected-field bulk update의 AST/backend·generic ORM·생성 facade와 Helpdesk 편집기·Admin·API·독립 client를 연결했다.
고정 Django 기준·native 잠금 대기의 비교와 실제 브라우저 흐름, 완성된 변경 묶음의 양 DB 영향 세 mode를 확인했다.
Hosted에서 Article 액션 알림과 Helpdesk·Linux 생성 소비자의 누적 race 시간 한도 문제를 확인했다.
알림/업무 수정의 영향 세 mode·브라우저를 통과했으며 모든 race 좌표의 분할과 실행 소유권을 확인했다.

## 다음 행동

실행 담당이 빠진 PostgreSQL system-state product 회귀도 실제 양 DB의 세 mode에서 확인하고 CI에 연결했다. GDJ-0110의 새 source로
두 작업의 Hosted 통합 milestone을 시작했다. 검증한 실패 수정 source를 게시하고,
필수 owner·실제 실행·새 capture의 source 결합/소비·최종 집계를 확인한다.
Writable expression·확장 query, codec 특성/storage provider·custom user model·인증/mail provider와 나머지 카탈로그 기능도
의존 순서에 따라 계속 구현한다. 현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다.
전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. 장기 목표는 [헌장](../CHARTER.md)과
[기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
