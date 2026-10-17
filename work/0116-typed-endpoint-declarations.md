---
id: GDJ-0116
status: complete
updated: 2026-10-08
baseline_commit: "8a97c78432014ac0d3d4e0ef21378f80a1724cc6"
integration_owner: "root"
---

# Typed endpoint와 실행·문서 선언의 연결

## 문제와 결과

Typed query/body와 출력이 각각 같은 schema를 소유하지만 endpoint는 입력 parsing·진단 표현·출력·상태와
OpenAPI operation을 별도로 조립한다. 필드가 다른 입력/출력을 handler에 잘못 연결하거나 실행과 문서의 권한·
상태가 달라도 현재 연결에서는 compile로 확인할 수 없다. 실제 typed 입력과 출력, 명시한 권한·상태·오류 정책으로
HTTP handler와 operation을 함께 준비한다. 모델 의미와 인증 transport를 추측하지 않는다.

첫 소비자는 Helpdesk의 티켓 요약 조회와 Label ensure다. 요약은 typed query와 하나의 read snapshot을 사용하고,
ensure는 typed model body와 조회/생성·출력 준비·audit·commit을 하나의 transaction에서 처리한다. 서로 다른 입력,
응답 구조, 단일/복수 성공 상태와 실패/취소 경계를 확인해 구체 API를 선택한다.

## 설계 경계

- 기존 Query/Body와 Output의 닫힌 변환·schema·자원 한도를 재사용한다. 새로운 struct reflection·입력 정규화나
  별도 schema/parser 쌍을 만들지 않는다. Query/body별 오류와 Label ensure의 query 거부 순서도 유지한다.
- 인증/CSRF/인가가 입력 읽기보다 먼저 실행된다. 실제 authentication adapter와 operation에 같은 권한 snapshot을
  연결한다. 초기화는 parser·handler·출력 getter를 실행하지 않는다. 지원하지 않는 admission은 준비 때 거부한다.
- Handler 입력과 성공 응답은 compile로 연결한다. 성공 응답을 준비한 정확한 Output과 endpoint를 연결하고
  다른 선언/한도에서 만든 응답이나 선언하지 않은 상태·형식을 실행 성공으로 통과시키지 않는다.
- 응답을 encode하는 시점을 handler가 소유할 수 있어야 한다. 특히 저장 뒤 자동으로 encode하도록 바꾸지 않는다.
  Label ensure의 출력 준비와 audit는 commit 전에 끝내고, 업무가 실패·취소·unknown outcome을 보고하면 준비한 응답도 노출하지 않는다.
  Handler가 확정한 성공/commit은 그 뒤의 context 취소만으로 번복하지 않는다.
- Expected client 오류는 명시적인 오류 envelope·상태만 사용하며 내부 Go 오류/원 cause를 client JSON으로 노출하지
  않는다. Authentication 실패의 상태·header는 기존 profile을 사용한다.
- Endpoint·schema·header/permission slice는 준비 후 불변 snapshot이다. Named output 정체성과 최종 문서의 충돌 검사,
  실제 router path grammar·request 수명·업무 범위·DB transaction 소유권은 기존 경계를 따른다.

Header/path typed binding·배열 body·범용 viewset·자동 CRUD의 완료를 주장하지 않는다. 이 미완료 범위는 카탈로그에
유지한다. 장기 선택은 기존 [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md), 실행 상세는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 구현과 검증

- [x] 실제 query 조회와 원자 ensure의 조립·응답 준비·권한/오류 순서 확인
- [x] 선언에 결합된 typed 준비 응답과 응답 한도·형식/상태·실패 소유권 구현
- [x] Typed 입력·handler·권한과 OpenAPI operation의 공통 준비 구현
- [x] 실제 요약·ensure 흐름과 서로 다른 DTO/상태·transaction 경계 연결 구현
- [x] 외부 compile·인가/CSRF·오류/소유권·실제 양 DB/독립 client·영향 세 mode 검증과 정리
- [x] endpoint와 다음 입력 결합을 포함한 후속 source의 전체 platform/Hosted 통합

Source `b14d0eaf3d49f01151c8b010fb97eac2a57532ef`의
[전체 CI 37692593301](https://github.com/progresshans/godj/actions/runs/37692593301) attempt 1에서
GDJ-0116/0117/0118/0119/0120을 함께 검증했다. 필수 실행과 no-skip, 실제 checkout/tree,
생성 drift·양 DB·세 모드·각 platform·same-run capture·최종 집계를 대조해 전체 통합을 완료했다.
선행 실패와 보정, 상세 실행은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 둔다.
후속 GDJ-0121의 새 구현과 전체 카탈로그 완성은 이 완료에 포함하지 않는다.
