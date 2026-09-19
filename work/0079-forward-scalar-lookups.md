---
id: GDJ-0079
status: active
updated: 2026-09-19
baseline_commit: "c8bb50df3f540f56f37f5691fff36a6e0f7fcc8b"
integration_owner: "root"
---

# Direct forward 관계의 scalar lookup

## 목표

Category 이름의 부분 일치·여러 이름으로 Ticket을 찾거나, 선택적 담당자의 nullable 값·상태·날짜로 조회할 수 있게 한다.
현재 root scalar가 지원하는 lookup을 직접 forward FK의 대상에도 typed/dynamic 공통 의미로 연결한다.
헌장·기능 카탈로그 전체 구현으로 가는 다음 query 기반 작업이다. 이 작업의 완료가 전체 ORM 또는 전체 프레임워크 완료는 아니다.

## 범위와 설계 경계

- 기존 Integer·Char/Text·DateTime의 nullable target과 Boolean target을 typed adapter·generation에 연결한다.
- Exact, ordered comparison, icontains, isnull, IN을 각 field의 기존 scalar capability에 맞춰 지원한다.
- Required/nullable source FK, AND/OR/NOT, cold/warm Count와 현재 eager projection을 함께 검증한다.
- Field의 선언상 nullable과 optional JOIN 뒤 nullable을 구분한다. 빈 IN과 explicit NULL-only IN의 부정은 다르며,
  공통 AST의 목록 보존·empty-result 분석·양 compiler의 null 보정이 같은 의미를 사용해야 한다.
- Dynamic은 직접 forward의 두/세 segment lookup, policy-before-value와 원자적 batch 실패를 유지한다.
  기존 source-key isnull과 target-field isnull의 경로를 구분한다.
- Reverse의 non-exact/OR/NOT, nested 관계, 다중 eager projection, 관계를 넘는 F와 새로운 schema field 종류는 별도 범위다.
  현재 Related field wrapper를 공유하는 reverse binder를 실수로 확장하지 않는다.

## 독립 관찰과 검증 계획

고정 Django 6.1의 fresh process에서 required/nullable FK, non-null/nullable target, 모든 현재 scalar lookup과
중첩 NOT·root AND/OR를 포함한 748개 관찰을 수집했다. 이는 설계 입력이며 GoDj 제품 검증은 아직 아니다.
제품·생성기·별도 generated consumer·actual SQLite/PostgreSQL·빈 query의 전체 preflight를 한 묶음으로 정리한다.
편집 중에는 필요한 compile만 확인하고, 완성 뒤 affected normal·race·CGO0, 독립 reference·generated drift를 실행한다.

GDJ-0078의 exact-source Hosted ORM을 완료했다. 이 작업의 baseline을 검증하는 독립 checkpoint이며
source·scope·실행 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
그 결과를 이 작업의 새로운 lookup 구현 PASS로 재사용하지 않는다. Hosted 실행 범위와 통합 시점은 관련 변경 묶음의 위험을 기준으로 정한다.
