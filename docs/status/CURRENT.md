# 현재 상태

- 갱신: 2026-09-21
- 활성 구현: [GDJ-0095 모델 고유성과 외부 참조 중복 방지](../../work/0095-model-uniqueness.md)
- 최근 완료: [GDJ-0094 JSON 모델과 외부 연동 데이터](../../work/0094-json-models.md)
- 최근 전체 검증: [JSON 수직 연결 Hosted full](https://github.com/progresshans/godj/actions/runs/35511311272), source `d1a0570b87791378bc24a2cd90ce4afa8326a653`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

JSONField를 Schema IR·migration·typed/dynamic query·생성 모델에서 Form/Admin·serializer·OpenAPI와 실제 Helpdesk/client까지 연결했다.
Root/path·forward의 비교·포함·key presence·선택·정렬과 literal 문자열 검색을 지원한다.
Helpdesk Admin/API의 search/source를 포함한 source `ef9b05c`의
[Hosted web 통합](https://github.com/progresshans/godj/actions/runs/35537035733)을 완료했다.
지원 표현과 미지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

모델의 column uniqueness를 진행 중이다. 양 DB의 독립 기준에 이어 `schema.Unique()`를 IR·생성 metadata·historical
definition/digest·자동 변경 계획에 연결했다. 지원하지 않는 DB 경로는 실행 전에 명시적으로 거부한다.
선언과 이력의 로컬 검증을 완료했으며 실제 UNIQUE 제약 생성·저장 충돌·Form/Admin/API 검증은 아직 구현 중이다.

## 다음 행동

SQLite와 PostgreSQL의 UNIQUE 제약 생성·변경·제거와 정확한 물리 catalog 검증, operation별 SQL 출력을 구현한다.
그 뒤 실제 insert/update 충돌·경쟁·실패/rollback을 Form/Admin/API와 Helpdesk 외부 참조의 중복 방지까지 연결한다.
구현과 검증 범위는 GDJ-0095와 TEST_EVIDENCE에 기록한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
