# 현재 상태

- 갱신: 2026-10-16
- 현재: [GDJ-0128 시각·기간 계산식과 기한 변경 미리보기](../../work/0128-temporal-expressions-and-deadline-preview.md)의 접속 진단 보완 완료, 최신 source 전체 CI 준비
- 직전 원격 검증 source: `9657a87346a1e6d1b903a7e692d8ee36ac7e1cce`
- 최근 전체 통합: source `b66a5aade96a2bb1124c0dbdc0f9edcf122fe70b`의 [CI 38675091247](https://github.com/progresshans/godj/actions/runs/38675091247), GDJ-0126 포함
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

공통 AST·typed capability·양 DB의 DateTime/Duration 조회·UPDATE·집계와 두 앱 생성 소비자를 연결했다.
Typed temporal parameter/output, Helpdesk 기한 미리보기 HTML/API·독립 client·브라우저 검증 코드와 원본 증거 감사를 준비했다.
수정 source의 양 DB·세 mode temporal 생성 소비자·Helpdesk·독립 client와 normal 브라우저의 원본 증거를 대조했다.
고정 Django 관측과 명시적 차이·실행 누락/변조를 거부하는 감사기도 확인했다.
첫 원격 검증의 반환 형식·고정 reference 오류/타입 가정을 바로잡았고, 수정 뒤 국소 실패 재현도 통과했다.
선행 GDJ-0127의 macOS ARM CGO0 누적 timeout에 대한 분할 보완도 포함한다.
[자동 전체 CI 38841006796](https://github.com/progresshans/godj/actions/runs/38841006796)의 나머지 필수 실행과 원본 증거 감사는
진행 중이다. 이전 source의 실패와 부분 성공은 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 구분했다.
실패 재현에서 드러난 PostgreSQL 접속 설정 오류의 진단 손실도 로컬에서 보완했다. 안전한 진단·연결 정리를
국소 재현했으며, 이 후속 변경은 아직 push 전이고 위 원격 실행에 포함되지 않는다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

수정 source의 자동 전체 CI에서 나머지 실제 DB·세 mode·전체 플랫폼 실행을 확인한다.
모든 필수 실행·artifact의 원본 증거를 대조한 뒤 환경별 상태를 갱신한다.
접속 진단 보완과 원격 필수 목록을 함께 push하고 새 source의 자동 전체 CI를 확인한다.
Date/Time 이동·Duration scaling/division·named annotation·function/subquery/window와 나머지
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
