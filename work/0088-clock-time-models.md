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

## 다음 행동

1. Date 작업의 Hosted 검증과 source closeout을 완료했다.
2. TimeField model/Form/DRF·실제 DB 관찰과 compatibility profile을 고정한다.
3. Clock 값과 전체 IR/생성/ORM/DB/소비자 연결, raw scalar 자원 한도·source binding을 함께 구현한다.
4. 완성된 변경 묶음에 대해 필요한 backend·race·consumer 검증을 실행한다.

현재는 별도 worktree의 값 초안과 독립 reference 준비 단계다. Raw 관찰은 model 85개·Form 152개·serializer 344개와 실제 DB 흐름을 포함한다.
Python 3.12/3.13의 24:00·초 생략 fraction 차이를 직접 관찰했다. TimeField 제품 지원이나 Go runtime 검증을 완료한 상태가 아니다.
