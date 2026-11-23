# 현재 상태

2026-11-23 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. `512c17f1`에서 새 프로젝트의 필수 파일 프로필을 검증했다. 공개 첫 계정 명령의 한도 초과 거부와 생성, Admin의 파일 생성·교체, 보고 실패 후 확정 상태 재조회를 SQLite·PostgreSQL에서 확인했다. 기본 모델과 두 사용자 정의 프로젝트의 최초 기동·재시작도 통과했다. [최신 검증](TEST_EVIDENCE.md#512c17f1-새-프로젝트의-필수-계정-파일)
2. 같은 소스에서 일반 Catalog 그룹 권한 쓰기, 생성 앱의 계정 관리·프로필·재설정, 채운 DB의 계정·권한 전환과 재접속을 양 DB·세 모드에서 확인했다. PostgreSQL 계정 저장소와 고정 Django의 양 DB 관측도 확인했다. [계정 수명 회귀](TEST_EVIDENCE.md#512c17f1-계정-수명-회귀)
3. I1의 전체 수용은 열려 있다. PR #9~#12의 기존 CI는 취소했다. PR #12부터 Linux 전체와 macOS 필수 검사를 새 커밋에서 확인하고, 릴리스 후보의 전체 플랫폼 검증을 별도로 기록한다. [CI 범위](../TESTING.md#원격-ci-실행-범위) · [취소와 병목 기록](TEST_EVIDENCE.md#2026-11-23-pr-검증의-macos-대기와-캐시)
4. M1은 채운 열의 필수·고유 전환, 적용 정의의 저장, Go 데이터 함수의 역사 행 접근에 이어 명시적 필드 이름 변경을 구현했다. 두 DB에서 재시작·역적용과 고정 Django의 행·관계 관측을 대조하고 외부 프로젝트 명령을 검증한다. 역방향 SQL, 모델 삭제, 모델 이름 변경은 후속 PR에서 순서대로 검증한다. [PR #13](https://github.com/progresshans/godj/pull/13) · [PR #14](https://github.com/progresshans/godj/pull/14) · [PR #15](https://github.com/progresshans/godj/pull/15). [필드 이름 변경 검증](TEST_EVIDENCE.md#m1-필드-이름-변경과-역사-데이터-대조) · [PR #9](https://github.com/progresshans/godj/pull/9) · [선행 데이터 함수 PR #11](https://github.com/progresshans/godj/pull/11)
