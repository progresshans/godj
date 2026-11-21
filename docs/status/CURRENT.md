# 현재 상태

2026-11-21 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. 새 프로젝트에서 기본 User와 사용자 정의 Member가 SQLite·PostgreSQL의 최초 기동과 재시작 8건을 통과했다. Chromium, 별도 HTTP 클라이언트와 SQL로 권한 변경, 프로필 저장, 비밀번호 재설정, 세션 폐기와 삭제를 확인했다. [실행 근거](TEST_EVIDENCE.md#a32041e1-새-프로젝트-계정-수명주기) · [구성 방법](../DEVELOPER_EXPERIENCE.md#선택-계정-구성)
2. 채운 DB의 선택 모델 전환은 SQLite·PostgreSQL 생성 앱에서 커밋 응답 유실과 복구까지 확인했다. 후속 수정에서 재조회가 이전 계정 역할의 불일치를 거부하고, 조회 도중에도 원래 대응표를 사용하는지 검증한다. [전환 원본](TEST_EVIDENCE.md#a32041e1-채운-db의-선택-계정-전환) · [모델 전환 절차](../DEVELOPER_EXPERIENCE.md#데이터가-있는-선택-계정-모델-전환)
3. 같은 제품 커밋에서 역사 이행·인증 진단과 나머지 I1-01~08의 필수 실행·독립 기준까지 대조한 뒤 I1을 수용한다. 다음 기본 순서는 M1의 schema/data migration이다. [통합 PR의 현재 검사](https://github.com/progresshans/godj/pull/1/checks) · [실행 계획](../EXECUTION_PLAN.md#우선순위와-기본-실행-순서)
