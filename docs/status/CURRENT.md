# 현재 상태

- 갱신: 2026-10-16
- 현재: [GDJ-0129 날짜·시각 추출과 기한별 업무 조회](../../work/0129-calendar-extraction-and-deadline-groups.md) 구현 중
- 검증 source: `012191bfaa35faaad673c8a6d464cbd067b0b5a6`
- 최근 전체 통합: [CI 38851526864](https://github.com/progresshans/godj/actions/runs/38851526864), attempt 2의 필수 실행·원본 증거 대조 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

GDJ-0127·0128의 구현과 전체 검증을 마치고, 기한의 날짜별 집계에 필요한 함수 AST를 확장한다.
고정 Django의 UTC 날짜·시간 분리와 달력/시계 part를 기준으로 typed/dynamic·양 DB·생성 소비자를 함께 준비한다.
새 작업의 구현·실행 결과는 아직 검증되지 않았으며 위 source의 전체 성공을 새 변경에 전이하지 않는다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

제품·생성 소비자·Helpdesk HTML/API/client와 55개 독립 관측 비교 코드를 준비했다.
필수 실행·브라우저 증거 등록과 최소 compile/문서 검사를 마치고 push해 같은 source의 전체 CI를 확인한다.
Date/Time 이동·Duration scaling/division·timezone/locale·named annotation·나머지 function/subquery/window와
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
