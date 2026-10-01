---
id: GDJ-0071
status: complete
updated: 2026-09-12
baseline_commit: "f7db3ed1ab0e7fcdedd9f4af0c1f760893bad823"
integration_owner: "root"
---

# API schema 정체성과 실제 생성 클라이언트 기반

## 목표와 우선순위

사용자는 빠른 MVP보다 필요한 기반을 먼저 꼼꼼하게 구현하도록 요청했다. Article에 추가한 OpenAPI를 관계가 있는
Helpdesk와 실제 생성 Go client까지 연결하면서, runtime 정책·schema identity·생략/null/int64와 재현 가능한 생성의
기반을 정리한다. 단면을 빨리 보이기 위해 protocol 의미를 줄이거나 기존 동작을 변경하지 않는다.

직전 조사와 GDJ-0070 구현은 위 baseline 로컬 commit에 보존했다. 이 작업의 공개 경계는
[개발 기준](../docs/DEVELOPMENT_CRITERIA.md)과 [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md)을 따른다.

## 범위와 소유권

- 실제 JSON subtree 정책을 middleware와 문서에서 공유한다. Helpdesk의 미설정 negotiation을 406으로 광고하지 않는다.
  Article의 기존 negotiation·routing error·권한 우선순위는 유지한다. 이 기반과 통합 document 변경은 root가 소유한다.
- 이름 있는 schema와 local `$ref`를 제공한다. 명시적 타입 정체성을 유지하고 중복·미해결·cycle·지원하지 않는 참조를
  생성 전에 거부한다. 자동 inline 확장·구조별 중복 제거·원격 참조는 도입하지 않는다. Schema/reference 파일은 독립 담당이 소유한다.
- Helpdesk는 한 번 구성한 API가 routes와 문서를 제공한다. input/output Spec을 실제 encoder와 공유하고 선택 category,
  bare list와 nested detail, 권한·parser 제한·transaction 의미를 유지한다. Helpdesk 파일은 독립 담당이 소유한다.
- 고정된 `ogen`으로 Article/Helpdesk client를 생성하고 별도 Go module에서 compile·실제 HTTP로 사용한다.
  생성 도구·client runtime 의존성은 framework runtime module과 분리한다. 생성물·설정·lock과 원본 문서의 일치를 검사한다.
- 전체 상태·현행 문서·검증 기록은 root만 갱신한다. 새 scalar/model field·scaffold·Helpdesk PATCH·로그인 체계·SDK 전체
  언어 지원은 이 작업에 섞지 않는다.

## 확인할 계약

- Named schema의 결정성, immutable 입력·출력, local ref와 reference graph의 명시적 한계
- 실제 negotiation 적용 여부와 경로에 따른 406 문서의 일치, 기존 Article와 Helpdesk HTTP 동작 보존
- Article PATCH의 omitted/null/empty/false와 full request default, request/response의 readonly·필수·closed shape 차이
- 생성 Go client의 int64 최댓값과 overflow·잘못된 응답 거부, 입력 타입의 생략/null/value 구분
- 실제 Session cookie·CSRF token과 Bearer 전송, 권한 선행·취소·오류 응답·누락 필수 검증 거부
- Helpdesk category가 서버에 의해 선택됨, 다른 category ticket 비공개, 관계 조회와 기존 DB 데이터 보존
- 동일 schema/config/generator/lock의 결정적인 생성과 drift, 실제 외부 module build·테스트 실패의 전파

## 검증과 진행

- [x] JSON policy와 named schema/local reference 기반
- [x] Helpdesk composition과 Article named schema 적용
- [x] 생성 client와 재현 가능한 별도 module 검증 연결
- [x] 관련 normal/race/CGO-disabled, SQLite/PostgreSQL과 client HTTP 회귀
- [x] 외부 OpenAPI/schema validation·generated drift·현행 문서·독립 리뷰

제품·test·생성 묶음의 compile 이후 normal/race/CGO-disabled와 전용 SQLite/PostgreSQL, 실제 외부 client HTTP를 통합 검증했다.
생성 파일 집합·내용과 실제 문서의 일치, 외부 validator, CI 분류/의존성 준비, export와 문서 절차도 확인했다.
전체 platform/Hosted는 이번 작업에서 실행하지 않았다. 실행 횟수·최종 source와 구체적인 결과는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 둔다.
