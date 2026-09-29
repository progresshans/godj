---
id: GDJ-0103
status: active
updated: 2026-09-29
baseline_commit: "cb76b165aa3379c8c40aabfa7d12354460c57bf7"
integration_owner: "root"
---

# Formset과 범위가 정해진 여러 행 편집

같은 Form/ModelForm의 여러 행을 한 요청으로 다루는 기능을 구현한다. Helpdesk의 여러 항목을 함께 입력·편집하는
실제 흐름을 소비자로 연결한다. 기존 행의 서버 소유 identity·인가·transaction과 폼의 순서/삭제 요청을 구분한다.
GDJ-0102의 initial source `cb76b165`는 Hosted full 62 jobs·8 owners와 새 capture 결합까지 확인했다. 이후 Formset 변경은 별도로 검증한다.

## 구현과 검증

- [x] 고정 Django에서 management·개수/상한·빈 추가 행·삭제/정렬·cross-form 오류를 독립 관찰
- [x] 불변 SetSpec/Set·prefix/management·bounded 생성·행 오류/선택과 pure validator 구현
- [x] native 33개·malformed/forged counts·중복 입력·소유권/비공개·실제 실행 횟수·관련 race/부정 대조 검증
- [x] 공통 model candidate와 typed 준비를 재사용하는 여러 행 준비/검증 연결
- [x] 실제 Helpdesk 입력/편집과 권한·서버 범위·오류 재표시·원자 저장/실패 경로 연결
- [x] 구조가 다른 모델 소비자·관련 실제 DB/race·필수 실패 대조와 영향 checkpoint
- [x] 현행 사용법·지원 범위·환경별 증거 정리
- [x] 여러 행 unique/복합 제약·compound cleaned 제외와 실제 HTTP 쓰기 전 거부
- [ ] core/typed/Helpdesk 제품 묶음의 Hosted full 통합 milestone

요청의 INITIAL_FORMS를 신뢰해 저장된 행을 추가 행으로 바꾸거나 생략할 수 없게 한다. 서버 initial 수와 요청의
management 일치를 검사하며, 행 수의 hard cap은 callback/폼 생성 전에 적용한다. 이 count 검사는 실제 모델 identity와
현재 인가·revision 검사를 대체하지 않는다. Order/Delete는 입력 의도이며 자동 저장·삭제·commit 권한이 아니다.

[ADR-0081](../docs/adr/0081-formset-counts-and-row-ownership.md)의 의미와 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)의
실행 범위를 따른다. Pure formset만으로 ModelFormSet·inline·file upload 전체 완료를 선언하지 않는다.

공통 실행의 사용법은 [Formset](../forms/formset.md)과 [typed 모델 준비](../forms/model/README.md)를 따른다.
서버 current 집합의 PK로 행을 연결하고 누락/중복/외부/추가 행 identity는 삭제 여부와 무관하게 거부한다. Field/model 검증은
한 번 실행하며 core와 같은 binding의 모델 후처리만 허용한다. Article·Ticket의 scalar/collection·서버 값·동시 준비와
고정 native 20개 관찰, 영향 normal/관련 race·기존 양 DB 저장·세 부정 대조를 확인했다.

실제 [Helpdesk 편집기](../examples/helpdesk/README.md#여러-티켓을-함께-편집하기)는 현재 cohort/인가·권한별 추가/삭제·원래 입력
재표시·페이지 범위와 원자 scalar/collection·PROTECT/CASCADE·audit를 연결했다. 양 DB의 실제 HTTP 성공/실패·롤백·재개와
관련 race, Add 거부/audit 실패/unknown outcome의 부정 대조를 확인했다. 새로운 전체 플랫폼 증거는 게시한 소스의 Hosted
통합에서 얻으며 선행 소스의 성공을 전이하지 않는다. Inline/files·전체 ModelFormSet 자동화는 이 작업의 완료와 별개다.

ModelFormSet의 고유값 검증은 고정 Django 23개 사례와 실제 양 DB HTTP로 연결했다. 겹친 제약은 검사 시작 시점의 완전한
튜플을 IR 순서로 비교하며, 후속 사용자 validator가 모델 진단을 없애지 않는다. `568b75b4`의 Hosted full은 새 하위 사례의
부모 테스트를 실행 정규식에서 선택하지 못해 필수 job이 실패했다. 실행할 부모와 확인할 전체 하위 이름을 구분하도록 고쳤고,
기존 선택자 0회/수정 선택자의 필수 32개 실행을 실제 Go에서 재현했다. 수정 소스의 전체 통합은 아직 남아 있다.
