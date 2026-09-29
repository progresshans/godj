# 현재 상태

- 갱신: 2026-09-29
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36397837881](https://github.com/progresshans/godj/actions/runs/36397837881), source `f5b0020fa6c3f2c150eed720464b8e6765521113`; 필수 owner·최종 집계·새 capture의 Git source 결합 확인
- 진행 중인 Hosted 통합: [full 36504649962](https://github.com/progresshans/godj/actions/runs/36504649962), source `cb76b165aa3379c8c40aabfa7d12354460c57bf7`; 기본 saver·cache·initial 수정 포함
- 선행 initial source `cb76b165aa3379c8c40aabfa7d12354460c57bf7`의 [Fast](https://github.com/progresshans/godj/actions/runs/36504615628): terminal success·실제 Go 검사 성공
- Source·환경·scope·실패/수정·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset core와 typed 모델의 여러 행 준비를 연결했다. 서버 current 집합의 PK로 행을 찾으며 누락/중복/외부/추가 PK는
전체 요청을 거부한다. 일반 Form의 검증 결과에 모델 후처리를 한 번 연결하고 다른 binding/행으로 교체하지 못하게 한다.
기존/새 active 후보와 기존 삭제 의도를 분리하며 실제 쓰기·현재 인가·transaction은 caller가 소유한다.
고정 native 20개 관찰·영향 normal/관련 race·기존 양 DB 저장과 세 부정 대조를 확인했다. 실행 상세는 위 Evidence에 있다.
[사용법](../../forms/model/README.md)과 [ADR-0081](../adr/0081-formset-counts-and-row-ownership.md)은 typed 준비 범위와
의도적인 native identity 차이를 구분한다. 실제 Helpdesk 여러 행 HTTP/원자 저장은 아직 남아 있다.

선행 core source `7d56b3c9ff284eb68ef743e5646c6dc0ab18da3c`의
[Fast](https://github.com/progresshans/godj/actions/runs/36507584740)는 실제 Go step·terminal success를 확인했다.
선행 initial source의 Hosted full은 마지막 플랫폼 작업을 진행 중이며 아직 최종 집계 전이다.
다른 source의 결과를 현재 변경의 PASS로 전이하지 않는다.

## 다음 행동

Helpdesk의 실제 여러 행 입력/편집에서 서버 소유 identity·현재 인가·오류 재표시·원자 저장/실패를 연결하고,
제품 소비자에서 필요한 DB/race/generated 범위를 통합 검증한다.
진행 중인 `cb76b165` Hosted 통합의 실패 또는 최종 집계·새 capture 결합을 확인한다.
Credential/session의 별도 저장 의미와 일반 typed 준비의 책임을 구분하며 나머지 ModelForm·custom user model·인증/mail provider와
기능 카탈로그를 이어 구현한다.

생성 소비자의 `-trimpath`와 기본 공유 cache·병렬 실행을 유지한다. 성공한 영향 검사와 무관한 전체 compile을 덧붙이지 않고,
로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 실행 규칙은 [검증 문서](../TESTING.md)를 따른다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
