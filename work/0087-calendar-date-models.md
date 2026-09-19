---
id: GDJ-0087
status: active
updated: 2026-09-20
baseline_commit: "917fda6735b620c5b238ed8d77002d5cf8e3ce6b"
integration_owner: "root"
---

# 시간대 없는 날짜를 모델과 소비자에 연결

## 결과와 범위

Helpdesk의 방문 예정일처럼 시각·시간대가 없는 달력 날짜를 모델에서 선언하고 저장·조회·편집한다.
DateTime의 UTC instant 의미와 구분되는 Date 값을 Schema IR·generated model/field·typed/dynamic Query AST,
실제 SQLite/PG migration·CRUD·Form/Admin·JSON/OpenAPI·독립 client까지 연결한다.
유효한 Gregorian 날짜·윤년·연도 경계, nullable/default·생략·명시적 null과 기존 행의 안전한 추가를 함께 다룬다.

현재 장기 범위와 구현 순서는 [기능 카탈로그](../docs/CAPABILITY_CATALOG.md), [로드맵](../docs/ROADMAP.md)을 따른다.
DateTime과 다른 날짜 의미는 [ADR-0061](../docs/adr/0061-datetime-field-and-canonical-instant-values.md)과 혼동하지 않는다.
일반적인 시간대/지역화·DateTime을 Date로 변경하는 data migration은 별도의 요구다.

## 확인한 경계

- 현재 IR·Query Value·generator·scanner에는 DateTime만 있고 Date는 없다.
- 고정 Django 6.1의 model DateField는 aware datetime을 기본 시간대로 옮겨 날짜를 취한다.
  Form DateField의 datetime 처리는 다르며 DRF 3.18.0 DateField는 datetime을 명시적으로 거부한다.
  이 입력 경계들을 하나의 coercion 규칙으로 합치지 않는다.
- Go 값의 초안은 `calendar.Date{Year, Month, Day}`다. Comparable value로 복사되며 invalid literal/zero는 검증에서
  거부한다. `New`와 canonical parser는 error를 반환하고 실패한 decode는 기존 receiver를 보존한다.
  Literal/default·driver scan·comparison·JSON·generator의 전체 경계를 확인한 뒤 공개 형태를 확정한다.
  time.Time의 시간대나 시각을 암묵적으로 버리는 API는 만들지 않는다.
- GDJ-0086의 제품은 기존 Draft PR에서 Hosted ORM 검증을 완료했다. 해당 결과는 이 Date 작업의 PASS가 아니다.

## 구현과 검증

1. 독립 Django/DRF의 날짜 parse·오류·Form/JSON presence/default·실제 DB query/migration 관찰을 고정한다.
2. 날짜 값과 IR·generator·typed/dynamic ORM·SQLite/PG의 공통 표현을 함께 구현한다.
3. 기존 Helpdesk 행의 nullable 날짜 추가와 Form/Admin·PUT/PATCH·OpenAPI/client를 연결한다.
4. 윤년·경계·날짜/시각 혼입·잘못된 값·실패 mutation·취소·query/cache·기존 데이터/역방향을 검증한다.
5. 한 변경 묶음 뒤 affected checkpoint를 실행하고 source와 환경별 결과를 TEST_EVIDENCE에 기록한다.

## 현재 상태와 다음 행동

별도 `feature/calendar-date-models`에서 고정 reference의 public DateField source와 현재 DateTime 경계를 확인했다.
독립 model·Form·serializer와 실제 DB 관찰을 고정하고 fresh process 재생을 확인했다.
`calendar.Date` 값·canonical text/JSON·실패 decode 보존 초안과 Gregorian cycle/경계 검증을 작성했다.
Compile-only만 확인했고 Go runtime은 실행하지 않았다. 다음은 IR·Query AST·generator·양 DB와 소비자 연결이다.
