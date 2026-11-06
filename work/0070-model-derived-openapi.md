---
id: GDJ-0070
status: complete
updated: 2026-09-12
baseline_commit: "6d30973ae3034e16dc56b9d2b9e0a6faffe1fb49"
integration_owner: "root"
---

# 모델과 실제 API 선언에서 OpenAPI 제공

## 결과와 범위

Article의 기존 JSON API를 사용할 client가 모델에서 파생한 요청·응답 schema와 실제 route·인증 정보를
OpenAPI 3.1 문서로 조회할 수 있게 한다. 같은 operation 선언이 실행 route와 문서에 사용된다.
GET `/api/openapi.json`은 기존 authenticated loopback 구성에서 Article view 권한으로 제공한다.

모델 의미는 기존 serializer의 IR projection을 재사용한다. Full/partial 요청과 ModelEncoder 응답을 구분하고
현재 body parsing·대상 확인·권한·transaction 순서는 변경하지 않는다. 문자열 trim 이후 제약과 정수 lexical·byte
제한처럼 JSON Schema만으로 동치 표현하지 못하는 부분은 명시적인 설명·확장 metadata로 기록한다.

첫 범위에는 schema 투영, immutable operation/document, 실제 Session/Bearer의 공개 인증 metadata,
Article 게시와 HTTP client 검증을 포함한다. HTML 문서 UI·생성 SDK·generic binder·DTO codegen·새 DB 기능은 별도다.
이전 요청의 조사·기준 문서 변경을 보존한다. Root는 통합·document·auth metadata·상태를,
병렬 구현은 schema projector와 Article 소비자 파일을 각각 소유한다.

기준: [개발 기준](../docs/DEVELOPMENT_CRITERIA.md), [호환성](../docs/COMPATIBILITY.md),
[API 의미](../docs/adr/0046-json-serializer-and-session-authenticated-article-api.md),
[인증 profile](../docs/adr/0049-first-party-bff-and-bearer-api-authentication.md).

## 구현과 검증

- [x] Schema/operation/document와 공개 auth metadata, 실제 Article 게시
- [x] Request/full/partial/response 투영·중복/잘못된 metadata·결정성·공유 격리 회귀
- [x] 기존 Article CRUD·오류 우선순위·Session/CSRF/Bearer와 실제 schema 소비자 HTTP 확인
- [x] 관련 normal/race/CGO-disabled·독립 OpenAPI validator·현행 문서 정리

검증은 API·serializer·Web auth·Article affected 범위가 소유한다. 새로운 DB·ORM·generated ABI 변경이 없으면
전체 로컬/Hosted platform을 이 작업의 중복 gate로 삼지 않는다. 외부 validator는 별도 격리된 개발 도구로
사용하고 Django oracle dependency를 변경하지 않는다. 실제 실행과 미실행 범위는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 남긴다.

## 현재 상태와 다음 행동

구현·독립 리뷰·관련 로컬 검증을 완료했다. [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md)에
투영·operation·인증 metadata의 소유권을 기록했다. 실제 실행은 TEST_EVIDENCE를 따른다.
OpenAPI 문서는 parser·인가·DB 불변조건의 대체 검증이 아니다. 후속 consumer 요구는 Helpdesk 같은 구조가 다른 API와
typed client 연결에서 선택하며, 이번 작업을 생성 SDK·범용 viewset·전체 platform 완료로 확장 해석하지 않는다.
