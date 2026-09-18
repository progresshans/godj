---
id: GDJ-0078
status: active
updated: 2026-09-19
baseline_commit: "ca654fd48036f39f3e77654ad42894c5dde118eb"
integration_owner: "root"
---

# Nullable ForeignKey의 대상 필터

## 목표

선택적 reviewer 같은 nullable ForeignKey를 경유해 대상 필드의 값으로 조회할 수 있게 한다.
Typed와 dynamic의 같은 Query AST, 실제 nullable 행과 대상 행, Boolean 조합·Count·실제 양 DB를 함께 연결한다.
헌장·기능 카탈로그의 완성으로 가는 다음 관계 기반 작업이며 전체 관계 범위의 완료를 뜻하지 않는다.

## 현재와 확인할 경계

현재 nullable FK의 root-key isnull과 eager projection은 있지만 대상 필드의 predicate는 명시적으로 거부한다.
Django의 고정 버전에서 nullable 관계의 exact filter와 AND/OR/NOT 조합, null 대상 행의 포함·제외, JOIN과 Count 결과를 먼저 관찰한다.
그 결과로 필요한 JOIN 종류와 null 보정의 소유자를 정하고 기존 필수 관계·Boolean composition·DB 오류 검증을 보존한다.
단순히 nullable 거부 조건을 없애서 SQL UNKNOWN 때문에 행을 잘못 제외하는 구현은 하지 않는다.

현재 대상은 직접 forward FK와 기존에 지원하는 대상 scalar의 exact lookup이다. 다중/중첩 eager materialization,
nullable 대상 scalar 자체·다른 lookup의 확장은 이 작업의 구체적인 의존성이 확인되면 범위를 다시 적는다.

## 검증 소유자

제품·생성기·독립 소비자와 실패 경로를 먼저 한 묶음으로 구현한다. 편집 중에는 필요한 compile만 확인한다.
완성된 묶음의 related normal·race·CGO0·actual SQLite/PostgreSQL·Django reference와 generated drift를 통합한다.
GDJ-0077 eager Count와 이 관계 query 확장을 합친 source에서 Hosted ORM checkpoint를 실행한다.
이전 source의 Hosted 결과와 새 코드를 합쳐 PASS로 표시하지 않는다. 아직 설계 관찰 전이며 구현·검증 완료를 주장하지 않는다.
