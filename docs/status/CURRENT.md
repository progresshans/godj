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

다음 기반은 모델의 column uniqueness다. 양 DB의 scalar·unique FK·ModelForm·기존 데이터 migration·실패/재시도·동시 쓰기를
독립 관찰했고, Python 네 버전의 fresh 기준 비교를 마쳤다. 아직 GoDj 제품에 unique 선언이나 제약 생성 기능은 없다.
기존 JSON/Float/Decimal 저장 정책과 backend 차이를 유지하며 사전 조회와 DB 무결성 보장을 구분한다.

## 다음 행동

Schema IR·historical definition/digest·생성 metadata에 고유성을 연결하고 양 DB의 선언·물리 catalog·migration 경계를 구현한다.
그 뒤 실제 insert/update 충돌·경쟁·실패/rollback을 Form/Admin/API와 Helpdesk 외부 참조의 중복 방지까지 연결한다.
구현과 검증 범위는 GDJ-0095와 TEST_EVIDENCE에 기록한다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
