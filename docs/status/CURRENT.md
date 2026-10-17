# 현재 상태

- 갱신: 2026-10-18
- 현재: [GDJ-0129 날짜·시각 추출](../../work/0129-calendar-extraction-and-deadline-groups.md) 전체 통합 검증과 [GDJ-0130 기간별 기한 조회](../../work/0130-calendar-truncation-and-period-groups.md) 구현
- GDJ-0129 기능 검증 source: `153cf7652578db28fbad7bc9fae4bf107109cfce`
- 최근 전체 검증 source: `012191bfaa35faaad673c8a6d464cbd067b0b5a6`
- 최근 전체 통합: [CI 38851526864](https://github.com/progresshans/godj/actions/runs/38851526864), attempt 2의 필수 실행·원본 증거 대조 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

GDJ-0129의 생성 ORM 소비자와 독립 Django 양 DB 비교를 normal·race·CGO0에서 확인했다.
Helpdesk 날짜별 조회와 독립 client의 세 mode·HTML/API·브라우저 결과도 원본과 대조했다.
나머지 전체 필수 실행과 플랫폼 검증은 [CI 39055628082](https://github.com/progresshans/godj/actions/runs/39055628082)에서 진행 중이다.
macOS Intel 명령 job의 외부 dependency DNS 실패는 실행 종료 뒤 같은 source에서 재확인한다.
기능별 검증과 이전 source의 전체 성공을 현재 source의 전체 검증으로 합산하지 않는다.
현재 외부 입력이 필요한 blocker는 없다.

GDJ-0130은 같은 함수 AST의 타입별 구간 시작 계산과 Helpdesk 일·주·월·분기·연 조회를 구현한다.
기반·생성 ORM·Helpdesk·독립 client의 최소 compile과 여섯 OpenAPI profile 재생성을 확인했다.
API/HTML·기간 선택·브라우저 검증 코드를 연결했으며 실제 동작은 아직 미검증이다. GDJ-0129의 성공을 전이하지 않는다.

## 다음 행동

GDJ-0129의 남은 필수 실행·전체 플랫폼·capture·원본 자료를 대조하고 문서와 PR의 검증 상태를 갱신한다.
GDJ-0130의 완성된 변경 묶음과 필수 실행 연결을 검토한 뒤 push하고 실제 양 DB·독립 관측·HTML/API/client·브라우저를 검증한다.
Date/Time 이동·Duration scaling/division·timezone/locale·named annotation·나머지 function/subquery/window와
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
