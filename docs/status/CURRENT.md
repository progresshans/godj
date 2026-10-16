# 현재 상태

- 갱신: 2026-10-16
- 현재: [GDJ-0127 컬렉션 계산식 집계](../../work/0127-collection-computed-aggregates.md)와 [GDJ-0128 시각·기간 계산식과 기한 미리보기](../../work/0128-temporal-expressions-and-deadline-preview.md)의 구현·전체 통합 검증 완료, 다음 요구 선정
- 검증 source: `012191bfaa35faaad673c8a6d464cbd067b0b5a6`
- 최근 전체 통합: [CI 38851526864](https://github.com/progresshans/godj/actions/runs/38851526864), attempt 2의 필수 실행·원본 증거 대조 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

컬렉션 계산식 집계와 DateTime/Duration 산술을 공통 AST·양 DB·typed/dynamic·생성 소비자에 연결했다.
Helpdesk 라벨별 조정 비용과 기한 미리보기의 HTML/API·독립 client·브라우저, 인가·저장 보존을 검증했다.
PostgreSQL 접속 설정의 안전한 진단과 연결 정리, 플랫폼 분할 보완도 같은 source의 전체 검증에 포함했다.
최초 실행의 성공과 runner 중단 뒤 재실행을 구분하고, 필수 작업·원 로그·artifact·source 결합을 모두 대조했다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

현행 구현과 Article/Helpdesk의 공개 API에서 다음에 필요한 모델·query 흐름을 선정한다.
필요한 공통 의미·backend 동작·생성 API·실제 소비자와 실패 경로를 한 묶음으로 확장한다.
Date/Time 이동·Duration scaling/division·named annotation·function/subquery/window와 나머지
[기능 카탈로그](../CAPABILITY_CATALOG.md)는 [헌장](../CHARTER.md)의 전체 목표로 유지한다.
