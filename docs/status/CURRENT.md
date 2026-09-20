# 현재 상태

- 갱신: 2026-09-21
- 활성 구현: [GDJ-0094 JSON 모델과 외부 연동 데이터](../../work/0094-json-models.md)
- 최근 완료: [GDJ-0093 UUID 모델과 외부 연동 참조](../../work/0093-uuid-models.md)
- 최근 전체 검증: [JSON 수직 연결 Hosted full](https://github.com/progresshans/godj/actions/runs/35511311272), source `d1a0570b87791378bc24a2cd90ce4afa8326a653`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

UUID 값·IR·query·양 DB·생성기·Form/Admin·serializer·OpenAPI·Helpdesk/client를 연결하고 통합 검증을 완료했다.
현재 구현 폭과 미지원 기능은 [구현 현황](IMPLEMENTATION_MATRIX.md)과 [Backend 범위](../BACKEND_MATRIX.md)가 소유한다.

JSONField를 Schema IR·query·migration·생성 모델에서 Form/Admin·serializer·OpenAPI와 실제 Helpdesk/client까지 연결했다.
양 DB의 SQL NULL/JSON null·숫자 정밀도·Form no-op·권한/CSRF·rollback·재접속과 native 저장 후 응답 실패의 로컬 통합 checkpoint를 완료했다.
JSON 수직 연결의 Hosted full과 후속 목록 응답 예산·generated JSON PostgreSQL 필수 실행의 Hosted web 검증을 완료했다.
명시적인 JSON key/index 경로의 exact/IN/isnull을 공통 AST·양 DB·typed/dynamic·생성 관계 소비자에 연결하고 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
PostgreSQL의 root/path·forward contains/contained_by와 SQLite의 명시적 capability 거부를 연결하고 로컬 checkpoint를 완료했다.
경로·containment source의 [Hosted ORM 통합](https://github.com/progresshans/godj/actions/runs/35517527972)을 완료했다.
Key-presence의 공통 AST·typed/dynamic·양 DB·생성 관계 소비자와 로컬 normal/race/CGO=0 검증을 마쳤다.
JSON 경로 projection의 공통 표현·nullable DTO와 양 DB compiler를 연결하고 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
Key-presence·root JSON 경로 projection의 [Hosted ORM 통합](https://github.com/progresshans/godj/actions/runs/35522171383)을 완료했다.
관계 filter source의 root scalar/JSON 경로 DTO 선택을 연결하고 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
Forward 대상 JSON 경로의 공통 선택 표현·JOIN·생성 소비자와 native JSONB 변환을 연결하고 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
Root 관계 DTO·forward JSON 경로 선택·native 변환의 [Hosted ORM 통합](https://github.com/progresshans/godj/actions/runs/35526500195)을 완료했다.
일반 forward scalar·whole JSON 선택의 typed nullable 결과와 공통 scanner를 연결하고 양 DB의 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
Source별 실행 범위와 결과는 TEST_EVIDENCE가 소유한다.

## 다음 행동

JSON 비교·정렬의 root/path 결과와 타입·NULL·정밀도 의미를 고정 Django의 실제 양 DB에서 독립 관찰한다.
DB별 차이와 기존 exact-number 정책을 확인해 후속 query 표현을 정하고, 조회 확장 묶음의 다음 통합 milestone을 이어간다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
