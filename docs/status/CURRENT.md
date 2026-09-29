# 현재 상태

- 갱신: 2026-09-29
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36504649962](https://github.com/progresshans/godj/actions/runs/36504649962), source `cb76b165aa3379c8c40aabfa7d12354460c57bf7`; 62 jobs·8 owners·최종 집계와 새 capture의 Git source 결합 완료
- Helpdesk source `568b75b40ac5da5b3d03b2406808d33c6c23f911`의 [Hosted full 36514445961](https://github.com/progresshans/godj/actions/runs/36514445961): 하위 사례만 등록한 부모 테스트의 선택 누락으로 필수 job 실패; 전체 PASS 아님
- Source·환경·scope·실행/수정 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset core·typed 모델 준비를 [Helpdesk 여러 행 편집](../../examples/helpdesk/README.md#여러-티켓을-함께-편집하기)에 연결했다.
서버 Category의 현재 페이지와 관계 선택지를 다시 읽고, 같은 transaction에서 여러 행 scalar/collection·삭제 정책·감사 기록을
저장한다. 권한/CSRF·cohort/identity·입력 재표시와 늦은 실패/롤백·unknown outcome을 실제 양 DB HTTP로 확인했다.
ModelFormSet의 unique field/복합 제약을 여러 행의 cleaned 입력에서 검사하고 중복 행/전체 진단과 typed/core exclusion을 함께 갱신한다.
필수 하위 사례의 부모를 실행하도록 Hosted 선택자를 고쳤으며 기존 선택자의 0회 실행과 수정 후 실제 실행을 대조했다.
영향 normal·관련 race·부정 대조와 native 기준을 확인했다.
[모델 Form 사용법](../../forms/model/README.md), [ADR-0081](../adr/0081-formset-counts-and-row-ownership.md)이 모델/제품 책임을 구분한다.

`cb76b165`의 전체 성공에는 Formset이 없다. 현재 코드의 전체 platform 완료를 선행 결과에서 추론하지 않는다.
실패한 실행과 수정한 선택자·고유값 검증을 구분하고, 수정 소스의 새 Hosted full 통합이 필요하다.

## 다음 행동

선택자 수정과 여러 행 고유값 검증을 게시한 뒤 Hosted full의 필수 owner·최종 집계·새 capture source 결합 또는 실패를 확인한다.
제품 통합이 닫힌 뒤 모델 Formset의 남은 자동화·parent/inline 범위를 고정 Django와 실제 소비자로 이어 확장한다.
Credential/session의 별도 저장 의미와 일반 typed 준비의 책임을 구분하며 custom user model·인증/mail provider와
기능 카탈로그의 남은 범위를 계속 구현한다.

생성 소비자의 `-trimpath`와 기본 공유 cache·병렬 실행을 유지한다. 성공한 영향 검사와 무관한 전체 compile을 덧붙이지 않고,
로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 실행 규칙은 [검증 문서](../TESTING.md)를 따른다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
