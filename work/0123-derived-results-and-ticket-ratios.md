---
id: GDJ-0123
status: complete
updated: 2026-10-12
baseline_commit: "98b416029aa9dda479c19ebe7e699a24b01c2d27"
integration_owner: "root"
---

# 집계 결과 단계와 티켓 현황 비율

GDJ-0122는 같은 행의 값을 계산해 조회와 집계 operand로 연결했다. 집계된 값은 비교·정렬할 수 있지만
다시 계산하거나 다른 조회 단계의 입력으로 사용할 수 없다. Helpdesk의 전체·열린 건수를 조합한 닫힌 건수와
열린 비율을 DB에서 계산하고, 같은 결과에 대한 필터·페이지와 HTML/API·독립 client를 연결한다.
이는 일반 named annotation·subquery/window로 이어지는 결과 소유권의 기반이며 전체 QuerySet 영역의 완료가 아니다.

## 구현 경계

- 모델 FieldRef와 별개인 불변 결과 열 참조를 사용한다. 결과 kind·NULL·선택 순서와 원 source metadata를
  구별하며 typed/dynamic 경로는 같은 AST를 사용한다. 다른 결과 shape의 참조, 선택되지 않은 값, 원 행 필드와
  결과 열의 부적절한 혼합은 실행 전에 거부한다. 가짜 모델 field나 원 metadata 변경으로 결과 열을 표현하지 않는다.
- 조회 단계는 이전 plan을 보존한다. 필터·집계·정렬·slice의 앞뒤 순서를 바꾸지 않으며 추가 계산·조회와 terminal
  재집계를 한 SQL statement로 실행한다. 단계·전체 node/SQL/parameter 예산과 관계 source의 소유권을 검증한다.
- 정수/Float 산술·CASE와 기존 scalar codec을 재사용하되 NULL·native 오류·int64 중간 overflow 검사를 유지한다.
  같은 단계의 중첩 aggregate와 별도 단계의 재집계를 구분한다. Decimal/Duration 산술을 암묵 Float 변환으로 대체하지 않는다.
- Typed 결과의 조립은 선택 열이 늘 때마다 새 고정 arity API를 요구하지 않도록 구성한다. 순수 DTO builder와
  private scanned-cell cache, fresh refinement·source session·context·scan/iteration/close 실패와 결과 미게시를 보존한다.
- Helpdesk는 현재 Category/ViewTicket 범위와 같은 읽기 snapshot에서 닫힌 건수·열린 비율을 계산한다.
  비율은 정수 basis point로 표현하고 query 입력·HTML/API schema·독립 client에서 같은 의미를 사용한다.
  원 Ticket·link·audit 보존과 전체 오류 응답·페이지 일관성을 함께 검증한다.

일반 이름 재정의/annotation registry, reverse/collection 집계 확장, 함수·subquery/window, Decimal/Duration 산술과
나머지 카탈로그는 계속 남아 있다. 최종 공개 API는 실제 typed/dynamic 소비자와 실패 계약에 맞춰 확정한다.
새로운 장기 의미는 기존 ADR-0090/0091에 반영한다.

## 진행

- [x] 결과 열 참조·단계·타입/NULL·소유권과 예산을 공통 AST에 연결
- [x] 양 backend compiler와 typed/dynamic ORM·생성 facade·결과 조립 구현
- [x] 고정 Django 독립 결과와 생성 소비자·compile-negative·실패/소유권 검증 연결
- [x] Helpdesk Form/HTML/API·독립 client·브라우저와 snapshot/read-only 경계 연결
- [x] 영향·생성 drift·양 DB·race/process/platform과 같은 source의 전체 원격 검증

Source `97b441c6f74994fd863b6ee99174478c8179c2e0`의
[전체 CI 38177282549](https://github.com/progresshans/godj/actions/runs/38177282549)와 원본 실행 증거 대조를 완료했다.
결과 단계·양 compiler·typed/dynamic lazy ORM·composable DTO·생성 facade와 Helpdesk HTML/API·독립 SDK를
같은 source에서 검증했다. 빈 projection과 COUNT 0 한 행, Duration AVG의 native 비교·마지막 decode,
scope·조회 세션·cache·부분 실패와 원 Ticket/link/audit 보존을 포함한다.
환경별 실행·원격 실패와 수정·불변 artifact/source 결합은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.
이 작업의 완료는 일반 annotation이나 기능 카탈로그 전체의 완료를 뜻하지 않는다.
