# 현재 상태

- 갱신: 2026-09-29
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36397837881](https://github.com/progresshans/godj/actions/runs/36397837881), source `f5b0020fa6c3f2c150eed720464b8e6765521113`; 필수 owner·최종 집계·새 capture의 Git source 결합 확인
- 진행 중인 Hosted 통합: [full 36504649962](https://github.com/progresshans/godj/actions/runs/36504649962), source `cb76b165aa3379c8c40aabfa7d12354460c57bf7`; 기본 saver·cache·initial 수정 포함
- 선행 initial source `cb76b165aa3379c8c40aabfa7d12354460c57bf7`의 [Fast](https://github.com/progresshans/godj/actions/runs/36504615628): terminal success·실제 Go 검사 성공
- Source·환경·scope·실패/수정·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset을 시작했다. 고정 Django의 33개 사례에서 management·빈 행·개수 제한·정렬/삭제·오류 순서를 관찰했다.
불변 SetSpec/Set과 count/빈 행/삭제/정렬·오류/선택을 구현했다. Pure/기존 model Form·Admin choice·관련 race와
세 부정 대조를 확인했다. [사용법](../../forms/formset.md)은 core 범위이며 typed 여러 행 준비·실제 Helpdesk 저장/권한은 남아 있다.
[ADR-0081](../adr/0081-formset-counts-and-row-ownership.md)의 count/소유권 경계와 native 차이를 따른다.

Blank 정책·model clean·단계별 DB 후처리와 typed instance 준비를 연결했다. 지원 범위는
[구현 현황](IMPLEMENTATION_MATRIX.md), 입력·준비·저장 소유권은 [ADR-0080](../adr/0080-model-blank-policy-and-form-post-clean.md)과
[사용법](../../forms/model/README.md)을 따른다. Source가 다른 Hosted 결과를 현재 변경의 검증으로 전이하지 않는다.

Admin BoundForm·Helpdesk typed 저장과 기본 `SaveManyToMany`를 연결했다. Typed binding에서 관계 저장 callback을
유도하고 쓰기 전에 모델/field 결합을 검사한다. 기존 인가/category·audit·실패 의미는 유지한다.

기존 initial과 새 입력의 길이·Decimal 정밀도를 분리했다. 유효한 수정 제출이 과거 저장값 때문에 막히지 않으며,
같은 invalid 값을 다시 제출하면 현재 제약으로 거부한다. Admin의 중복 초기값 검사를 정리하고 모델 snapshot 일치와
Decimal 표시를 보존한다. 고정 native·영향 normal/관련 race·실제 양 DB·부정 대조를 확인했다.
실패/기대 수정과 package별 입력 집합 확인 후 성공한 검사 재사용은 Evidence에 구분한다.

생성 소비자의 `-trimpath`는 임시 절대경로 때문에 생기던 중복 build cache를 줄인다. 기본 cache·병렬 실행·실제 테스트
실행은 유지하며 성공한 검사와 무관한 전체 compile을 덧붙이지 않는다. 실행 규칙은 [검증 문서](../TESTING.md)를 따른다.

## 다음 행동

Formset 행 검증을 반복하지 않고 모델 후보와 typed 준비에 연결한다. 이어 Helpdesk의 실제 여러 행 입력/편집에서
서버 소유 identity·현재 인가·오류 재표시·원자 저장/실패를 검증한다.
진행 중인 `cb76b165` Hosted 통합의 실패 또는 최종 집계·새 capture 결합을 확인한다.
선행 `f5b0020f` 전체의 성공은 이후 source에 전이하지 않는다. 공유 cache와 동시 실행 속도를 유지한다.
Credential/session의 별도 저장 의미와 일반 typed 준비의 책임을 구분하며 나머지 ModelForm·custom user model·인증/mail provider와
기능 카탈로그를 이어 구현한다. 로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
