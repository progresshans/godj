# 현재 상태

- 갱신: 2026-10-18
- 현재: [GDJ-0130 기간별 기한 조회](../../work/0130-calendar-truncation-and-period-groups.md)의 실제 동작·전체 통합 검증
- GDJ-0130 제품·생성기·소비자 source: `6a05456f09ce24b8639855e7487f80966919ef0f` — 원격 검증 미완료
- 최근 전체 검증 source: `153cf7652578db28fbad7bc9fae4bf107109cfce`
- 최근 전체 통합: [CI 39055628082](https://github.com/progresshans/godj/actions/runs/39055628082), attempt 2의 필수 실행·원본 증거 대조 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

[GDJ-0129 날짜·시각 추출](../../work/0129-calendar-extraction-and-deadline-groups.md)의 양 DB·세 mode·
HTML/API/client·브라우저와 전체 플랫폼의 필수 실행·원본 대조를 완료했다.
현재 외부 입력이 필요한 blocker는 없다.

GDJ-0130은 같은 함수 AST의 타입별 구간 시작 계산과 Helpdesk 일·주·월·분기·연 조회를 구현한다.
기반·생성 ORM·Helpdesk·독립 client의 최소 compile과 여섯 OpenAPI profile 재생성을 확인했다.
API/HTML·기간 선택·브라우저 검증 코드를 연결했으며 실제 동작은 아직 미검증이다. GDJ-0129의 성공을 전이하지 않는다.
원격 CI에서 확인한 Trunc의 CASE/집계 반환 타입 차이와 SQLite DATE 열의 scan 비교를 보완했다.
원본을 보존한 실패 재현과 수정 비교를 확인했으며, 수정 source의 실제 양 DB·전체 통합은 다시 검증해야 한다.

## 다음 행동

GDJ-0130 제출 source의 실제 양 DB·독립 관측·HTML/API/client·브라우저와 전체 플랫폼을 원격 CI에서 검증한다.
필수 실행·원본 증거를 확인하고 실패가 있으면 원인을 수정한 같은 source의 결과를 다시 대조한다.
Date/Time 이동·Duration scaling/division·timezone/locale·named annotation·나머지 function/subquery/window와
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
