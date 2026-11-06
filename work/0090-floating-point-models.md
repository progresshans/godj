---
id: GDJ-0090
status: complete
updated: 2026-09-20
baseline_commit: "94f2743e0f47b1f0f7a4c514e7a2f09fa9991935"
integration_owner: "root"
---

# 작업량 수치의 Float 모델·소비자 연결

Helpdesk에서 소수와 지수 표기를 포함한 작업량 수치를 선언하고 저장·조회·편집한다.
FloatField의 IR/default·typed/dynamic ORM·양 DB·Form/Admin·JSON/OpenAPI와 독립 client를 함께 연결한다.
Duration의 Hosted full은 source `79637ef3f5943c9490027723527fb5074b01411f`에서 완료했다.
Float 제품·생성기·소비자·실패 경로를 함께 구현하고 로컬 및 Hosted ORM checkpoint를 통과해 기존 Draft PR에 통합했다.

## 먼저 확인한 경계

고정 Django 6.1/DRF 3.18.0/Python 3.14.3의 FloatField를 실제 실행했다. SQLite에서 최소 양수 subnormal과 최대 finite binary64,
0.1은 유지되지만 -0.0은 +0.0이 되고 NaN은 NULL이 됐다. ±Infinity는 저장됐다.
Form은 non-finite를 invalid로 거부하지만 DRF FloatField는 허용하고 JSONRenderer가 ValueError를 반환했다.
이 차이를 모델·DB·Form·JSON에 같은 규칙으로 묵시적으로 적용하지 않는다.

공통 JSON 계층은 Duration 작업에서 exact Number token과 숫자 byte 한도를 도입했다. Float 변환은 값의 정밀도와
각 계층의 admission/error 의미를 명시한 뒤 연결한다. NaN을 NULL로 바꾸거나 렌더할 수 없는 값의 입력을 저장한 뒤에야
JSON 응답이 실패하는 흐름을 정상 성공으로 취급하지 않는다. ±0·NaN의 equality/cache/no-op 의미는 ADR-0068을 따른다.

## 독립 관찰 준비

[Django/DRF runner](../conformance/runners/django/float_reference.py)는 Go 코드와 기대 결과를 읽지 않고 public API를 실행한다.
Model 89·Form 136·serializer 360·실제 JSON number 16, SQLite add/reopen/update/rollback/reverse와 root/relation query 각 9를 기록했다.
[Raw 결과](../internal/floattest/testdata/django61.json)는 float bits를 별도로 기록하므로 ±0·NaN·Infinity를 비표준 JSON number로 쓰지 않는다.
Python 네 버전의 fresh 실행에서 같은 의미를 확인했다. PostgreSQL 17.5의 별도 pgx binary probe에서는 signed zero·최소 subnormal·
최대 finite·NaN·±Infinity를 유지했고 NaN=NaN, NaN>Infinity, -0=0을 확인했다. Probe의 임시 table은 transaction rollback으로 제거했다.
이 관찰은 GoDj Float 모델 구현이나 지원 환경의 전체 검증이 아니다.

모델은 binary64와 NULL을 구분하고 DB 저장 제약은 backend가 검사하는 방향이다. Form/JSON은 finite 값만 받으며 serializer의
non-finite 선제 거부는 위 DRF 관찰과 구분해 명시해야 한다. IR/default와 query/cache의 NaN canonicalization, ±0 및 no-op 의미는
[ADR-0068](../docs/adr/0068-binary64-field-and-finite-json-boundaries.md)에 정리했다. SQLite NaN은 NULL로 바꾸기 전에 명시적으로 거부해야 한다.

## 구현과 검증 경계

Binary64 model/default·IR/wire·historical migration·typed/dynamic/F/IN·projection/aggregate·관계·양 DB를 연결했다.
Form/Admin·finite JSON/OpenAPI와 Helpdesk nullable effort, 실제 별도 ogen client도 구현했다. 특수값 저장과 입력/출력 거부는
[DEV-0015](../docs/DEVIATIONS.md#dev-0015--float-non-finite-입력을-저장json-rendering-전에-거부)에서 구분한다.

초기 checkpoint에서 historical default 복원과 Helpdesk non-null create/update 분기 누락을 보완했다. 새 HTTP runtime의 CSRF token을
그 runtime에서 먼저 발급받도록 테스트 설정도 바로잡았다. 수정 source를 고정해 affected 일반/race·필수 PostgreSQL·CGO0,
생성 drift·독립 client·물리 storage와 출력 실패의 로컬 checkpoint를 완료했다. 명령과 실제 source·결과는 TEST_EVIDENCE 한 곳에서 기록한다.

## 완료와 후속

제품 source `784dbf644f71c2d3507371c2afcc117d0f746ffb`의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35484302381),
attempt 1에서 관련 portable Go·relation·targeted command·PostgreSQL owner를 검증했다. 전체 platform 검증은 요청하지 않았으며
최근 Hosted full은 Duration source의 결과다. 실제 job 목록과 source·환경별 범위는 TEST_EVIDENCE를 따른다.

이 작업을 완료하고 [GDJ-0091 Decimal](0091-decimal-cost-models.md)의 정확한 값·저장·입력과 실제 예상 비용 흐름으로 이어간다.
Float 산술이나 넓은 expression·나머지 모델 기능을 모두 구현했다는 뜻은 아니다.
