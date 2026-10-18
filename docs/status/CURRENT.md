# 현재 상태

- 갱신: 2026-10-18
- 현재: [GDJ-0131 기간 배율·라벨별 조정 시간](../../work/0131-duration-scaling-and-label-time-preview.md)의 전체 재검증과 [GDJ-0132 달력 산술·서비스 일정](../../work/0132-calendar-arithmetic-and-service-schedule-preview.md) 구현
- 원격 검증 제품·생성기·소비자 source: `73ee17042b42b48701026e15c329405fe210afe1` — GDJ-0132 작업 사본은 포함하지 않음
- 최근 전체 검증 source: `0ce12980ffdf9c66b50d6e2e120e522e55ca6cf8`
- 최근 전체 통합: [CI 39074001761](https://github.com/progresshans/godj/actions/runs/39074001761), attempt 1의 필수 실행·원본 증거 대조 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

[GDJ-0130 기간별 기한 조회](../../work/0130-calendar-truncation-and-period-groups.md)는 타입별 구간 계산과
Helpdesk 일·주·월·분기·연 조회를 구현하고 양 DB·세 mode·HTML/API/client·브라우저와 전체 플랫폼의 검증을 완료했다.
현재 외부 입력이 필요한 blocker는 없다.

GDJ-0131은 최종 읽기/쓰기까지 정확한 분수를 유지하는 Duration 배율·나눗셈·비율과 Helpdesk의 조정 시간 합계를
양 backend·생성 소비자·공통 입력 검증·HTML/API·OpenAPI/독립 client·브라우저에 연결했다.
선행 source의 일부 소비자를 확인했고, CI에서 드러난 Float 오류 분류 누락과 scanner/독립 비교 테스트의 잘못된 가정을
수정 전후 집중 재현으로 대조했다. 후속 CI에서 드러난 macOS Intel race의 테스트 준비 파일 공개 경합도
집중 재현·보완했다. 현재 source는 [Draft PR #2](https://github.com/progresshans/godj/pull/2)의
[CI 39085925758](https://github.com/progresshans/godj/actions/runs/39085925758)에서 전체 검증을 다시 진행한다. 이전 source의 성공을 전이하지 않는다.

GDJ-0132의 Date/Time 산술·두 생성 앱·독립 비교와 Helpdesk 서비스 일정 HTML/API·독립 client·브라우저 검사를 연결했다.
최소 compile과 OpenAPI 생성물 갱신은 확인했다. 아직 원격 source에 포함하지 않았으며 양 DB의 실제 산술·독립 관측·
HTTP/client·브라우저·전체 플랫폼 검증은 남아 있다.

## 다음 행동

GDJ-0131의 양 DB 기반·생성 소비자와 라벨별 시간 조정의 HTML/API/client·브라우저를 같은 source의 전체 CI에서 검증한다.
필수 실행·원본 증거를 대조하고 실패가 있으면 원인을 수정한 source에서 다시 확인한다.
GDJ-0132의 완성한 변경 묶음을 제출하고 같은 source의 양 DB·생성 소비자·독립 Django 비교와 Helpdesk HTML/API/client·브라우저를 검증한다.
Timezone/locale·named annotation·나머지 function/subquery/window와
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
