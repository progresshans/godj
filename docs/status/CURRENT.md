# 현재 상태

- 갱신: 2026-09-28
- 현재 작업: [GDJ-0102 모델의 빈 입력 정책과 Form 후처리](../../work/0102-model-blank-policy-and-post-clean.md)
- 최근 완료한 전체 검증: [Hosted full 36391162296](https://github.com/progresshans/godj/actions/runs/36391162296), source `1b2fc49267181f321c0a079844e945cd6cf81584`; 필수 owner·최종 집계·새 capture의 Git source 결합 확인
- 진행 중인 저장 조정/제품 연결: [Hosted full 36397837881](https://github.com/progresshans/godj/actions/runs/36397837881), source `f5b0020fa6c3f2c150eed720464b8e6765521113`
- 기본 saver `c5f5e56a8603ff22bc4b8324f1e261bf147935bf`의 [Fast](https://github.com/progresshans/godj/actions/runs/36402154359): terminal success·실제 Go 검사 성공
- Source·환경·scope·실패/수정·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Blank 정책·model clean·단계별 DB 후처리와 typed instance 준비를 연결했다. 지원 범위는
[구현 현황](IMPLEMENTATION_MATRIX.md), 입력·준비·저장 소유권은 [ADR-0080](../adr/0080-model-blank-policy-and-form-post-clean.md)과
[사용법](../../forms/model/README.md)을 따른다. Source가 다른 Hosted 결과를 현재 변경의 검증으로 전이하지 않는다.

Admin BoundForm과 Helpdesk typed 저장에 이어 기본 `SaveManyToMany`를 추가했다. Typed forward binding에서 관계 이름과
저장 callback을 유도하고 모델/field 결합을 scalar 쓰기 전에 검사한다. 자동/명시적 through·자기 참조·실패 의미를 기존 ORM에
위임하며 현재 인가/category·audit는 애플리케이션이 소유한다. 영향 세 mode/양 DB·독립 생성 소비자·부정 대조를 확인했다.
중단된 race의 성공한 core 결과와 별도 generated 재실행, 공간/공용 cache 삭제로 실패한 시도는 Evidence에 구분한다.
부가 로컬 전체 compile은 미완료이며 이번 영향 검증의 PASS로 세지 않는다.

생성 소비자 자식에만 `-trimpath`를 적용해 동일 소스의 임시 경로 차이로 생기던 cache 중복을 줄였다.
격리·기본 cache·병렬 실행·실제 test 실행/race는 유지한다. Normal은 해당 package 전체, race/CGO=0은 관련 범위와
양 DB·두 부정 대조를 확인했다. 실행 규칙은 [검증 문서](../TESTING.md), 목록 집계 정정과 한계는 Evidence를 따른다.

## 다음 행동

`f5b0020f` Hosted 전체의 필수 owner·최종 집계와 새 capture의 provenance/Git source 결합을 확인한다.
선행 `1b2fc492`의 62 jobs·8 owners 통합은 완료했다. 관찰 지연만으로 기존 run을 재시작하지 않는다.
기본 saver와 생성 소비자 cache 개선을 포함한 후속 source의 Hosted 통합을 이어 확인한다.
공유 build cache와 동시 실행 속도를 유지하며 성공한 검사와 무관한 전체 compile을 반복하지 않는다.
Credential/session의 별도 저장 의미와 일반 typed 준비의 책임을 구분하며 나머지 ModelForm·custom user model·인증/mail provider와
기능 카탈로그를 이어 구현한다. 로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
