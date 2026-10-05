# 현재 상태

- 갱신: 2026-10-06
- 현재 작업: [GDJ-0114 Typed query 입력과 parameter 선언](../../work/0114-typed-query-parameters.md)
- 통합 대기: [GDJ-0112 그룹 집계](../../work/0112-grouped-aggregation-and-ticket-summary.md), [GDJ-0113 Typed 출력](../../work/0113-typed-api-response-shapes.md)
- 최근 Hosted full 완료: [37365281161](https://github.com/progresshans/godj/actions/runs/37365281161), source `720c9be6211f00a146a39d00957a81ce69ed294f`
- 최근 완료: [GDJ-0111 QuerySet 갱신](../../work/0111-query-update-and-writable-expressions.md); GDJ-0109/0110도 같은 source에서 통합 완료
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

그룹 집계와 현재 Category의 업무 요약을 구현하고 기준 대조·영향 세 mode·브라우저·정적 검사·DB 정리를 완료했다.
업무 응답의 JSON 조립과 OpenAPI에서 같은 property·nullable·한도를 중복 선언하는 부분을 typed 출력 선언으로 연결했다.
모델 응답은 기존 IR/encoder 의미를 재사용하고 인가·조회·transaction 경계는 각 업무가 소유한다.
Typed 출력과 실제 요약·상세 API/client의 영향 세 mode·외부 compile·정적 검사·정리를 완료했다.
요약과 Label/티켓–Label 목록의 query 변환·범위·기본값·OpenAPI parameter 중복을 typed 입력 선언으로 연결했다.
실제 문서/고정 client의 재생성과 영향 세 mode·양 DB·독립 HTTP/compile·drift와 정리를 완료했다.
선행 bulk 생성·수정·QuerySet 갱신은 같은 source의 Hosted 전체 통합을 완료했다.
그룹 집계와 typed API는 그 source 이후 구현이며 선행 성공을 전이하지 않는다.

## 다음 행동

Typed query source 발행을 마쳤다. 다음으로 JSON body의 context 취소 경계를 보완하고,
기존 모델 Serializer의 full/partial 결과를 typed DTO·부재/null·같은 schema에 연결한다.
Codec 특성/storage provider·custom user model·인증/mail provider와 나머지 카탈로그 기능도
의존 순서에 따라 계속 구현한다. 현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다.
전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. 장기 목표는 [헌장](../CHARTER.md)과
[기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
