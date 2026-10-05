# 현재 상태

- 갱신: 2026-10-06
- 현재 작업: [GDJ-0111 QuerySet 갱신과 쓰기 표현식](../../work/0111-query-update-and-writable-expressions.md)
- 통합 대기: [GDJ-0109 Native bulk 생성](../../work/0109-bulk-creation-and-ticket-import.md), [GDJ-0110 Native bulk update](../../work/0110-bulk-update-and-ticket-editing.md)
- 진행 중인 Hosted full: [37359348832](https://github.com/progresshans/godj/actions/runs/37359348832), source `0f9cdb8386c9b9e4ec4599c698cfd311c80dd4a6`
- 최근 완료: [GDJ-0108 행 잠금과 조회 후 생성 또는 갱신](../../work/0108-row-locking-and-update-or-create.md)
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

QuerySet의 native 갱신과 scalar expression 기반을 구현하고 실제 양 DB·세 mode·독립 생성 소비자에서 검증했다.
FK typed field와 이전 in-flight 읽기의 값/오류가 새 cache generation에 섞이지 않는 경계도 반영했다.
Helpdesk 우선순위 명령·Admin/API/독립 client를 연결했고 양 DB·세 mode와 실제 브라우저를 확인했다.
선행 Hosted의 macOS Intel 명령 fixture 준비 실패 뒤 준비 예산·진단을 보완했고, 통합한 실제 외부 명령도 확인했다.
새 source의 Hosted 전체 검증은 진행 중이다.

## 다음 행동

새 full의 필수 owner·실제 실행·새 capture의 source 결합/소비·최종 집계를 확인한다.
확장 query, codec 특성/storage provider·custom user model·인증/mail provider와 나머지 카탈로그 기능도
의존 순서에 따라 계속 구현한다. 현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다.
전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. 장기 목표는 [헌장](../CHARTER.md)과
[기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
