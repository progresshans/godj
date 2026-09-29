# 현재 상태

- 갱신: 2026-09-29
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36504649962](https://github.com/progresshans/godj/actions/runs/36504649962), source `cb76b165aa3379c8c40aabfa7d12354460c57bf7`; 62 jobs·8 owners·최종 집계와 새 capture의 Git source 결합 완료
- Typed 준비 source `812d0136962ca46251deb22ad80e84077402ea80`의 [Fast](https://github.com/progresshans/godj/actions/runs/36510853114): 실제 Go step·terminal success
- Source·환경·scope·실행/수정 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset core·typed 모델 준비를 [Helpdesk 여러 행 편집](../../examples/helpdesk/README.md#여러-티켓을-함께-편집하기)에 연결했다.
서버 Category의 현재 페이지와 관계 선택지를 다시 읽고, 같은 transaction에서 여러 행 scalar/collection·삭제 정책·감사 기록을
저장한다. 권한/CSRF·cohort/identity·입력 재표시와 늦은 실패/롤백·unknown outcome을 실제 양 DB HTTP로 확인했다.
최종 normal·관련 race·세 부정 대조와 cookie/로그인 복귀 경로의 startup 검사를 완료했다.
[모델 Form 사용법](../../forms/model/README.md), [ADR-0081](../adr/0081-formset-counts-and-row-ownership.md)이 모델/제품 책임을 구분한다.

`cb76b165`의 전체 성공에는 Formset이 없고 `812d0136`의 Fast에는 후속 Helpdesk 화면이 없다.
현재 코드의 전체 platform 완료를 선행 결과에서 추론하지 않는다. 새 core/typed/제품 묶음을 Hosted full 통합 대상으로 삼는다.

## 다음 행동

게시한 Formset/Helpdesk 묶음의 Hosted full을 실행하고, 필수 owner·최종 집계·새 capture source 결합 또는 실패를 확인한다.
제품 통합이 닫힌 뒤 모델 Formset의 남은 자동화·parent/inline 범위를 고정 Django와 실제 소비자로 이어 확장한다.
Credential/session의 별도 저장 의미와 일반 typed 준비의 책임을 구분하며 custom user model·인증/mail provider와
기능 카탈로그의 남은 범위를 계속 구현한다.

생성 소비자의 `-trimpath`와 기본 공유 cache·병렬 실행을 유지한다. 성공한 영향 검사와 무관한 전체 compile을 덧붙이지 않고,
로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 실행 규칙은 [검증 문서](../TESTING.md)를 따른다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
