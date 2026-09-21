---
id: GDJ-0097
status: active
updated: 2026-09-22
baseline_commit: "4f92d68869d5491c4b56e83da40b79a4c7866bb7"
integration_owner: "root"
---

# 모델 복합 고유성과 Category 라벨

## 결과와 선택 이유

Helpdesk의 각 Category에 라벨 사전을 두고 같은 Category 안에서만 라벨 이름을 고유하게 저장한다.
다른 Category의 같은 이름은 허용한다. Category는 서버가 배정한 접근 범위이며 Form/API 입력으로 임의 변경하지 않는다.
라벨의 생성·조회·수정·삭제를 Admin/API/OpenAPI와 독립 client까지 연결한다.

[카탈로그](../docs/CAPABILITY_CATALOG.md)의 schema constraint·ManyToMany 기반을 순서대로 넓힌다.
현재 column Unique만으로는 `(category, name)`이나 다음 association의 두 key 조합을 표현할 수 없다.
고정 Django 6.1의 자동 ManyToMany intermediary가 두 key의 unique_together와 CASCADE를 사용하는 것도 확인했다.
이번 작업은 실제 Label 소비자가 요구하는 모델 단위 복합 고유성이다. ManyToMany·CASCADE 자체와 conditional/expression constraint는
계속 남은 카탈로그 범위로 관리하며 이 작업의 완료에 포함하지 않는다.

## 구현 조건

- [ ] 독립 Django 기준: 같은/다른 Category의 중복, required/nullable 조합, self update·changed fields, 사전 검증·native 오류와 migration 실패/복구
- [ ] Schema IR의 모델 단위 제약과 이름·field 순서·storage column 소유권, 생성 metadata·clone/equality/digest
- [ ] historical definition·autodetect·create/add/remove/reverse, 기존 중복 실패 시 row·catalog·recorder/revision 보존
- [ ] SQLite/PostgreSQL native 복합 UNIQUE와 물리 ownership 검증, table remake·named index/constraint·target schema 보존
- [ ] 공통 ORM 사전 검증과 self exclusion, 확인된 중복/실행 오류 구분·native race·취소·rollback/unknown outcome
- [ ] Label 모델·migration·scoped Admin/API/OpenAPI/client와 명시적 permission·CSRF
- [ ] generated drift·양 DB·관련 race/CGO/process와 필요한 통합 검증, source·환경별 증거와 제한 기록

## 현재와 다음

아직 새 제약이나 Label을 구현하지 않았다. 고정 Django의 public UniqueConstraint와 ModelForm/migration 독립 관찰을 임시 artifact에서 준비했다.
현재 column uniqueness의 IR·historical wire·backend physical ownership·validation 경로를 확인했으며, 다음은 독립 runner·관찰을 현행 소스에 연결하고 선언·이력을 구현하는 일이다.
기준 관찰의 통과를 GoDj 복합 고유성의 구현이나 검증 완료로 합치지 않는다.
기존 내부 형식이나 테스트 모양을 보존하려고 별도의 호환 계층을 만들지 않는다.
API 표면과 제약의 지원 범위는 실제 소비자·양 DB 실패 의미를 확인하면서 정하며, 사전 검사만으로 고유성을 보장하지 않는다.
작업과 필요한 기반의 관계는 [로드맵](../docs/ROADMAP.md), 전체 완성 기준은 [헌장](../docs/CHARTER.md)이 소유한다.
