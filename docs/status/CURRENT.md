# 현재 상태

- 갱신: 2026-10-12
- 현재: [GDJ-0123 집계 결과 단계와 티켓 현황 비율](../../work/0123-derived-results-and-ticket-ratios.md)의 원격 통합과 브라우저 검사 수정
- 최근 전체 통합: source `0ea21fa44144c3ab4df7d90b1d5fd0eadb899779`의 [CI 38110947560](https://github.com/progresshans/godj/actions/runs/38110947560), GDJ-0122 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

GDJ-0122의 같은 행 scalar/CASE와 Helpdesk 우선순위 미리보기는 해당 source의 전체 통합을 완료했다.
후속 작업은 집계된 값을 다시 계산·조회하는 결과 단계다. 모델 FieldRef와 결과 열을 구분하고 기존 필터·집계·slice와
읽기 snapshot·타입/NULL·실패 의미를 보존하는 구현과 검증 사례를 연결했다. 새 동작의 실제 양 DB·원격 검증은 남아 있다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

원격에서 발견한 브라우저 NULL 열 판정을 수정한 묶음을 push하고 기존 Draft PR의 새 전체 CI를 확인한다.
새 생성 소비자·빈 결과/NULL·양 DB·브라우저·독립 client와 같은 source의 전체 실행 근거를 검사한다.
Named annotation·관계 computed operand·Decimal/Duration 산술·function/subquery/window,
codec/storage·custom user/auth/mail과 나머지 요구도 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 범위로 유지한다.
전체 목표는 [헌장](../CHARTER.md)을 따른다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
