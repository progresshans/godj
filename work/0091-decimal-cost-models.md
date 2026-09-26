---
id: GDJ-0091
status: complete
updated: 2026-09-20
baseline_commit: "09307794071874900f317a01e7b413f11d784e60"
integration_owner: "root"
---

# 소수점 비용의 Decimal 모델·소비자 연결

Helpdesk의 예상 비용을 DecimalField로 선언하고 저장·조회·편집하는 수직 단면을 구현한다.
소수 자릿수와 정밀도가 모델 의미이며 binary64로 바꾸어 저장 정확도를 잃지 않도록 값·IR/default·DB·Form/Admin·JSON/OpenAPI를 함께 설계한다.
GDJ-0090의 Hosted ORM은 Float source에서 완료했다. 이 작업은 독립 관찰과 사전 실험을 바탕으로 Decimal의 값·저장 경계를 정하고 구현한다.
독립 기준 준비만으로 Decimal 제품 구현이나 지원 범위의 완료를 뜻하지 않는다.

## 먼저 확인한 동작

고정 Django 6.1/DRF 3.18.0의 독립 public API를 실행했다. Model 89·Form 136·serializer 360·JSON number 19,
precision 조합 90개와 SQLite의 실제 add/reopen/update/rollback/reverse, root/relation query 각 9개를 기록했다.
원본 Decimal의 문자열·sign·digits·exponent와 SQLite 물리 값의 종류·float bits를 따로 보존한다.
[독립 runner](../conformance/runners/django/decimal_reference.py)와 [raw 결과](../internal/decimaltest/testdata/django61.json)를 따른다.

Django model의 to_python·get_prep_value와 full clean은 서로 다르다. Form은 precision validator를 사용하며 DRF는 검증 후 정해진 scale로
quantize하고 기본 JSON 문자열을 만든다. JSON 숫자를 Python float로 읽은 뒤의 결과도 문자열 입력과 구분한다.
Django Form은 sNaN 입력을 invalid로 거부하지만 non-null 초기 Decimal과 비교하는 changed 계산은 InvalidOperation을 낸다.
이 실패를 정상 비교값으로 바꾸어 기록하지 않는다.

Django SQLite의 NUMERIC 경로는 30자리 Decimal을 다른 정수로 저장하거나 저장 뒤 조회에서 InvalidOperation을 낸다.
초과 소수 자릿수의 반올림은 SQLite/Django 읽기의 half-even과 PostgreSQL NUMERIC 저장의 half-away-from-zero가 다르다.
이 차이를 감춘 공통 round-trip이나 다른 backend 결과의 대용 검증을 채택하지 않는다. 양 DB의 ±0 저장은 양수 zero로 돌아온다.

## 저장 방식의 사전 실험

SQLite NUMERIC의 손실을 재현한 뒤 field별 scale과 무관한 canonical binary key를 별도로 시험했다.
부호·adjusted exponent·coefficient 순서로 모든 field가 같은 값 공간을 사용하면 SQLite의 기본 BLOB 비교로 정확한 숫자 순서를 유지할 수 있었다.
두 prototype test에서 2,005개 값과 실제 SQLite 비교·정렬·Min/Max를 확인했다. Custom collation이나 process-global 등록은 사용하지 않았다.
[ADR-0069](../docs/adr/0069-exact-decimal-values-and-storage.md)에서 SQLite BLOB order key와 PostgreSQL native NUMERIC,
공통 write precision 검사와 외부 값 거부를 채택했다. 설계의 채택과 환경별 실행 완료는 구분한다.
이 실험 자체를 제품 구현이나 Django의 NUMERIC 저장 형식과의 호환으로 표시하지 않는다.

## 현재 구현과 다음 행동

1. Exact Decimal 값과 precision arm을 Schema IR·default·clone·digest·wire/resource budget에 연결했다.
2. Typed/dynamic exact·ordered·IN·F·projection·Min/Max와 forward/reverse 관계, generated model·scanner·create/patch/save를 연결했다.
3. SQLite BLOB과 PostgreSQL NUMERIC의 exact parameter·scanner·physical preflight, historical create/add/remove·autodetect/no-op을 구현했다.
   생성 모델로 실제 양 DB의 30자리/1000자리 경계·서로 다른 scale의 F 비교·null·rollback·재접속·외부 잘못된 저장값을 검증했다.
4. Form/Admin·serializer의 원문 precision 검증과 Helpdesk nullable 예상 비용, fixed-scale JSON/OpenAPI·별도 client를 연결했다.
   Form changed의 숫자 equality, scale 초기값·escape·null/생략, 정확한 JSON 숫자와 fixed-scale 문자열을 검증한다.
5. 비용 소비자의 affected normal·race·CGO0와 source `d106e73d5338cff107623351c48ac4f5778fff8c`의 Hosted ORM을 완료했다.
   Precision AlterField/backfill·unbounded NUMERIC은 현재 구현 범위로 표시하지 않는다.

모델·DB와 입력·실제 소비자를 같은 GDJ-0091에서 연결했다. 현재 검증 환경과 남은 실행은 TEST_EVIDENCE가 소유한다.
후속 [GDJ-0092](0092-decimal-precision-migrations.md)에서 기존 비용의 정밀도 변경과 데이터 보존을 이어간다. 별도 release milestone을 만들지 않는다.

기준 실행·환경·hash는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0091--decimal의-독립-정밀도저장-기준-준비)가 소유한다.
기존 transaction·권한·자원 한도·생성 실패 보존 경계를 유지하며 실제 소비자와 검증을 한 묶음으로 연결한다.
