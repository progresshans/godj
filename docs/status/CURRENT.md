# 현재 상태

2026-11-21 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. 생성 삭제자 호출을 고친 뒤 SQLite normal·PostgreSQL race에서 인증 중 변경 6종이 통과했다. 두 환경에서 발견한 약한 비밀번호 진단 검사도 수정했다. 후속 커밋의 전체 원격 결과를 대조한다. [두 작업의 원본과 후속 수정](TEST_EVIDENCE.md#3a31d85e-인증-검사와-후속-수정)
2. 새 starter의 기본 User가 SQLite·PostgreSQL에서 재시작과 삭제까지 통과했다. 두 DB의 Member가 멈춘 Admin 프로필 제출에는 필수 직접 권한의 거부·저장을 구분하는 검사를 추가했다. 후속 원격 실행에서 Member의 재시작과 삭제까지 대조한다. [브라우저 원본과 수정 근거](TEST_EVIDENCE.md#99eca524-새-프로젝트-브라우저) · [구성 방법](../DEVELOPER_EXPERIENCE.md#선택-계정-구성)
3. 채운 DB의 계정 전환·권한과 세션 격리·복구를 포함해 I1-01~08을 같은 제품 커밋의 환경별 증거와 대조한다. I1 수용 뒤 기본 순서는 M1의 schema/data migration이다. [실행 계획](../EXECUTION_PLAN.md#우선순위와-기본-실행-순서)
