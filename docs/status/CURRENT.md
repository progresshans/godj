# 현재 상태

- 갱신: 2026-09-29
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36516565253](https://github.com/progresshans/godj/actions/runs/36516565253), source `6d8afda5086ba3fc058376a60dc567bdcd5a05d7`; 62 jobs·8 owners·최종 집계와 새 capture의 Git source 결합 완료
- Source·환경·scope·실행/수정 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset core·typed 모델 준비와 [Helpdesk 여러 행 편집](../../examples/helpdesk/README.md#여러-티켓을-함께-편집하기),
여러 행 unique/복합 제약·compound cleaned 제외의 통합을 완료했다. 서버 current 집합·권한/CSRF·관계·삭제 정책·감사 기록을
같은 transaction에서 처리하고 늦은 실패/rollback·unknown outcome을 검증했다. Hosted 필수 하위 사례의 부모 선택 누락을
수정한 위 source의 전체 통합과 source 결합을 확인했다.

이후 공통 InlineSpec이 canonical project FK·서버 부모와 자식 집합을 결합한다. 저장된 부모와 pending 부모의 검증/key 연결,
삭제/빈 행의 부모 위조 거부와 nullable/cross-app/OneToOne을 구현하고 Helpdesk HTTP에 연결했다. 새 부모 저장→자식 저장과
늦은 실패의 전체 rollback도 실제 양 DB로 확인했다. Admin inline 권한 분기의 기반인 조회 전용 기존 행은 서버 initial과 형제
고유성을 보존하고 일반 입력/후처리가 기존 행을 바꾸거나 저장 후보로 만들지 못하게 한다. 추가 행과 명시적 삭제는 별도로 준비한다.
영향 normal·관련 race·native 관찰/부정 대조를 확인했다. [모델 Form 사용법](../../forms/model/README.md)과
[ADR-0081](../adr/0081-formset-counts-and-row-ownership.md)이 모델/제품 책임과 native 차이를 구분한다.

위 Hosted full에는 Inline과 조회 전용 행의 후속 변경이 없다. 로컬 영향 검증을 현재 전체 platform 완료로 확대하지 않는다.

## 다음 행동

Admin의 모델별 저장 callback 경계에 부모·자식 전체의 합성 저장을 연결한다. 권한별 inline 표시/입력과 오류 재표시,
새 부모 HTML 생성·자식 저장·실패 rollback을 실제 소비자로 완성한다. ModelFormSet의 나머지 자동화/files를 이어 구현하며,
이 후속 묶음의 다른 환경 검증은 다음 통합 checkpoint가 소유한다.
Credential/session의 별도 저장 의미와 일반 typed 준비의 책임을 구분하며 custom user model·인증/mail provider와
기능 카탈로그의 남은 범위를 계속 구현한다.

생성 소비자의 `-trimpath`와 기본 공유 cache·병렬 실행을 유지한다. 성공한 영향 검사와 무관한 전체 compile을 덧붙이지 않고,
로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 실행 규칙은 [검증 문서](../TESTING.md)를 따른다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
