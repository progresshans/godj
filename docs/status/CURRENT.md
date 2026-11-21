# 현재 상태

2026-11-21 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. 생성 삭제자 호출을 고친 뒤 SQLite normal·PostgreSQL race에서 인증 중 변경 6종이 통과했다. 두 환경에서 발견한 약한 비밀번호 진단 검사도 수정했다. 후속 커밋의 전체 원격 결과를 대조한다. [두 작업의 원본과 후속 수정](TEST_EVIDENCE.md#3a31d85e-인증-검사와-후속-수정)
2. 새 starter의 기본 User는 SQLite·PostgreSQL에서 재시작과 삭제까지 통과했다. Member는 양 DB에서 필수 직접 권한의 거부와 저장 상태 보존을 확인했다. 같은 폼에서 권한을 검색해 저장한 뒤 재시작과 삭제까지 대조한다. [브라우저 원본과 수정 근거](TEST_EVIDENCE.md#ddf2b250-member-프로필-거부) · [구성 방법](../DEVELOPER_EXPERIENCE.md#선택-계정-구성)
3. 채운 DB의 계정 전환·권한과 세션 격리·복구를 포함해 I1-01~08을 같은 제품 커밋의 환경별 증거와 대조한다. I1 수용 뒤 기본 순서는 M1의 schema/data migration이다. [실행 계획](../EXECUTION_PLAN.md#우선순위와-기본-실행-순서)
