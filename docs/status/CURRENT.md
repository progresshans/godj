# 현재 상태

- 갱신: 2026-10-15
- 현재: [GDJ-0127 컬렉션 계산식 집계와 라벨별 조정 비용](../../work/0127-collection-computed-aggregates.md) 구현·소비자 준비 완료, 원격 실행 전
- 최근 전체 통합: source `fceff454724fce732da5632a21921b5492361555`의 [CI 38380836199](https://github.com/progresshans/godj/actions/runs/38380836199), GDJ-0125 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

컬렉션의 숫자·Decimal 계산과 CASE/FILTER를 완료된 집계에 연결하고, Helpdesk 라벨별 조정 비용의
HTML/API·독립 client·브라우저 검증 코드를 통합했다. 로컬 생성·compile과 독립 정적 검토를 마쳤으며 실행 검증은 남아 있다.
이전 [GDJ-0126](../../work/0126-decimal-expressions-and-cost-preview.md) source의 전체 CI도 아직 진행 중이다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

이전 source 전체 CI의 최종 실행과 원본 증거를 대조한 뒤 새 묶음을 push한다.
새 source의 양 DB·세 mode·client·브라우저와 전체 플랫폼 필수 실행을 확인한다.
Named annotation·Duration/temporal 산술·function/subquery/window,
codec/storage·custom user/auth/mail과 나머지 요구도 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 범위로 유지한다.
전체 목표는 [헌장](../CHARTER.md)을 따른다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
