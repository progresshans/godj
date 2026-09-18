---
id: GDJ-0074
status: active
updated: 2026-09-19
baseline_commit: "fca8cbfa38c072c9f8825f000a90f206ce291ddf"
integration_owner: "root"
---

# DateTimeField와 모델 시각 값

## 목표와 의존성

완성 목표를 이어 모델에서 날짜·시각을 저장하고 비교하는 기반을 구현한다. Helpdesk 처리 기한 같은 업무 값을 문자열의
우연한 정렬이나 정수 epoch 관례로 흩어 구현하지 않고 Schema IR→Query→양 DB→Form/Admin/API로 연결한다.
현재 IR·Query Value·Form·serializer에는 시간 scalar가 없으므로 저장·비교 의미를 먼저 확정한다.

## 범위

- 고정 Django 6.1 aware DateTimeField의 값·정밀도·시간대·빈 입력·실패 결과를 실제 관찰한다.
- Go의 시간 값과 schema/default·generated model·nullable pointer·공통 Query scalar를 연결한다.
  같은 순간의 offset 차이와 monotonic clock 정보가 equality·cache·default 정체성에 영향을 주지 않도록 한다.
- SQLite/PostgreSQL의 물리형·driver 변환·지원 범위·정밀도·sort/filter/aggregate 의미를 backend가 소유한다.
  조용한 precision 손실이나 각 backend의 서로 다른 시간 해석을 방치하지 않는다.
- Form/Admin과 JSON/OpenAPI에서 시간대가 있는 입력과 출력, null·생략·invalid 값을 명시한다.
  지역화·DST 모호성의 범위를 확인한 뒤 지원하지 않는 동작은 명시적으로 처리한다.
- Helpdesk nullable `due_at`을 새 migration으로 추가하고 기존 데이터·권한·CSRF·요청 한도와 독립 생성 client를 검증한다.
- auto_now/auto_now_add, 날짜 추출 lookup·임의 timezone 변환·모든 지역화·Date/Time/Duration은 이 작업과 구분해 계속 남은 범위로 관리한다.

## 현재 확인과 미확정

로컬 locked Django 6.1에서 offset-bearing 입력과 UTC 입력, naive/date-only 입력, 빈 값과 잘못된 날짜의 Form 처리를 확인했다.
현재 UTC 설정에서는 naive/date-only 입력이 UTC로 해석되고 9자리 소수초 입력은 microsecond로 잘린다.
SQLite adapter는 aware 값을 connection timezone의 naive 값으로 바꿔 저장하고 PostgreSQL adapter는 driver에 값을 전달한다.
이 관찰만으로 GoDj의 전체 parser·timezone 정책을 채택하지 않는다. 지원 시간 범위·정밀도·정규화와 Form 입력 정책을 먼저 정한다.

## 실행 순서

1. 고정 관찰 roster와 공통 time scalar·backend encoding 결정.
2. IR·historical codec·generator·ORM·DB와 의미 있는 회귀를 하나의 변경 묶음으로 구현.
3. Form/Admin·serializer/OpenAPI·Helpdesk·외부 client까지 연결.
4. 관련 normal/race/CGO0·실제 양 DB·고정 reference와 drift 검증 후 기록.

이 작업의 제품 구현·실제 DB 검증은 아직 시작하지 않았다. GDJ-0073과 이전 Hosted full은 이 작업의 PASS가 아니다.
작업 상세 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 한 번 기록한다.
