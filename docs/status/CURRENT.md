# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0092 Decimal 정밀도 변경과 기존 값 보존](../../work/0092-decimal-precision-migrations.md)
- 최근 완료: [GDJ-0091 Decimal 모델·Form/Admin·Helpdesk API·독립 client 연결](../../work/0091-decimal-cost-models.md)
- 최근 관련 검증: [Decimal precision Hosted full](https://github.com/progresshans/godj/actions/runs/35496796910), source `06f601ed4d939d4f60b2313b4c70b33eaf4a5939`
- 최근 전체 검증: 같은 Decimal precision Hosted full; 직접 PostgreSQL sentinel 보강은 별도 진행 중
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Decimal의 exact 값·precision IR·query·생성·양 DB·historical migration을 Form/Admin·JSON/OpenAPI·Helpdesk 예상 비용과 독립 client까지 연결했다.
원문 자릿수, fixed-scale 문자열, null/생략, 권한·rollback과 generated 소비자의 로컬 및 Hosted ORM checkpoint를 완료했다.
현재 구현 폭과 미지원 기능은 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

GDJ-0092는 기존 비용이 있는 상태에서 모델의 precision·scale을 변경하는 흐름이다. 고정 Django의 12개 migration profile과
별도 native PostgreSQL 사전 실험을 준비했다. 기존 값의 반올림·조회 실패와 역방향 복구의 차이를 구분했다.
Precision-only AlterField와 Helpdesk 한도 확장·독립 client를 연결했고 관련 21 package의 일반·race 및 선택 CGO=0 로컬 통합 checkpoint를 완료했다.

## 다음 행동

Hosted full 62개 job의 성공과 source를 확인했다. 추가 감사에서 빠진 직접 PostgreSQL root 세 개를 필수 목록에 넣고
같은 제품 바이트의 후속 reference scope에서 실행한다. 그 결과까지 기록한 뒤 다음 UUID 모델 작업으로 넘어간다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 현재 ORM 결과를 전체 플랫폼 PASS로 합치지 않는다.
