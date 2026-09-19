---
id: GDJ-0087
status: active
updated: 2026-09-20
baseline_commit: "13937986914317d365f580f2adead277d73e2ce1"
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

- 작업 시작 시 IR·Query Value·generator·scanner에는 DateTime만 있었다. 별도 Date arm과 generated/scanner 연결을 추가했다.
- 고정 Django 6.1의 model DateField는 aware datetime을 기본 시간대로 옮겨 날짜를 취한다.
  Form DateField의 datetime 처리는 다르며 DRF 3.18.0 DateField는 datetime을 명시적으로 거부한다.
  이 입력 경계들을 하나의 coercion 규칙으로 합치지 않는다.
- Go 값은 `calendar.Date{Year, Month, Day}`다. Comparable value로 복사되며 invalid literal/zero는 검증에서
  거부한다. `New`와 canonical parser는 error를 반환하고 실패한 decode는 기존 receiver를 보존한다.
  Literal/default·driver scan·comparison·JSON·generator의 의미는 [ADR-0065](../docs/adr/0065-calendar-date-field-and-input-boundaries.md)에 정했다.
  time.Time의 시간대나 시각을 암묵적으로 버리는 API는 만들지 않는다.
- GDJ-0086의 제품은 기존 Draft PR에서 Hosted ORM 검증을 완료했다. 해당 결과는 이 Date 작업의 PASS가 아니다.

## 구현과 검증

1. 독립 Django/DRF의 날짜 parse·오류·Form/JSON presence/default·실제 DB query/migration 관찰을 고정한다.
2. 날짜 값과 IR·generator·typed/dynamic ORM·SQLite/PG의 공통 표현을 함께 구현한다.
3. 기존 Helpdesk 행의 nullable 날짜 추가와 Form/Admin·PUT/PATCH·OpenAPI/client를 연결한다.
4. 윤년·경계·날짜/시각 혼입·잘못된 값·실패 mutation·취소·query/cache·기존 데이터/역방향을 검증한다.
5. 한 변경 묶음 뒤 affected checkpoint를 실행하고 source와 환경별 결과를 TEST_EVIDENCE에 기록한다.

## 현재 상태와 다음 행동

Calendar Date의 값/IR/AST·generated ORM·양 DB migration·Form/Admin·Helpdesk·OpenAPI/독립 client를 연결했다.
독립 reference와 정상·실패·취소·copy·rollback·재연결·역방향 검증을 추가했고 affected 일반/race/CGO0과 생성물 drift·vet을 통과했다.
제품 source `b2b01f80f9a8b04c1893f8dc5d24e9b19ba4b087`를 기존 Draft PR에 통합했다.
[Hosted ORM](https://github.com/progresshans/godj/actions/runs/35471559786)의 terminal 결과와 실제 scope를 확인한다. 실행별 source·범위는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0087--calendar-date의-모델소비자-연결)에 기록한다.
