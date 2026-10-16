# 현재 상태

- 갱신: 2026-10-16
- 현재: [GDJ-0127 컬렉션 계산식 집계와 라벨별 조정 비용](../../work/0127-collection-computed-aggregates.md)의 전체 CI 실패를 확인하고 macOS ARM relation 소비자 분할 보완 중
- 최근 전체 통합: source `b66a5aade96a2bb1124c0dbdc0f9edcf122fe70b`의 [CI 38675091247](https://github.com/progresshans/godj/actions/runs/38675091247), GDJ-0126 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Source `d911b030`의 [전체 CI 38700011581](https://github.com/progresshans/godj/actions/runs/38700011581)는
macOS ARM CGO0 생성 소비자의 누적 package timeout으로 실패했다. 같은 플랫폼의 normal/CGO0도
기존의 runtime owner·세 consumer shard로 나누며 필수 root/child·시간 한도·no-skip 검사를 유지한다.
분할 보완 후 같은 source의 원격 전체 실행과 원본 증거 대조가 필요하다.
별도 `feature/temporal-expressions` 작업 공간의 GDJ-0128은 DB·HTTP·독립 client 검증 코드를 작성 중이며
이 브랜치의 실행 근거와 구분한다. 현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

완성한 분할 보완을 push하고 자동 전체 CI에서 모든 플랫폼·DB·세 mode의 필수 실행과 원본 증거를 확인한다.
독립 client·브라우저·보존/실패 경로까지 포함해 GDJ-0128 묶음을 계속 완성한다.
Named annotation·Duration/temporal 산술·function/subquery/window,
codec/storage·custom user/auth/mail과 나머지 요구도 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 범위로 유지한다.
전체 목표는 [헌장](../CHARTER.md)을 따른다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
