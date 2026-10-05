# 현재 상태

- 갱신: 2026-10-06
- 현재 작업: [GDJ-0113 Typed API 응답과 출력 schema](../../work/0113-typed-api-response-shapes.md)
- 통합 대기: [GDJ-0109 Native bulk 생성](../../work/0109-bulk-creation-and-ticket-import.md), [GDJ-0110 Native bulk update](../../work/0110-bulk-update-and-ticket-editing.md), [GDJ-0111 QuerySet 갱신](../../work/0111-query-update-and-writable-expressions.md), [GDJ-0112 그룹 집계](../../work/0112-grouped-aggregation-and-ticket-summary.md)
- 진행 중인 Hosted full: [37365281161](https://github.com/progresshans/godj/actions/runs/37365281161), source `720c9be6211f00a146a39d00957a81ce69ed294f`
- 최근 완료: [GDJ-0108 행 잠금과 조회 후 생성 또는 갱신](../../work/0108-row-locking-and-update-or-create.md)
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

그룹 집계와 현재 Category의 업무 요약을 구현하고 기준 대조·영향 세 mode·브라우저·정적 검사·DB 정리를 완료했다.
업무 응답의 JSON 조립과 OpenAPI에서 같은 property·nullable·한도를 중복 선언하는 부분을 typed 출력 선언으로 연결했다.
모델 응답은 기존 IR/encoder 의미를 재사용하고 인가·조회·transaction 경계는 각 업무가 소유한다.
Typed 출력과 실제 요약·상세 API/client의 영향 세 mode·외부 compile·정적 검사·정리를 완료했다.
후속 구현은 진행 중인 Hosted source와 분리하며 그 실행을 새 기능의 검증으로 계산하지 않는다.

## 다음 행동

다음 typed 입력 연결을 실제 parameter·검증·OpenAPI의 중복 선언에서 구체화한다.
선행 full의 runner 실패를 분리하며 필수 owner·실제 실행·새 capture의 source 결합/소비·최종 집계를 확인한다.
Codec 특성/storage provider·custom user model·인증/mail provider와 나머지 카탈로그 기능도
의존 순서에 따라 계속 구현한다. 현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다.
전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. 장기 목표는 [헌장](../CHARTER.md)과
[기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
