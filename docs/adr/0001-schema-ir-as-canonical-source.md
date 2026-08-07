# ADR-0001: Schema IR을 모델 의미의 정규화된 단일 원본으로 사용

- 상태: Accepted
- 날짜: 2026-08-07
- 관련 문서: [Architecture](../ARCHITECTURE.md)

## 맥락

ORM, migration, Form, Admin, API, OpenAPI, GIS, content types가 모델 정의를 각자 해석하면 의미와 버전이 쉽게 달라집니다. Django의 모델 class는 선언과 runtime type을 동시에 담당하지만 Go에서는 정적 타입 생성을 위해 선언과 생성 타입을 분리해야 합니다.

## 결정

사용자는 Go 기반 Schema DSL로 의미를 선언하고, validation/normalization을 거쳐 versioned Schema IR을 만듭니다. Schema IR이 모든 하위 소비자의 canonical source입니다.

- Codegen, migration state, runtime metadata는 IR을 소비합니다.
- DSL object를 각 module이 직접 해석하지 않습니다.
- IR은 결정적, 직렬화 가능, hash 가능, validation 가능해야 합니다.
- 선언 순서처럼 의미 있는 순서와 canonical serialization 순서를 구분합니다.
- Historical Model도 같은 state 의미를 사용합니다.

## 결과

하나의 정의에서 정적 코드와 동적 metadata를 함께 만들 수 있고 migration diff와 reproducibility를 검증할 수 있습니다. 반면 IR versioning, default/validator identifier, backward compatibility를 설계해야 합니다.

## 의도적으로 결정하지 않은 것

DSL의 정확한 문법, codegen bootstrap 입력 방식, IR 파일 encoding, custom field ABI는 정하지 않았습니다.

## 검증

M1 전까지 normalization round-trip, deterministic hash, 선언 순서 보존, unknown-version rejection, codegen/runtime metadata equivalence를 테스트합니다.

## 이후 확정된 진화

GDJ-0004에서 create omission이 application default를 잃지 않도록 Schema IR v2에 typed
scalar default의 존재를 추가하기로 했습니다. Default는 DSL이나 codegen이 별도로
재해석하지 않고 IR validation, hash, generated write builder와 migration state가 함께
소비합니다. 상세 write 의미는 [ADR-0009](0009-m2-explicit-write-change-state.md)를
따릅니다.
