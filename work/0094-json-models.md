---
id: GDJ-0094
status: active
updated: 2026-09-20
baseline_commit: "7da91ad5fbd6284622fb372e7d8051120584424e"
integration_owner: "root"
---

# JSON 모델과 외부 연동 데이터

외부 UUID에 대응하는 구조화된 데이터를 모델에 저장하고 Form/Admin/API로 편집하는 흐름을 준비한다.
현재 UUID work의 Hosted 통합과 별개로 고정 Django/DRF의 public JSONField 결과를 먼저 관찰한다.
JSONField 제품 지원이나 새로운 DB/query 범위의 완료를 뜻하지 않는다.

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
model 54·Form 66·serializer 168·실제 JSON parser 32개 입력과 SQLite schema editor·ORM·실패/복구 관찰을 보존했다.
Python 네 버전의 fresh 비교를 완료했다. UUID의 검증 source·완료 여부는 GDJ-0093과 TEST_EVIDENCE가 소유한다.

SQLite의 whole JSON equality는 객체 키 순서와 1/1.0 표기에 영향을 받지만 native PostgreSQL jsonb equality는 이를 같은 값으로 비교했다.
SQLite에서 외부 duplicate key의 whole-document Python decode는 마지막 값, key lookup은 첫 값을 관찰했다.
JSON null과 SQL NULL은 저장·query에서 다르며 모델의 Python None readback만으로는 구분되지 않는다.
Django 6.1의 명시적 JSONNull()과 deprecated exact None 경고도 따로 기록했다.

다음은 이 차이를 고려한 값·숫자 정밀도·소유권과 DB별 capability 설계다. 공통 AST가 있다는 이유로 모든 backend의 JSON 비교를 같다고 가정하지 않는다.
이 관찰만으로 GoDj JSON 제품 구현이나 Django PostgreSQL 비교를 완료 처리하지 않는다.

UUID의 Hosted 통합을 완료한 뒤 이 작업을 활성 구현으로 이어간다. 현재 JSON 값 패키지와 any-root bounded decode를 구현 중이다.
Canonical object key/공백 정리와 정확한 숫자 token, 명시적 JSON null·invalid zero, 입력/decoded container의 소유권부터 연결하며 runtime 검증은 아직 전이다.
