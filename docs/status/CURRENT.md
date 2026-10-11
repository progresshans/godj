# 현재 상태

- 갱신: 2026-10-12
- 현재: [GDJ-0122 계산식 조회와 우선순위 미리보기](../../work/0122-computed-results-and-priority-preview.md) 완료, 후속 Query 기반의 의존성 확인
- 최근 전체 통합: source `0ea21fa44144c3ab4df7d90b1d5fd0eadb899779`의 [CI 38110947560](https://github.com/progresshans/godj/actions/runs/38110947560), GDJ-0122 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

같은 scalar AST를 조회 결과·조건·정렬·그룹 키·집계 operand에 연결하고, CASE를 typed/dynamic 경로에 추가했다.
Helpdesk의 우선순위 요약은 원본 priority·권한·읽기 snapshot을 유지하며 변경 예정값을 제공한다.
제품·생성 소비자·실패 경로와 양 DB·세 모드·전체 플랫폼, 같은 실행의 capture 소비·최종 집계를 대조했다.
GDJ-0122의 구현과 해당 source의 전체 통합을 완료했다. 환경별 실제 실행은 TEST_EVIDENCE를 따른다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

현재 Query AST와 결과 소유권을 확인해 named annotation·집계 뒤 계산/query stage·관계 computed operand의
의존성을 정하고 다음 실제 소비자 흐름을 연결한다. Decimal/Duration 산술·function/subquery/window,
codec/storage·custom user/auth/mail과 나머지 요구도 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 범위로 유지한다.
전체 목표는 [헌장](../CHARTER.md)을 따른다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
