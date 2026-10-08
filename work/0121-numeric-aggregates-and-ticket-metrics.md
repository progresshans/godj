---
id: GDJ-0121
status: active
updated: 2026-10-08
baseline_commit: "b14d0eaf3d49f01151c8b010fb97eac2a57532ef"
integration_owner: "root"
---

# 숫자 합계·평균과 업무 지표

현재 Count/Min/Max는 원본 행 수와 극값을 표현한다. Helpdesk의 비용·노력·시간 요약에 필요한 Sum/Avg와
원본 필드 범위를 넘는 결과를 같은 immutable Query AST에 추가한다. 선행 GDJ-0120의 영향 검증은 완료됐으며
source `b14d0eaf`의 전체 Hosted 통합도 완료됐다. 그 실행 결과를 이 변경의 PASS로 사용하지 않는다.

## 구현 경계

- 정수·Float·Decimal·Duration의 nullable/non-null root와 finite forward operand를 typed/dynamic 표면에 연결한다.
  Sum은 같은 scalar 종류, 정수 Avg는 float64, 다른 Avg는 같은 scalar 종류의 Optional을 반환한다.
  원본 FieldRef·precision·관계 identity를 바꾸지 않고 결과 kind와 nullable 의미를 AST에서 분리한다.
- 빈 원본·모두 NULL·빈 filter의 Sum/Avg는 NULL이다. 접힌 empty는 I/O 없이 같은 결과를 반환한다.
  Distinct·조건부 집계·그룹·HAVING/정렬은 기존 source/cache/context/동일 expression 소유권을 유지한다.
  Boolean·JSON·문자/시간점의 숫자 집계와 collection operand는 명시적으로 거부한다.
- Decimal은 exact 합계와 고정 PostgreSQL NUMERIC의 division scale·half-away 규칙을 따른다.
  SQLite는 저장 BLOB를 native numeric으로 강제 변환하지 않는다. 집계별 상태·field precision 검사와 결과의
  전역 Decimal 한도를 분리하며 float·문자·잘못된 physical 값·overflow를 조용히 수용하지 않는다.
- 정수 합계의 결과는 int64이며 overflow를 오류로 반환한다. Float 모델의 NaN/Infinity 계약과 native backend
  산술을 보존하고 SQLite의 NaN→NULL이 빈 결과로 보이지 않게 한다. Duration 평균은 microsecond half-even과
  backend의 실제 표현 범위를 명시한다. 합계/평균의 native 차이는 독립 reference와 구분해 기록한다.
- Helpdesk의 같은 Category snapshot·인가·페이지에서 비용 합계·노력 평균·시간 합계를 HTML/API와 독립 생성
  client에 노출한다. NULL·외부 저장값·큰 값·오류/취소·조회 전용 경계와 OpenAPI/생성 drift를 함께 확인한다.

기존 [ADR-0091](../docs/adr/0091-grouped-results-and-having.md)과
[ADR-0069](../docs/adr/0069-exact-decimal-values-and-storage.md)에 장기 의미를 둔다.
독립 Django 관찰과 구현·환경별 검증은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.
전체 annotation·계산식 operand·subquery/window 등 남은 카탈로그 요구는 이 작업의 완료와 구분한다.

## 진행

- [x] 양 DB의 독립 기준과 결과 타입·precision·overflow·NULL 요구 확인
- [x] AST·typed/dynamic 결과·backend 산술/오류와 원본 metadata 소유권 구현
- [x] 생성 facade/독립 소비자의 타입·실제 DB·실패 경계와 회귀 검사
- [x] Helpdesk HTML/API·독립 client·문서/생성물 drift 연결
- [x] 로컬 normal의 영향·실제 양 DB·독립 SDK/HTTP·브라우저 검증과 정리
- [ ] 같은 source의 원격 세 모드·전체 platform 통합과 정확한 미완료 기록

로컬 검증은 구현과 테스트 소스의 hash로 연결했으며 실행 상세는 TEST_EVIDENCE를 따른다.
원격 전체 통합이 끝나기 전에는 이 작업이나 전체 카탈로그를 완료로 표시하지 않는다.
