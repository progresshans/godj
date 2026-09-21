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
definition/digest·자동 변경 계획에 연결했다. PostgreSQL의 named UNIQUE와 SQLite의 선언된 unique index를
실제 DDL·catalog·저장 충돌 오류에 연결했다. 실패/재시도·역방향 적용과 SQLite remake의 index·행·sequence·FK 보존을
각 backend의 로컬 checkpoint에서 검증했다. [소유권과 구현 경계](../adr/0072-column-uniqueness-and-constraint-ownership.md)를 따른다.
Operation별 여러 SQL과 metadata-only의 빈 묶음을 구분하는 공통 renderer·root·프로젝트 runner도 사용한다.
공통 ORM의 생성·수정 고유성 사전 검증도 연결했다. 실제 쓰기와 같은 typed 입력 검증을 사용하고,
수정 시 자기 행 제외·NULL/생략 구분·DB 오류/취소와 필드 진단 분리를 양 DB에서 확인했다.
Form/Admin/API와 Helpdesk의 실제 UUID unique migration·generated client까지 연결했다.
사전 중복과 native 저장 충돌은 field/non-field 진단으로 전달하고, 실패한 수정은 기존 데이터를 보존한다.
SQLite의 정상 rollback은 입력 오류를 그대로 전달하며 정리 실패·취소·불확실한 결과는 실행 오류로 유지한다.
양 DB HTTP 소비자와 SQLite 기반 generated client의 로컬 normal·race·CGO-disabled checkpoint를 완료했다.

## 다음 행동

고유성의 선언·migration·양 DB·ORM·입력 소비자를 묶은 고정 source의 Hosted full 통합 검증을 이어간다.
초회 실행에서 발견한 격리 fixture 의존성·외부 SQL renderer fixture와 portable SQLite reference 차이를 수정했다.
관련 로컬 compile·복구·race·CGO-disabled와 독립 SQLite/Python 검사를 확인했으며 수정 source의 Hosted 재검증이 남아 있다.
필수 platform·process·generated 소비자와 source/실행 누락을 확인하고 GDJ-0095의 완료 범위를 기록한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
