# 아키텍처

GoDj의 모델 의미와 계층별 소유권을 설명한다. 현재 지원 폭은 [구현 현황](status/IMPLEMENTATION_MATRIX.md),
동시성·실패 의미는 [CONCURRENCY](CONCURRENCY.md), 결정 이유는 [ADR](adr/README.md)를 따른다.
아래 경계는 현재 개발 기준이며 과거 생성 파일 이름이나 내부 버전을 보존하기 위한 호환 계층은 만들지 않는다.

## 전체 흐름

```text
Schema DSL → normalized Schema IR
               ├─ codegen → model, fields, descriptor, codec, project relation binding
               ├─ migration historical state
               └─ runtime metadata → Form / Admin / API

typed query ─┐
             ├─ immutable Query AST → backend compiler → database
lookup input ┘
```

| 계층 | 소유하는 일 | 소유하지 않는 일 |
|---|---|---|
| schema / schema/ir | 모델 의미의 검증·정규화·직렬화 | DB I/O, 생성된 model import |
| codegen | 모델별 정적 타입·접근자·descriptor 연결과 안전한 publication | 모델 공통 runtime 동작의 반복 복사 |
| orm / query | typed query, 평가·cache, DB 독립 AST | SQL dialect, 연결 pool |
| db / backend 구현 | compile, 값 변환, I/O, transaction, capability | Admin 권한 정책 |
| migrations | definition, history, graph, historical state, 실행 의도 | 현재 model struct로 과거 schema 추측 |
| project / internal/projectcheck | 프로젝트 설정과 실행 파일의 조립·명령 수명 | product policy를 전역 환경에서 추측 |
| forms / admin / serializers / api / web | 모델 metadata·권한·표현의 조합 | 생성 model에 숨은 I/O를 template에서 실행 |

## Schema와 생성

Schema IR은 normalized model 의미의 원본이다. 각 소비자가 별도 field/default/nullability 정의를 만들지 않는다.
입력은 검증 후 복사하고, caller가 가진 slice·map·nested metadata를 바꿔 이미 만든 schema나 query 의미를 변경할 수 없어야 한다.
FK target은 app/model identity로 선언하고 project binding 시 graph 전체를 해석한다. 현재 FK target과 relation kind의 제한은
[Backend Matrix](BACKEND_MATRIX.md)에 명시한다.

선언 package는 generated target package를 import하지 않는다. 사용자 코드가 새 model/field 이름을 참조해 기존 생성물이
compile되지 않아도 생성기는 실행할 수 있어야 한다. [ADR-0006](adr/0006-codegen-input-package-boundary.md)이 이 bootstrap
분리의 이유를 보존한다.

`codegen.ProjectSpec`은 app schema와 project layout을 연결한다. 생성기는 전체 후보를 검증한 뒤 publication하며,
실패 시 기존 정상 결과를 보존한다. 결정적 output, gofmt, 입력 identity, generated drift와 전체 후보 compile은 의미 있는
검증이다. 과거 파일 수·test 이름·byte 길이를 그대로 유지하는 것은 제품 계약이 아니다.

공통 relation cache는 `orm.RelationCache[T]` runtime이 소유하고 생성 코드는 typed 연결을 만든다.
Project bundle의 renderer는 같은 준비된 모델·관계 해석을 공유한다.

생성물의 manifest와 recovery journal은 서로 다른 입력의 파일이 섞이거나 중단 뒤 부분 결과가 정상으로 인정되는 일을 막는다.
소유 파일, source namespace와 입력 snapshot을 확인하고 다른 사용자의 파일을 덮어쓰지 않는다. Publication의 원자성과
복구 범위는 local filesystem 기준이며 임의 네트워크 filesystem의 crash durability를 보장하지 않는다.
[ADR-0036](adr/0036-project-schema-generated-bundle-and-recoverable-publication.md)에 대안과 failure model이 있다.

## ORM과 관계

Typed selector는 model/field/value type을 compile time에 연결한다. 동적 lookup은 요청에서 받은 이름을 metadata로
검증한 뒤 같은 AST를 만든다. 알 수 없는 field·lookup·relation 경로는 SQL 실행 전에 명시적으로 거부한다.
AST는 immutable value이며 parameter binding과 identifier quoting은 backend compiler가 소유한다.
DB 독립 projection·ordering·relation key·scalar 의미 검사는 `db/internal/queryplan`이 공유한다. 각 compiler는
물리 identifier 제한과 quoting, schema qualification, parameter 형식과 SQL 배치를 소유한다.

