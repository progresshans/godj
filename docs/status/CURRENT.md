# 현재 상태

2026-11-26 기준 가장 최근에 수용한 작업은 C1 Project/Core다. Q1/G-ORM·A1·F1·C1은 각 제품 커밋에서 검증을 마쳤다. [구현 범위](IMPLEMENTATION_MATRIX.md) · [C1 수용 기록](TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)

I1 사용자 정의 계정과 M1 스키마·데이터 마이그레이션이 PR #1 브랜치에 모였다. CPython 3.14.10 기준으로 이전했고 Linux 대표 작업이 전체 Python suite와 311개 의미 비교를 맡는다. 140개 full 검사가 통과한 뒤 Linux race의 준비·배정 시간을 제출 커밋별로 측정했다. [기준 이전 근거](TEST_EVIDENCE.md#python-31410-기준-이전과-full-ci) · [후속 CI 측정](TEST_EVIDENCE.md#pr-1-full-ci-후속-최적화) main 병합은 보류했다. [PR #1 최신 검사](https://github.com/progresshans/godj/pull/1/checks) · [I1 요구와 실제 소비자](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자) · [M1 범위](../EXECUTION_PLAN.md#m1의-첫-구현과-검증)

## 재개 지점

1. `512c17f1`에서 새 프로젝트의 필수 파일 프로필을 검증했다. 공개 첫 계정 명령의 한도 초과 거부와 생성, Admin의 파일 생성·교체, 보고 실패 후 확정 상태 재조회를 SQLite·PostgreSQL에서 확인했다. 기본 모델과 두 사용자 정의 프로젝트의 최초 기동·재시작도 통과했다. [최신 검증](TEST_EVIDENCE.md#512c17f1-새-프로젝트의-필수-계정-파일)
2. 같은 소스에서 일반 Catalog 그룹 권한 쓰기, 생성 앱의 계정 관리·프로필·재설정, 채운 DB의 계정·권한 전환과 재접속을 양 DB·세 모드에서 확인했다. PostgreSQL 계정 저장소와 고정 Django의 양 DB 관측도 확인했다. [계정 수명 회귀](TEST_EVIDENCE.md#512c17f1-계정-수명-회귀)
3. I1의 전체 수용은 열려 있다. M1의 #12~#15는 각 PR의 최종 커밋에서 Linux 전체와 macOS 해당 범위를 차례로 통과했다. 기존 통합 플랫폼 검증은 완료했다. [CI 범위](../TESTING.md#원격-ci-실행-범위) · [#12](https://github.com/progresshans/godj/actions/runs/45165498699) · [#13](https://github.com/progresshans/godj/actions/runs/45204325810) · [#14](https://github.com/progresshans/godj/actions/runs/45235604044) · [#15](https://github.com/progresshans/godj/actions/runs/45253620235)
4. #9~#17을 통합한 `e8555cf8`의 기존 전체 177개 작업이 통과했다. 새 full의 Linux 전수와 Darwin 별도 의무는 같은 후보의 원격 원본으로 수용한다. [기존 전체 CI](https://github.com/progresshans/godj/actions/runs/45674268509) · [새 검증 기준](../TESTING.md#원격-ci-실행-범위)
