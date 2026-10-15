# 현재 상태

- 갱신: 2026-10-15
- 현재: [GDJ-0126 Decimal 계산식과 예상 비용 조정 미리보기](../../work/0126-decimal-expressions-and-cost-preview.md) 원격 실패 수정 후 전체 검증 중
- 최근 전체 통합: source `fceff454724fce732da5632a21921b5492361555`의 [CI 38380836199](https://github.com/progresshans/godj/actions/runs/38380836199), GDJ-0125 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

컬렉션 필드 집계와 조건을 공통 AST·양 DB·typed/dynamic facade에 연결하고 Helpdesk 라벨 사용 현황에서 소비했다.
같은 source의 양 DB·세 mode·브라우저·전체 플랫폼 실행과 원본 증거 대조를 완료했다.
정확한 Decimal 계산을 조회·집계·갱신에 연결하고 예상 비용의 조정 결과를 조회하는 묶음을 push했다.
첫 원격 CI에서 드러난 인증 구성 테스트·SQLite 오류 분류·참조 기대값을 수정해 push했다.
새 source의 Decimal 양 DB·세 mode와 client·브라우저 검증을 대조했으며 전체 플랫폼 통합은 아직 미완료다.
현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

진행 중인 CI의 남은 환경과 최종 집계를 확인하고 전체 원본 증거를 대조한다.
Named annotation·Duration/temporal 산술·function/subquery/window,
codec/storage·custom user/auth/mail과 나머지 요구도 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 범위로 유지한다.
전체 목표는 [헌장](../CHARTER.md)을 따른다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
