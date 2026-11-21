# 현재 상태

2026-11-22 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

현재 작업은 I1 사용자 정의 계정이다. PR #8의 변경은 통합 PR #1에 합쳐졌고 I1의 전체 수용은 남아 있다. [통합 PR #1](https://github.com/progresshans/godj/pull/1) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)

## 재개 지점

1. `026a9627`에서 고정 Django 계정 관측 9종을 SQLite·PostgreSQL에서 각각 실행해 원본 18개를 대조했다. 같은 소스에서 기본 User·사용자 정의 Member의 브라우저 최초 기동·재시작 8건과 계정 구성 7종의 첫 계정·Admin·JSON 관리가 통과했다. [Django·생성 앱·브라우저 검증](TEST_EVIDENCE.md#026a9627-고정-django-계정의-양-db-검증)
2. 같은 소스에서 비밀번호 재설정, 프로필 편집, 채운 DB의 계정·권한 전환과 응답 유실 후 재접속을 양 DB의 세 모드에서 확인했다. 각 모드에서 생성 앱 검사 10,005개가 통과했다. [생성 앱과 전환 검증](TEST_EVIDENCE.md#026a9627-계정-모델-전환과-생성-앱)
3. I1-03에 남은 구현은 그룹 권한을 편집할 때 중간 행 자체가 선택 계정·그룹·권한인 경우다. 일반 Catalog는 이 행의 생성 정책을 받을 수 없어 쓰기 구성을 거부한다. [I1 요구표](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자) · [Catalog 쓰기 범위](../adr/0076-credential-snapshots-and-session-binding.md#선택-계정의-typed-grouppermission-관리)
4. `026a9627`의 PostgreSQL 계정 저장소는 세 모드에서 통과했다. Linux arm64 race에서는 SQLite 패키지의 시간 제한으로 필수 검사 19개가 남았다. `f8379412`에서 이 패키지와 나머지 플랫폼을 확인한다. I1-01~08 수용 뒤 M1 스키마·데이터 마이그레이션으로 넘어간다. [이전 실패](TEST_EVIDENCE.md#026a9627-linux-arm64-race-sqlite-패키지-시간-제한) · [수정 후 확인한 범위](TEST_EVIDENCE.md#f8379412-sqlite-시간-제한-수정-후-원격-검증) · [CI](https://github.com/progresshans/godj/actions/runs/44941172969)
