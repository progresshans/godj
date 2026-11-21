# 현재 상태

2026-11-22 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. `c6629558`의 새 프로젝트에서 기본 User와 사용자 정의 Member가 SQLite·PostgreSQL의 최초 기동과 재시작 8건을 통과했다. Chromium, 별도 HTTP 클라이언트와 SQL로 권한 변경, 프로필 저장, 비밀번호 재설정, 세션 폐기와 삭제를 확인했다. [실행 근거](TEST_EVIDENCE.md#c6629558-전환-기록-재조회-검증) · [구성 방법](../DEVELOPER_EXPERIENCE.md#선택-계정-구성)
2. 같은 커밋의 채운 DB 전환은 양 DB의 normal·race·CGO 비활성에서 각각 141개를 통과했다. Linux amd64 SQLite에서는 익명 세션 로그인과 비밀번호 재설정의 경합 검사 하나가 실패했다. 익명 세션 데이터·만료 시각의 기대값을 바로잡고 로그인과 재설정이 각각 먼저 커밋되는 순서를 추가했다. 후속 커밋의 양 DB 결과를 확인한다. [전환 및 경합 원본](TEST_EVIDENCE.md#c6629558-전환-기록-재조회-검증) · [모델 전환 절차](../DEVELOPER_EXPERIENCE.md#데이터가-있는-선택-계정-모델-전환)
3. 정책이 다른 생성 코드의 혼용 거부, 프로필 관계·파일, 나머지 플랫폼의 필수 실행과 독립 기준까지 같은 소스에서 대조한다. I1-01~08의 수용 조건을 충족하면 M1 스키마·데이터 마이그레이션으로 넘어간다. [요구별 확인](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자) · [통합 PR의 현재 검사](https://github.com/progresshans/godj/pull/1/checks) · [실행 계획](../EXECUTION_PLAN.md#우선순위와-기본-실행-순서)
