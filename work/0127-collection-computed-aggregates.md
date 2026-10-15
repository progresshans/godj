# GDJ-0127: 컬렉션 계산식 집계와 라벨별 조정 비용

- 상태: 계약 채택, 구현 중; 실행 검증 전
- 시작: 2026-10-15
- 기준: `b66a5aade96a2bb1124c0dbdc0f9edcf122fe70b` (GDJ-0126의 전체 CI 검증은 별도로 진행 중)
- 근거: [개발 판단 기준](../docs/DEVELOPMENT_CRITERIA.md), [ADR-0090](../docs/adr/0090-query-update-and-scalar-expressions.md), [ADR-0091](../docs/adr/0091-grouped-results-and-having.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md)

## 필요한 기반과 소비자

라벨별 연결 티켓의 예상 비용에 배율·가감액을 적용한 합계를 조회한다. 각 티켓의 값을 계산한 뒤 집계해야
NULL·CASE·나눗셈·반올림과 FILTER가 보존된다. 집계나 페이지를 읽은 뒤 Go에서 보정하는 것으로 대신하지 않는다.

## 계약

- 기존 불변 scalar AST에 finite collection 경로를 보존한다. 계산식의 구성과 사용 문맥의 cardinality 검증을
  나누며, 컬렉션 계산은 완료된 COUNT/MIN/MAX/SUM/AVG 안에서만 실행한다. 일반 projection·그룹 키·행 정렬·
  WHERE의 computed comparison·UPDATE와의 경계는 유지한다. 중첩 집계에는 명시적 결과 stage가 필요하다.
- 기존 kind·Decimal precision·입력 검증·Optional·오류·깊이/노드/SQL/parameter 예산을 유지한다. 숫자 kind 간
  암묵 변환은 없다. Root·관련 leaf·selected result의 source authority는 사용 문맥과 별도로 검사한다.
- 같은 collection의 값과 CASE/FILTER 조건은 같은 joined row를 참조한다. 집계용 JOIN 해석은 원본 Filter가
  materialize한 최신 occurrence를 재사용하며 원본 불변 표현식을 수정하지 않는다. 독립 collection의 행 곱,
  중복 through 행, 값 기준 DISTINCT, 원본 NOT/EXISTS와 집계 FILTER의 행 조건 의미를 유지한다.
- 빈 관계와 NULL을 보존한다. CASE fallback이 LEFT JOIN의 빈 행을 처리하는 의미를 별도로 관찰한다.
  FILTER에서 제외된 행과 실행되지 않은 CASE branch를 먼저 계산하지 않는다. 기여하는 값의 손상·범위 초과는
  후속 연산·HAVING·decoder로 숨기지 않는다.
- Typed 숫자/Decimal capability와 dynamic expression 집계는 같은 AST·compiler·decoder를 사용한다.
  Dynamic 이름의 권한은 명시적인 sealed BoundModel이 소유한다. 행용 BindExpression의 범위를 넓히지 않는다.
- Helpdesk의 라벨별 조정 비용은 현재 Category·ViewLabel/ViewTicket·한 읽기 snapshot을 사용한다.
  집계마다 현재 Category FILTER를 적용하고 물리적인 다른 Category 링크의 값·존재·오류를 노출하지 않는다.
  입력과 결과는 정확한 Decimal 문자열이며 기존 저장값·관계·보고서·audit를 보존한다. HTML/API·OpenAPI·
  독립 client·브라우저에서 입력·페이지·실패 경계를 연결한다.

## 구현과 검증

고정 Django 6.1의 독립 SQLite/PostgreSQL 관측으로 행별 계산 집계·NULL·CASE·DISTINCT·FILTER·JOIN 재사용을
대조한다. GoDj exact SQLite Decimal의 차이는 기존 deviation을 적용하며 실제 관측 범위를 명시한다.
구조가 다른 두 앱의 생성 소비자에서 typed/dynamic·cross-app·양 DB·세 mode·compile-negative·
취소/cache/session·물리 값 손상과 저장 보존을 검사한다. 기존 원격 실행 owner와 필수 inventory에 연결한다.

로컬은 포맷·최소 compile과 관찰된 실패 재현에 사용한다. 전체 묶음 완성 후 push하고 자동 전체 CI의
같은 source 실행과 원본 artifact를 대조한다. 진행 중인 GDJ-0126의 결과를 이 변경의 PASS로 승격하지 않는다.

현재 공통 AST·양 compiler·typed/dynamic 집계와 생성 facade를 연결하고 실제 프로젝트 생성물을 갱신했다.
컬렉션 참조 관측을 기존 30개에서 계산식 19개를 추가한 49개로 확장하고, 생성 소비자의 필수 실행을
양 DB owner에서 130개 child run/pass와 8개 compile-negative로 구성했다. 관측과 DB 테스트는 아직 실행 전이다.
핵심·생성 소비자는 로컬 compile-only로 확인했으며 새 3개 부정 대조도 실제 compiler 거부를 확인했다.
Helpdesk 라벨 사용 현황의 factor/delta 입력·원래/조정 비용 합계·required nullable API 출력·HTML과
실제 DB 검증 사례를 연결하고 compile-only로 확인했다. 브라우저 probe의 필수 검사를 28개로 확장했으며
아직 실행 전이다. 독립 client의 티켓별 유리수 기대값·문자열 query·required nullable wire를 연결하고
실제 API에서 여섯 OpenAPI profile과 고정 ogen 생성물을 갱신했다. Helpdesk profile만 변경됐으며
독립 client 빌드를 compile-only로 확인했다. 같은 source의 원격 실행·원본 증거 대조가 남아 있다.

## 완료 조건

- [ ] 공통 AST의 집계 cardinality·source authority와 양 compiler의 JOIN/CASE/FILTER를 연결한다.
- [ ] Typed/dynamic·생성 facade와 두 앱의 native·부정 대조·실패/보존 검증을 연결한다.
- [ ] 독립 Django 관측과 backend 차이를 실제 결과로 기록한다.
- [ ] 라벨별 조정 비용의 HTML/API·권한·snapshot·client·브라우저를 연결한다.
- [ ] 같은 source의 필수 전체 실행과 원본 증거를 대조하고 현행 문서에 반영한다.

Named annotation·Duration/temporal 산술·function/subquery/window와 나머지 기능 카탈로그는 계속 전체 목표로 유지한다.