Nullable read와 write의 omitted/null/value는 구분한다. Save의 update field 선택·force mode·PK 유무는 명시적 입력이다.
DB rollback이 application memory를 자동 복원하지는 않는다. Query plan과 평가 cache도 별개의 수명이다.

관계 상태는 project가 연결하고 model 객체의 소유권에 따라 관리한다. 같은 객체의 cache 공유, 복사·Fresh 이후 독립성,
eager/prefetch의 성공 후 일괄 publication과 assignment 뒤 FK/cache reconciliation을 구분한다.
Lazy relation I/O는 context와 error를 갖는 호출로 드러난다. 서로 다른 materialization 사이의 전역 identity map은 없다.
더 자세한 복사·취소 계약은 [CONCURRENCY](CONCURRENCY.md)에 있다.

## Migration

```text
strict definition sources → opaque LoadedDefinitionSet
  → complete graph/history validation → historical ProjectState
  → fresh target plan → backend-owned revision session
  → each step: validate fence → schema + recorder + successor revision → commit
```

Definition은 실행 가능한 Python/Go plugin이 아니라 current data format이다. Unknown field/version, duplicate key/identity,
비정상 문자열·범위 초과·resource limit과 graph 모순을 명시적으로 거부한다. 전체 load가 성공하기 전에는 partial set을
게시하지 않는다. Canonical digest는 정규화된 의미를 식별하며 caller-owned 원문이나 runtime model을 execution authority로
다시 사용하지 않는다. Codecs는 backend handle이나 credential을 포함하지 않는다.

Loader가 정의·source inventory를 복사하고 검증된 immutable graph를 한 번 게시한다. 내부 lifecycle과 SQL projection은
이를 빌려 읽으며 매번 전체 정의를 복사하거나 같은 graph를 재구축하지 않는다. `Digest`는 저장된 문자열을 반환하고,
`Sources`와 `Definitions`는 각각 요청한 mutable view만 복사한다. 외부 raw reconstructor 입력은 별도 검증·복사 경계다.
지원 operation의 non-nil 포인터는 이 경계에서 값으로 정규화한다. Typed nil과 알 수 없는 operation은 오류 의미를 보존한다.

Historical `ProjectState`는 적용할 당시의 schema를 dependency 순서로 재구성한다. 현재 generated model의 field를 읽어
과거 migration을 복원하지 않는다. 모든 definition을 검증하고 chronology·known history·exact target을 확인한 뒤 backend
실행을 시작한다. Source-only check와 DB-backed history check는 서로 다른 결과다.

