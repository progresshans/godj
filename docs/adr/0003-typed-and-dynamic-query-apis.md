# ADR-0003: Typed query API와 Dynamic lookup API를 함께 제공

- 상태: Accepted
- 날짜: 2026-08-07
- 관련 질문: Q-007, Q-008, Q-013

## 맥락

일반 application은 잘못된 model/field/value 조합을 compile time에 막는 API가 유리합니다. 반면 Admin, URL query parameter, search builder, Historical Model은 compile 후 들어오는 문자열 field path를 처리해야 합니다.

## 결정

- 일반 application code의 기본은 generated typed field/predicate API입니다.
- dynamic lookup은 metadata validation, type coercion, allowlist와 구조화된 오류를 거쳐 사용합니다.
- 두 API는 동일한 Query AST node와 backend compiler를 사용합니다.
- dynamic input을 SQL 문자열 조합으로 바로 보내지 않습니다.
- projection 결과가 compile time에 정해지면 generic top-level function 또는 generated projection을 사용하고, 실행 시 결정되면 명시적 dynamic row를 사용합니다.

## 결과

타입 안전성과 Django식 문자열 lookup이라는 두 요구를 함께 충족할 수 있습니다. 반면 error-bearing builder의 chaining, relation path typing, coercion/security 규칙은 prototype이 필요합니다.

## 의도적으로 결정하지 않은 것

`FilterKw`의 정확한 signature, error accumulation 시점, relation selector API, result cache는 정하지 않았습니다. 문서 초안의 chaining 예시는 normative API가 아닙니다.

## 검증

M1에서 typed/dynamic exact와 icontains가 같은 normalized AST와 결과를 만들고, unknown field/lookup과 disallowed path가 query 실행 전에 명시적 오류가 되는지 검증합니다.
