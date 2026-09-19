---
id: GDJ-0082
status: active
updated: 2026-09-19
baseline_commit: "7e5a933db69435154842162287ef86ef7172bc17"
integration_owner: "root"
---

# 여러 단계의 forward 관계를 따르는 조회

## 목표

게시물의 작성자·검토자에서 소속 팀까지 따라가 그 이름과 상태로 조회한다. 현재 direct forward lookup의 한 단계 제약을
풀고, 같은 model을 다른 경로로 만나도 각 경로의 JOIN·NULL·predicate 의미를 보존한다. 이후 nested eager materialization이
공유할 경로 기반을 이 실제 조회 흐름에서 정한다. 장기 목표는 헌장·기능 카탈로그의 완성이며 출시 범위를 정하는 작업이 아니다.

## 구현 경계

- Schema IR의 FK 선언으로 유한한 forward chain을 해석한다. 논리 model/FK identity와 root에서 도달한 경로 identity를 구분한다.
  두 경로가 같은 target model이나 동일 FK 선언을 다시 사용해도 alias가 충돌하지 않아야 한다. 공유 prefix만 같은 JOIN으로 합친다.
- Typed/dynamic lookup은 동일 immutable Query AST를 사용한다. Generated typed 탐색은 schema의 자기·상호 참조 때문에
  무한히 펼쳐지거나 traversal 조합마다 타입을 만들지 않아야 한다. 구체 API는 현재 소비자와 생성 구조를 확인한 뒤 결정한다.
- 각 hop의 source/target/table/key/nullability와 연결을 검증한다. 잘못된 경로·위조한 metadata와 resource 경계를 pre-I/O 거부한다.
- Required/nullable chain의 INNER/LEFT 선택, AND/OR/NOT, source FK isnull과 target-field NULL을 독립 Django 결과로 확인한다.
  상위 LEFT JOIN 뒤의 required target을 잘못 INNER JOIN하여 root를 누락하지 않는다. Count·Distinct·slice·empty 의미를 유지한다.
- 현재 direct eager 선택과 nested filter의 조합도 같은 alias inventory를 사용한다. 기존 복수 선택/cache 실패 의미를 보존한다.
- Nested eager cache graph, reverse의 넓은 Boolean/다중 경로 의미, OneToOne/ManyToMany는 이 작업의 완료 주장에 포함하지 않는다.
  이들은 미완료 기능으로 남으며 위 경로 기반의 후속 구현으로 이어간다.

## 현재와 다음

GDJ-0081 제품 source는 baseline에 통합됐고 별도 Hosted ORM 검증이 진행 중이다. 이 작업은 별도 worktree에서 이어가며
기존 CI source를 변경하지 않는다. 먼저 고정 Django 6.1의 경로·NULL·alias 관찰과 현재 IR/resolver/compiler의 연결을 확인한다.
아직 이 범위의 제품 구현·동작 검증은 없다. 제품·생성기·실제 양 DB/생성 소비자를 하나의 변경 묶음으로 완성한 뒤
영향 범위의 checkpoint를 실행하며, 환경과 source 증거는 TEST_EVIDENCE에서 분리한다.
