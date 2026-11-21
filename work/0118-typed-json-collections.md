---
id: GDJ-0118
status: complete
updated: 2026-10-08
baseline_commit: "eae80329b30ee971fa0a62fb4eda861a50aeed14"
integration_owner: "root"
---

# Typed JSON 배열과 원자적 여러 건 생성

## 문제와 결과

현재 typed body는 한 객체를 연결한다. Helpdesk의 여러 티켓 생성은 같은 모델 Spec을 배열의 각 행에 수동으로 적용하고
항목 예산·행 번호 진단·DTO 변환을 따로 조립한다. 공통 닫힌 입력으로 이 연결을 옮기고 Article의 여러 건 생성에도 사용한다.
모델 의미·요청 개수/자원 정책·업무 인가와 저장의 소유권을 구분한다.

## 구현 경계

- 배열은 기존 Parser.ParseListFor와 같은 Body/Spec의 full/partial 검증을 사용한다. 전체 body와 항목의 compact JSON
  예산을 별도로 적용한다. 개수 범위는 runtime과 OpenAPI가 공유하며 실패에는 부분 DTO 목록을 반환하지 않는다.
- 각 행의 Spec/typed 변환과 순수 추가 검증을 선언 순서로 수행하고 진단에 원래 zero-based index를 붙인다.
  실패 행 뒤의 다른 행 진단도 모으되 내부 오류·context 취소에는 중단한다. Setter·validator는 순수·동시 사용 안전·비보관
  계약이며 body를 읽거나 쓰기를 commit하지 않는다. 진단 수집의 메모리·실행 한도와 과도한 오류의 전체 거부를 명시한다.
  `index`는 바깥 행을 가리키고 integer-list field의 기존 위치는 `item_index`로 구분한다. 같은 이름의 두 index가
  서로 다른 위치를 뜻하던 모호성을 제거하며 단건 field 진단은 원래 `index`를 유지한다.
- Endpoint의 배열 body는 같은 bare JSONBody의 named item identity를 공유한다. Single/array operation에서 같은 입력을
  사용해도 component 하나를 공유하며 임의 schema와 decoder의 결합, 중복 body 읽기나 wrapped policy의 유실은 거부한다.
- 큰 collection의 오류도 한정된 error representation을 사용한다. Input 진단과 직접 Reject의 같은 envelope/예산을
  endpoint에 연결한다. 내부/rollback/unknown outcome을 validation으로 바꾸거나 진단 일부를 성공처럼 게시하지 않는다.
- Helpdesk bulk create는 1..40개·전체/항목 한도, 진단의 순서/index, 외부 JSON·현재 Category/labels·고유성,
  native BulkCreate와 저장 row/digest·전체 출력·audit·commit 경계를 유지한다. Ticket의 같은 typed body를 단건 생성에서도 사용한다.
  단건 생성도 같은 prepared transaction owner 안에서 응답을 준비하고 확인된 commit 뒤에만 게시한다.
- Article은 같은 공개 입력 API로 bounded bulk create를 제공한다. 인가·CSRF, 전체 candidate·고유성, native BulkCreate,
  입력 순서의 최종 row·출력 준비·기존 mutation hook과 commit을 연결하고 실패·취소·unknown에는 전체 결과를 거부한다.
- 실제 OpenAPI·고정 생성 client와 양 DB의 최종 데이터를 검증한다. 기존 Article의 full/partial·기준 계약을 유지하며
  새 여러 건 생성의 원자성은 명시한 application 정책이다.

Header, arbitrary JSON union·nested writable serializer, 자동 CRUD/viewset과 bulk PATCH의 별도 ID 선택 의미는 후속 범위다.
기존 bulk PATCH는 현재 검증·scope·원자성을 유지한다. 이 구분은 전체 카탈로그 완료 범위를 축소하지 않는다.
장기 의미는 기존 [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md), 실행은
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 진행

- [x] 기존 배열 parser·항목 예산·indexed 진단·두 업무의 저장/출력 경계 확인
- [x] Typed 배열과 named item 공유·bounded 오류 표현 구현
- [x] Helpdesk/Article의 실제 여러 건 생성과 단건 입력 연결
- [x] 외부 compile·실제 HTTP·인증/CSRF·양 DB/client·실패/취소·drift 영향 검증
- [x] source와 후속 통합 범위 기록
- [x] 후속 입력 작업과 결합한 source의 전체 platform/Hosted 통합

Source `b14d0eaf3d49f01151c8b010fb97eac2a57532ef`의
[전체 CI 37692593301](https://github.com/progresshans/godj/actions/runs/37692593301) attempt 1에서
GDJ-0116/0117/0118/0119/0120을 함께 검증했다. 필수 실행과 no-skip, 실제 checkout/tree,
생성 drift·양 DB·세 모드·각 platform·same-run capture·최종 집계를 대조해 전체 통합을 완료했다.
선행 실패와 보정, 상세 실행은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 둔다.
후속 GDJ-0121의 새 구현과 전체 카탈로그 완성은 이 완료에 포함하지 않는다.
