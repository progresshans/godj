# 현재 상태

- 갱신: 2026-10-16
- 현재: [GDJ-0128 시각·기간 계산식과 기한 변경 미리보기](../../work/0128-temporal-expressions-and-deadline-preview.md) 제품·소비자·검증 코드를 연결하고 원격 실행 준비 중
- 기준 source: `c6e96c41fb981111a11d2a93914ee305b0c513a0`, 선행 수정 `9edc6b56`까지 반영한 구현 묶음
- 최근 전체 통합: source `b66a5aade96a2bb1124c0dbdc0f9edcf122fe70b`의 [CI 38675091247](https://github.com/progresshans/godj/actions/runs/38675091247), GDJ-0126 포함
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

공통 AST·typed capability·양 DB의 DateTime/Duration 조회·UPDATE·집계와 두 앱 생성 소비자를 연결했다.
Typed temporal parameter/output, Helpdesk 기한 미리보기 HTML/API·독립 client·브라우저 검증 코드와 원본 증거 감사를 준비했다.
현재는 최소 compile·생성·compile-negative와 검사기 단위 확인 단계이며 실제 새 DB/HTTP·관측·client/browser 실행 성공은 아니다.
선행 GDJ-0127 source `d911b030`의 전체 CI는 macOS ARM CGO0 누적 timeout으로 실패했다. 분할을 보완한 source `9edc6b56`의
[자동 전체 CI 38835004030](https://github.com/progresshans/godj/actions/runs/38835004030)가 진행 중이며 이 temporal 코드의 증거와 구분한다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

통합한 묶음의 자동 전체 CI에서 실제 DB/관측·세 mode·브라우저와 실패/저장 보존을 검증한다.
모든 필수 실행·artifact의 원본 증거를 대조한 뒤 환경별 상태를 갱신한다.
Date/Time 이동·Duration scaling/division·named annotation·function/subquery/window와 나머지
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
