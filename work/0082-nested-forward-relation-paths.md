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
  무한히 펼쳐지거나 traversal 조합마다 타입을 만들지 않아야 한다. Target model별 generic field group과 지연 method를 사용해 root Go type을 유지한다.
- 각 hop의 source/target/table/key/nullability와 연결을 검증한다. 잘못된 경로·위조한 metadata와 resource 경계를 pre-I/O 거부한다.
- Required/nullable chain의 INNER/LEFT 선택, AND/OR/NOT, source FK isnull과 target-field NULL을 독립 Django 결과로 확인한다.
  상위 LEFT JOIN 뒤의 required target을 잘못 INNER JOIN하여 root를 누락하지 않는다. Count·Distinct·slice·empty 의미를 유지한다.
- 현재 direct eager 선택과 nested filter의 조합도 같은 alias inventory를 사용한다. 기존 복수 선택/cache 실패 의미를 보존한다.
- Nested eager cache graph, reverse의 넓은 Boolean/다중 경로 의미, 일반 self/cyclic migration, OneToOne/ManyToMany는 이 작업의 완료 주장에 포함하지 않는다.
  이들은 미완료 기능으로 남으며 위 경로 기반의 후속 구현으로 이어간다.

## 현재와 다음

GDJ-0081 제품 source는 baseline에 통합됐고 Hosted ORM 검증도 완료했다. 이 작업은 별도 worktree에서 이어가며
기존 CI source를 변경하지 않는다. 고정 Django 6.1의 경로·NULL·alias 관찰과 현재 IR/resolver/compiler의 연결을 확인했다.
공통 AST·JOIN planner·typed/dynamic 경로·generator를 수정했고 3-app cyclic 생성 모듈까지 compile했다.
양 DB·외부 생성 소비자의 normal/race/CGO0 checkpoint를 완료했다.
환경과 source별 증거는 TEST_EVIDENCE에서 분리한다. 통합 source `3ab0a7dd97d6a29c56b7f75f07b7533a44e9bfc0`의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35421304637) 완료를 확인한다.
별도 GDJ-0083에서 중첩 eager materialization/cache 구현을 이어간다.

## 독립 관찰과 설계 입력

고정 Django 6.1 fresh process에서 146개 관찰을 수집했다. Person–Team–Organization과 self-reference의 유한한 반복 경로,
작성자·검토자의 같은 target model, optional backup 경로, Boolean/NOT·reverse 중복·slice, direct eager 조합을 포함한다.
Django Query의 setup_joins·trim_joins·build_filter와 JoinPromoter.update_join_types source를 같은 설치 버전에서 확보하고 JOIN 종류 결정과 실제 SQL을 대조했다.
실행 환경과 reference 재생 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0082--nested-forward-경로의-독립-관찰)에 기록한다.
이 관찰은 GoDj의 구현/검증 완료를 뜻하지 않는다.

- 상위 optional 경로가 OR/NOT 때문에 LEFT로 남으면 아래 required FK도 LEFT로 유지해야 한다.
- `reviewer__backup__organization__isnull`의 organization FK는 물리적으로 required여도 상위 관계 부재를 표현한다.
  마지막 target JOIN만 생략하며, 상위 Person/Team JOIN과 NULL을 보존하도록 source-key 경로를 넓힌다.
- 같은 Team.parent 또는 Person.manager 선언을 재방문하는 유한 경로는 각각 다른 alias를 가진다. Model/FK identity만으로
  JOIN을 합칠 수 없다. 동일 prefix는 공유하되 다른 route의 같은 physical declaration은 분리하고 metadata 일관성은 함께 검증한다.
- Typed 탐색은 `relations.BlogPost.Author.Team().Organization().Name`처럼 target model의 generic field group을
  lazy method로 확장한다. Direct entry의 scalar field 접근을 유지하고, 경로/깊이 조합마다 타입을 생성하지 않는다.
  `orm.ChainForward`는 같은 project snapshot·중간 model identity를 검증하며 최초 오류 cause를 유지한다.
- 경로는 최대 64 FK이며 SQLite는 query 전체의 실제 JOIN 63개와 root 1개까지 허용한다. Source-key trim도
  마지막 JOIN만 제거하므로 길이와 실제 JOIN 수는 다를 수 있다. 제한 초과는 빈 결과여도 I/O 전에 거부한다.
