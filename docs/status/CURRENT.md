# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0094 JSON 모델과 외부 연동 데이터](../../work/0094-json-models.md)
- 최근 완료: [GDJ-0093 UUID 모델과 외부 연동 참조](../../work/0093-uuid-models.md)
- 최근 전체 검증: [UUID Hosted full](https://github.com/progresshans/godj/actions/runs/35503256679), source `7da91ad5fbd6284622fb372e7d8051120584424e`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

UUID 값·IR·query·양 DB·생성기·Form/Admin·serializer·OpenAPI·Helpdesk/client를 연결하고 통합 검증을 완료했다.
현재 구현 폭과 미지원 기능은 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

JSONField의 값·Schema IR·공통 query·historical migration·ORM과 생성 모델을 SQLite/PostgreSQL 저장 경로에 연결했다.
SQL NULL/JSON null과 숫자 정밀도, native JSONB 확장 한도·값 소유권의 로컬 통합 checkpoint를 완료했다.
Form/Admin/API·OpenAPI·Helpdesk 소비자와 추가 JSON lookup은 후속 구현이다. 새 JSON 코드의 실행 범위는 TEST_EVIDENCE에 따로 기록한다.

## 다음 행동

Form의 JSON 원문·빈 값·값 기준 변경 감지와 serializer의 입력/응답 nullability를 연결한다.
JSON 내부 key와 API envelope의 이름/보안 규칙, 기존 NUL·resource 한도를 구분해 검증한다.
같은 모델에서 Form/Admin·serializer·OpenAPI와 실제 Helpdesk 외부 데이터 소비자까지 이어가며 실패·취소·rollback·재접속을 검증한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
