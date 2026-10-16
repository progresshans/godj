# 현재 상태

- 갱신: 2026-10-16
- 현재: [GDJ-0128 시각·기간 계산식과 기한 변경 미리보기](../../work/0128-temporal-expressions-and-deadline-preview.md)의 원격 fixture 오류 수정·국소 재현 완료
- 최근 제출 source: `458fb20449267f09dd87c20ef4318b11893a1b90`, 새 temporal 검증 코드의 실패 확인
- 최근 전체 통합: source `b66a5aade96a2bb1124c0dbdc0f9edcf122fe70b`의 [CI 38675091247](https://github.com/progresshans/godj/actions/runs/38675091247), GDJ-0126 포함
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

공통 AST·typed capability·양 DB의 DateTime/Duration 조회·UPDATE·집계와 두 앱 생성 소비자를 연결했다.
Typed temporal parameter/output, Helpdesk 기한 미리보기 HTML/API·독립 client·브라우저 검증 코드와 원본 증거 감사를 준비했다.
같은 source의 세 mode 독립 client 실행과 normal 브라우저의 원본 로그·artifact·화면을 대조했다.
독립 양 DB 관측을 대조했고, 기간 반환 형식·고정 reference의 오류/타입을 잘못 가정한 검증 코드를 수정했다.
국소 실패 재현은 통과했으며 수정 source의 고정 DB·세 mode·전체 플랫폼 검증은 남아 있다.
선행 GDJ-0127의 macOS ARM CGO0 누적 timeout에 대한 분할 보완도 포함해 source `458fb204`로 통합했다.
[자동 전체 CI 38837344321](https://github.com/progresshans/godj/actions/runs/38837344321)의 실패 원본과 수정 근거는
[TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록했다. 이 source를 전체 성공으로 취급하지 않는다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

수정 묶음을 push하고 자동 전체 CI에서 실제 DB/관측·세 mode·브라우저와 실패/저장 보존을 다시 검증한다.
모든 필수 실행·artifact의 원본 증거를 대조한 뒤 환경별 상태를 갱신한다.
Date/Time 이동·Duration scaling/division·named annotation·function/subquery/window와 나머지
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
