# 현재 상태

- 갱신: 2026-10-08
- 현재 작업: [GDJ-0119 Typed header와 Identity revision 조건](../../work/0119-typed-header-inputs.md)
- 검증 운영: PR 생성·push·재개와 main push에서 [전체 원격 CI](../TESTING.md)를 자동 실행한다. 로컬은 포맷·최소 compile와 실패 재현을 맡는다.
- 최근 영향 검증 완료·후속 통합 대기: [GDJ-0118 Typed JSON 배열과 원자적 여러 건 생성](../../work/0118-typed-json-collections.md)
- 영향 검증 완료·전체 통합 재검증 대기: [GDJ-0117 Typed 경로와 단계 입력](../../work/0117-typed-path-and-ordered-inputs.md), source `55e0453d` 및 인증 관찰자/기준 잠금 보정 `587aeea0`
- 후속 전체 통합 대기: [GDJ-0116 Typed endpoint](../../work/0116-typed-endpoint-declarations.md), source `18fc0451`의 영향 세 mode·양 DB/client·drift/정리 완료
- 최근 통합 완료: [GDJ-0112 그룹 집계](../../work/0112-grouped-aggregation-and-ticket-summary.md), [GDJ-0113 Typed 출력](../../work/0113-typed-api-response-shapes.md), [GDJ-0114 Typed query 입력](../../work/0114-typed-query-parameters.md), [GDJ-0115 Typed JSON 입력](../../work/0115-typed-json-body-inputs.md)
- GDJ-0116/0117 Hosted 보정: [37579524401](https://github.com/progresshans/godj/actions/runs/37579524401)의 인증 관찰자 실패를 source `f45eb512`에서 수정하고 영향 검증 완료
- 기준 잠금 보완: [37581945355](https://github.com/progresshans/godj/actions/runs/37581945355)의 checksum 누락을 source `587aeea0`에서 고치고 영향 검증 완료
- 보완 source의 Hosted full 실패: [37584339052](https://github.com/progresshans/godj/actions/runs/37584339052), source `6d0473cdf9d649158e5fcd731cbf1a43a0261211`의 macOS Intel 관계 normal job
- 최근 Hosted full 완료: [37569379536](https://github.com/progresshans/godj/actions/runs/37569379536), source `def77d5e1c949a87d538181d05a72a3c75e97201`
- 보정 전 원격 CI: [37656685420](https://github.com/progresshans/godj/actions/runs/37656685420), source `0b2816efed4f6d04e671781401dfea02187687a3` (Identity 테스트 namespace 실패)
- Source·환경·실행 상세와 미완료 근거: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

[GDJ-0119](../../work/0119-typed-header-inputs.md)는 typed header·endpoint 입력 결합과 Identity의 revision 조건을 구현했다.
원격 Linux/amd64에서 typed parameter/OpenAPI/endpoint와 독립 SDK의 normal/race/CGO0·재생성 drift·실제 HTTP를 확인했다.
원격에서 발견한 Identity header 테스트의 namespace 설정 오류를 보정했고 해당 두 root의 90 run/pass를 로컬에서 확인했다.
선행 Intel Mac의 누적 package 시간 초과에 대해 필수 검사를 유지한 normal 실행 분할도 준비했다.
수정 source의 Identity native 양 DB·platform/cold와 최종 집계는 새 자동 CI에서 확인한다.
선행 [배열 입력](../../work/0118-typed-json-collections.md)의 영향 검증과 취소된 source `632cf6db`의 CI는
현재 source의 실행 결과와 구분한다.
선행 endpoint/경로 source의 Hosted full 실패는 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록했다.
PR 자동 CI에서 현재 source의 전체 통합을 확인하며 이전 source의 부분 성공과 구분한다.
구현과 환경별 실행 완료를 구분하며 결과는 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록한다.

## 다음 행동

Header를 포함한 PR source의 자동 CI에서 필수 job·capture·최종 집계를 확인하고 실패한 검증을 보정한다.
기존 428/400/412 의미와 인증·경로/query·header·body·DB 순서를 실제 소비자와 독립 client로 확인한다.
Codec 특성/storage provider·custom user model·인증/mail provider와 나머지 카탈로그 기능도
의존 순서에 따라 계속 구현한다. 현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다.
전체/cold 검증은 PR과 main의 원격 CI가 소유한다. 장기 목표는 [헌장](../CHARTER.md)과
[기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
