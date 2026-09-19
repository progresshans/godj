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

## 현재 준비

독립 runner의 model 67개·Form 106개·serializer 272개, 실제 SQLite add/reopen/update/reverse·root/relation query 각 9개·Min/Max와
signed int64 microsecond 양 끝값 및 범위 밖 write 거부를 기록했다. Raw SHA256은
`5c9eca13f8632e3f2ea79a8d0a017c5905a432da8b75073730ff4fb6cb969dba`다.
Python 3.12.13/3.13.15/3.14.3/3.14.7의 fresh replay는 각각 1 test PASS, skip/warning/exception 0이다.
범위 초과 시 실제 SQLite가 OverflowError를 반환하고 기존 행을 보존하는 것도 원문에 남겼다.

`duration.Duration{Days, Microseconds}`의 normalized day/subday 값과 canonical 표현 초안을 만들었다.
Django 모델의 ±999999999일 범위를 유지하고 SQLite의 signed int64 microsecond 변환 한도는 별도 error로 분리한다.
초기 int64-only 초안은 모델 범위를 DB 한도로 제한하므로 보완했다. 음수 정규화·전체 모델 경계·backend 경계와 중간 곱 overflow를 구분한다.
`go test -exec /usr/bin/true ./duration`의 compile 확인만 했으며 Go runtime 검증과 제품 통합을 완료하지 않았다.

## 다음 행동

1. Duration model/Form/DRF·실제 SQLite 저장 범위·negative/zero/overflow의 독립 관찰을 고정한다.
2. 필요하면 공통 number 기반을 먼저 명시하고, 기간 값·양 DB representation과 canonical wire를 설계한다.
3. 의미가 확정된 한 변경 묶음으로 생성·DB·소비자와 실패 경로를 구현하고 영향 범위의 실제 검증을 실행한다.

Time의 Hosted ORM 완료 후 이 작업을 활성화했다. 현재는 값·입력 기반의 구현 단계다. Duration 제품 지원이나 Go runtime 검증을 완료한 상태가 아니다.
