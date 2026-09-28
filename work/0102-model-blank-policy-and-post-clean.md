---
id: GDJ-0102
status: active
updated: 2026-09-28
baseline_commit: "d95b969b3297bcad13fd6760706e733684cb8add"
integration_owner: "root"
---

# 모델의 빈 입력 정책과 Form 후처리

DB의 NULL 허용과 입력의 빈 값 허용을 Schema IR에서 분리한다. 기본 User와 Helpdesk의 Form에 같은 모델 정책을
사용하고, field cleaning만으로 통과한 값도 모델 의미와 현재 DB 상태에 따라 검증할 수 있게 연결한다.
GDJ-0100/0101의 Hosted 통합은 source `ea2867f4`에서 완료했으며 이 변경의 검증으로 전이하지 않는다.

## 구현 조건

- [x] 고정 Django/DRF에서 null·blank·default와 Form/ModelForm·선택값·오류 제외·unique/constraint 순서를 독립 관찰
- [x] Scalar/FK/ManyToMany의 blank 정책을 선언·정규화 IR·생성 metadata·historical wire/digest에 보존
- [x] 저장 구조를 바꾸지 않는 blank 변경과 reverse를 capability·history/revision fence·자동 계획·SQL projection에 연결
- [x] Form의 required/empty 값과 모델 null/blank 검증을 구분하고, 이미 실패한 field·명시적 override·default 후보의 의미를 유지
- [x] 모델 field 후처리와 read-only DB 검사·인가·오류 소유권을 실제 User/Helpdesk 소비자와 최종 저장 재검사에 연결
- [x] 기존 DB 데이터·관계·credential/session/audit 보존, 구조가 다른 생성 소비자, 실패·부정 대조와 영향 checkpoint 검증
- [ ] Blank·내부 Form API·제품 DB 후처리를 포함한 통합 source의 Hosted 전체와 새 capture source 결합 확인

## 현재와 다음

Blank를 Nullable과 독립적인 IR·generated metadata·historical wire/digest에 보존하고 scalar ChangeBlank와
AlterManyToMany, 자동 계획·reverse·capability·DDL 없는 저장 정책에 연결했다. Form input과 model candidate,
생략/default/명시적 empty·검증 단계별 exclusion, pure model validator와 별도 read-only unique/constraint API도 구현했다.
기반 normal·독립 생성 모듈의 양 DB 보존과 native 입력 대조를 수행했으며 정확한 source·범위·차이는 Evidence를 따른다.

User/Helpdesk/Article에 명시적 Blank 선언과 새 migration·생성물을 추가했다. Admin은 Form.Submitted로 원래 제출의
생략과 반복 값을 보존해 최신 선택 목록으로 재검증하고 BoundForm.Input의 모델 후보를 typed create/patch에 연결한다.
수정 시 initial은 인가된 현재 row와 revision에서 얻는다. Pure model validator는 제외된 stored scalar도 갖는
현재 snapshot을 요구하며, 추가 값은 렌더링이나 저장 입력에 노출하지 않는다. 재사용 User creation Form도 모델 후처리를 사용한다.
실제 CLI 연결에서 발견한 Blank private wire 누락과 imported app의 External 소유권 손실도 수정했다.
영향 세 mode·생성/migration drift, actual DB/CLI/서버/재시작 normal과 세 부정 대조를 확인했다.
최종 foundation 세 mode도 현재 generated metadata와 함께 확인했다.
BoundForm.CheckDatabase와 Admin의 읽기 ValidateCreate/ValidateChange를 추가했다. User 수정·Ticket·TicketLabel에
현재 권한을 갖는 DB read scope의 model clean → unique field → 오류 적용/제외 재계산 → constraint를 연결했다.
User는 현재 저장된 actor·row/revision을 다시 확인하고 TicketLabel은 양 endpoint의 category 소속을 다시 확인한다.
실행·취소·scope 정리 실패는 부분 입력 진단을 공개하지 않는다. Registry는 현재 Snapshot의 PK/revision을 후보에 소유한다.
영향 세 mode·실제 양 DB와 독립 생성 모듈, 네 부정 대조를 확인했다.
Group/Permission·Label/ServiceReport도 후속 묶음으로 연결하고 영향 세 mode·양 DB·네 추가 부정 대조를 확인했다.
Group은 현재 권한과 선택, Permission은 현재 row/revision을 같은 snapshot에서 확인한다. Label의 category는 서버 범위만
명시적으로 공급하며 Report는 Ticket 소속을 확인한 뒤 OneToOne unique를 검사한다. 실행 상세는 Evidence를 따른다.
기존 ValidateUniqueCreate/Update의 complete write 검증과 최종 write fence/DB 제약/transaction은 유지한다.

고정 Django 32 profile/160 input과 양 DB ModelForm 16개 사례의 원문을 확보했다. Go 대조는 현재 pure input 160개이며
156개 의미 일치와 기존 checkbox 4개 차이·Integer widget 8 profile 차이를 구분한다. DB 16개 사례도 독립 생성 소비자에서
양 DB로 대조했다. 각각 15개는 유효성·오류·cleaned 값·unique/constraint 단계별 제외·조회 수가 일치한다.
나머지 제외된 모델 field와 추가 입력의 이름 중복은 GoDj의 기존 construction refusal이며 native 일치로 세지 않는다.
blank=True empty는 모델 clean_fields에서 건너뛰며 숫자 None도 포함한다. 이를 nonnullable 저장 허용으로 옮기지 않는다.
기존 scalar/ManyToMany policy 외의 변경을 metadata-only migration에 섞지 않는다.
JSON API의 노출·required/empty override와 명시적 omission default는 별도 정책이며 암묵적으로 바꾸지 않는다.
전체 ModelForm·custom user model의 완료를 이 기반의 존재만으로 선언하지 않는다.
다음은 이 구현 source의 Hosted 통합과 일반 model clean의 값 변환·저장 후보에 대한 독립 기준 확인이다.

장기 의미는 [ADR-0080](../docs/adr/0080-model-blank-policy-and-form-post-clean.md), 실행 상세는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.
