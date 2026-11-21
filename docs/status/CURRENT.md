# 현재 상태

2026-11-22 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. `d7584767`에서 I1-03의 일반 Catalog 그룹 권한 생성·수정을 양 DB의 세 모드에서 검증했다. 실제 Admin과 관리 JSON, 회원 권한 회수·세션 폐기·롤백·재접속을 포함한다. 같은 소스에서 고정 Django 계정 관측 18개와 기본 User·사용자 정의 Member의 브라우저 최초 기동·재시작 8건도 확인했다. [그룹 권한과 브라우저 검증](TEST_EVIDENCE.md#d7584767-일반-catalog의-그룹-권한-쓰기)
2. 같은 소스에서 첫 계정·프로필·비밀번호 재설정, 채운 DB의 계정·권한 전환과 응답 유실 후 재접속을 양 DB·세 모드로 확인했다. Linux arm64 SQLite race에서 이전에 끝나지 않았던 필수 검사 19개와 패키지 종료도 확인했다. [생성 앱의 전환](TEST_EVIDENCE.md#d7584767-생성-앱의-계정-수명과-전환) · [SQLite 재검증](TEST_EVIDENCE.md#d7584767-linux-arm64-race-sqlite-재검증)
3. 후속 구현에서 새 프로젝트의 첫 계정 명령과 Admin에 파일 업로드 정책·보고 함수를 연결했다. 필수 문서를 받는 새 Member 앱으로 한도 초과 거부, 최초 생성, Admin 파일 생성·수정과 재시작을 양 DB에서 확인한다. 해당 소스의 플랫폼·전체 CI와 I1-01~08을 수용한 뒤 M1 스키마·데이터 마이그레이션으로 넘어간다. [프로젝트 사용법](../DEVELOPER_EXPERIENCE.md#선택-계정-구성) · [I1 요구표](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자) · [통합 PR](https://github.com/progresshans/godj/pull/1)
