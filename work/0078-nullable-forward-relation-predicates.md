---
id: GDJ-0078
status: complete
updated: 2026-09-19
baseline_commit: "6eb40412501b8945dad06a75f95e3b39510e2fe0"
integration_owner: "root"
---

# Nullable ForeignKey의 대상 필터

## 목표

선택적 reviewer 같은 nullable ForeignKey를 경유해 대상 필드의 값으로 조회할 수 있게 한다.
Typed와 dynamic의 같은 Query AST, 실제 nullable 행과 대상 행, Boolean 조합·Count·실제 양 DB를 함께 연결한다.
헌장·기능 카탈로그의 완성으로 가는 다음 관계 기반 작업이며 전체 관계 범위의 완료를 뜻하지 않는다.

## 현재와 확인할 경계

시작 source에는 nullable FK의 root-key isnull과 eager projection이 있지만 대상 필드의 predicate는 명시적으로 거부됐다.
Django의 고정 버전에서 nullable 관계의 exact filter와 AND/OR/NOT 조합, null 대상 행의 포함·제외, JOIN과 Count 결과를 먼저 관찰한다.
그 결과로 필요한 JOIN 종류와 null 보정의 소유자를 정하고 기존 필수 관계·Boolean composition·DB 오류 검증을 보존한다.
단순히 nullable 거부 조건을 없애서 SQL UNKNOWN 때문에 행을 잘못 제외하는 구현은 하지 않는다.

현재 대상은 직접 forward FK와 기존에 지원하는 대상 scalar의 exact lookup이다. 다중/중첩 eager materialization,
nullable 대상 scalar 자체·다른 lookup의 확장은 이 작업의 구체적인 의존성이 확인되면 범위를 다시 적는다.

## 검증 소유자

제품·생성기·독립 소비자와 실패 경로를 먼저 한 묶음으로 구현한다. 편집 중에는 필요한 compile만 확인한다.
완성된 묶음의 related normal·race·CGO0·actual SQLite/PostgreSQL·Django reference와 generated drift를 통합한다.
GDJ-0077 eager Count와 이 관계 query 확장을 합친 source에서 Hosted ORM checkpoint를 완료했다.
이전 source의 Hosted 결과와 새 코드를 합쳐 PASS로 표시하지 않는다. 독립 설계 관찰과 제품·생성기 구현, 실제 DB/소비자의 최종 로컬 checkpoint를 완료했다. 통합 source와 Hosted의 정확한 scope·결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.

## 독립 관찰과 채택할 실행 의미

고정 Django 6.1/SQLite의 fresh process에서 73개 case를 관찰했다. Char·Text·int64·DateTime과 대상 PK,
required/nullable forward 관계·root scalar·nullable relation isnull의 AND/OR/NOT 및 이중·삼중 부정을 포함한다.
Positive nullable 대상 일치는 INNER JOIN이고, null 행을 허용하는 OR/NOT에는 LEFT OUTER JOIN이 필요했다.
NOT 대상 비교에는 실제 joined 대상 column의 IS NOT NULL 보정이 붙으며 단순한 NOT 비교는 같은 결과를 내지 않는다.

직접 forward relation predicate를 Boolean 조합에 허용하고 reverse의 OR/NOT은 기존 미지원으로 보존한다.
공통 queryplan이 필터에서 반드시 존재해야 하는 forward edge를 계산한다. AND는 필수 edge의 합집합, OR는 교집합이며
NOT 아래에서는 De Morgan 의미와 nullable leaf의 null 허용을 반영한다. 필수가 아닌 nullable forward edge는 LEFT JOIN,
필수 edge는 INNER JOIN으로 내린다. Eager projection도 같은 edge 계획을 사용하며 다른 eager/filter edge의 materialization 제한은 유지한다.
Compiler의 홀수 부정 leaf에서는 joined 대상 column을 null 보정에 사용한다. Context·resource·source provenance 검증을 보존한다.
원래 Django의 target PK lookup JOIN 생략 최적화는 이 범위의 요구가 아니며 같은 결과·오류·query 수를 검증한다.
공통 AST·binder·JOIN 계획·양 compiler와 generated query adapter를 구현했다. Source-key 존재 증명은 실제 조건의 hop과
정확히 일치해야 한다. 하위 Query AST가 기존에 허용하던 Boolean·nullable 대상과 backend별 식별자 정책도 보존한다.
환경별 실행의 최종 기록은 TEST_EVIDENCE가 소유한다.
