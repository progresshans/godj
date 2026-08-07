# GoDj 아키텍처

- 상태: 핵심 방향 Accepted, 세부 API Proposed
- 마지막 검토: 2026-08-08

이 문서는 안정적인 계층과 책임을 정의합니다. 코드 예시가 있더라도 개별 공개 API는 compile prototype, contract test, Accepted ADR 없이 확정된 것이 아닙니다.

## 전체 흐름

```text
사용자 Schema DSL
        ↓ validate / normalize
정규화된 Schema IR
   ├── codegen ──→ generated model / fields / descriptor / codec binding
   ├── migration snapshot / project state
   ├── runtime metadata
   └── documentation / introspection
        ↓
Generic Core: Manager[M], QuerySet[M], Predicate[M], Field[M,V]
        ↓
불변 Query AST / execution plan
        ↓
Backend compiler + schema editor + adapters
        ↓
SQLite / PostgreSQL / MySQL / MariaDB / Oracle
```

## 계층별 책임

| 계층 | 책임 | 하지 않는 일 |
|---|---|---|
| Schema DSL | 사람이 모델 의미를 선언하는 API | DB 실행, 생성 코드 형식 결정 |
| Schema IR | 모든 소비자가 공유하는 정규화·직렬화 가능한 의미 | ORM이나 Admin 구현 import |
| Codegen | 모델별 Go 타입, typed fields, descriptor와 codec 연결 생성 | 런타임 쿼리 해석, DB별 SQL 생성 |
| Generic Core | 모델 공통 동작 재사용, 컴파일 시 타입 보존 | 문자열로 새 struct/field 이름 생성 |
| Runtime Metadata | 동적 lookup, Admin, Historical Model, introspection | 두 번째 schema 원본 역할 |
| Query AST | typed/dynamic API가 공유하는 DB 독립 쿼리 의미 | SQL dialect 문자열 보유 |
| Backend | AST compilation, DDL, introspection, value adaptation, capability | 상위 Admin/API 정책 알기 |
| 상위 모듈 | Form, Admin, API, Realtime, GIS 등 제품 경험 | 하위 계층의 독립성을 역전하기 |

## 단일 원본 규칙

Schema DSL은 입력 형식이고 Schema IR이 정규화된 의미의 단일 원본입니다. Codegen, migration state, runtime metadata, Form/Admin/API schema가 각자 DSL을 해석하거나 별도 모델 정의를 만들면 안 됩니다.

Schema IR은 최소한 다음 성질을 가집니다.

- schema version이 명시됨
- 결정적 serialization과 hash가 가능함
- 선언 순서처럼 의미 있는 순서와 canonical ordering을 구분함
- closure 대신 안정적인 identifier로 default/validator를 참조함
- 현재 모델과 historical model이 같은 의미 구조를 공유할 수 있음
- backward/forward compatibility 정책을 테스트할 수 있음

## Codegen, Generics, Metadata의 역할

```text
Codegen: Post, PostFieldSet, PostDescriptor처럼 모델마다 다른 형태
Generics: Manager[Post], QuerySet[Post]처럼 모든 모델에 공통인 동작
Metadata: author__email__icontains처럼 실행 중 결정되는 경로
```

Go에서는 메서드가 receiver에 없는 새 type parameter를 선언할 수 없습니다. 예를 들어 `QuerySet[M].SelectInto[R]` 형태는 사용할 수 없으므로, 추가 결과 타입이 필요한 연산은 `SelectInto[M, R](...)` 같은 최상위 함수 또는 별도 generic builder로 설계합니다.

M1에서는 `orm`이 `ModelDescriptor[M]` interface를 소유하고 codegen이 상태 없는
concrete descriptor를 생성합니다. `Metadata()`는 독립 복사를 반환하고 `Scan` 반환
타입이 `M`을 보존합니다. Runtime freeze/registry 없이 생성·compile 시점부터 frozen인
경계는 [ADR-0007](adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md)에
고정했습니다. Relation binding이 필요해질 때 확장 여부를 다시 검토합니다.

GDJ-0008의 `godj-codegen-m2-v3`는 `ModelDescriptor[M].CloneModel(M) M` 구현을 생성합니다.
Nullable pointer를 포함한 model별 deep clone으로 QuerySet canonical cache와 caller 값을
격리하고, 기존 `CloneWriteModel`은 같은 clone 구현에 위임합니다. 이 descriptor ABI 변경은
generator version, golden/compile test와 generated drift gate로 함께 고정합니다.

## Query 경로

일반 애플리케이션은 typed predicate를 기본으로 사용하고, HTTP query, Admin, Historical Model은 allowlist와 coercion이 있는 dynamic lookup을 사용합니다. 둘은 같은 AST node와 오류 taxonomy로 수렴해야 합니다.

```text
typed field predicate ─┐
                      ├─→ validated expression ─→ Query AST ─→ backend compiler
dynamic lookup ────────┘
```

