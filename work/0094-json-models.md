---
id: GDJ-0094
status: completed
updated: 2026-09-21
baseline_commit: "7da91ad5fbd6284622fb372e7d8051120584424e"
integration_owner: "root"
---

# JSON 모델과 외부 연동 데이터

외부 UUID에 대응하는 구조화된 데이터를 모델에 저장하고 Form/Admin/API로 편집하는 흐름을 준비한다.
완료한 UUID 기반에서 고정 Django/DRF의 public JSONField 결과를 관찰하고 모델부터 소비자까지 연결한다.
기본 수직 연결 뒤 JSON key/path·contains 등 추가 연산의 의미와 지원 경계를 이어간다.

## 먼저 확인할 의미

- SQL NULL과 JSON null, 빈 object/array/string 및 bool/number의 구분
- 숫자의 정밀도·표기와 객체 키 순서·중복 key, Unicode/NUL·잘못된 surrogate 및 non-finite 입력
- 복합 default와 값의 소유권, Form 초기값·원문·값 기준 변경 감지
- Serializer의 생략/null/default·JSON 값과 JSON을 담은 문자열의 차이·렌더링 실패
- 기존 DB의 nullable 추가·실제 저장·query 결과·rollback·재접속·reverse
- SQLite와 native PostgreSQL JSON 저장·비교·오류의 차이와 backend capability 경계

Schema IR에서 생성 model·query·migration·Form/Admin/API로 이어지는 값의 표현은 독립 관찰 뒤 결정한다.
기존 bounded JSON parser·명시적 실패·exact number·copy/cache 소유권을 약화하지 않는다.
각 JSON query 연산은 의미와 실제 지원 backend를 검토하며 기존 scalar 연산을 일괄 허용하지 않는다.

## 현재

[독립 runner](../conformance/runners/django/json_field_reference.py)와 [raw](../internal/jsontest/testdata/django61.json)에
model 62·Form 88·serializer 192·실제 JSON parser 43개 입력과 SQLite schema editor·ORM·실패/복구 관찰을 보존했다.
Python 네 버전의 fresh 비교를 완료했다. UUID의 검증 source·완료 여부는 GDJ-0093과 TEST_EVIDENCE가 소유한다.

SQLite의 whole JSON equality는 객체 키 순서와 1/1.0 표기에 영향을 받지만 native PostgreSQL jsonb equality는 이를 같은 값으로 비교했다.
SQLite에서 외부 duplicate key의 whole-document Python decode는 마지막 값, key lookup은 첫 값을 관찰했다.
JSON null과 SQL NULL은 저장·query에서 다르며 모델의 Python None readback만으로는 구분되지 않는다.
Django 6.1의 명시적 JSONNull()과 deprecated exact None 경고도 따로 기록했다.

값·숫자 정밀도·소유권과 DB별 capability는 이 차이를 반영한다. 공통 AST가 있다는 이유로 모든 backend의 JSON 비교를 같다고 가정하지 않는다.
이 관찰만으로 GoDj JSON 제품 구현이나 Django PostgreSQL 비교를 완료 처리하지 않는다.

[JSON 값과 저장 설계](../docs/adr/0071-json-values-and-native-storage-boundaries.md)에 따라 값·IR·strict default wire와 historical digest,
공통 query의 exact/IN/F exact·SQL isnull·projection, typed/dynamic ORM, root/eager·forward/reverse 생성 코드를 연결했다.
SQLite TEXT의 JSON_VALID CHECK와 native PostgreSQL JSONB parameter/adapter·numeric expansion preflight를 구현했다.
실제 외부 generated module에서 기존 DB nullable 추가·stored null·정밀도·query/관계·cache/clone·실패·취소·rollback·reopen·reverse의
로컬 checkpoint를 완료했다. 환경·명령·source·실행 목록과 제한은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.

