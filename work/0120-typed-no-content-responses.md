---
id: GDJ-0120
status: active
updated: 2026-10-08
baseline_commit: "cc2c2f2564bfca11955ca1baa0d047c8f1555b21"
integration_owner: "root"
---

# Typed 204 응답과 삭제 endpoint

Article와 Helpdesk Label 삭제는 실제 204 응답과 OpenAPI를 따로 연결한다. Typed endpoint가 JSON 응답만
허용해 기존 경로/입력·권한·준비 응답의 공통 선언을 사용할 수 없다. 닫힌 NoContent Output을 추가하고
두 삭제 소비자를 연결한다. 모델/관계 삭제와 인가·transaction·audit의 원본은 기존 업무에 둔다.

## 구현 경계

- NoContent는 `Output[struct{}]`의 명시적 204 응답 선언이다. JSON Shape나 가짜 schema를 만들지 않는다.
  같은 Output의 immutable Prepared만 반환하며 별도 선언·JSON 출력과 혼용하지 않는다.
- Context를 확인해 본문 없는 응답을 준비한다. 기본 header는 비어 있고 명시적인 resource metadata는 허용한다.
  Content-Length·Transfer-Encoding·Trailer는 거부한다. JSON Encode/JSON helper로 사용하면 명시적인 오류다.
  Endpoint는 같은 선언에서 content 없는 204를 기술한다.
- 기존 404·권한/CSRF·관계 범위·오류와 삭제 결과를 유지한다. 고정 응답은 삭제 전에 준비하고,
  성공한 삭제/commit을 확인한 뒤에만 게시한다. 실패·취소·unknown에는 준비된 응답을 버리며 자동 재시도하지 않는다.
- Article 삭제는 기존 prepared transaction owner로 callback 완료와 mutation hook을 확인한다. 확인된 missing은
  직접 404로 분류할 수 있게 보존하며 wrapped/joined 실패는 풀지 않는다. 기존 validation 400을 두 operation에 명시한다.
- 같은 Repository를 쓰는 Article Admin도 확인된 missing만 직접 `admin.ErrObjectNotFound`로 반환한다.
  Site 조회 뒤 대상이 없어지는 수정/삭제와 오류 은폐·wrapped/joined 실패·unknown commit의 HTTP/저장/audit를 함께 검사한다.
- 공개 compile-positive/negative·output/endpoint 소유권, 실제 Session/Bearer와 양 DB의 관계/audit·취소/unknown,
  실제 문서·생성 client drift와 HTTP를 함께 확인한다. 전체 실행은 원격 CI가 소유한다.

204의 본문/trailer와 Content-Length 정책은 [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.3.5),
Transfer-Encoding은 [RFC 9112](https://www.rfc-editor.org/rfc/rfc9112.html#section-6.1)를 따른다.
205·streaming·여러 종류의 성공 DTO와 자동 viewset은 별도 요구다. 기존 [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md)에
장기 의미를, [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 실행 결과를 기록한다.

## 진행

- [x] 기존 두 삭제 handler와 typed Output/Endpoint의 제한 확인
- [x] 닫힌 NoContent declaration/Prepared·OpenAPI·오류/소유권 연결
- [x] Article·Label 삭제의 실제 소비자와 실패 경계 연결
- [ ] 공개 compile·실제 HTTP/DB/client·drift와 원격 검증

선행 GDJ-0119 source `cc2c2f25`의 부분 성공과 이 작업의 새 검증 대상을 구분한다.
Intel CGO0 분할 보완을 포함한 PR source의 자동 전체 CI가 이번 변경의 실행을 검증한다.
