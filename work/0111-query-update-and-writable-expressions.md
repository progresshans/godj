---
id: GDJ-0111
status: active
updated: 2026-10-06
baseline_commit: "c1d1d3f67c7383af2df1b0bebdd5213ff8616957"
integration_owner: "root"
---

# QuerySet 갱신과 현재 행을 사용하는 쓰기 표현식

## 결과와 범위

현재 QuerySet 조건에 맞는 행을 하나의 native UPDATE로 갱신한다. 상수와 같은 행의 필드 참조·숫자 연산을
DB 독립 scalar AST로 표현하고 typed/dynamic 입력을 같은 metadata·compiler·transaction 계약에 연결한다.
각 행을 애플리케이션에 읽어 값을 계산한 다음 단건 저장을 반복하는 경로를 QuerySet update로 제공하지 않는다.

실제 소비자는 Helpdesk의 선택 티켓 우선순위 상향이다. 현재 Category·선택 집합·권한과 라벨 무결성을 확인하고,
Low를 Normal로, Normal을 Urgent로 바꾸며 Urgent는 그대로 둔다. 미설정 값은 Normal로 명시적으로 설정하고,
선언된 선택지 밖의 기존 값은 임의로 정규화하지 않는다. 기존 선택 작업·API/독립 client에서 실제 변경·audit·
완성된 응답을 하나의 transaction으로 확인한다. 일반 ORM의 NULL/연산 의미와 이 업무 정책은 구별한다.

GDJ-0109/0110의 수정 source `19dd8178ae6f4a3d853ab6879543efa333cffb8a`는
[Hosted full 37335450948](https://github.com/progresshans/godj/actions/runs/37335450948)에서 PostgreSQL race/core 시간 제한으로
통합을 완료하지 못했다. 필수 검사를 유지한 CI 분할 source로 다시 확인한다.
분할 후 CI 필수 목록 검사의 옛 배열 참조도 수정했다. 새 통합은 source `faa5a3396ad3a7e9e2807e2fcab26ec0fe037484`의
[Hosted full 37348366499](https://github.com/progresshans/godj/actions/runs/37348366499)에서 PostgreSQL 분할 job은 통과했지만
macOS Intel의 명령 fixture 준비 실패가 확인됐다. 별도 준비 예산·단계 진단 보완을 새 업무 source와 통합해 검증한다.
그 실행은 선행 source를 검증하며 이 작업의 새 구현을 검증한 것으로 사용하지 않는다. 별도 작업 사본에서 구현한다.

공통 기반과 업무 소비자·fixture 보완을 통합한 source `0f9cdb8386c9b9e4ec4599c698cfd311c80dd4a6`의
[Hosted full 37359348832](https://github.com/progresshans/godj/actions/runs/37359348832)에서 오래된 외부 compile
fixture 두 가정과 PostgreSQL race 생성 소비자 package의 18분 제한이 확인됐다. 현재 API의 positive/negative
fixture와 해당 coordinate 예산을 보정하고 영향 검증했다. 전체 owner·source/capture·최종 집계는 새 source에서 확인한다.
보정 source `a143ad3ac44a499ce852a815b2f11a86b1e4bb24`의
[Hosted full 37363189671](https://github.com/progresshans/godj/actions/runs/37363189671)은 runner를 배정받지 못했다.
제품 차이 없이 문서만 갱신한 `720c9be6211f00a146a39d00957a81ce69ed294f`의
[Hosted full 37365281161](https://github.com/progresshans/godj/actions/runs/37365281161)을 진행 중이다.
Runner 배정/종료 문제와 실제 assertion을 구분하며, 필요한 job의 완료와 source/capture 결합은
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다. 후속 GDJ-0112는 이 source에 포함되지 않는다.

## 구현과 검증

- [x] 고정 Django의 query update·field expression·NULL/수치·실패·cache·부모/동시성 의미를 독립 관찰
- [x] 불변 scalar/assignment AST와 원 query 범위, field/타입 소유권·자원 한도·명시적인 미지원 오류
- [x] SQLite/PostgreSQL native UPDATE·조건 재평가·원래 행의 값과 atomic/savepoint·취소/불확실한 결과
- [x] generic ORM·typed 생성 facade·동적 입력, 잘못된 model/field/타입 거부와 독립 생성 소비자
- [x] Helpdesk 우선순위 명령의 현재 권한·범위·무변경·원자 audit·Admin/API/독립 client
- [ ] 완성한 변경 묶음의 영향 검사·정식 기준 대조·생성 drift·필요한 통합과 현행 의미/증거 기록

## 현재 경계와 다음 구현

고정 Django의 외부 결과와 실제 DB 관찰을 사용해 assignment 간 원래 값 참조, nullable 연산, 수치 범위,
빈/무변경 결과·필터·관계/collection scope, slicing·ordering·eager/lock/cache의 쓰기 의미를 확인한다.
Field reference가 관계 join을 요구하는 경우, primary key·columnless relation·aggregate/subquery expression 등의
지원 경계를 오류와 함께 명시한다. 아직 검증하지 않은 연산을 문자열 SQL로 통과시키지 않는다.

Schema IR의 field/codec 의미가 원본이며 model별 정적 API는 codegen, 공통 입력 준비·수명은 generic ORM,
SQL과 native 수치 동작은 backend capability/compiler가 소유한다. 기존 Boolean 조건 AST와 scalar 값 AST를
구분하되 같은 의미를 typed/dynamic API마다 다시 구현하지 않는다. 입력·출력·query cache의 공유와 변경 주체도 명시한다.

SQL affected count와 업무의 실제 변경 건수를 구별한다. ORM의 native 갱신이 model Save/Clean이나 audit를 자동
실행한다고 주장하지 않으며 consumer가 필요한 검증·audit를 소유한다. 뒤쪽 업무 실패·취소는 확인 가능한 범위에서
전체를 rollback하고 commit/rollback unknown을 성공이나 재시도 가능 상태로 축소하지 않는다.

일반 annotation/group/having·window/subquery/function, 모델 Save/BulkUpdate에 expression을 넣는 추가 표면과
나머지 기능 카탈로그는 이 작업의 성공으로 완료 처리하지 않는다. 공통 scalar AST가 후속 표현식을 확장할 기반이 된다.
공통 scalar/assignment·native UPDATE·typed/dynamic 및 생성 facade의 영향 검증과 독립 대조를 완료했다.
의미는 [ADR-0090](../docs/adr/0090-query-update-and-scalar-expressions.md)에 둔다. Helpdesk 우선순위 명령은
Admin/API/독립 client에 연결했고 업무 양 DB·세 mode와 브라우저를 확인했다. Hosted source 통합을 진행하며,
기준/기반·수직 소비자 완료를 전체 플랫폼 완료로 계산하지 않는다.

[개발 판단 기준](../docs/DEVELOPMENT_CRITERIA.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md),
[검증 전략](../docs/TESTING.md)을 따른다. 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에만 기록한다.
현재 외부 입력이 필요한 blocker는 없다.
