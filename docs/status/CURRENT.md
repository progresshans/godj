# 현재 상태

- 갱신: 2026-09-21
- 활성 구현: [GDJ-0094 JSON 모델과 외부 연동 데이터](../../work/0094-json-models.md)
- 최근 완료: [GDJ-0093 UUID 모델과 외부 연동 참조](../../work/0093-uuid-models.md)
- 최근 전체 검증: [JSON 수직 연결 Hosted full](https://github.com/progresshans/godj/actions/runs/35511311272), source `d1a0570b87791378bc24a2cd90ce4afa8326a653`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

JSONField를 Schema IR·query·migration·생성 모델에서 Form/Admin·serializer·OpenAPI와 실제 Helpdesk/client까지 연결했다.
정확한 JSON token·SQL NULL/JSON null 구분과 SQLite/native PostgreSQL 저장·rollback·재접속 경계를 유지한다.
Root/path·forward 조회에 exact/IN/isnull·key presence·backend별 containment와 literal 대소 비교를 연결했다.
Root/forward JSON 경로 및 일반 forward scalar·whole JSON의 typed nullable DTO 선택과 ASC/DESC 정렬도 구현했다.
정렬 전용 optional JOIN·정확한 경로 숫자·DISTINCT 결과 컬럼·cold Count·페이지네이션을 같은 조회 표현으로 처리한다.
지원하는 전체 표현과 미지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

JSON/forward 정렬의 고정 source `d504cf9`는
[Hosted ORM 통합](https://github.com/progresshans/godj/actions/runs/35533945489)을 완료했다.
후속으로 whole/path·forward JSON의 literal 문자열 검색과 Helpdesk Admin/API의 search/source를 연결했다.
기본 Django와 GoDj canonical 저장 profile·숫자/NUL의 명시적 차이를 구분해 실제 양 DB와 독립 client의 로컬 checkpoint를 마쳤다.
실행 source·범위·결과는 [TEST_EVIDENCE](TEST_EVIDENCE.md)가 소유한다.

## 다음 행동

JSON 문자열 검색·실제 소비자의 고정 source에 대한 Hosted `web` 통합을 검증한다.
이어 외부 참조 중복 방지에 필요한 모델 고유성 제약과 기존 DB migration 의미를 다음 기반으로 점검한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
