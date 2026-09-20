---
id: GDJ-0094
status: active
updated: 2026-09-20
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
CI 선택/필수 목록에 넣은 후속은 별도 `web` 범위로 검증한다. 이전 source의 full과 후속 검증을 구분한다.
JSON을 ordered scalar로 일괄 허용하지 않는다. Key/path·contains 등 추가 JSON 연산도 backend capability와 실제 결과를 확인하며 이어간다.
현재 기반 구현과 검증은 JSONField 전체 및 프레임워크 목표의 완료가 아니다. 실행별 source·환경·실패/수정은 TEST_EVIDENCE에 기록한다.