Form/Admin의 JSON 원문·빈 값·변경 감지, serializer 입력/응답 nullability·OpenAPI와 실제 Helpdesk·독립 client를 연결했다.
빈 문자열 key는 선언된 JSONField 내부에서만 허용하고 API envelope의 이름과 NUL·Unicode·공유 자원 한도는 유지한다.
Form의 동등한 numeric spelling과 stored JSON null을 실제 UPDATE에서 보존하며, Helpdesk의 실제 DB readback과 응답 검증을
transaction 안에서 처리해 native 확장으로 응답할 수 없는 값이 commit되지 않게 했다. 양 DB와 HTTP consumer의 normal/race/CGO=0 checkpoint를 마쳤다.
JSON 수직 연결 source의 Hosted full을 마쳤다. 실제 목록의 aggregate JSON 예산을 보강하고 generated JSON PostgreSQL 소비자를
CI 선택/필수 목록에 넣은 후속도 별도 `web` 범위에서 통과했다. 이전 source의 full과 후속 검증을 구분한다.
JSON을 ordered scalar로 일괄 허용하지 않는다. Contains 등 추가 JSON 연산도 backend capability와 실제 결과를 확인하며 이어간다.
명시적인 key/index 경로의 exact/IN/isnull을 공통 AST·양 DB·typed/dynamic·생성 관계 소비자에 연결했다.
고정 Django의 양 DB lookup raw를 보존하고 타입·숫자 정확도와 missing/JSON null/root SQL NULL을 구분한다.
SQLite native path의 empty/NUL key 오조회를 재현해 bounded codec을 사용하는 SQL 함수로 보정하고 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
PostgreSQL root/path·forward contains/contained_by를 추가하고 같은 식의 SQLite capability 거부를 연결했다.
별도 33개 문서·168개 조건의 독립 관찰을 소비하는 로컬 checkpoint와 JSON 조회 통합 milestone의 `orm` scope Hosted를 완료했다.
Key-presence의 별도 양 DB 96조건 관찰을 보존하고 공통 AST·typed/dynamic·root/path·forward 연결의 로컬 normal/race/CGO=0 검증을 마쳤다.
SQLite의 literal empty/NUL key 보정과 빈 목록 확장은 ADR-0071/DEV-0017에 명시한다.
앞선 source의 platform 결과를 새 key-presence 구현의 검증으로 사용하지 않는다. JSON 경로 projection의 공통 선택 표현·nullable DTO와 양 DB compiler도 연결했다.
Public Django 값·missing/root NULL 관찰을 보존하고 scalar·생성 소비자·예제의 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
Key-presence·projection과 공통 선택 표현을 묶은 조회 통합 milestone의 Hosted `orm` scope를 완료했다.
관계 filter source에서 root scalar/JSON 경로 DTO를 선택하도록 확장하고 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
Forward 관계 대상의 JSON 경로 선택에 필요한 optional JOIN·값의 nullability·source metadata 경계를 독립 관찰했다.
공통 선택 표현·양 DB·생성 소비자·native 결과 변환을 연결하고 로컬 normal/race/CGO=0 checkpoint를 마쳤다.
Root 관계 DTO와 forward 경로 선택을 묶은 Hosted ORM 통합 milestone을 완료했다.
11종 필수·nullable forward scalar/whole JSON의 독립 Django 관찰을 보존하고 공통 선택 표현·typed scanner를 연결했다.
실제 양 DB의 normal/race/CGO=0 checkpoint를 마쳤으며, 앞선 Hosted source의 결과를 새 구현의 검증으로 사용하지 않는다.
JSON root/path·forward의 literal 대소 비교와 Boolean 조합을 독립 관찰하고 공통 AST·typed/dynamic·양 DB에 연결했다.
SQLite는 기본 Django와 기존 GoDj canonical 저장 profile을 따로 보존하며 path 숫자를 반올림하지 않는 비교 함수를 사용한다.
실제 양 DB와 소비자의 normal/race/CGO=0 checkpoint를 마쳤다.
일반 forward scalar 선택과 JSON 대소 비교를 묶은 source의 Hosted ORM 통합을 완료했다.
Root/path·forward JSON과 11종 forward scalar의 ASC/DESC 정렬을 공통 값 표현·양 DB·generated 소비자에 연결했다.
정렬 전용 optional JOIN·정확한 numeric key·DTO DISTINCT와 full-model 내부 정렬 셀·cold Count·slice를 유지한다.
독립 model/DTO 순서·Count 관찰과 로컬 normal/race/CGO=0 checkpoint를 마쳤으며, 새 공통 compiler·JOIN source의
Hosted ORM 통합 milestone도 완료했다.
Whole/path·forward JSON의 literal IContains와 Helpdesk Admin/API search/source·독립 생성 client를 연결했다.
숫자 token·NUL 검색의 명시적 차이는 독립 SQL probe와 DEV-0017로 기록하고 양 DB의 normal/race/CGO=0 checkpoint를 마쳤다.
같은 고정 source의 Hosted web 통합도 완료했다. 다음 모델 무결성 요구는 [GDJ-0095](0095-model-uniqueness.md)가 소유한다.
현재 기반 구현과 검증은 JSONField 전체 및 프레임워크 목표의 완료가 아니다. 실행별 source·환경·실패/수정은 TEST_EVIDENCE에 기록한다.
