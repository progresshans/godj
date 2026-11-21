# 현재 상태

2026-11-22 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. `f3474ab1`에서 기본 User·사용자 정의 Member의 새 프로젝트가 SQLite·PostgreSQL 최초 기동·재시작 8건을 통과했다. 채운 DB의 계정 모델 전환과 프로필 파일·관계 편집도 양 DB의 세 실행 모드에서 확인했다. [실행 근거](TEST_EVIDENCE.md#f3474ab1-익명-로그인과-비밀번호-재설정의-커밋-순서) · [모델 전환 절차](../DEVELOPER_EXPERIENCE.md#데이터가-있는-선택-계정-모델-전환)
2. 익명 로그인과 비밀번호 재설정은 어느 쪽이 먼저 커밋되어도 양 DB에서 반환값과 저장 상태가 일치했다. PostgreSQL race에서는 이 경합을 통과한 뒤 삭제 검사 도중 패키지의 18분 제한에 걸렸다. 담당 파티션의 시간을 조정한 후속 CI에서 필수 검사와 패키지 종료를 대조한다. [실패 위치와 수정 근거](TEST_EVIDENCE.md#f3474ab1-익명-로그인과-비밀번호-재설정의-커밋-순서) · [통합 PR의 현재 검사](https://github.com/progresshans/godj/pull/1/checks)
3. 공유 중간 테이블을 통한 계정·카탈로그 전환과 나머지 플랫폼·독립 기준까지 같은 소스에서 확인한다. I1-01~08을 수용하면 M1 스키마·데이터 마이그레이션으로 넘어간다. [요구별 확인](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자) · [실행 계획](../EXECUTION_PLAN.md#우선순위와-기본-실행-순서)
