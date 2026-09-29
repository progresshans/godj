---
id: GDJ-0102
status: active
updated: 2026-09-29
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
- [x] Blank·내부 Form API·제품 DB 후처리 source `211499d0`의 Hosted 전체와 새 capture source 결합 확인
- [x] Pure model clean의 명시적 scalar 변환·제외 필드 저장 입력·Admin 현재 row/revision 소유권과 독립 생성 모델 연결
- [x] 후속 model clean의 영향 세 mode·양 DB·native 대조·실패/5개 부정 대조 완료
- [x] 생성 descriptor·ORM typed 준비와 Form instance/collection·command 입력 소유권 구현
- [x] Typed 준비·표현 불가능한 NULL 경계의 영향 세 mode·양 DB·생성 drift·7개 부정 대조 완료
- [x] 후속 clean/prepare `1b2fc492`의 Hosted 통합·62 jobs/8 owners/새 capture source 결합 완료
- [x] Scalar/선택 collection 저장 조정과 deferred 단계·실패/현재 인가 경계를 독립 생성 소비자에 연결
- [x] 저장 lifecycle의 고정 Django 관찰·영향 세 mode·양 DB·6개 부정 대조 완료
- [x] Admin BoundForm callback과 Helpdesk typed 저장 연결·기존 인가/범위/JSON/실패 경계 보존
- [x] 영향 세 mode·양 DB·203 packages compile-only·4개 부정 대조 완료
- [x] Typed 관계 binding에서 기본 Form saver 유도·모델/field 결합 검사·실제 Helpdesk 연결
- [x] 교차 앱·자동/명시적 through·self 관계·실패의 영향 세 mode/양 DB·4개 부정 대조
- [x] 저장 조정/제품 연결 `f5b0020f`의 Hosted 62 jobs/8 owners·새 capture source 결합 완료
- [ ] 기본 saver 후속 source의 Hosted 통합 완료
- [x] 생성 소비자의 임시 절대경로로 인한 중복 build/cache 개선과 실행/격리·source 변경 검증
- [x] 기존 initial과 새 입력 제약 분리·Admin 스냅샷/표시·수정 저장·native 대조와 영향 검증

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
Hosted 통합에서 optional choices fixture의 Blank와 독립 동시 migration 프로젝트의 Article 이력 복사 누락을 확인했다.
기존 엄격한 기대를 유지하며 fixture를 보완하고 실제 양 DB/CLI/재시작과 독립 생성 소비자의 영향 세 mode를 통과했다.
보완 source `211499d0`의 실제 Fast와 [Hosted full](https://github.com/progresshans/godj/actions/runs/36383539735)의 62 jobs·8 owners·새 capture source 결합을 완료했다.
기존 실패 run은 대체 취소됐으며 이 작업의 통합 완료로 세지 않는다.
후속 model clean은 `PostClean.Fields`에 선언한 scalar 변경 집합을 반환한다. 후보와 Form.Cleaned를 분리하고 오류가
남아도 후보 변경을 보존하며 field cleaning을 재실행하지 않는다. 명시적으로 바꾼 제외 field만 유효한 Input에 포함한다.
Admin은 현재 전체 row를 요구하고 PK/revision 출력과 숨긴 HTTP 입력 위조를 거부하며 typed 저장·audit 검사에 연결한다.
Native 양 DB 13개 사례를 독립 생성 모델의 DB 검사·준비·저장·rollback과 대조하고 영향 세 mode·5개 부정 대조를 완료했다.
Python clean 반환값의 무시와 Go의 명시적 변경 집합, Python instance identity와 Go typed 준비 소유권은 같은 API가 아니다.
후속 model clean source `36632e24`의 실제 Hosted Fast Go 검사도 성공했다. 선행 통합 `211499d0`을 후속 구현의
Hosted 전체 검증으로 전이하지 않는다. Generated SetFieldValue와 ORM의 ModelValues/ApplyValues, Form의 typed instance
준비를 추가해 scalar별 수동 복사와 NULL의 zero-value 변환을 없앤다. Nullable pointer와 PK 존재·미변경 field를 보존하고
collection 저장 의도와 command 입력을 별도 보관한다. 준비는 자동 commit하지 않으며 후속 저장 조정과 구분한다.
Native 15개 사례 중 기존 13개의 의미와 nonnullable NULL 2개의 준비 시점 차이를 분리하고 영향 세 mode·양 DB·생성 drift·7개 부정 대조를 완료했다.

후속 clean/typed 준비 commit `1b2fc49267181f321c0a079844e945cd6cf81584`를 양 branch에 원자적으로 push했다.
[Hosted full 36391162296](https://github.com/progresshans/godj/actions/runs/36391162296)과
[Fast 36391165591](https://github.com/progresshans/godj/actions/runs/36391165591)을 실행했다. Fast와 full의 terminal success를 확인했다. Full은 62 jobs·8 owners·최종 집계·새 capture의 Git source 결합까지 완료했다. Source가 다른 검증을 전이하지 않는다.

후속 `PreparedInstance.Save/SaveCollections`는 caller가 소유하는 typed 모델과 선택 collection adapter를 연결한다.
Scalar 뒤 IR 순서로 저장하고 사전 구성 검사·빈 선택/제외·PK presence·context/session lifetime·오류 전달을 보존한다.
고정 native 양 DB 15개 사례의 저장 상태를 대조하며 literal COMMIT 오류 2개의 Go outcome-unknown 차이는 명시한다.
독립 생성 소비자에서 현재 권한·row/revision·선택 범위가 달라지는 쓰기와 late callback 실패·만료/취소도 검증했다.
영향 normal/race/CGO=0과 여섯 부정 대조를 통과했다. 이 저장 조정 source `ce52685c`의 실제 Hosted Fast도 성공했다.
후속 Admin Create/Update는 최종 BoundForm을 전달하며 PrepareInstance는 명세/PK를 확인하고 clean 재실행 없이 typed 준비한다.
Helpdesk Ticket은 현재 category/label 범위·complete write uniqueness·JSON 원문·changed/audit·응답 변환과 같은 transaction을
유지하며 이 API를 사용한다. 수정은 변경 field mask와 deferred 관계 저장으로 제외 column 재기록을 막는다. 나머지 typed
credential/session adapter는 bound.Input을 읽으며 내부 callback 호환 분기는 없다. 영향 세 mode·양 DB·전체 compile-only와
네 부정 대조를 완료했다. CI owner 등록 정정은 Go 입력/로컬 필수 집합 동일성 확인과 최종 CI 도구 검사로 구분한다.
저장 조정/제품 연결 source `f5b0020f`의 Hosted 통합을 완료했다. 이후 source의 통합은 별도로 확인한다.
제품 연결 commit `996ff5eccf0393781080d834ddd9636e981e53bd`를 양 branch에 원자적으로 push했고
[Fast 36397043744](https://github.com/progresshans/godj/actions/runs/36397043744)의 실제 Go step·terminal success를 확인했다.
선행 full 완료 후 같은 제품 코드를 가진 문서 후속 commit `f5b0020fa6c3f2c150eed720464b8e6765521113`의
[Hosted full 36397837881](https://github.com/progresshans/godj/actions/runs/36397837881)의 필수 owner·최종 aggregate·새 capture의 Git source 결합을 완료했다.

기본 `SaveManyToMany`는 typed 선언에서 saver를 만들고 저장 전에 전체 모델/field 결합을 확인한다.
Helpdesk와 독립 생성 소비자에 연결했으며 실행/중단/환경 복구와 검증 한계는 Evidence에 구분한다.
Generated 소비자 자식의 `-trimpath`와 실제 캐시 identity/실행 marker·두 부정 대조로 경로 중복을 개선했다.
일반 package 전체와 관련 race/CGO=0·양 DB를 확인했다. 공유 build cache·동시 실행 속도를 유지하고,
이미 성공한 영향 검사를 반복하거나 전체 compile을 추가하는 관행을 피한다. 실제 DB/race/실패 검증은 계속 유지한다.

기존 initial은 표시·변경 비교의 원본으로 유지하고 입력 제약은 제출값에 적용한다. Admin의 중복 제약 검사를 없애되
인가된 모델 snapshot 일치 검사는 유지하며, Decimal initial이 입력 scale을 넘을 때도 값을 보존한다. 고정 native 대조와
Form/model·Admin·Helpdesk의 영향 normal/관련 race·양 DB·세 부정 대조를 완료했다. 실패 수정과 재사용 증거는 Evidence를 따른다.

장기 의미는 [ADR-0080](../docs/adr/0080-model-blank-policy-and-form-post-clean.md), 실행 상세는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.
