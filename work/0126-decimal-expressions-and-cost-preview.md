# GDJ-0126: Decimal 계산식과 예상 비용 조정 미리보기

- 상태: 계약 채택, 구현·검증 묶음 작성, 원격 전체 검증 대기
- 시작: 2026-10-15
- 기준: `ebc9615d520406f5e50f42a6b5b08b8078b44d41`
- 근거: [개발 판단 기준](../docs/DEVELOPMENT_CRITERIA.md), [ADR-0069](../docs/adr/0069-exact-decimal-values-and-storage.md), [ADR-0090](../docs/adr/0090-query-update-and-scalar-expressions.md), [ADR-0091](../docs/adr/0091-grouped-results-and-having.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md)

## 필요한 기반과 소비자

Helpdesk의 nullable 예상 비용에 배율과 가감액을 적용한 결과를 조회한다. 계산한 비용으로 필터·정렬·페이지를
만들어야 하므로 한 페이지를 읽은 뒤 Go에서만 계산하면 전체 결과의 순서·총수와 일치하지 않는다.
현재 공통 scalar AST는 정수·Float 산술만 허용한다. Decimal을 float로 바꾸지 않고 같은 조회·갱신 기반에 연결한다.

## 계약

- 같은 Decimal kind의 덧셈·뺄셈·곱셈·나눗셈·나머지·부호 반전을 공통 불변 scalar AST로 표현한다.
  int64/float64·문자열과의 암묵 변환은 없다. 원본·single-valued 관계·선택 결과의 scope와 기존 자원 한도를 유지한다.
- `orm.Decimal(operand)`의 Decimal 전용 capability는 계산·Optional projection·SUM/AVG/MIN/MAX를 연결한다.
  Decimal AVG도 Decimal이다. 기존 int64/float64 평균의 float64 반환을 Decimal에 적용하지 않는다.
  Dynamic와 생성 facade는 같은 AST와 decoder를 사용한다.
- 각 원본 필드의 physical 값과 선언 precision을 검사한다. 계산의 중간값·결과에는 원본 field precision 대신
  전역 Decimal의 significant digits·adjusted exponent 한도를 적용한다. 잘못된 값이나 범위 초과를 다른 연산·조건으로 숨기지 않는다.
  NULL과 실제 zero를 구분하며 CASE에서 실행되지 않은 행 branch를 Go에서 먼저 계산하지 않는다.
- PostgreSQL은 native NUMERIC 산술과 division scale·SQLSTATE를 유지한다. SQLite는 canonical exact BLOB을
  statement 내부의 stateless 함수에서 계산한다. SQLite 나눗셈은 canonical operand의 scale과 PostgreSQL 17.10의
  division-scale 규칙으로 한 번 half-away-from-zero 반올림한다. 저장/중간 NUMERIC의 trailing scale에 따른 PostgreSQL과의
  차이는 명시적으로 관찰하며 양 DB의 모든 결과가 같다고 주장하지 않는다. SQLite의 zero division/remainder는 NULL,
  PostgreSQL은 native 오류다. Django SQLite NUMERIC affinity와의 차이는 기존 exact Decimal deviation에 기록한다.
- 계산된 Decimal의 SUM/AVG는 각 기여 값을 검증하고 정확한 상태를 집계 invocation마다 소유한다. SQLite는 canonical
  값의 scale을 사용하고 PostgreSQL은 native NUMERIC 결과 scale을 유지한다. 원본 field 집계의 기존 의미는 유지한다.
  DISTINCT·FILTER·empty/all-NULL·HAVING·결과 단계·페이지에도 같은 표현식 identity와 결과 domain을 사용한다.
- Decimal UPDATE는 원본 행의 계산만 허용한다. 최종 값이 target precision/scale에 정확히 들어가는지 DB statement 안에서
  검사하며 PostgreSQL의 암묵 반올림을 허용하지 않는다. 직접 literal·field 복사의 기존 사전 검증, 선택되지 않은 CASE branch의
  사전 검증, related/selected assignment 거부와 전체 statement/transaction rollback·cache 소유권을 유지한다.
- 예상 비용 미리보기의 HTML/API는 ViewTicket·현재 Category·하나의 읽기 snapshot을 사용한다. 저장된 비용은 바꾸지 않고
  예상 비용·계산 결과·필터 후 총수와 페이지를 제공한다. Decimal 입력과 결과는 명시적인 정확한 문자열이며 null을 0으로 만들지 않는다.
  Ticket·관계·보고서·audit를 보존하고 취소·cursor·snapshot·출력 실패에 부분 결과를 게시하지 않는다.

비용 미리보기의 입력은 `factor`(6/4, 기본 1), `delta`(14/2, 기본 0), 선택 `min_cost`(21/6), `p`(1..50001)다.
닫힌 typed parameter codec은 Decimal.String의 canonical 문자열과 동일한 문법·precision을 OpenAPI에 게시한다.
응답은 exact canonical 문자열·required null을 사용하고 adjusted cost 내림차순(NULL 마지막)·ID 오름차순이다.

## 구현과 검증 위치

고정 Django의 독립 양 DB 관측에서 public expression·aggregate·UPDATE의 결과와 오류를 보존한다.
PostgreSQL과 SQLite의 의미 차이, GoDj exact SQLite의 의도한 차이와 native invariant를 구분한다.
다른 구조의 두 앱 생성 소비자는 typed/dynamic·읽기/쓰기·CASE·관계/결과 scope·NULL·precision·division·
잘못된 physical 값·취소/cache/session·statement rollback을 검사한다. 모델·결과 타입의 잘못된 연결은 compile-negative로 확인한다.
Helpdesk HTML/API·실제 DB·권한/범위·독립 OpenAPI client·브라우저를 함께 연결한다.

제품 코드·생성기·소비자·테스트를 먼저 정리하고 로컬은 포맷·최소 compile과 실패 재현에 사용한다.
Push 후 기존 자동 전체 CI owner에서 양 DB·세 mode·전체 플랫폼·generated drift와 원본 실행 증거를 확인한다.
실행하지 않은 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 PASS로 기록하지 않는다.

## 완료 조건

- [ ] 공통 AST·양 compiler의 Decimal 계산/집계/assignment와 precision·source·자원·오류 경계를 연결한다.
- [ ] Typed/dynamic·생성 facade·cross-app 소비자·compile-negative와 실패/rollback을 검증한다.
- [ ] 고정 Django의 독립 관측, backend 차이와 GoDj exact SQLite deviation을 실제 결과로 구분한다.
- [ ] Helpdesk의 비용 미리보기·권한·snapshot·HTML/API·독립 client·브라우저·저장 보존을 연결한다.
- [ ] 같은 source의 필수 실행과 원본 증거를 대조하고 현행 지원/상태 문서를 갱신한다.

Named annotation·collection computed operand·Duration/temporal 산술·function/subquery/window와 나머지 카탈로그는
계속 전체 제품 목표로 유지한다. 이 작업의 완료로 그 범위를 완료 처리하지 않는다.
