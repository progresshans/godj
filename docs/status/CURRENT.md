# 현재 상태

- 갱신: 2026-09-28
- 최근 완료한 통합: [GDJ-0100 다중 사용자·credential/session lifecycle](../../work/0100-multi-user-credential-and-session-lifecycle.md)
- 현재 구현 작업: [GDJ-0102 모델의 빈 입력 정책과 Form 후처리](../../work/0102-model-blank-policy-and-post-clean.md)
- 최근 구현·영향 검증 완료: [GDJ-0101 이메일 필드와 모델 입력 검증](../../work/0101-email-fields-and-model-input-validation.md)
- 최근 완료: [GDJ-0099 ManyToMany와 Ticket 라벨 컬렉션](../../work/0099-many-to-many-and-ticket-label-collections.md)
- 최근 완료한 전체 검증: [Hosted full](https://github.com/progresshans/godj/actions/runs/36374533286), source `ea2867f4323fb34e713fc1d10d8a62c05c785d37`; 62 jobs·8 owners·새 capture의 source 결합 확인
- 진행 중인 Blank/모델 DB 검증 통합: [Hosted full](https://github.com/progresshans/godj/actions/runs/36381300069), source `d2c9d6fef328af5a87dd61cc507b61fe8295912a`
- 최근 모델 DB 검증 [Fast](https://github.com/progresshans/godj/actions/runs/36379234202), source `b8cc51b5`: 실제 Go 검사 성공
- Source·환경·scope·선행 실패와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

EmailField를 별도 IR kind, 문자열 query·저장, Form/serializer·EmailInput에 연결했다.
기본 User의 email migration과 Admin/API·독립 client, 계정 reset의 공통 검증을 구현했다.
기존 데이터·credential/session/audit·호스트 관계를 보존하며 canonical migration 이력을 확인한다.
Native와 다른 NUL transport 경계, 입력 default·기존 출력의 의미는 [ADR-0079](../adr/0079-email-fields-and-input-semantics.md)를 따른다.

User·Group·Permission 관리, 저장 인증과 일반 계정 login/logout·password change/reset,
일반/Admin 사용자 생성 Form의 재사용과 prepare/commit 소유권도 구현했다.
상세 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md), [Account 사용법](../../identity/account/README.md),
[사용자 생성 Form](../../identity/USER_CREATION.md)을 따른다.

Blank의 IR·생성기·migration·Form 후보와 User/Helpdesk/Article 선언·새 이력·생성물을 구현했다.
Admin은 원래 제출을 보존하고 수정 시 현재 row/revision에서 후보를 만든다.
모델 DB 후처리의 unique field → 오류 적용/제외 재계산 → constraint를 하나의 읽기 scope에 연결했다.
User 수정·Group/Permission·Ticket·Label·ServiceReport·TicketLabel은 현재 인가·row·관계를 확인한다.
Label의 category는 서버 범위가 소유하며 일반 ModelForm의 제외 정책은 유지한다. 최종 저장의 transaction 검사는 그대로다.
영향 normal/race/CGO=0·실제 양 DB·독립 생성 소비자와 단계별 부정 대조를 확인했다.
고정 Django의 DB 사례는 각 DB의 15개 의미 일치와 제외 필드 이름을 추가 입력에 재사용하는 1개 명시적 차이를 구분한다.
현재 정책은 [ADR-0080](../adr/0080-model-blank-policy-and-form-post-clean.md), 실행 범위는 Evidence를 따른다.

## 다음 행동

진행 중인 Blank/DB 후처리 통합의 필수 owner·최종 집계·새 capture source 결합을 확인한다.
새 Blank/DB 후처리에 선행 source의 Hosted 검증을 전이하지 않는다.
다음 모델 Form 확장은 model clean의 값 변환·저장 후보 의미를 고정 Django에서 먼저 확인한다.
Custom user model·전체 ModelForm 후처리·다른 인증 provider와 운영 mail provider 검증은 남아 있다.
로컬 전체와 Hosted 전체를 관성적으로 중복 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
