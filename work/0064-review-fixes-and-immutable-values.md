# GDJ-0064: 추가 결함 수정과 불변 값의 복사 정리

- 상태: 구현 및 간단한 로컬 검증 완료. 전체 CI·DB·race·platform은 후속 통합 범위다.
- 기준: `863724a06cd6fe75d1b8f6fe3a23b9cf10c8ec1d` 위 작업 사본.
- 사용자 범위: 2026-09-08 추가 검토 항목을 수정한다. GitHub CI까지 전부 확인하지 않고 간단히 로컬 확인만 한다.
- 실행 결과의 정본: [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0064--추가-결함-수정과-불변-값의-복사-정리).

## 구현 범위

- [x] 정상 signed CSRF pair라도 다른 browser origin의 변경 요청을 거부한다. 익명 발급·session rotation·key ring은 유지한다.
- [x] ORM panic 경로의 rows 정리와 evaluation flight 해제를 분리해 보장한다. 성공한 전체 결과만 cache한다.
- [x] nil-header cookie 적용을 처리하고 Response.WithHeaders로 status·body·routing origin을 보존한다.
- [x] static/parameter Reverse의 percent·공백·Unicode literal과 escape 후 resource cap을 처리한다.
- [x] single-file writer가 검증 후 취소를 publication 전에 다시 확인한다.
- [x] CI consumer retry가 같은 run의 확인된 성공 producer artifact/attempt를 사용한다. source·payload 검증은 유지한다.
- [x] 미사용 함수 17개를 제거하고 활성 distinct-process handler/registry/회귀 범위를 보존한다.
- [x] serializer 불변 값과 Limit/Offset/Distinct의 반복 복사를 줄인다.
- [x] app별 normalized schema+hash를 생성 호출 안에서 공유한다. 생성 결과의 결정성과 오류 경계를 유지한다.

Attestation의 source 정책과 Python oracle classifier는 서로 다른 의미를 유지한다. 겉모양이 비슷하다는 이유로 공통화하지 않는다.
새로운 범용 relation/query 기능과 production 배포는 이 작업 범위에 포함하지 않는다.

## 검증 경계

수정 패키지의 핵심 회귀·기존 golden 비교와 영향받은 소비자 compile을 로컬에서 확인한다. 전체 make ci,
make generate-check, codegen consumer/process matrix, PostgreSQL·race·CGO-disabled·Hosted 검증은 이번 완료 조건이 아니다.
이전 고정 소스의 full-scope 성공을 이번 작업 사본의 성공으로 기록하지 않는다.
