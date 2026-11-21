# 현재 상태

2026-11-21 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. 새 프로젝트에서 기본 User와 사용자 정의 Member가 SQLite·PostgreSQL의 최초 기동과 재시작 8건을 통과했다. Chromium, 별도 HTTP 클라이언트와 SQL로 권한 변경, 프로필 저장, 비밀번호 재설정, 세션 폐기와 삭제를 확인했다. [실행 근거](TEST_EVIDENCE.md#a32041e1-새-프로젝트-계정-수명주기) · [구성 방법](../DEVELOPER_EXPERIENCE.md#선택-계정-구성)
2. 채운 DB에서 계정 전환과 복구를 검증 중이다. 이행 전후의 권한·세션 격리, 읽기 소유권 거부, 커밋 미확정 시 반환값과 실제 저장 결과를 양 DB·race 원본에서 대조한다. [전환 요구](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자) · [쓰기 결과](TEST_EVIDENCE.md#역사-runtime-쓰기-결과의-확정) · [읽기 소유권](TEST_EVIDENCE.md#역사-runtime의-읽기-소유권)
3. 같은 제품 커밋에서 인증 진단과 나머지 I1-01~08의 필수 실행·독립 기준까지 대조한 뒤 I1을 수용한다. 다음 기본 순서는 M1의 schema/data migration이다. [원격 CI](https://github.com/progresshans/godj/actions/runs/44911895052) · [실행 계획](../EXECUTION_PLAN.md#우선순위와-기본-실행-순서)
