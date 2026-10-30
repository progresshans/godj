# 현재 상태

- 갱신: 2026-10-30
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
   Catalog 참조 fixture와 외부 compile의 확인된 실패를 수정했다. macOS의 누적 실행과 단일 실행 시간초과는
   조정·진단 후 원격에서 다시 대조한다. 한정 재현·수정과 실행 원본·판정은 TEST_EVIDENCE에 둔다.
2. 추가 M2M 프로필의 생성·편집·조회·Form/Admin/API와 collection/lifecycle projection, 공개 첫 계정 명령의 collection 입력/전송을 전체 원격 검증한다.
3. 준비된 두 선택 모델의 mapping과 새 대상 계정 생성·실제 key 대응표 저장, source 퇴역·session 폐기·domain/receipt와
   이력 upgrade·복구/restart, 일반 host의 계정/그룹/권한 FK·OneToOne·업무 through 재매핑과 실제 schema expansion/contraction을 원격 검증한다.
   필요한 non-PK RemoveField 기반과 준비된 권한 catalog의 전체 대응표·권한/소속 보존·이력 대조도 함께 확인한다.
   Typed catalog target 생성·실제 key로 연결하는 계정/host 참조·부분 공유 revision과 manifest v7의 원격 검증을 포함한다.
4. 실제 INSERT key·전체 typed 행을 소유하는 공통 collection 결과와 이를 연결한 공유 물리 through 전환을 원격 검증한다.
   일반/UNIQUE·부분 공유·계정/프로필 생성과 최종 정책 뒤 전체 삽입 행 대조, nullable 행 변조의 rollback을 포함한다.
   일반 생성/편집의 최종 삽입 행 검사와 준비된 대상 전환의 물리 링크 고정도 포함한다. 선행 공유 through 소비자의
   PostgreSQL signed key fixture 실패를 수정했으며 원본 실행·부분 통과·수정 검증 경계는 TEST_EVIDENCE에 둔다.
   추가한 collection 읽기 관측·최종 topology 재검사와 선택 계정의 그룹·직접 권한·추가 프로필 through 생성/편집을 원격 검증한다.
   해싱 전후 인가·복수 생성·삭제·공유 소속의 revision/감사/세션, 생성 정책과 편집 정책의 독립 권한을 포함한다.
   선택 Group/Permission의 through 생성과 실제 key 검증·빈 자체 소속·삭제/교체·종속 계정/session rollback을 원격 검증한다.
   일반·첫 계정 생성의 보류한 부모 FK·범위 밖 해싱·실제 key 결합과 권한/원자성·선언 fingerprint v4를 원격 검증한다.
   전환의 직접 계정/catalog INSERT가 만드는 through의 전체 행·NULL endpoint 관측과 실제 key·rollback을 원격 검증한다.
   전환 계정 생성의 원본 key/revision 참조·실제 FK/OneToOne 결합과 through/host migration 분기 순환 보완을 원격 검증한다.
   Catalog 생성 행의 FK/OneToOne·선택 정책·실제 key 의존 순서와 최종 incoming 검사를 원격 검증한다.
   신규 계정/catalog 공동 할당과 완성 소속 이후 정책·중간 scalar/물리 링크 검사를 원격 검증한다.
   Nullable FK 순환의 단계적 계정/catalog 생성·전체 임시 행 고정·실제 key 완성과 rollback을 원격 검증한다.
   전환 collection의 추가 identity 생성·양쪽 실제 FK 결합·원본 인가·전체 batch/inventory·manifest v8/receipt v3와
   기존 INSERT 재사용·실패/복구/restart를 원격 검증한다. 새 전환 collection의 계정 참조 존재·receipt 종류 처리 수정을 원격 검증한다.
   필수 FK 순환 할당과 역사 systemstate 이행·고정 Directory/Manager 소비자를 완성한다.

## 검증 경계

C1 source `b19aeedd`의 필수 원격 실행과 C1-01~08 수용 대조를 완료했다.
C1과 선행 변경은 [통합 PR #1](https://github.com/progresshans/godj/pull/1)에 모아 두며,
I1의 새 제품 코드·환경별 검증은 이 결과에 포함하지 않는다. 실행 상세는 TEST_EVIDENCE에 둔다.
현재 외부 입력이 필요한 blocker는 없다.
나머지 ORM 및 전체 카탈로그의 잔여 요구는 실행 계획의 담당 묶음에 유지한다.
