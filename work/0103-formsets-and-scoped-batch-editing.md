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
- [x] canonical 부모 FK·InlineSet과 pending key 준비·실제 HTTP/양 DB parent-child 저장 및 rollback
- [x] core/typed/Helpdesk 제품 묶음의 Hosted full 통합: source `6d8afda5`, 이후 Inline는 별도 검증
- [x] 조회 전용 기존 행과 추가 행의 분리·typed 저장 준비 차단·형제 고유성/부모/PK 보존
- [x] Admin inline의 권한별 표시/입력·부모/자식 합성 저장과 실제 HTML 성공/실패 경로
- [x] 동적 미저장 행 추가/제거·권한별 prototype·실제 브라우저 입력/오류 재표시/SQLite 저장
- [ ] 남은 ModelFormSet/files 범위의 구현·후속 통합

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
통합에서 얻으며 선행 소스의 성공을 전이하지 않는다. Files·전체 ModelFormSet 자동화는 별도 미완료 범위다.

ModelFormSet의 고유값 검증은 고정 Django 23개 사례와 실제 양 DB HTTP로 연결했다. 겹친 제약은 검사 시작 시점의 완전한
튜플을 IR 순서로 비교하며, 후속 사용자 validator가 모델 진단을 없애지 않는다. `568b75b4`의 Hosted full은 새 하위 사례의
부모 테스트를 실행 정규식에서 선택하지 못해 필수 job이 실패했다. 실행할 부모와 확인할 전체 하위 이름을 구분하도록 고쳤고,
기존 선택자 0회/수정 선택자의 필수 32개 실행을 실제 Go에서 재현했다. 수정 소스의 전체 통합은 아래 실행에서 완료했다.

`6d8afda5`의 Hosted full `36516565253`은 62 jobs·8 owners·최종 집계와 새 capture의 Git source 결합까지 완료했다. 이후 InlineSpec은 canonical
project FK와 양 manager를 결합하고 서버 부모를 hidden 입력·모델 후보·형제 unique 검사에 연결했다. 다른 부모/중복 scalar를
삭제/빈 행에서도 전체 거부한다. Pending 부모는 저장으로 key를 받은 뒤 순수 준비하며 nullable FK도 orphan 준비를 허용하지 않는다.
고정 native 22개 입력/4개 저장 관찰, 양 DB의 부모/자식 저장·지연 쓰기·늦은 실패 rollback, 실제 HTTP 위조 거부와 관련 race/
세 부정 대조를 확인했다. 처음 지원하지 않는 FK default로 만든 테스트 fixture는 실패했고 canonical 정책 거부로 고친 모델 범위를
다시 검증했다. 실행별 source/범위는 TEST_EVIDENCE에 분리했다. 새 부모 HTML 화면과 Admin inline UI는 아래 후속 구현에서 연결했다.

고정 Admin에서 조회 전용 기존 행은 POST의 일반 값이 없어도 initial을 유지하고 callback을 건너뛴다는 동작을 확인했다.
공통 SetConfig의 ReadOnlyInitial로 일반 입력과 명시적 ORDER/DELETE를 분리했다. 모델 후보는 서버 값을 유지하며 독립/전체
준비에서 읽기 전용 행을 쓰기로 바꾸지 않는다. 새 행의 고유성 비교에서는 기존 행을 제외하지 않는다. Native 10개 관찰,
양 DB의 기존 행 무변경/새 행 저장과 같은 source의 normal·관련 race·세 실패 대조를 확인했다. 삭제 행의 callback 생략과
native의 삭제 전 DB 고유성 거부는 pure 준비와 구분한다. 권한별 inline 표시와 단일 합성 저장은 후속 Admin 계층에 연결했다.

Admin은 canonical typed inline을 부모 registration의 합성 callback에 연결했다. 서버에서 선언한 추가 행과 빈 prototype을 표시하며
권한별 current 조회·readonly·extra/DELETE, 원래 입력/오류 재표시와 binding owner를 유지한다. Helpdesk AdminRegistry는
transactional audit를 명시적으로 받아 새 티켓과 보고서·양쪽 감사 기록을 같은 transaction에 저장한다. 현재 부모/자식 scope,
삭제 전 DB unique·child-only audit·늦은 실패와 unknown outcome을 실제 양 DB HTML 소비자로 검증한다. 독립 Registry와
Application 상태는 바꾸지 않는다. 현재/실행 범위는 CURRENT와 TEST_EVIDENCE, API는 [Admin inline](../admin/inlines.md)에 있다.

동적 UI는 미저장 행만 추가/제거하며 기존 identity·INITIAL_FORMS·다른 inline과 원래 입력값을 유지한다. 고정 Django의
inline script를 동작 참고로 읽었지만 전체 브라우저 동등성을 주장하지 않는다. 실제 GoDj 브라우저 소비자는 두 HasMany inline,
min/max·오류 행 재번호·readonly/no-add·새 부모/자식의 SQLite 저장을 확인한다. Files·전체 ModelFormSet 저장 자동화는 남아 있다.
