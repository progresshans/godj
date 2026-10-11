# 현재 상태

- 갱신: 2026-10-11
- 현재: [GDJ-0122 계산식 조회와 우선순위 미리보기](../../work/0122-computed-results-and-priority-preview.md) 수정 source의 전체 원격 CI 검증
- 최근 전체 통합: source `02f350d3598f41dd34b0ddcb1fbf82474139aea4`의 [CI 37706678533](https://github.com/progresshans/godj/actions/runs/37706678533), GDJ-0121 숫자 집계 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

같은 scalar AST를 조회 결과·조건·정렬·집계 operand에 연결하고, CASE를 typed/dynamic 경로에 추가했다.
Helpdesk의 우선순위 요약에 변경 예정값을 보여주되 원본 priority와 집계·권한·읽기 snapshot을 유지한다.
독립 Django 관찰과 생성물 준비를 마쳤다. 첫 원격 실행의 생성물/SQL 기대값 불일치와 참조 시계의 실제 날짜 의존성을
수정했고 해당 실패를 재현해 확인했다. 생성 소비자의 borrowed session 호출도 고쳐 제한된 SQLite 실패 재현을
통과했다. 제품 코드·생성 소비자·실패 경로의 전체 통합 검증은 남아 있다.
Source `fd5c19f0`에서 원격 브라우저와 core 세 모드를 확인했고, CI 도구 부정 테스트의 원격 실행 누락을 보완했다.
독립 SDK race의 전체 context 만료도 확인해 계측 모드의 누적 실행 예산을 조정했다. 후속 source의 전체 검증은 남아 있다.

선행 숫자 집계 source는 83개 Hosted job, 필수 실행·세 모드·OS/arch·same-run capture/source·최종 집계까지
확인했다. 그 결과를 후속 계산식 코드의 PASS로 사용하지 않는다. 현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

검증 도구 실행 누락을 고친 묶음을 게시하고 전체 CI의 영향 검사·generated drift·실제 양 DB/실패·독립 SDK와 race/process/platform을
같은 source의 실행 로그로 확인한다. 모든 scalar codec의 계산 결과와 native source-kind가 다른 경우를 검증하고,
원격 브라우저의 필수 실행·전체 Ticket 값 보존과 정리 증거는 확인했고, 전체 실행의 source·최종 집계를 이어서 대조한다.
이어 annotation의 집계 단계·관계·subquery/window, codec/storage·custom user/auth/mail 등 남은 카탈로그를
의존성에 따라 진행한다. 전체 범위는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)를 유지한다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
