---
id: GDJ-0122
status: active
updated: 2026-10-11
baseline_commit: "02f350d3598f41dd34b0ddcb1fbf82474139aea4"
integration_owner: "root"
---

# 계산식 조회와 우선순위 미리보기

기준 source의 scalar 값 AST는 갱신에만 쓰이고 조회/집계 operand는 원본 필드에 묶여 있었다. 원본 필드 metadata를
계산 결과처럼 바꾸지 않고, 같은 값 AST를 projection·predicate·ordering·group key·aggregate operand로 연결한다.
Helpdesk의 기존 요약 화면/API에서 우선순위를 한 번 올린 뒤의 값을 같은 읽기 snapshot으로 미리 본다.
선행 숫자 집계 source의 Hosted 전체 통합은 완료했다. 그 결과를 이 변경의 PASS로 쓰지 않는다.

## 구현 경계

- Typed/dynamic 경로가 하나의 immutable 값·Boolean AST를 사용한다. Scalar 결과 종류·NULL과 원본
  필드의 schema 종류·precision을 분리하며 다른 source, 잘못된 값, node/depth/SQL/parameter 예산을 검증한다.
- 기존 int64/float64 산술을 읽기·숫자 집계에 연결한다. Nullable 결과는 Optional이며 정수 AVG는 float64다.
  CASE는 순서가 있는 같은 행 조건과 explicit default를 갖고 조회와 갱신에서 같은 tree를 쓴다.
  중간 정수 overflow를 허용하지 않고 native zero-division/Float NaN·계획 시 constant 평가 차이를 드러낸다.
- 계산 결과 조건의 NOT는 SQL 3값 의미를 따른다. 기존 field lookup의 NULL 보정을 임의로 복사하지 않는다.
  Projection/Distinct·slice·그룹/HAVING·빈 source·source cache·context/session/decoder 실패를 함께 검증한다.
- Helpdesk 요약은 현재 우선순위로 그룹을 유지하며 CASE로 계산한 `raise_to_priority`·표시 이름·변경 여부를
  HTML/API/독립 생성 client에 연결한다. NULL은 Normal, Low/Normal은 한 단계 상승, Urgent/legacy 값은 보존한다.
  같은 Category/인가/페이지/읽기 snapshot과 전체 오류 응답·원 row/audit 보존을 유지한다.

일반 named annotation, aggregate 뒤 계산과 추가 query stage, reverse/collection operand, 함수/subquery/window,
Decimal·Duration 산술/계산식 SUM/AVG는 남은 카탈로그 요구다. 한 기반 작업을 전체 목표의 완료로 바꾸지 않는다.
장기 의미는 ADR-0090/0091에, 독립 관찰과 환경별 실제 실행은 TEST_EVIDENCE에 기록한다.

## 진행

- [x] 고정 Django의 annotation/산술 77개와 CASE/읽기 20개를 실제 양 DB에서 독립 반복 관찰
- [x] 공통 scalar renderer·결과/조건 AST·typed/dynamic·생성 facade 구현
- [x] 생성 소비자의 독립 결과 대조·compile-negative·cache/source/실패 테스트와 필수 CI 목록 작성
- [x] Helpdesk 요약·OpenAPI/독립 client와 브라우저 probe 연결, 고정 생성물 갱신
- [ ] 위 제품 경로의 실제 양 DB·실패·읽기 무결성 검증과 브라우저 실행
- [ ] 영향 검증·생성 drift·관련 race/process 및 같은 source의 원격 전체 통합 검증

제품 영향 테스트의 첫 원격 실행에서 실패를 발견했다. 로컬은 포맷·필요한 compile과 실패 재현에 사용하며,
전체 검증은 기존 Draft PR의 CI가 소유한다. 이전 source의 전체 통합이나 독립 Django 관찰을 현재 제품 PASS로 쓰지 않는다.
최소 compile의 template Boolean 이름과 새 외부 소비자의 backend capability 오류를 수정했다.
첫 원격 실행에서 발견한 golden/SQL 기대값 불일치를 보완했고, 기존 password-change 참조의 session 읽기까지
시계를 고정해 실제 날짜에 따른 만료를 제거했다. 정상 예상값과 부정 대조는 유지하며 과거/미래 호스트 시계도 검사한다.
생성 소비자의 borrowed session 생성자도 바로잡았다. 실제 실패 root의 제한된 SQLite 재현은 통과했으며
세션 수명 검사와 잘못된 타입의 컴파일 거부는 유지했다. PostgreSQL/race의 검증은 남아 있다.
선택한 native 결과 종류가 원 모델에 없는 경우를 포함한 scalar codec/typed NULL 검사를 추가했다.
브라우저는 Portable normal integration에 고정 도구·실제 로그인·필수 결과·native readback·artifact 경로를 연결했다.
이 추가 검사의 실제 실행은 후속 전체 CI가 소유한다.