Planner는 불변 identity graph를 사용하며 같은 입력에 canonical plan을 만든다. 비교 불가능한 sibling의 합법적 순서는
Django와 다를 수 있다. [DEV-0002](DEVIATIONS.md#dev-0002--app-zero의-incomparable-sibling은-godj-canonical-order를-유지)는 이를 명시적으로
분류하며 final schema/history, dependency order와 durable prefix를 대신 생략하지 않는다.

Plan은 실행 권한을 가진 불변 token이 아니다. Preview 뒤 writer가 history를 바꿀 수 있으므로 실행은 새 revision snapshot에서
fresh plan을 만든다. Each-step fence는 DDL/recorder 첫 mutation 전에 검사하고 schema·recorder·successor revision을 한
transaction에 묶는다. 전체 migration 목록을 하나의 outer transaction으로 감싸지 않아 성공한 앞부분은 뒤 실패 후에도 남는다.

SQLite FK DDL은 같은 pinned connection의 FK 설정·물리 schema를 검증하고, 허용한 경우에만 remake한다. 기존 rows,
NULL/default 의미, PK/sequence와 FK constraint를 보존해야 한다. 검증하지 않은 index·trigger·inbound/self/cyclic schema를
조용히 재작성하지 않는다. 지원하지 않는 작업은 mutation 전에 capability error로 끝낸다.

`sqlmigrate`는 target 직전 historical state의 forward intent를 pure renderer로 projection한다. Preview SQL을 실행 plan으로
재사용하지 않으며 renderer가 DB opener, recorder, transaction 또는 credential을 갖지 않는다. 실제 migration 실행은 backend
capability와 fresh history를 다시 검증한다.

## CLI와 프로젝트

전역 `godj`는 `godj.toml`로 project-owned runner를 찾는다. Runner는 선언 schema·migration catalog·backend opener·operator
정책을 조립한다. 전역 도구와 프로젝트의 명시적 protocol을 구분하여 host의 다른 설정이나 source를 암묵적으로 섞지 않는다.

잘못된 argv와 descriptor는 불필요한 build/init/I/O 전에 거부한다. 실제 child 실행은 timeout·cancel·signal·stdout/stderr·reap의
소유자를 하나로 둔다. 성공 결과는 strict bounded protocol로 검증한 뒤 게시한다. Child/build의 실패 원인을 보존하되
credential·사용자 secret·원문 private path를 공개 오류에 붙이지 않는다. Build 실패만 category/code 뒤에 제한된 sanitized 원인을 추가한다.
공통 빌드 도구는 dependency/compiler cache를 재사용하며 runtime의 private workspace·환경과 분리한다. 자세한 실행 범위는 [TESTING](TESTING.md)에 있다.

`internal/wirejson`은 strict lexical/구조 검사와 bounded read를, `internal/projectwire`는 ProjectSpec·Schema IR의 wire 표현과
크기 계산을 소유한다. Envelope·숫자/배열/깊이 budget·오류 우선순위·overflow 후 drain 정책은 각 protocol이 선택한다.
Definition JSON의 결정적인 source/JSON-pointer 오류 선택은 별도 loader 책임이다.
CLI의 retained project·workspace·build·child·cleanup은 공용 실행 owner가 관리한다. 명령별 terminal policy가 공개 결과를
선택하며, 완료된 migrate/read 결과나 durable publication을 늦은 취소로 덮어쓰지 않는다. TTY credential과 foreground server의
전용 수명은 각각 operator와 runserver가 소유한다.

`makemigrations`는 model difference에서 지원하는 작업만 작성한다. Input/catalog을 publication 시점에도 다시 확인하여 stale
plan이 파일을 덮어쓰지 않게 한다. `showmigrations`는 한 snapshot의 상태 출력이며 이후 writer를 막는 lock이 아니다.

## Web, Form, Admin, API

Web request는 명시적 context·routing·representation 경계를 갖는다. Template은 closed value를 render하고 기본 escape를
적용한다. Model method나 arbitrary attribute lookup이 template evaluation 중 I/O를 실행하게 하지 않는다. Safe HTML은
검토 가능한 construction 경계에서만 만든다.

Form/Admin/API는 normalized model metadata를 소비한다. Field allowlist, read-only, nullable와 validation은 의미가 같을 때
공유하고, HTML form 제출과 JSON PUT/PATCH의 omitted 규칙처럼 서로 다른 protocol 의미는 유지한다. Persistence·permission·audit는
application이 명시적으로 연결한다. Admin snapshot은 실제 list/form 필드를 요구하고 저장 전용 새 필드의 매핑을 강제하지 않는다.
명시적으로 제공한 snapshot 값은 known field/type 검사를 받는다. Serializer가 임의 model memory나 credential을 reflection으로 노출하지 않는다.

Authentication은 Session 또는 명시적으로 선택한 Bearer profile을 사용한다. Bearer가 잘못되었을 때 다른 credential로 fallback하지
않으며 권한 거부·인증 실패·CSRF 실패를 구분한다. Raw token/password와 verifier cause는 logs·errors·audit에 남기지 않는다.
Durable credential은 explicit provisioning 후 `OpenExisting`으로 열며 startup이 비밀번호를 다시 받거나 권한을 몰래 바꾸지 않는다.
Session Store의 `Touch`는 현재 record의 idle/absolute 만료 판정·갱신·만료 삭제를 원자적으로 소유하고
active/expired/missing을 구분한다. Manager는 detached `Load`의 오래된 deadline으로 같은 ID를 무조건 삭제하지 않는다.
Touch는 현재 record의 단조로운 access/idle deadline과 고정 absolute lifetime을 보존하며 rotation된 ID를 되살리지 않는다.

System-state와 application mutation이 같은 transaction이어야 하는 흐름은 동일 backend coordination domain에서 수행한다.
Multi-runtime 안전성은 같은 normalized policy를 사용하고 fence에 참여하는 writer 사이의 계약이다. 비협력 writer·외부 process의
직접 SQL이나 자동 분산 policy 전파까지 보장하지 않는다.

## 설계 기록

과거 format tuple·optional relation handoff·additive generated companion 유지 규칙은
[ADR-0035](adr/0035-pre-release-current-only-format-and-generated-publication.md)에서 폐기했다.
그때의 strict decoding, immutable snapshot, provenance, historical replay, revision fence, rollback/unknown outcome,
FK physical preflight와 candidate failure preservation은 위 현행 경계에 남긴다.
대체된 ADR 원문과 실행 과정은 [고정 Git 문서](https://github.com/progresshans/godj/tree/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr)에서
필요할 때만 확인한다.
