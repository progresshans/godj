# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0094 JSON 모델과 외부 연동 데이터](../../work/0094-json-models.md)
- 최근 완료: [GDJ-0093 UUID 모델과 외부 연동 참조](../../work/0093-uuid-models.md)
- 최근 전체 검증: [UUID Hosted full](https://github.com/progresshans/godj/actions/runs/35503256679), source `7da91ad5fbd6284622fb372e7d8051120584424e`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

UUID 값·IR·query·양 DB·생성기·Form/Admin·serializer·OpenAPI·Helpdesk/client를 연결하고 통합 검증을 완료했다.
현재 구현 폭과 미지원 기능은 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

JSONField의 고정 Django/DRF·SQLite와 별도 native PostgreSQL·client 표현 관찰을 준비했다.
SQL NULL/JSON null, 숫자·객체 순서·중복 key·Unicode 및 입력/응답 nullability 차이를 확인했다.
현재 변경 가능한 자료구조를 공유하지 않는 JSON 값과 bounded parser 연결을 구현 중이다.
이 새 JSON 구현은 아직 runtime 검증 전이며 UUID source의 PASS에 포함하지 않는다.

## 다음 행동

JSON 값·숫자 정밀도·소유권을 Schema IR과 공통 query에 연결한다. DB별 JSON capability와 실제 저장·조회·migration을 함께 구현한다.
같은 모델에서 Form/Admin·serializer·OpenAPI와 실제 Helpdesk 외부 데이터 소비자까지 이어가며 실패·취소·rollback·재접속을 검증한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
