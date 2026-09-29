# 현재 상태

- 갱신: 2026-09-29
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36516565253](https://github.com/progresshans/godj/actions/runs/36516565253), source `6d8afda5086ba3fc058376a60dc567bdcd5a05d7`; 62 jobs·8 owners·최종 집계와 새 capture의 Git source 결합 완료
- Source·환경·scope·실행/수정 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset·모델 여러 행 unique·canonical InlineSpec·조회 전용 기존 행을 기반으로 [Admin inline](../../admin/inlines.md)의
권한별 HTML 표시와 부모·자식 합성 저장 callback을 연결했다. 기존 행의 일반 입력과 PK/DELETE·추가 행을 구분하고,
서버 부모/cohort·binding owner·optional child revision과 오류 재표시를 유지한다.

[Helpdesk Admin](../../examples/helpdesk/README.md#admin에서-티켓과-보고서를-함께-편집하기)은 explicit transactional audit를 받아
새 티켓/보고서 생성과 수정·삭제·감사 기록을 하나의 transaction에서 수행한다. 실제 HTML 성공 제어의 재제출, child-only/무변경,
readonly/Add-only/deny overlay, 위조·DB unique·늦은 scope/쓰기/audit 실패·rollback/unknown outcome을 검증한다.
구현/실행 상세와 실패 후 수정은 [TEST_EVIDENCE](TEST_EVIDENCE.md), 설계는 [ADR-0081](../adr/0081-formset-counts-and-row-ownership.md)에 둔다.

위 마지막 Hosted full은 Formset 통합 source이며 이후 Inline/readonly/Admin 변경을 포함하지 않는다. 현재 영향 검증을 전체
platform 성공으로 확대하지 않는다. 이 묶음의 다음 Hosted full을 별도 source로 검증한다.

## 다음 행동

Admin/Formset 통합 source의 Hosted full과 capture 결합을 확인하고, 동적 행 추가/제거 UI와 남은 ModelFormSet/files 범위를
구현한다. 현재 inline은 선언한 추가 행을 서버에서 렌더링하며 arbitrary 자동 persistence를 제공하지 않는다.
Credential/session의 별도 저장 의미와 일반 typed 준비의 책임을 구분하며 custom user model·인증/mail provider와
기능 카탈로그의 남은 범위도 계속 구현한다.

생성 소비자의 `-trimpath`와 기본 공유 cache·병렬 실행을 유지한다. 성공한 영향 검사와 무관한 전체 compile을 덧붙이지 않고,
로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 실행 규칙은 [검증 문서](../TESTING.md)를 따른다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
