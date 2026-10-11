# 현재 상태

- 갱신: 2026-10-11
- 현재: [GDJ-0122 계산식 조회와 우선순위 미리보기](../../work/0122-computed-results-and-priority-preview.md) 제품·생성물 연결, 원격 통합 검증 준비
- 최근 전체 통합: source `02f350d3598f41dd34b0ddcb1fbf82474139aea4`의 [CI 37706678533](https://github.com/progresshans/godj/actions/runs/37706678533), GDJ-0121 숫자 집계 포함
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

같은 scalar AST를 조회 결과·조건·정렬·집계 operand에 연결하고, CASE를 typed/dynamic 경로에 추가했다.
Helpdesk의 우선순위 요약에 변경 예정값을 보여주되 원본 priority와 집계·권한·읽기 snapshot을 유지한다.
독립 Django 관찰과 생성물 준비를 마쳤으며 제품 코드·생성 소비자·실패 경로의 통합 검증은 남아 있다.

선행 숫자 집계 source는 83개 Hosted job, 필수 실행·세 모드·OS/arch·same-run capture/source·최종 집계까지
확인했다. 그 결과를 후속 계산식 코드의 PASS로 사용하지 않는다. 현재 외부 입력이 필요한 blocker는 없다.

## 다음 행동

포맷·필요한 최소 compile 확인 뒤 기존 Draft PR에 push한다. 영향 검사·generated drift·실제 양 DB/실패·독립 SDK와
race/process/platform은 같은 source의 전체 원격 CI에서 확인한다. 변경한 브라우저 probe의 실제 실행도 남아 있다.
이어 annotation의 집계 단계·관계·subquery/window, codec/storage·custom user/auth/mail 등 남은 카탈로그를
의존성에 따라 진행한다. 전체 범위는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)를 유지한다.

로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 공유 cache·생성 소비자의 `-trimpath`와
[검증 문서](../TESTING.md)의 실행 소유권을 유지한다.
