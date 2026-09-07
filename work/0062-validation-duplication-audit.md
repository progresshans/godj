---
id: GDJ-0062
status: complete
updated: 2026-09-08
baseline_commit: "6ecf0b628465014ee1b1260454a08ce67713bd1a"
---

# 테스트·검증 지원 코드의 중복 점검

검증 코드 전반에서 같은 실패 조건을 반복 검사하거나 같은 입력·환경을 다시 준비하는 부분을 찾아 줄인다.
테스트 이름과 파일 수보다 위험 검증의 소유권, 상태 격리, 읽기 쉬운 테스트를 유지한다.

## 범위와 방법

- Git 관리 Go·Python 코드를 구조적으로 검색하고 테스트·conformance·검증 도구의 중복 후보를 검토한다.
- 동일 함수, 메시지·이름만 다른 함수, 반복 artifact 목록·입력 로딩·프로젝트/DB 준비를 구분한다.
- 입력·오류·취소·cleanup·mutable state 소유권이 같은 부분만 공통화한다. 다른 DB·플랫폼·실패 단계의 검증은 유지한다.
- 고정 reference/oracle과 제품 runtime/API는 유지한다. actual 생성과 expected 로딩의 경계를 합치지 않는다.
- 제거한 검사의 남은 소유자와 보존한 후보의 이유, 실제 감소량은 TEST_EVIDENCE에 기록한다.

## 진행

- [x] 전체 후보 검색과 의미·위험 검토
- [x] 명확한 중복 검사·준비 코드 통합
- [x] 관련 normal/race/CGO-disabled·reference·부정 회귀
- [x] 최종 소스 통합 검증과 완료 기록

코드 변경을 먼저 묶고 편집 중에는 필요한 compile만 확인한다. 관련 검증은 완성한 묶음에서 실행한다.
전체 플랫폼 검증은 이번 점검을 통합하는 마지막 Hosted 실행이 소유하며 로컬 전체 gate를 중복하지 않는다.
실행 증거는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 한 번 기록한다.

## 다음 행동

고정 소스의 전체 Hosted 검증과 같은 실행의 source-bound PostgreSQL evidence 확인을 완료했다.
추가 활성 작업과 blocker는 없다. 구현·환경별 실행·보존한 위험과 감소량은 TEST_EVIDENCE를 따른다.
