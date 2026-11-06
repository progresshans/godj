# 현재 상태

- 갱신: 2026-11-07
- 현재: [전체 범위 실행 계획](../EXECUTION_PLAN.md)의
  [I1 / GDJ-0138 사용자 정의 계정](../../work/0138-custom-account-lifecycle.md)을 구현·검증 중이다.
  [PR #8](https://github.com/progresshans/godj/pull/8)과 후속 변경을
  [통합 PR #1](https://github.com/progresshans/godj/pull/1)에 모은다. 구현 통합과 제품 수용은 구분한다.
- 최근 제품 완료 범위: [C1 / GDJ-0137 Project/Core](../../work/0137-project-core-startup.md).
  C1과 선행 F1/A1/Q1/G-ORM의 같은 source 수용 근거는 [TEST_EVIDENCE](TEST_EVIDENCE.md)를 따른다.

## 다음

1. I1의 현재 source에서 전체 원격 CI와 필수 원본을 대조한다. 선택 계정·프로필·catalog·collection/through 관리,
   공개 첫 계정 명령, 명시적 모델 전환과 native 필수 FK graph를 포함한다. 기본/custom 변형·독립 SDK·실제 browser·
   별도 process·migration/restart, 양 DB·race·CGO-disabled·전체 platform/cold-build의 요구를 유지한다.
2. 역사 `systemstate`의 최초 선택 domain 이행·원문 보존·퇴역 runtime 차단·receipt/복구를 양 DB에서 확인한다.
   Article의 명시적 이행 소비자와 새 독립 생성 앱의 별도 operator process→custom 모델 전환→host schema 정리→
   재접속/현재 인가·역사 조회를 확인한다. 소비자 작성과 실제 환경별 실행은 TEST_EVIDENCE에서 구분한다.
3. 남은 고정 Directory/Manager 및 내장 Admin 소비자를 선택 모델 owner로 정리한다.
   고정 JSON 제거와 선택 Admin의 비밀번호 상태·LastLogin 표시·생성/수정 Form 복합 진단·권한별 화면·기본 비밀번호 정책,
   명시적 login 입력 정책·Unicode 생성/저장 한도 편집과 독립 기준 소비자 이관을 원격에서 확인하고,
   기존 독립 기준·실패·인가·데이터 무결성 검증이 계속 어디서 실행되는지 대조한다.
4. [I1-01~08 수용 요구](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)의 제품·생성기·실제 소비자와
   같은 source의 검증 근거를 모두 연결한 뒤 완료를 판단한다. 세부 미완료 범위는 해당 work에 유지한다.

## 검증 경계

C1 source `b19aeedd`의 필수 원격 실행과 C1-01~08 수용 대조를 완료했다. 이 결과를 새 I1 코드의 PASS로 사용하지 않는다.
I1의 현재 실패·수정·부분 통과·원본 대조와 환경별 미확인은 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록한다.
현재 외부 입력이 필요한 blocker는 없다. 나머지 ORM 및 전체 카탈로그의 요구는 실행 계획의 담당 묶음에 유지한다.
