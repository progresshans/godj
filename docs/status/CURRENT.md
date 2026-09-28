# 현재 상태

- 갱신: 2026-09-28
- 현재 구현 작업: [GDJ-0102 모델의 빈 입력 정책과 Form 후처리](../../work/0102-model-blank-policy-and-post-clean.md)
- 최근 완료한 전체 검증: [Hosted full](https://github.com/progresshans/godj/actions/runs/36383539735), source `211499d05f6763e3dc5ecf501fd4539393c264cb`; 62 jobs·8 owners·새 capture와 Git source 결합 확인
- 진행 중인 후속 clean/typed 준비: [Hosted full](https://github.com/progresshans/godj/actions/runs/36391162296), source `1b2fc49267181f321c0a079844e945cd6cf81584`
- 같은 source의 [Fast](https://github.com/progresshans/godj/actions/runs/36391165591): terminal success·실제 Go 검사 성공
- Source·환경·scope·실패/수정·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Blank의 IR·생성 metadata·DDL 없는 migration과 Form 후보/단계별 DB 후처리를 구현하고 선행 통합을 완료했다.
User 수정·Group/Permission과 Helpdesk의 Ticket/Label/ServiceReport/TicketLabel은 현재 권한·row/revision·관계를
확인한 읽기 검증과 최종 저장의 transaction 검사를 구분한다. 선행 인증 lifecycle과 EmailField도 이 source에 포함된다.
지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md), 입력·후처리 소유권은 [ADR-0080](../adr/0080-model-blank-policy-and-form-post-clean.md)을 따른다.

후속 model clean은 명시한 scalar 변경과 제외 필드의 서버 파생 값을 저장 입력에 연결한다. Form.Cleaned와 후보·오류를
구분하며 Admin의 PK/revision·숨긴 입력·최종 write fence를 유지한다. 이 변경의 영향 세 mode·양 DB·부정 대조를 완료했다.

생성 descriptor와 공통 ORM의 typed 값 변환, `BindInstance/PreparedInstance`도 구현했다. 현재 모델·nullable pointer·PK
존재를 보존하고 collection 저장 의도와 command 입력을 분리한다. 잘못된 nonnullable NULL은 Go zero value로 저장하지 않는다.
고정 Django 15개 사례의 13개 의미 대조와 2개 준비 시점 차이를 명시한다. [사용법](../../forms/model/README.md)을 따른다.
Typed 준비의 영향 세 mode·실제 양 DB·생성 drift·7개 부정 대조를 완료했다. 선행 full `211499d0`의 성공을 이 후속 source에 전이하지 않는다.

`PreparedInstance.Save/SaveCollections`의 scalar·선택 collection 저장 조정도 구현했다. Caller 모델의 identity와
IR 순서·빈 선택·제외·실패/rollback을 독립 생성 소비자에서 대조하고 영향 세 mode·양 DB·6개 부정 대조를 완료했다.
지연 FK COMMIT 오류는 기존 outcome-unknown 계약을 유지한다. 이 후속 저장 코드는 진행 중인 `1b2fc492` full에 포함되지 않는다.

## 다음 행동

진행 중인 `1b2fc492` Hosted 전체의 필수 owner·최종 집계·새 capture 결합을 확인한다. 관찰 지연만으로 재실행하지 않는다.
새 저장 조정 API를 실제 제품 adapter에 이어 연결하고 현재 actor·row/revision·선택 범위와 최종 commit 경계를 확인한다.
Custom user model·다른 인증 provider·운영 mail provider와 기능 카탈로그의 나머지 범위도 남아 있다.
로컬 전체와 Hosted 전체를 관성적으로 중복 실행하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
