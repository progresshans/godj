---
id: GDJ-0089
status: active
updated: 2026-09-20
baseline_commit: "a4d1a6db24410c90fae3231095648e5413dbd039"
integration_owner: "root"
---

# 작업 소요 기간을 모델과 소비자에 연결

Helpdesk의 작업 소요 기간을 날짜/clock/UTC instant와 별도인 Duration 값으로 선언하고 저장·조회·편집한다.
IR/default·generated ORM·typed/dynamic query·SQLite와 PostgreSQL 저장·Form/Admin·JSON/OpenAPI·독립 client를 함께 연결한다.
Zero와 NULL, 음수와 microsecond, 큰 기간의 정확성·overflow와 기존 행의 추가/실패 의미가 범위다.

## 준비에서 확인한 경계

- 고정 Django 6.1은 PostgreSQL에서 interval, SQLite 등에서 signed bigint microseconds를 쓴다.
- Python timedelta의 모델 범위, SQLite int64 저장 범위, Go time.Duration의 int64 nanosecond 범위는 다르다.
  Go time.Duration을 그대로 모델 값으로 삼아 범위를 조용히 줄이거나 float seconds로 큰 기간을 변환하지 않는다.
- Standard Django duration 문자열의 초 이하 입력은 먼저 여섯 자리를 취하지만 ISO fraction은 float/timedelta 경로를 따른다.
  음수·rounding·int64 경계와 범위 초과 오류의 시점을 실제 reference로 고정한 뒤 canonical 값/파서를 정한다.
- 현재 GoDj JSON parser는 canonical signed int64 number만 허용한다. DRF DurationField의 fractional JSON number를 지원하려면
  공통 number 값/decoder 정책을 검토해야 한다. 기간을 연결한다는 이유로 기존 전역 JSON 제한을 묵시적으로 완화하지 않는다.

## 현재 구현과 검증

`duration.Duration`의 전체 모델 범위와 canonical 값·IR/default·typed/dynamic ORM·generation·양 DB 저장·Form/Admin/JSON을 연결했다.
Helpdesk nullable elapsed와 `0010_ticket_elapsed`, OpenAPI 및 독립 ogen client를 함께 구현했다.
공통 JSON number는 원문과 자원 한도를 보존하고 Duration numeric coercion은 필드가 소유한다. [ADR-0067](../docs/adr/0067-duration-model-range-and-number-input.md)을 따른다.

독립 관찰은 model 67·Form 106·serializer 272·JSON number 15·실제 SQLite lifecycle/query와 int64 양 끝 및 overflow다.
JSON numeric 관찰을 추가한 raw SHA256은 `e5cda610c3b48c31c2c9e788db77acaa5954d31cce61149b7c9913017596580e`다.
Fresh Python 3.12.13/3.13.15/3.14.3/3.14.7에서 실제 runtime fingerprint와 전체 관찰을 대조했다.

첫 통합 실행에서 발견한 공통 DB value-kind와 migration loader의 Duration 등록 누락을 수정했다.
생성 relation의 configuration error 전달도 연결했다. 제품 `f06bc7a01060b014a129631f60f5d677978adcef`의 affected 일반/race·양 DB·CGO0·생성물·독립 client·정적 검증을 통과했다. source `7e338bf28d27d12516d6732e7ae5f38f7b19bda5`의 [첫 Hosted full](https://github.com/progresshans/godj/actions/runs/35478903468)은 이전 migration 시나리오의 stale digest 때문에 Python job 네 개가 실패했다. 유일한 변경 시나리오를 추적해 기준을 수정했고 Python 네 버전의 전체 scenario digest를 다시 확인했다. source `79637ef3f5943c9490027723527fb5074b01411f`의 [새 Hosted full](https://github.com/progresshans/godj/actions/runs/35479740366)로 대체했으며 작업은 active다.
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 다음 행동

1. 새 모델·input·양 DB와 실제 Helpdesk·독립 client의 실패 경로를 확인한다.
2. 수정한 제품 `f06bc7a01060b014a129631f60f5d677978adcef`의 affected 일반/race·CGO0·generated drift와 필요한 정적 검사를 수행한다.
3. Date·Time·Duration과 JSON number 기반의 통합 milestone에서 Hosted full을 실행해 전체 플랫폼·고정 PG·Python/current capture를 확인한다.

출시 일정 없이 카탈로그의 남은 기능을 계속 구현한다. 특정 field의 완료와 전체 프레임워크 완성을 구분한다.
