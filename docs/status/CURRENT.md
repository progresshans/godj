# 현재 상태

- 갱신: 2026-10-21
- 현재: [전체 범위 실행 계획](../EXECUTION_PLAN.md)의 B0 복구를 완료하고
  [Q1 / GDJ-0134 named annotation](../../work/0134-named-query-annotations.md)을 진행한다.
- 최근 제품 완료 범위: [GDJ-0133 시간대·지역별 기한 달력](../../work/0133-named-timezones-and-local-deadline-calendar.md).
  제품 source·환경별 검증 및 복구 근거는 [TEST_EVIDENCE](TEST_EVIDENCE.md)를 따른다.

## 다음

1. Named AST·양 compiler·typed/dynamic ORM·생성 facade와 두 생성 소비자, Helpdesk 기한 미리보기의 원격 검증을 완성한다.
2. 고정 Django의 양 DB 77개 관측과 실제 생성 소비자 결과·오류·저장 보존을 같은 source에서 대조한다.
3. G-ORM 전환 기준 충족 후 주 작업을 A1의 모델 중심 Form/Admin/API 조립으로 옮긴다.
   나머지 ORM 기능과 다른 영역은 실행 계획의 담당 묶음과 완료 기준에 따라 계속 수행한다.

## 검증 경계

Helpdesk와 독립 비교 경로까지 작성했다. 원격 CI의 명명 결과 Page decoder·AST 비교 입력과
코드 검토에서 재현한 첫 집계의 숨긴 명명 값 grouping 결함을 수정했다. 제한된 로컬 실패 재현은 전체 검증과 구분한다.
GDJ-0134의 같은 source에 대한 DB·runtime·실제 소비자 검증은 아직 완료하지 않았다. 현재 외부 입력이 필요한 blocker는 없다.
