# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0092 Decimal 정밀도 변경과 기존 값 보존](../../work/0092-decimal-precision-migrations.md)
- 최근 완료: [GDJ-0091 Decimal 모델·Form/Admin·Helpdesk API·독립 client 연결](../../work/0091-decimal-cost-models.md)
- 최근 관련 검증: [Decimal Hosted ORM 완료](https://github.com/progresshans/godj/actions/runs/35490634932), source `d106e73d5338cff107623351c48ac4f5778fff8c`
- 최근 전체 검증: [Duration Hosted full](https://github.com/progresshans/godj/actions/runs/35479740366), source `79637ef3f5943c9490027723527fb5074b01411f`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Decimal의 exact 값·precision IR·query·생성·양 DB·historical migration을 Form/Admin·JSON/OpenAPI·Helpdesk 예상 비용과 독립 client까지 연결했다.
원문 자릿수, fixed-scale 문자열, null/생략, 권한·rollback과 generated 소비자의 로컬 및 Hosted ORM checkpoint를 완료했다.
현재 구현 폭과 미지원 기능은 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

GDJ-0092는 기존 비용이 있는 상태에서 모델의 precision·scale을 변경하는 흐름이다. 고정 Django의 12개 migration profile과
별도 native PostgreSQL 사전 실험을 준비했다. 기존 값의 반올림·조회 실패와 역방향 복구의 차이를 구분했다.
Precision-only AlterField와 Helpdesk 한도 확장·독립 client를 연결했고 관련 21 package의 일반·race 및 선택 CGO=0 로컬 통합 checkpoint를 완료했다.

## 다음 행동

검증한 변경을 기존 Draft PR에 통합하고 같은 제품 source로 Hosted full milestone을 실행한다.
지원 플랫폼과 Linux 전용 process 회귀를 확인한 뒤 환경별 결과와 남은 완성 범위를 갱신한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 현재 ORM 결과를 전체 플랫폼 PASS로 합치지 않는다.
