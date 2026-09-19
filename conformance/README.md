# Conformance

Conformance는 고정한 reference와 GoDj의 독립 실행 결과를 비교한다. Python 내부 구조를 재현하는 테스트가 아니다.

## 데이터와 실행의 소유권

| 경로 | 역할 |
|---|---|
| [contracts](contracts/) | contract ID, 비교 dimension, 실행 상태와 provenance |
| [profiles](profiles/) | 정확한 Django/DRF/Python/DB/locale 환경 |
| [runners/django](runners/django/) | Django 관찰과 명시적 GoDj decision reference |
| [oracles](oracles/) | 재현 가능한 기준 observation |
| [runners/godj](runners/godj/) | 실제 public/runtime 경로를 실행하는 independent actual |
| [internal/protocol](internal/protocol/) | strict payload validation, normalization/comparison, 제한된 deviation |
| [fixtures](fixtures/) | 명시적 not-implemented observation과 reviewed sparse difference |
| 제품 scenario package | 실제 외부 module/CLI/DB/process 흐름 |

Profile이 Django 이름을 가졌다고 모든 payload가 Django 동작은 아니다. GoDj 고유 format·CLI·resource 정책은 decision
provenance를 갖는다. Contract에 없는 extra diagnostic을 제품 동작처럼 comparator에 넣지 않는다.
MIG-112..115의 reference는 portable result만 비교하며, durable no-mutation과 fresh-process proof는 SQLite/PostgreSQL
제품 테스트가 소유한다.

Django와 GoDj producer는 oracle·expected·not-implemented fixture를 읽어 actual을 만들지 않는다.
서로 다른 source나 environment의 결과는 현재 실행처럼 합치지 않는다. Byte identity는 reference integrity와 deterministic
encoding에 사용하고 모든 테스트 이름·개수를 영구 고정하는 용도로 사용하지 않는다.

## 비교와 실패

Normalizer는 허용한 표현 차이를 정규화하고 결과 순서·NULL·error·DB mutation을 지우지 않는다.
Comparator negative control은 각 declared dimension의 실제 변경을 감지해야 한다. Static not-implemented observation은 유효한
protocol일 수 있지만 실제 reference와 비교하면 실패해야 한다.

Deviation은 [현재 원장](../docs/DEVIATIONS.md)의 contract/selector 범위로 제한한다. DEV-0002의 app-zero reverse sibling은
Django의 `B1, A3, A2, A1`과 GoDj의 canonical `A3, A2, B1, A1` 순서가 다를 수 있다. 비교 불가능한 sibling의 차이를
기록할 뿐 dependency order·durable prefix·결과 state를 무시하지 않는다. 이 구분은 prose 유무 대신 reference와 sparse
expectation의 실제 값을 검사한다.

Reference-only MIG-075..086은 제품 registry에 등록되지 않은 진단이다. Oracle가 있거나 파일이 compile된다는 이유로 passing으로
승격하지 않는다. 테스트 삭제·통합 시 어떤 실제 위험이 다른 test에서 보존되는지 확인한다.

## 실행

[TESTING](../docs/TESTING.md)이 빠른 feedback, 관련 integration, CLI/process, reference와 전체 platform의 실행 시점을 설명한다.
명령과 source의 실행 결과는 [Evidence](../docs/status/TEST_EVIDENCE.md)에 기록한다.

- Product scenario는 사용자 API, 별도 module과 실제 child/DB를 사용한다. Test-local counters만으로 process 종료·restart를 대신하지 않는다.
- DB/schema/temp/port는 lane마다 분리한다. 필요한 모듈·build cache는 재사용하되 격리 자체를 검사하는 경계는 유지한다.
- Source-bound PostgreSQL producer는 해당 source/observer/profile의 실제 scenario를 실행한다. Consumer는 같은 신뢰된 실행의
  artifact identity·digest·required scenario를 검증한다. Stale actual은 fail-closed다.
- Job/selector가 필수 경로를 누락했거나 skip했거나 JSON 로그가 잘렸으면 그 scope의 PASS가 아니다.

## 과거 실험

초기 codegenbootstrap, relationbinding, lifecyclefence, projectcheck 모형은 실제 생성기·프로젝트 publication·ORM/DB 검증으로 대체했다.
과거 코드는 [기준 Git 트리](https://github.com/progresshans/godj/tree/da1bfc524c4f205075fc7fac7f00b437473a5e1f/conformance)에 있다.
검증 위험의 현재 소유자는 활성 work에 기록한다. 경쟁 commit 뒤 durable prefix와 recorder 변조 rollback은 실제 SQLite 회귀로 이관했다.
고유한 결정 이유는 현행 아키텍처/동시성 문서와 관련 ADR에 남긴다.
