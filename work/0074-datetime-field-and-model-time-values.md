---
id: GDJ-0074
status: complete
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

## 구현과 검증

[ADR-0061](../docs/adr/0061-datetime-field-and-canonical-instant-values.md)의 UTC 연도 1..9999·microsecond 정책으로
IR/default·Query scalar·양 DB·generated model/Create/Patch/Save·nullable scan·projection/aggregate를 연결했다.
Form은 offset-free 입력을 UTC로 해석하고 JSON은 explicit offset의 RFC3339를 요구한다.
Helpdesk due_at은 실제 generator의 새 0004 migration이며 기존 세 migration은 보존한다.

고정 Django 6.1/Python 3.14.3의 48개 원본 관찰 중 46개는 Form 값·오류·widget을 대조한다.
NUL 뒤를 버리는 2개 reference 결과는 보존하고 [DEV-0011](../docs/DEVIATIONS.md#dev-0011--datetime-입력의-nul을-거부하고-문자열-전체를-해석)의
명시적 invalid 회귀로 구분한다. 전체 locale/DST 호환을 주장하지 않는다.

고정 ogen의 기본 인코더가 소수초를 생략하는 실제 소비자 실패를 발견했다. 표준 date-time과 지원되는 RFC3339Nano extension을
실제 문서에 게시해 재생성하고 값의 정밀도를 유지했다. Client 생성물의 수동 수정이나 검증 완화는 하지 않았다.

Local normal의 최종 관련 패키지, 실제 SQLite/PostgreSQL Helpdesk, 외부 client, pinned reference, 전체 compile/vet와 generated drift를
통과했다. Race/CGO0 checkpoint와 문서 검사도 마쳤다. 상세 실행·실패·scope·source는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 통합 milestone

GDJ-0073 Text와 GDJ-0074 DateTime의 누적 scalar/default/generator/backend/Form/API 변경을 하나의 Hosted full milestone으로 묶는다.
이 full이 Linux/macOS, normal/race/CGO0, 고정 PostgreSQL 17.10, process/reference 및 cold-build 검증을 소유한다.
로컬 전체 matrix를 중복하지 않는다. 기존 b43552a의 full 결과는 이 두 변경의 PASS가 아니다.
첫 full에서 Python 3.12/3.13의 24시 거부와 3.14의 수용을 같은 expected로 비교한 profile 오류를 발견했다.
제품과 기준 fixture는 보존하고 compatibility expected의 차이를 명시했으며 네 Python 버전의 해당 테스트를 각각 통과했다.
수정한 통합 source로 다시 실행하고 terminal 결과·job 누락·skip·artifact를 확인한 뒤 환경별 상태를 갱신한다.

현재 DateTime의 제한된 구현 범위와 Text+DateTime 누적 Hosted full을 완료했다. 검증 source는
`8fd8936d634b5038a534936c15a2b1cfac4b853b`이며 실제 실행과 실패 수정은 TEST_EVIDENCE에 기록했다.
날짜·시간대 기능 전체나 프레임워크 전체가 완료된 것은 아니다. 다음은 공개 scalar IN·빈 목록·NULL/부정 조건·empty query의 실행 의미를 확장한다.
