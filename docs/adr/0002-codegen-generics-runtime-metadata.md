# ADR-0002: Codegen, Generics, Runtime Metadata의 역할 분리

- 상태: Accepted
- 날짜: 2026-08-07
- 관련 질문: Q-001, Q-005, Q-006

## 맥락

Go generics만으로 schema의 문자열 이름에서 새 struct와 field selector를 만들 수 없고, codegen만으로 Admin query parameter나 historical model처럼 compile 후 결정되는 동작을 처리하기 어렵습니다. reflection 중심 구현은 타입 안전성과 hot path 비용을 악화시킬 수 있습니다.

## 결정

- Codegen은 모델마다 다른 concrete model, typed field set, descriptor/codec binding을 생성합니다.
- Generics는 `Manager[M]`, `QuerySet[M]`, `Predicate[M]`, `Field[M,V]`처럼 모델 공통 동작과 타입을 유지합니다.
- Runtime Metadata는 dynamic lookup, Admin, introspection, migration historical state를 처리합니다.
- Reflection은 bootstrap 또는 extension boundary에서 근거가 있을 때만 사용합니다.
- 새 result type parameter가 필요한 연산은 generic method가 아니라 최상위 generic function 또는 별도 generic type으로 설계합니다.

## 결과

일반 application path는 compile-time type safety와 IDE 지원을 얻고, 동적 제품 기능도 같은 모델 의미를 사용할 수 있습니다. 대신 generated/runtime 표현이 drift하지 않도록 동일 IR에서 만들고 equivalence test가 필요합니다.

## 의도적으로 결정하지 않은 것

Descriptor가 interface인지 concrete type인지, generated file 수와 이름, nullable representation, bootstrap mechanism은 정하지 않았습니다.

## 검증

M1에서 서로 다른 model predicate를 섞는 코드가 compile되지 않고, typed/dynamic path가 같은 AST를 만들며, generated code가 external package에서 compile됨을 검증합니다.
