---
id: GDJ-0117
status: active
updated: 2026-10-07
baseline_commit: "897468fe56e994ada73a4f2e5b71ae4b2bcf59b4"
integration_owner: "root"
---

# Typed 경로와 순서가 있는 입력 결합

## 문제와 결과

Typed endpoint는 query 또는 body를 연결하지만 경로 값과 여러 입력의 결합은 handler가 반복해서 작성한다.
Article 수정은 대상 조회를 먼저 수행해 없는 대상의 잘못된 body에도 404를 반환하고, Helpdesk 수정은 body 검증 뒤
대상을 조회한다. 이 외부 계약을 하나의 무조건적인 parsing 순서로 바꾸지 않는다.
같은 route compiler의 int64/str 값을 typed 입력으로 옮기고 명시한 단계 순서로 기존 binder와 업무 준비를 결합한다.

## 구현 경계

- Route grammar·변환·byte 한도와 path schema는 Web/OpenAPI가 계속 소유한다. 문자열 경로를 다시 decode하거나
  body/query codec으로 재파싱하지 않는다. 선언한 이름/종류·중복·누락은 초기화에서 검사한다.
- Sequence는 source별 닫힌 parser와 schema를 결합한다. 동일 body의 이중 읽기, 독립 query parser 충돌과
  NoQuery/parameter 충돌을 준비 때 거부한다. Shared Input의 component identity는 조합/준비 단계를 넘어 보존한다.
- Resolve는 앞선 typed 입력을 검증한 뒤 명시적인 업무 조회/준비를 수행한다. Admitted Principal과 같은 request/context를
  전달한다. 초기화에서는 실행하지 않으며 앞선 오류/취소에는 뒤 단계·body·handler를 실행하지 않는다.
  Callback은 요청을 보관하거나 body를 소비하지 않는다. 입력 준비에서 쓰기를 확정하지 않으며 transaction은 최종 업무가 소유한다.
- 전체 입력이 준비되기 전에는 handler에 부분 결과를 넘기지 않는다. 같은 Spec의 full/partial·부재/null과 진단을 보존하고
  Go 타입이 다른 단계의 연결은 compile로 거부한다. Callback의 순수성/동시 사용과 I/O 책임을 명시한다.
- 실제 소비자는 Article/Helpdesk 수정의 서로 다른 검증 순서를 사용한다. Typed 출력으로 옮기는 쓰기는 encode 시점을
  commit 앞으로 연결하며 확인된 성공과 pre-commit 취소·rollback/commit 불확실성을 구분한다.

Header 입력·배열 body·자동 CRUD/viewset·출력 union과 임의 route converter는 이 단면의 완료 범위가 아니다.
장기 의미는 기존 [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md), 환경별 실행은
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 진행

- [x] 경로 compiler/accessor와 Article/Helpdesk의 parsing/lookup/commit 순서 확인
- [x] Typed 경로·순서 결합·명시적 준비와 schema identity/한도 구현
- [x] 실제 두 소비자·body/출력·transaction 경계 연결
- [x] 외부 compile·실제 HTTP·권한/CSRF·양 DB·독립 client/drift·취소/실패와 영향 검증
- [x] source와 후속 통합 범위 기록
- [ ] GDJ-0116과 같은 source의 전체 platform/Hosted 통합

GDJ-0117 source `55e0453d`의 영향 세 mode·양 DB·외부 compile·독립 client/drift·고정 Article API 대조와 정리를 완료했다.
GDJ-0116 source `18fc0451`은 영향 세 mode·양 DB·외부 compile·독립 client/drift와 정리를 완료했다. 선행 Hosted full `37569379536`은
`def77d5e`의 GDJ-0112/0113/0114/0115만 검증하며 이 작업에 전이하지 않는다. 현재 외부 입력이 필요한 blocker는 없다.

후속 전체 통합은 source `63478ee6`의 [Hosted full 37579524401](https://github.com/progresshans/godj/actions/runs/37579524401)에서 진행 중이다.
