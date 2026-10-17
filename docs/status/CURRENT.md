# 현재 상태

- 갱신: 2026-10-18
- 현재: [GDJ-0130 기간별 기한 조회](../../work/0130-calendar-truncation-and-period-groups.md)의 원격 검증과 [GDJ-0131 기간 배율](../../work/0131-duration-scaling-and-label-time-preview.md)의 기반 검증 준비
- GDJ-0130 제품·생성기·소비자 source: `0ce12980ffdf9c66b50d6e2e120e522e55ca6cf8` — 원격 검증 미완료
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
원격 CI에서 확인한 Trunc의 CASE/집계 반환 타입 차이와 후속 native replay, SQLite DATE 열의 scan 비교를 보완했다.
원본을 보존한 실패 재현과 수정 비교를 확인했으며, 수정 source의 실제 양 DB·전체 통합은 다시 검증해야 한다.

GDJ-0131은 최종 읽기/쓰기까지 정확한 분수를 유지하는 Duration 배율·나눗셈·비율을
양 backend와 생성 소비자에 연결했다. 최소 compile과 새 타입 오용 거부를 확인했으며 독립 Django 비교 코드도 작성했다.
[Draft PR #2](https://github.com/progresshans/godj/pull/2)의 첫 CI에서 관측 완료 검사와 새 계산 패키지의 source 결합 누락을 확인해 수정했다.
실제 제품 DB 실행·PG 관측과 Helpdesk 연결은 미완료다. 후속 source의 전체 CI를 확인하며
지원·환경별 검증 범위는 [TEST_EVIDENCE](TEST_EVIDENCE.md)를 따른다.

## 다음 행동

GDJ-0130 제출 source의 실제 양 DB·독립 관측·HTML/API/client·브라우저와 전체 플랫폼을 원격 CI에서 검증한다.
필수 실행·원본 증거를 확인하고 실패가 있으면 원인을 수정한 같은 source의 결과를 다시 대조한다.
GDJ-0131 기반의 실제 검증과 라벨별 소요 시간 조정의 HTML/API/client·브라우저를 이어서 완성한다.
Date/Time 이동·Duration scaling/division의 소비자·timezone/locale·named annotation·나머지 function/subquery/window와
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
