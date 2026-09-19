---
id: GDJ-0081
status: active
updated: 2026-09-19
baseline_commit: "7397a73b933eef4d30c5a8fa12c84a79fc7945e9"
integration_owner: "root"
---

# 여러 direct forward 관계를 함께 읽기

## 목표

Post의 author와 reviewer, 서로 다른 target model의 관계를 한 query에서 함께 읽고 각 accessor의 cache를 준비한다.
GDJ-0080의 여러 filter JOIN과 별개로, 여러 selected target을 실제로 scan·검증·materialize하는 다음 기반을 구현한다.
헌장·기능 카탈로그의 완성 목표를 유지한다. 이 작업은 direct selection의 확장이며 임의 깊이의 관계 graph 완료가 아니다.

## 구현 경계

- Query AST에 immutable selected projection 집합을 둔다. 순서가 바뀌거나 같은 관계를 다시 선택해도 결정적인 column 배치와 결과를 갖는다.
  중복 제거 전에 동일 FK·target columns·nullability·project/root provenance를 대조한다. Filter-only edge와 projection edge를 구분한다.
- Source와 모든 target columns를 한 번에 scan한다. 서로 다른 target Go type을 보존하는 닫힌 typed adapter와 공통 runtime을 사용한다.
  단일 target runtime을 별도로 유지하거나 target 조합마다 생성하는 방식으로 중복을 늘리지 않는다.
- 모든 target의 key/NULL/row integrity와 Rows.Close·취소를 확인한 뒤에만 전체 cache를 게시한다.
  실패·부분 scan·잘못된 target·잘못된 selector는 다른 selected cache를 먼저 게시하지 않는다.
- Typed builder·dynamic 이름 목록·model별 facade selector가 같은 query/evaluation 경로를 사용한다.
  필요한 내부 API·생성 ABI를 함께 바꾸며 옛 이름·테스트 문구를 보존하려는 호환 계층을 추가하지 않는다.
- All·First·Count와 Filter·OrderBy·Distinct·Offset·Limit·Fresh가 모든 선택을 보존한다. Cold Count는 selected projections를 모두 제외한다.
  Source/target cache, 같은 target PK의 서로 다른 FK, reverse filter로 중복된 source 객체의 복제·소유권을 보존한다.
- Nested traversal, reverse eager projection, 무인자 자동 graph 확장은 이번 범위 밖이며 후속 구현으로 남긴다.

## 관찰과 검증

고정 Django 6.1의 fresh process에서 220개 multiple-selection 관찰을 수집했다. Required/nullable selection,
두 관계의 같은 target type·세 관계의 서로 다른 target type, reordered/repeated selectors, reverse 중복과 slice,
cold/warm All·First·Count를 포함한다. 독립 invalid-selection 관찰 8개도 분리했다.
Django Count가 잘못된 selected 이름을 검사하지 않는 경우와 GoDj의 기존 selection/configuration 검증 차이를 명시한다.
Python 객체 identity 공유는 목표가 아니며 Go의 clone/cache invariants는 별도로 검증한다.

제품·생성기·단일/복수 selection 소비자·양 DB·실패 경로를 하나의 변경 묶음으로 완성한 뒤 affected normal·race·CGO0와
reference·generated drift를 실행한다. Materialization ABI의 통합 변경이 다음 Hosted ORM checkpoint를 소유한다.
완료된 GDJ-0080 Hosted는 baseline의 별도 source 검증이다. source·환경별 완료를 혼합하지 않는다.

## 통합 경계

GDJ-0080 self-reference 보완은 baseline에 통합됐고 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35414363995)도 완료했다.
정확한 source·scope는 TEST_EVIDENCE가 소유한다. 이 작업은 별도 worktree에서 구현하며 기존 검증 source를 바꾸지 않는다.

## 구현 checkpoint

Immutable 복수 projection, typed target adapter와 공통 scan/cache runtime, composable object builder와 variadic facade를 연결했다.
단일 target 경로도 이 runtime으로 합쳤다. 기존 단일 선택 제약의 compile-fail fixture는 지원 기능의 positive consumer로 이동하며,
잘못된 source model·다른 project selector·잘못된 target scanner·충돌한 중복 binding은 별도의 음성 검증을 유지한다.
Generated Article·Helpdesk·relationfixture와 기존 소비자를 현재 ABI로 갱신했다. Normal·race·CGO0와 독립 Django 재생을 완료했다. Hosted ORM 통합 검증이 남아 있다.
실행 수·source·실패 시도·환경은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0081--여러-direct-forward-target-동시-선택)에 기록한다.
