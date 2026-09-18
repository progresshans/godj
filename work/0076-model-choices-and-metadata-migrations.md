---
id: GDJ-0076
status: active
updated: 2026-09-19
baseline_commit: "adb3ea62f7c8f9a57c623634e2c11b60f04374bc"
integration_owner: "root"
---

# 모델 선택값과 메타데이터 변경 이력

## 목표와 현재 작업

문자열·일반 정수의 선택값과 표시명을 Schema IR에 한 번 선언하고 generated metadata·Form Select·Admin·serializer·OpenAPI에 연결한다.
기존 Helpdesk priority의 선택값 추가/수정은 새 migration으로 기록하며 기존 행과 이전 migration을 보존한다.
전체 완성 목표는 기능 카탈로그이며 일반 physical AlterField나 다른 scalar의 choices는 별도 남은 범위다.

`feature/field-choices`의 별도 작업 사본에서 고정 Django 6.1/DRF 3.18.0의 독립 관찰과 초기 IR/DSL·generated metadata 구현을 진행했다.
Default와 choices의 공통 scalar, value/label/order의 clone·hash·equality와 선언 검증을 연결하고 실제 generator로 기존 생성물을 갱신했다.
현재 compile 확인 단계다. 기능·비교 PASS가 아니며 기준 source의 IN 검증에 포함하지 않는다.

## 이어갈 경계

- Django의 choices는 입력/명시적 검증의 의미이며 일반 저장에 자동 DB 제약을 추가하지 않는다. 기존 범위 밖 값은 읽기·출력에서 보존한다.
- Choice Form은 Select를 쓰고 임의 trim을 하지 않는다. Request enum과 response의 실제 출력 범위를 구분한다.
- 선택값·표시명·순서 변경은 SQL 없이도 historical migration 상태에 남는다. 현재 writer의 기존 field metadata 변경 거부 경계를 넓혀야 한다.
- Historical codec·전진/역방향 state transition·revision/recorder, Form/serializer/Admin/OpenAPI와 독립 consumer를 먼저 함께 완성한다.
- 구현 묶음 뒤 실제 양 DB와 reference·race·필요한 generated drift를 검증한다. Callable/grouped choices와 Python enum 내부 호환은 이 범위가 아니다.
