---
id: GDJ-0119
status: complete
updated: 2026-10-08
baseline_commit: "632cf6db3feafd9622d230170e8eddee73a2545a"
integration_owner: "root"
---

# Typed header와 Identity revision 조건

## 문제와 결과

Identity의 User/Group/Permission 수정·삭제와 password 명령은 같은 If-Revision을 직접 읽고,
OpenAPI에서는 별도로 int64 범위·필수 여부를 선언한다. Scalar/presence의 닫힌 입력으로 두 선언을 연결한다.
Header는 URL query decoding과 다른 wire source이며 인증·CSRF와 전체 HTTP header admission은 기존 transport가 소유한다.

## 구현 경계

- 하나의 typed Header가 선언한 이름·같은 scalar codec·required/default/optional·값 byte 예산과 schema를 소유한다.
  이름은 대소문자와 무관하게 정확히 한 map entry/한 value여야 한다. 중복 value·case alias를 합치거나 comma로
  나누지 않는다. HTTP가 전달한 값을 trim/URL decode하지 않고 client 오류는 값 없는 진단으로 반환한다.
- Query와 header가 같은 정수/문자열 변환을 사용하되 lexical metadata와 header control 제한은 wire source에 맞춘다.
  준비된 선언/metadata는 불변이며 Optional 값과 문자열 결과를 요청마다 소유한다. Zero/잘못된 선언은 준비 또는 사용 때 오류다.
- Endpoint.Header는 같은 parameter를 Sequence/NoQuery/Resolve와 결합한다. 같은 이름의 header 중복과 인증/CSRF/
  representation 소유권 충돌은 초기화에서 거부한다. Header와 query의 같은 이름은 서로 다른 source다.
- Identity는 Required canonical 1..MaxInt64-1과 19-byte 값을 사용한다. 누락 428/precondition_required,
  빈 값·중복·비정규 표기 400/invalid_precondition, 업무의 오래된 revision 412를 유지한다. Missing을 optional schema로
  속이지 않는다. Authentication/CSRF → path/query → header → body → manager 순서와 현재 권한/write fence를 보존한다.
- 공개 API의 compile·런타임/metadata와 실제 Session/Bearer HTTP, 양 DB의 무변경/수정/revision/취소·독립 생성 client와
  전체 OpenAPI drift를 확인한다. Identity 전체 endpoint/transaction API의 전면 전환은 이 작업의 선행 조건이 아니다.

Cookie·반복/list header, arbitrary decoder/schema 쌍, 자동 CRUD/viewset은 별도 요구다. 장기 의미는 기존
[ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md), 환경별 실행은
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 둔다. 완성한 변경을 PR에 push하면 같은 source의 전체 원격 CI를 자동 실행한다.

## 진행

- [x] Identity의 현재 header 문법·상태·admission/body/DB 순서와 OpenAPI 소유권 확인
- [x] Typed header scalar/presence와 같은 parameter·endpoint 결합 구현
- [x] Identity 실제 소비자·generated client와 실패 경계 연결
- [x] 보정 source `cc2c2f25`의 원격 compile/HTTP/양 DB·세 mode·drift와 source 기록
- [x] GDJ-0118/0120과 결합한 source의 전체 platform/Hosted 통합

Source `b14d0eaf3d49f01151c8b010fb97eac2a57532ef`의
[전체 CI 37692593301](https://github.com/progresshans/godj/actions/runs/37692593301) attempt 1에서
GDJ-0116/0117/0118/0119/0120을 함께 검증했다. 필수 실행과 no-skip, 실제 checkout/tree,
생성 drift·양 DB·세 모드·각 platform·same-run capture·최종 집계를 대조해 전체 통합을 완료했다.
선행 실패와 보정, 상세 실행은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 둔다.
후속 GDJ-0121의 새 구현과 전체 카탈로그 완성은 이 완료에 포함하지 않는다.
