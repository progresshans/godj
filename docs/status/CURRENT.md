# 현재 상태

- 갱신: 2026-10-18
- 현재: [GDJ-0129 날짜·시각 추출과 기한별 업무 조회](../../work/0129-calendar-extraction-and-deadline-groups.md) 구현 중
- 검증 source: `012191bfaa35faaad673c8a6d464cbd067b0b5a6`
- 최근 전체 통합: [CI 38851526864](https://github.com/progresshans/godj/actions/runs/38851526864), attempt 2의 필수 실행·원본 증거 대조 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

GDJ-0129의 함수 AST·typed/dynamic·양 DB·생성 소비자와 Helpdesk HTML/API/client·브라우저 코드를 제출했다.
Source `6d97eec3`의 [전체 CI 38877148664](https://github.com/progresshans/godj/actions/runs/38877148664)는 실패했다.
독립 관측의 출력 이름 충돌과 Query AST 테스트의 관계 경로 구성을 고쳤으며 수정 source의 전체 검증은 남아 있다.
위 GDJ-0127·0128 source의 전체 성공을 새 변경에 전이하지 않는다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

실패 수정과 증거를 push하고 새 source의 전체 CI·필수 실행·양 DB 독립 관측·브라우저 원본을 대조한다.
이전 macOS 작업의 의존성 다운로드 DNS 오류도 새 실행에서 확인한다.
Date/Time 이동·Duration scaling/division·timezone/locale·named annotation·나머지 function/subquery/window와
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
