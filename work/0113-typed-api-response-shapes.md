---
id: GDJ-0113
status: active
updated: 2026-10-06
baseline_commit: "2be7433d0d310ee0c8cbc39bee09670285bc7407"
integration_owner: "root"
---

# Typed API 응답과 출력 schema의 결합

## 문제와 결과

Helpdesk의 집계 응답은 같은 property 이름·nullable·배열/정수 한도를 OpenAPI 선언과 JSON 조립에 각각 적는다.
모델 기반 응답은 IR에서 의미를 가져오지만 모델이 아닌 업무 결과와 중첩 객체에서는 같은 연결을 다시 만든다.
[개발 기준](../docs/DEVELOPMENT_CRITERIA.md)에 따라 Go 값에서 공개할 필드를 타입으로 연결하고 하나의 불변
출력 선언에서 JSON 검증·출력과 OpenAPI를 유도한다. 모델 의미는 기존 ModelEncoder와 Schema IR에서 가져온다.

실제 소비자는 우선순위 업무 요약과 모델을 포함한 티켓/Category 상세 응답이다. 공개 필드, NULL·기존 값·정수/배열
범위, 이름 있는 schema와 기존 client의 외부 동작을 보존한다. 인가·Category 범위·transaction·오류를 임의로
추론하거나 출력 검증을 이미 확정한 쓰기 뒤로 옮기지 않는다. 원래의 명시적 handler와 JSON 응답도 계속 사용할 수 있다.

## 설계 경계

Go getter/DTO 타입과 출력 타입의 잘못된 연결은 compile 단계에서 거부한다. 중복 이름·nil reader·zero codec·
잘못된 한도/이름·schema 연결은 시작 시 명시적 오류다. 출력은 입력용 trimming/default/choices 검증을 재실행하지
않으며 required·omitted·nullable 의미를 섞지 않는다. 공개하지 않은 모델 필드나 arbitrary struct reflection으로
출력 필드를 늘리지 않는다. 명시한 출력 필드는 getter와 schema가 같은 선언을 공유한다.

출력 값과 선언의 ownership, context·전체 byte/depth/value 예산, 배열/객체의 중첩과 오류 위치를 함께 정한다.
오류에는 정상 부분 JSON·응답을 반환하지 않는다. 기존 serializer의 exact int64/decimal/JSON·codec 정책과
OpenAPI component identity·cycle/resource 검사, 실제 authentication operation의 소유권을 재사용한다.
추가 primitive/입력 binding·endpoint DSL·viewset·다른 client 언어는 구현한 것으로 계산하지 않는다.

이 작업은 Django의 새 외부 동작을 채택하지 않는다. 기존 model serializer의 고정 계약, OpenAPI와 실제 HTTP/client
비교, Go 타입/ownership/자원 한도를 기준으로 한다. `api/output`의 닫힌 Shape/Property/Output과 기존 writer를 공유하는 lazy Projection을 실제 소비자로 확인했다.
장기 결정은 [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md)에 반영한다.

## 구현과 검증

- [x] 현행 집계/중첩 응답의 수동 property·schema·JSON 연결과 model encoder 소유권 확인
- [x] 하나의 출력 선언, typed getter·중첩/배열/nullable·기존 model encoder 연결과 오류/예산
- [x] 실제 Helpdesk 요약/상세 API를 같은 출력 계약으로 연결하고 선언 중복 제거
- [x] 독립 compile 거부·runtime/schema·실패/취소/복사/동시성 대조와 실제 client
- [x] 필요한 영향 세 mode·양 DB·drift와 현행 의미/실행 증거 기록
- [ ] GDJ-0112와 연결한 후속 source의 통합 milestone 전체 platform/Hosted 확인

선행 Hosted full [37365281161](https://github.com/progresshans/godj/actions/runs/37365281161)은
`720c9be6211f00a146a39d00957a81ce69ed294f`만 검증하며 이 작업이나 GDJ-0112에 전이하지 않는다.
GDJ-0112와 이 기반을 연결한 source의 전체 platform 검증은 다음 통합 milestone에서 소유한다.
로컬은 관련 package/소비자의 영향 검사와 필요한 DB·race·CGO=0을 사용하고 전체/cold를 중복하지 않는다.
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md), 활성 상태는 [CURRENT](../docs/status/CURRENT.md)에 둔다.
제품·소비자와 영향 검증은 완료했고 후속 source의 통합 milestone은 남아 있다. 현재 외부 입력이 필요한 blocker는 없다.
