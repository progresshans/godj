# 현재 상태

- 갱신: 2026-10-15
- 현재: [GDJ-0125 컬렉션 집계와 라벨 사용 현황](../../work/0125-collection-aggregates-and-label-usage.md) 통합 완료, 다음 기반 검토
- 최근 전체 통합: source `fceff454724fce732da5632a21921b5492361555`의 [CI 38380836199](https://github.com/progresshans/godj/actions/runs/38380836199), GDJ-0125 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

컬렉션 필드 집계와 조건을 공통 AST·양 DB·typed/dynamic facade에 연결하고 Helpdesk 라벨 사용 현황에서 소비했다.
같은 source의 양 DB·세 mode·브라우저·전체 플랫폼 실행과 원본 증거 대조를 완료했다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

모델 중심 흐름에서 다음 계산·조회 요구와 기존 AST의 경계를 확인하고 필요한 기반과 소비자 계약을 선택한다.
Named annotation·Decimal/Duration 산술·function/subquery/window,
codec/storage·custom user/auth/mail과 나머지 요구도 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 범위로 유지한다.
전체 목표는 [헌장](../CHARTER.md)을 따른다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
