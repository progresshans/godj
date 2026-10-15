# 현재 상태

- 갱신: 2026-10-15
- 현재: [GDJ-0127 컬렉션 계산식 집계와 라벨별 조정 비용](../../work/0127-collection-computed-aggregates.md) 첫 원격 소비자 실패 수정, 후속 전체 검증 대기
- 최근 전체 통합: source `b66a5aade96a2bb1124c0dbdc0f9edcf122fe70b`의 [CI 38675091247](https://github.com/progresshans/godj/actions/runs/38675091247), GDJ-0126 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

컬렉션의 숫자·Decimal 계산과 CASE/FILTER를 완료된 집계에 연결하고, Helpdesk 라벨별 조정 비용의
HTML/API·독립 client·브라우저 검증 코드를 통합했다. Source `c6e96c41`의
[첫 전체 CI 38691196268](https://github.com/progresshans/godj/actions/runs/38691196268)에서 조건부 COUNT 소비자 실패를 확인했다.
실패를 재현하고 명시적인 티켓 ID 집계로 검사를 수정했으며 수정 source의 전체 검증은 남아 있다.
이전 [GDJ-0126](../../work/0126-decimal-expressions-and-cost-preview.md) source는 전체 CI와 원본 실행 증거 대조를 완료했다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

통합된 묶음의 자동 전체 CI에서 같은 source의 양 DB·세 mode·client·브라우저와
전체 플랫폼 필수 실행·원본 증거를 확인한다.
Named annotation·Duration/temporal 산술·function/subquery/window,
codec/storage·custom user/auth/mail과 나머지 요구도 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 범위로 유지한다.
전체 목표는 [헌장](../CHARTER.md)을 따른다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
