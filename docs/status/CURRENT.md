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
독립 기준의 Python 네 버전 비교를 완료했으며 값·IR·query·DB·생성기 연결을 진행한다. 제품 runtime 검증과 입력/소비자 연결은 남아 있다.

## 다음 행동

UUID의 생성된 외부 소비자로 기존 DB의 nullable 추가·기본값·조회/집계·관계·rollback·reopen을 확인한다.
이후 Form/Admin·serializer·OpenAPI·Helpdesk external_reference와 독립 client까지 이어간다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 현재 ORM 결과를 전체 플랫폼 PASS로 합치지 않는다.
