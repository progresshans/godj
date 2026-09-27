---
id: GDJ-0101
status: completed
updated: 2026-09-28
baseline_commit: "76f7b8e44650c874cb61f43e323f52c15707bb15"
integration_owner: "root"
---

# 모델 이메일 필드와 공통 입력 검증

기본 User의 이메일은 CharField와 소비자별 validator로 표현돼 있다. 카탈로그의 EmailField를
Schema IR에서 선언하고 모델·migration·Form/Admin/API가 그 의미를 함께 사용하도록 한다.
GDJ-0100 source의 Hosted 전체는 별도로 진행하며 이 후속 변경에 결과를 전이하지 않는다.

## 구현 조건

- [x] 고정 Django/DRF의 Form·serializer·저장·Char/Email 변환을 독립 관찰하고 출처와 차이를 기록
- [x] EmailField 선언·IR·생성 모델·문자열 query·relation·양 DB의 저장 의미를 연결
- [x] Char/Email의 저장 구조가 같은 변경을 명시적 capability·historical metadata·fenced migration·reverse에 연결
- [x] Form/serializer의 이메일 입력과 EmailInput·실제 Admin/계정 소비자·OpenAPI/독립 client를 연결
- [x] 기존 identity email 데이터와 미입력·NULL·오류·인가·rollback/unknown 조건을 보존
- [x] 변경 묶음의 영향 검사·생성 drift·양 DB·부정 대조와 source 범위를 기록

## 현재와 다음

독립 입력 corpus와 Django/DRF observer를 작성했다. SQLite/PostgreSQL 각각 Form 144개·serializer 96개의 입력 관측,
문법상 잘못된 값의 명시적 저장과 Char→Email→Char의 데이터 보존을 확인했다. PostgreSQL은 DDL 없이, SQLite는 remake로 같은 데이터를 보존했다. 제품·기본 Identity·독립 client 연결과 영향 normal/race/CGO=0·양 DB 검증을 완료했다.
Native Form은 기본 320자, model EmailField는 기본 254자를 사용한다. Go도 입력·저장의 책임을 나누며
단순히 EmailField라는 이름 때문에 모든 ORM 저장에 문법 검사를 추가하지 않는다.
설계는 [ADR-0079](../docs/adr/0079-email-fields-and-input-semantics.md), 실행 결과는 TEST_EVIDENCE가 소유한다.

기본 이메일 필드의 구현은 custom user model·모든 validator·다른 인증 provider 또는 전체 프레임워크 완료를 뜻하지 않는다.

실제 migrate/runserver·양 DB A/B/C 재시작의 명시적 이력을 새 migration까지 갱신하고 각 harness 모드를 검증했다.
생성 drift·migration 변경 없음·소스 결합·부정 대조를 확인했다. 단계별 source와 실행 범위는 TEST_EVIDENCE를 따른다.
새 source의 Hosted 전체 검증은 GDJ-0100 통합 milestone이 소유한다.
