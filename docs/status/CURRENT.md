# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0094 JSON 모델과 외부 연동 데이터](../../work/0094-json-models.md)
- 최근 완료: [GDJ-0093 UUID 모델과 외부 연동 참조](../../work/0093-uuid-models.md)
- 최근 전체 검증: [UUID Hosted full](https://github.com/progresshans/godj/actions/runs/35503256679), source `7da91ad5fbd6284622fb372e7d8051120584424e`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

UUID 값·IR·query·양 DB·생성기·Form/Admin·serializer·OpenAPI·Helpdesk/client를 연결하고 통합 검증을 완료했다.
현재 구현 폭과 미지원 기능은 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

JSONField를 Schema IR·query·migration·생성 모델에서 Form/Admin·serializer·OpenAPI와 실제 Helpdesk/client까지 연결했다.
양 DB의 SQL NULL/JSON null·숫자 정밀도·Form no-op·권한/CSRF·rollback·재접속과 native 저장 후 응답 실패의 로컬 통합 checkpoint를 완료했다.
새 JSON source의 전체 platform 검증과 key/path·contains 등 추가 lookup은 남아 있다. 실행 범위는 TEST_EVIDENCE가 소유한다.

## 다음 행동

JSON 모델부터 실제 외부 client까지 연결한 통합 milestone의 Hosted full을 확인한다. 로컬 전체 platform 실행을 중복하지 않는다.
다음 JSON key/path·contains는 고정 Django의 missing/JSON null/SQL NULL과 backend별 지원 의미를 먼저 확인해 공통 AST에 연결한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
