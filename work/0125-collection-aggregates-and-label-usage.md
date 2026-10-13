# GDJ-0125: 컬렉션 집계와 라벨 사용 현황

- 상태: implementation in progress — 첫 원격 실패 수정, 원격 통합 검증 미완료
- 시작: 2026-10-13
- 기준: `31722acfa53c119cefe858e5dcb11e1a3f76e20e`
- 근거: [ADR-0091](../docs/adr/0091-grouped-results-and-having.md), [Q-011/Q-013](../docs/OPEN_QUESTIONS.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md)

## 필요한 기반과 소비자

Helpdesk의 현재 Category에서 라벨별 연결 티켓 수·열린 티켓 수와 비용·노력·시간을 조회한다.
연결이 없는 라벨도 표시하므로 Ticket를 원본으로 그룹화하는 기존 요약으로는 같은 결과를 만들 수 없다.
현재 관계 집계 operand와 조건은 single-valued 경로로 제한된다. 컬렉션의 행 다중성을 집계의 명시적 의미로 연결한다.

## 계약

- 집계 필드 경로는 정규화한 Schema IR의 finite forward/reverse FK·OneToOne·ManyToMany 혼합 경로다.
  집계 전용 생성·검증 경계가 COUNT/MIN/MAX/SUM/AVG를 만든다. 일반 projection·group key·scalar/CASE operand와
  UPDATE assignment의 single-valued 제한은 유지한다. JSON 집계와 일반 collection 계산식은 별도 계약이다.
- COUNT는 NULL을 제외하며 빈 관계는 0이다. MIN/MAX/SUM/AVG는 기존 kind·precision·Optional decoder를 유지하고,
  빈 관계·모두 NULL은 NULL이다. DISTINCT는 COUNT/SUM/AVG에 명시하며 여러 JOIN의 행 곱을 자동 제거하지 않는다.
  같은 값의 서로 다른 행과 같은 행의 중복 연결을 구분한다.
- 원본 Filter가 만든 관계 행에 대해 집계한다. 연속 Filter가 독립 JOIN을 만든 경우 같은 경로의 가장 최근
  materialized collection JOIN을 집계에서 재사용한다. 없는 경로는 outer JOIN으로 추가한다. 부정된 원본 collection
  조건의 EXISTS와 집계 FILTER 안의 행 조건·NULL 보호는 서로 다른 문맥이다. 집계 조건만으로 원본 그룹을 제거하지 않는다.
- Typed/dynamic과 생성 facade는 같은 AST와 compiler를 사용한다. 결과 표현식의 identity와 selected-result scope를
  보존하면서 compiler가 집계용 JOIN을 해석한다. 원본/관련 leaf metadata·모델 key·경로의 상충과 전체 자원 예산을 검사한다.
- 기존 source slice/eager/lock·비키 ordering 제한과 명시적 결과 stage를 유지한다. 그룹의 Distinct는 입력 JOIN 행을
  미리 줄이지 않으며 filtered/related 전역 aggregate의 distinct source는 계속 거부한다. 원본 filter 뒤 grouping과
  선택 결과에 대한 HAVING을 구분하며, 일반 named annotation의 호출 순서 전체를 지원한 것으로 취급하지 않는다.
- 새 HTML/API의 라벨 사용 현황은 ViewLabel과 ViewTicket을 모두 요구한다. Category·그룹 총수·페이지는 하나의 읽기
  snapshot에 속한다. 모든 지표에서 현재 Category 밖의 티켓 연결을 제외하되 0건 라벨은 보존한다.
  Label·Ticket·TicketLabel·ServiceReport·audit를 변경하지 않는다. 취소·snapshot/cursor/출력 실패에 부분 결과를 게시하지 않는다.

## 현재 구현과 다음 검증

집계 constructor·typed/dynamic capability·양 compiler에 collection 필드와 FILTER를 연결했다.
공통 JOIN resolver는 원본 Filter의 가장 최근 collection occurrence를 해석하면서 선택 표현식 identity를 유지한다.
별도 두 앱의 생성 소비자와 30개 독립 Django 관찰, 8개 native invariant와 모델/decoder compile-negative를 작성했다.
Helpdesk는 Label root·지표별 Category 조건·두 view 권한·snapshot·HAVING/Page·HTML/API·OpenAPI client를 연결했다.
공통 읽기 snapshot 소유권 검사로 기존 요약/상태 조회의 실패 경계를 유지하고 late/unfinished callback도 검증한다.
브라우저는 기존 두 읽기 화면에 라벨 사용 현황과 Label/연결 readback을 추가했다.

구현 묶음은 push했으며 첫 원격 실행의 관측기 이름 충돌과 Helpdesk 테스트 준비 경로를 수정했다.
후속 원격 소비자의 실패로 드러난 Decimal MIN/MAX 입력 검증을 보완하고 세션 생성자와 Distinct 기대값을 바로잡았다.
로컬에서는 실패 재현 범위인 SQLite 독립 관측과 Helpdesk 라벨 사용 현황을 확인했다. 전체 양 DB·race·브라우저 검증은 미완료다.
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)의 GDJ-0125 절을 따른다.
다음 단계는 수정 push와 자동 전체 CI이며 완료 조건은 같은 source의 실행 결과를 확인한 뒤 표시한다.

## 완료 조건과 검증 위치

- [ ] AST의 집계 문맥과 source 권한·공통 JOIN 재사용·양 compiler를 연결하고 행 scalar 경계를 유지한다.
- [ ] Typed/dynamic·생성 facade·cross-app 소비자와 kind/metadata/자원·NULL·중복·cache/session/실패 경로를 검증한다.
- [ ] 고정 Django의 독립 양 DB 관찰로 reverse/M2M·조건·여러 JOIN·연속 필터의 결과를 대조한다.
- [ ] Helpdesk HTML/API·두 조회 권한·외부 Category 부정 대조·snapshot·독립 client·브라우저를 연결한다.
- [ ] 제품 코드·생성기/소비자·테스트 묶음을 포맷·최소 compile 후 push하고 자동 전체 CI에서 실행한다.
- [ ] 같은 source의 필수 실행·원본 증거를 대조하고 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 환경별 결과를 기록한다.

이번 작업의 완료는 collection 계산식·일반 named annotation·Decimal/Duration 산술·function/subquery/window나
나머지 카탈로그 전체의 완료를 뜻하지 않는다. 해당 범위는 전체 제품 목표로 유지한다.
