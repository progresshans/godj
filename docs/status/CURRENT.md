# 현재 상태

- 갱신: 2026-10-28
- 현재: [전체 범위 실행 계획](../EXECUTION_PLAN.md)의
  [I1 / GDJ-0138 사용자 정의 계정](../../work/0138-custom-account-lifecycle.md)을 구현 중이다.
  [PR #8](https://github.com/progresshans/godj/pull/8)의 구현과 후속 관리 JSON을
  [통합 PR #1](https://github.com/progresshans/godj/pull/1) 작업 브랜치에 모아 검증한다.
  후속 변경도 완성된 코드 묶음 단위로 #1에 계속 통합하며, 구현 통합과 제품 수용은 구분한다.
- 최근 제품 완료 범위: [C1 / GDJ-0137 Project/Core](../../work/0137-project-core-startup.md).
  같은 source의 전체 CI·원본 대조와 선행 F1/A1/Q1/G-ORM 근거는 [TEST_EVIDENCE](TEST_EVIDENCE.md)를 따른다.

## 다음

1. 읽기 전용 생성 model·선택 계정 OpenAPI·Helpdesk 전환과 FK/OneToOne·파일 프로필 편집/Form 생성·초기 계정 CLI 파일 입력·생성 관계 수명을 #1에서 전체 원격 검증한다.
   기본/custom 일곱 변형·독립 SDK·실제 browser·외부 operator·migration/restart와 현재 인가/감사의 필수 실행을 대조한다.
   확인된 실패의 한정 재현·수정과 실행 원본·판정은 TEST_EVIDENCE에 둔다.
2. 추가 M2M 프로필의 생성·편집·조회·Form/Admin/API와 collection/lifecycle projection, 공개 첫 계정 명령의 collection 입력/전송을 전체 원격 검증한다.
3. 준비된 두 선택 모델의 mapping과 새 대상 계정 생성·실제 key 대응표 저장, source 퇴역·session 폐기·domain/receipt와
   이력 upgrade·복구/restart, 일반 host FK/OneToOne/업무 through 재매핑과 실제 schema expansion/contraction을 원격 검증한다.
   필요한 non-PK RemoveField 기반도 함께 확인한다. Catalog 관계 mapping·역사 systemstate 이행과 남은 고정 Directory/Manager 소비자,
   선택 identity를 through로 자동 생성하는 조립과 전환 대상의 through 수명을 완성한다.

## 검증 경계

C1 source `b19aeedd`의 필수 원격 실행과 C1-01~08 수용 대조를 완료했다.
C1과 선행 변경은 [통합 PR #1](https://github.com/progresshans/godj/pull/1)에 모아 두며,
I1의 새 제품 코드·환경별 검증은 이 결과에 포함하지 않는다. 실행 상세는 TEST_EVIDENCE에 둔다.
현재 외부 입력이 필요한 blocker는 없다.
나머지 ORM 및 전체 카탈로그의 잔여 요구는 실행 계획의 담당 묶음에 유지한다.