QuerySet 체이닝은 기존 plan을 변경하지 않고 새 plan을 만듭니다. M1 dynamic API는
`ParseDynamic`에서 construction 오류를 즉시 반환하고, 성공한 `Predicate[M]`를 typed
`Filter`에 넘깁니다. GDJ-0008부터 immutable plan과 pointer evaluation state를 분리합니다.
Direct Go value copy는 state를 공유하지만 `Filter`/`OrderBy`/성공한 `Limit`와 `Fresh`는
새 state를 받습니다. 성공한 full `All`만 cache하고 같은 state의 동시 `All`은
singleflight하며, 실패/cancellation은 cache하지 않습니다. Owner와 waiter context를
격리하고 generated deep clone을 통해 canonical cache를 caller에게 직접 노출하지
않습니다. Cold `Count`/`Exists`/`At`/`First`는 full cache를 채우지 않고 warm terminal은
cache를 재사용하며, `Iterate`는 cache를 우회·보존합니다. 이 경계는
[ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)에 고정합니다.

## Model과 Migration

생성 모델은 가능한 한 데이터 타입으로 유지하고, row scan, value encode, metadata, state 접근은 생성 descriptor/codec 경계에 둡니다.

Migration은 현재 생성 타입을 과거 migration state에 사용하지 않습니다. GDJ-0004는
Schema IR 기반 `ProjectState`, typed operation, executor와 같은 SQLite transaction의
schema editor/recorder를 [ADR-0010](adr/0010-m2-migration-state-and-executor-boundary.md)에
따라 검증했습니다. [ADR-0013](adr/0013-immutable-migration-planner.md)은 historical
`ProjectState`와 applied migration history를 분리하고, operation/backend를 보관하지 않는
immutable identity graph가 caller-supplied AppliedState와 target으로 zero-I/O plan을
계산하도록 결정했습니다. GDJ-0010은 이 immutable Planner와 structured graph/history/
target error를 구현하고 MIG-005..016 actual adapter로 검증했습니다. Planning의 logical
state와 zero-I/O metrics는 실제 database probe가 아니라 backend를 import하지 않는 pure
structural 경계에서 산출합니다. Public migration file 형식, loader/CLI, data callback,
multi-plan execution, locking과 crash recovery는 여전히 Q-012의 후속 결정입니다.

## CLI와 프로젝트 실행

전역 `godj` CLI는 `version`, `startproject`, `startapp`, 프로젝트 탐색과 orchestration을 담당합니다. 프로젝트 설정·앱·모델·사용자 command가 필요한 작업은 프로젝트 코드를 포함한 바이너리에서 실행합니다.

```text
godj CLI
  ├─ 독립 명령
  └─ 프로젝트 탐색/빌드/실행
           ↓
프로젝트 바이너리
  ├─ serve
  ├─ migrate
  ├─ createsuperuser
  └─ custom commands
```

`manage.py` 파일은 복제하지 않지만 프로젝트 전용 실행기라는 역할은 보존합니다. `go generate`는 보조 진입점이고 공식 orchestration은 `godj generate`입니다.

## 의존 방향

화살표는 “왼쪽이 오른쪽을 import할 수 있음”을 의미합니다.

```text
schema DSL ─→ schema/ir
codegen ────→ schema/ir
migrations ─→ schema/ir, backend contracts
orm ────────→ query, schema/ir metadata, backend contracts
backends ───→ query, schema/ir, backend contracts
forms/auth/templates ─→ metadata와 제한된 ORM interface
admin/api/realtime ───→ 공개 하위 module interface
gis extension ────────→ schema/query/backend의 명시적 extension point
```

금지 예시는 `schema/ir → orm`, `query → admin`, `orm → admin`, `orm → api`, `forms → admin`, `backend → 상위 제품 모듈`입니다. 거대한 범용 `core` 패키지는 만들지 않습니다. 실제 패키지가 생기면 dependency test로 검증하고 interface 소유 패키지를 명시합니다.

## Codegen bootstrap 경계

선언 package와 generated target을 분리하고 generator는 Schema IR만 의미 입력으로
사용합니다. Target 교체 전 candidate를 `gofmt`/parse하고 Go overlay로 실제 target
package를 compile하며, 실패하면 last-good bytes를 보존합니다. 이 결정과 M0
rename/delete/stale fixture는 [ADR-0006](adr/0006-codegen-input-package-boundary.md)에
기록합니다. 전역 CLI와 project library version protocol은 Q-010으로 남아 있어 현재
runner는 `internal/cmd/m1generate`입니다.

## 목표 저장소 구조

최종적으로 `cmd/godj`, schema/IR, codegen, query, ORM, backends, migrations, forms, templates, admin, auth, API, realtime, GIS, i18n, contrib, testing/conformance, examples가 필요할 수 있습니다. 구현하지 않은 미래 패키지는 미리 만들지 않습니다.

현재 핵심 미결정 사항은 [OPEN_QUESTIONS.md](OPEN_QUESTIONS.md), 채택된 이유는 [adr/](adr/README.md)에 기록합니다.
