# 현재 상태

2026-11-21 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. 선택 계정의 인증 테스트가 생성된 `AccountsUser` 삭제자를 사용하도록 수정했다. 수정본의 전체 원격 CI와 필수 실행 결과를 확인한다. [이전 실패와 수정 범위](TEST_EVIDENCE.md#ad25f5ed-원격-ci)
2. 새 프로젝트에서 로그인 필드 이름을 바꾸고 필수 프로필을 추가한 계정을 사용한다. 첫 계정 명령·Admin·API·비밀번호 재설정·비활성화·삭제·재시작을 SQLite와 PostgreSQL, 독립 HTTP 클라이언트와 실제 브라우저에서 확인한다. [현재 scaffold의 조립 범위](../DEVELOPER_EXPERIENCE.md#선택-계정-구성)
3. 채운 DB의 계정 전환·권한과 세션 격리·복구를 포함해 I1-01~08을 같은 제품 커밋의 환경별 증거와 대조한다. I1 수용 뒤 기본 순서는 M1의 schema/data migration이다. [실행 계획](../EXECUTION_PLAN.md#우선순위와-기본-실행-순서)
