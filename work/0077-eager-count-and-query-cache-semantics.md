---
id: GDJ-0077
status: active
updated: 2026-09-19
baseline_commit: "d0f079481d660682c7a18884ce2c305234f6ad38"
integration_owner: "root"
---

# 관계 조회의 Count와 캐시 의미

## 목표

관계를 함께 읽는 목록에서 같은 query의 Count로 페이지네이션할 수 있게 한다. Typed·dynamic·facade가 공통 runtime을 사용하고
필터의 JOIN multiplicity, Distinct와 슬라이스를 보존한다. 이 작업은 전체 프레임워크 완성 목표의 한 기능 묶음이다.

## 의미와 구현

고정 Django 6.1의 독립 SQLite 관찰 22개 경우에서 eager materialization만 cold Count에서 제외되는 것을 확인했다.
필터에 필요한 JOIN은 남고, full All cache가 있으면 개수를 재사용한다. Cold Count는 eager와 원래 QuerySet의 cache를 채우지 않는다.
Invalid binding과 context 오류를 제거해서 집계를 성공시키지 않는다. 미지원 관계 조건은 기존 backend capability의 명시적 오류를 따른다.
Count는 관련 객체를 materialize하거나 그 객체의 무결성을 검사하는 terminal이 아니다.

[기존 select_related 결정](../docs/adr/0029-one-hop-forward-select-related.md)에 Count의 장기 의미를 추가하고,
IR·AST 자체와 양 backend의 기존 COUNT 구현을 재사용한다. 새로운 다중/중첩 eager materialization은 이 범위에 포함하지 않는다.

## 검증

제품·생성기·독립 소비자·실제 SQLite/PostgreSQL·실패 및 cache/race 회귀를 하나의 묶음으로 정리한다.
편집 중 compile 확인만 하고, 완성된 변경 묶음에서 필요한 affected normal·race·CGO0·generated drift와 Django reference를 실행한다.
현재 구현 중이며 제품 runtime 검증은 아직 완료하지 않았다. 실제 source와 실행 범위는 TEST_EVIDENCE에 기록한다.
