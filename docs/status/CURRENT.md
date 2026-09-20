# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0093 UUID 모델과 외부 연동 참조](../../work/0093-uuid-models.md)
- 최근 완료: [GDJ-0092 Decimal 정밀도 변경과 기존 값 보존](../../work/0092-decimal-precision-migrations.md)
- 최근 관련 검증: [직접 PostgreSQL 회귀 보강 reference scope](https://github.com/progresshans/godj/actions/runs/35499184070), source `153bf08531d7ff59a795386a8f2e643d250efe4c`
- 최근 전체 검증: [Decimal precision Hosted full](https://github.com/progresshans/godj/actions/runs/35496796910), source `06f601ed4d939d4f60b2313b4c70b33eaf4a5939`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Decimal 모델·Form/Admin·JSON/OpenAPI·Helpdesk 비용의 연결과 기존 값의 precision-only migration을 완료했다.
현재 구현 폭과 미지원 기능은 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

UUID는 고정 Django/DRF의 값·Form/serializer·실제 SQLite migration/query 관찰과 별도 native PostgreSQL probe를 준비했다.
독립 기준의 Python 네 버전 비교와 값·IR·query·양 DB·생성기 연결의 로컬 DB/race checkpoint를 완료했다.
Form/Admin·serializer·OpenAPI와 실제 Helpdesk/client까지 연결했다. 입력 Unicode profile 차이를 보완하고 로컬 통합 검증을 완료했다.
첫 Hosted full에서 외부 복구 fixture의 UUID source 복사 누락을 발견해 보완했다. 수정 source의 Hosted full 통합 검증은 남아 있다.

## 다음 행동

UUID의 현재 source를 게시하고 Hosted full checkpoint에서 exact/compatibility reference와 지원 플랫폼의 필수 실행을 확인한다.
완료 뒤 카탈로그의 다음 모델·관계 요구를 실제 소비자와 연결한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 현재 ORM 결과를 전체 플랫폼 PASS로 합치지 않는다.
