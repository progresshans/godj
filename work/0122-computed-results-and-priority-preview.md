---
id: GDJ-0122
status: complete
updated: 2026-10-12
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
- [x] 위 제품 경로의 실제 양 DB·실패·읽기 무결성 검증과 브라우저 실행
- [x] 영향 검증·생성 drift·관련 race/process 및 같은 source의 원격 전체 통합 검증

원격 피드백에서 발견한 생성물/SQL 기대값·borrowed session 연결·참조 시계 의존성을 보완했다.
Typed/dynamic NULL 대입은 공통 assignment 생성에서 target kind를 가진 새 scalar로 정규화하며,
공유 원 표현식과 명시적 타입 제약을 유지한다. 누적 race 소비자의 전체 context 예산과 개별 동시성·취소·정리
예산을 분리했고 모든 커밋/롤백·감사 기록 검사는 보존했다. 실제 실패·재현·수정 과정은 TEST_EVIDENCE에 기록했다.

Source `0ea21fa4`에서 제품·생성 소비자의 양 DB·세 모드, 독립 SDK·브라우저·전체 플랫폼을 검증했다.
같은 source의 [CI 38110947560](https://github.com/progresshans/godj/actions/runs/38110947560)에서
모든 필수 job·실행과 capture 소비·최종 집계를 대조해 전체 통합을 완료했다.
각 실행의 source·필수 경로·실패 대조와 전체 검증 receipt는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
