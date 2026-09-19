---
id: GDJ-0088
status: active
updated: 2026-09-20
baseline_commit: "2841206da18aef2b68eb4ebb0b36b4a8dd846be4"
integration_owner: "root"
---

# 시간 전용 값을 모델과 소비자에 연결

## 결과와 범위

Helpdesk 방문 예정일에 대응하는 방문 시간을 달력 날짜·시간대 없는 clock 값으로 선언하고 저장·조회·편집한다.
TimeField의 IR·default·generated model/field·typed/dynamic query·양 DB TIME·Form/Admin·JSON/OpenAPI와 독립 client를 연결한다.
Midnight zero value와 NULL, microsecond precision, 지원 범위 밖 clock·시각/날짜 혼입·실패 경로를 구분한다.
일반 timezone/locale 선택이나 Duration/DateTime 연산을 이 필드의 지원으로 추정하지 않는다.

## 준비에서 확인한 기준

고정 Django 6.1/DRF 3.18.0/Python 3.14.3의 public TimeField·Form·dateparse 소스를 읽고 입력을 관찰했다.
Model/JSON은 ISO 입력의 offset을 제거하고 clock components를 유지하지만 Form은 그 문자열을 거부한다.
Python 3.14.3의 24:00은 model/JSON에서 midnight가 되고 Form에서는 invalid다. Fraction 7자리는 model/JSON에서 truncate되고
Form에서는 invalid다. Source의 설명문만으로 이 경계들을 합치지 않는다. 고정 raw reference와 Python 버전별 차이를 먼저 기록한다.

`clock.Time{Hour, Minute, Second, Microsecond}`의 comparable 값 초안을 만들었다. Midnight는 유효한 zero이고 NULL은 pointer nil이다.
Canonical 표현은 HH:MM:SS 및 nonzero microsecond의 여섯 자리 fraction이다. Constructor/parser는 오류를 반환하고 실패한 decode는 receiver를 보존한다.
공개 타입·default/scanner/DB·JSON grammar는 전체 연결과 독립 관찰을 확인하며 확정한다.

## 현재 구현과 다음 행동

Date 작업은 source `8aa3c477e9ef5cfa733d0a2dea1d33c6d402d3b0`의 Hosted ORM success로 완료했다.
Time은 별도 Go 값·IR/AST/default·generator·양 DB TIME·Form/Admin·Helpdesk service_at·OpenAPI·독립 client까지 연결했다.
[ADR-0066](../docs/adr/0066-clock-time-field-and-precision-boundaries.md)에 채택한 의미를 기록한다.

첫 affected checkpoint에서 Form 초기 소수초의 Django 기본 위젯 차이와 fixture 갱신 누락을 확인했다.
기본 위젯과 소수초 보존 위젯의 독립 관찰을 모두 남기고, 정밀도를 보존하는 Go 입력은 후자와 대조한다.
수정한 source의 affected 일반/race·양 DB·CGO0·생성물/독립 client·Python profile을 통과했다.
제품 source `9f0ffa8aeea143dd0da48789761235004dd4fa59`를 기존 Draft PR에 통합하고 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35475136652)을 실행했다.
해당 source의 terminal 결과와 실제 scope를 확인한다. 상세 실행과 source는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.
