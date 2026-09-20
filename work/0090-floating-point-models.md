---
id: GDJ-0090
status: planned
updated: 2026-09-20
baseline_commit: "94f2743e0f47b1f0f7a4c514e7a2f09fa9991935"
integration_owner: "root"
---

# 작업량 수치의 Float 모델·소비자 연결

Helpdesk에서 소수와 지수 표기를 포함한 작업량 수치를 선언하고 저장·조회·편집한다.
FloatField의 IR/default·typed/dynamic ORM·양 DB·Form/Admin·JSON/OpenAPI와 독립 client를 함께 연결한다.
구현 순서는 Duration의 Hosted full 검증을 마친 후 확정한다. 현재는 별도 worktree의 독립 조사 단계다.

## 먼저 확인한 경계

고정 Django 6.1/DRF 3.18.0/Python 3.14.3의 FloatField를 실제 실행했다. SQLite에서 최소 양수 subnormal과 최대 finite binary64,
0.1은 유지되지만 -0.0은 +0.0이 되고 NaN은 NULL이 됐다. ±Infinity는 저장됐다.
Form은 non-finite를 invalid로 거부하지만 DRF FloatField는 허용하고 JSONRenderer가 ValueError를 반환했다.
이 차이를 모델·DB·Form·JSON에 같은 규칙으로 묵시적으로 적용하지 않는다.

공통 JSON 계층은 Duration 작업에서 exact Number token과 숫자 byte 한도를 도입했다. Float 변환은 값의 정밀도와
각 계층의 admission/error 의미를 명시한 뒤 연결한다. NaN을 NULL로 바꾸거나 렌더할 수 없는 값의 입력을 저장한 뒤에야
JSON 응답이 실패하는 흐름을 정상 성공으로 취급하지 않는다. ±0·NaN의 equality/cache/no-op 의미도 결정해야 한다.

## 독립 관찰 준비

[Django/DRF runner](../conformance/runners/django/float_reference.py)는 Go 코드와 기대 결과를 읽지 않고 public API를 실행한다.
Model 89·Form 136·serializer 360·실제 JSON number 16, SQLite add/reopen/update/rollback/reverse와 root/relation query 각 9를 기록했다.
[Raw 결과](../internal/floattest/testdata/django61.json)는 float bits를 별도로 기록하므로 ±0·NaN·Infinity를 비표준 JSON number로 쓰지 않는다.
Python 네 버전의 fresh 실행에서 같은 의미를 확인했다. PostgreSQL 17.5의 별도 pgx binary probe에서는 signed zero·최소 subnormal·
최대 finite·NaN·±Infinity를 유지했고 NaN=NaN, NaN>Infinity, -0=0을 확인했다. Probe의 임시 table은 transaction rollback으로 제거했다.
이 관찰은 GoDj Float 모델 구현이나 지원 환경의 전체 검증이 아니다.

모델은 binary64와 NULL을 구분하고 DB 저장 제약은 backend가 검사하는 방향이다. Form/JSON은 finite 값만 받으며 serializer의
non-finite 선제 거부는 위 DRF 관찰과 구분해 명시해야 한다. IR/default와 query/cache의 NaN canonicalization, ±0 및 no-op 의미는
[제안 ADR-0068](../docs/adr/0068-binary64-field-and-finite-json-boundaries.md)에 정리했다. SQLite NaN은 NULL로 바꾸기 전에 명시적으로 거부해야 한다.

## 다음 행동

1. Model/Form/DRF의 실제 입력·오류와 JSON 숫자 변환을 독립 reference로 고정한다.
2. SQLite와 PostgreSQL의 finite/subnormal/non-finite·zero sign 저장·비교와 unsupported 경계를 분리한다.
3. 필요한 장기 의미를 정하고 값·생성·DB·소비자·실패 경로를 한 묶음으로 구현한다.

Go 제품 구현이나 runtime 검증을 완료한 상태가 아니다. 현재 활성 작업은 GDJ-0089다.
