# 현재 상태

- 갱신: 2026-10-13
- 현재: [GDJ-0124 관계 값 표현식과 서비스 보고서 상태 조회](../../work/0124-related-expressions-and-report-status.md) 통합 완료·후속 관계 집계 계약 정리
- 최근 전체 통합: source `7afe56f73d615788d7aa7baa5e959a4ec27f6993`의 [CI 38346173948](https://github.com/progresshans/godj/actions/runs/38346173948), GDJ-0124 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

GDJ-0124의 단일 값 관계 scalar·계산값 간 비교와 Helpdesk 보고서 상태 조회를 위 source에서 통합 완료했다.
공통 AST·양 DB·typed/dynamic facade·독립 client·브라우저와 필수 실행·원본 증거를 대조했다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

라벨별 티켓 사용 현황에 필요한 다중 관계 집계의 계약과 독립 Django 기준을 정리한다.
빈 라벨의 0건·JOIN 중복·조건부 집계와 Category 범위·조회 권한을 함께 다룬다. 이 후속 기능은 아직 미구현이다.
Named annotation·Decimal/Duration 산술·function/subquery/window,
codec/storage·custom user/auth/mail과 나머지 요구도 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 범위로 유지한다.
전체 목표는 [헌장](../CHARTER.md)을 따른다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
