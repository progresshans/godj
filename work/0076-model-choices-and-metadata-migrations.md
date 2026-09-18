---
id: GDJ-0076
status: active
updated: 2026-09-19
baseline_commit: "adb3ea62f7c8f9a57c623634e2c11b60f04374bc"
integration_owner: "root"
---

# 모델 선택값과 메타데이터 변경 이력

## 목표

상태·우선순위의 선택값과 표시명을 Schema IR에 한 번 선언하고 generated metadata·Form Select·Admin·serializer·OpenAPI에 연결한다.
기존 Helpdesk priority의 선택값 추가/수정도 새 migration으로 기록하며 기존 행과 이전 migration을 보존한다.
완성 목표는 전체 기능 카탈로그이고, 이 작업은 문자열·일반 정수 choice의 실제 사용 흐름을 연결하는 범위다.

## 확인한 의미

고정 Django 6.1/DRF 3.18.0의 [독립 관찰기](../conformance/runners/django/choices_reference.py)에서 다음 외부 의미를 확인했다.
GoDj 구현의 실제 대조 결과와 환경별 검증은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.

- 선택값 검증은 Field clean·Form·serializer 입력에 있으며 일반 ORM 저장에 자동 DB CHECK를 추가하지 않는다.
- 선택값 밖의 기존 값은 저장·읽기에서 보존되고 표시명 lookup과 serializer 출력은 원래 값으로 돌아간다.
- Choice Form은 Select를 쓰고 문자열을 임의로 trim하지 않는다. Integer의 문자열 표현도 membership을 먼저 검사한다.
- choices/label 변경은 migration의 AlterField 상태를 만들지만 해당 변경 자체의 SQL은 없다.
- Choices 변경은 GoDj의 historical state transition과 writer에 함께 기록한다.

## 연결한 경계

1. IR choices의 value/label/order·clone/hash·검증과 generated descriptor 소유권. 문자열·int64의 닫힌 typed 선언을 우선 연결한다.
2. Choice 입력의 Form·serializer validation, 선택 widget과 escaping·nullable/default/invalid input. 출력은 저장된 범위 밖 값을 숨기지 않는다.
3. Request OpenAPI enum과 response의 실제 출력 범위를 구분하고 독립 client로 왕복한다.
4. Historical definition·autodetector·전진/역방향 state transition·revision/recorder를 연결한다. Metadata-only 변경 때문에 테이블을 재생성하지 않는다.
5. 기존 Helpdesk 데이터에서 선택값 추가·label/order 변경·역방향 복원, Form/Admin/API와 실제 양 DB를 함께 검증한다.

Callable/grouped choices, Python enum 내부 API·이름의 복제, 다른 scalar 종류의 선택값과 일반 physical AlterField는 별도 남은 범위다.
반복 단계마다 전체 검증하지 않고 하나의 구현 묶음을 먼저 완성한다. 편집 중 compile 확인 뒤 관련 consumer/DB/reference/race를 통합한다.

## 현재

IR·생성기·Form/Admin·serializer/OpenAPI와 choices-only AlterField를 연결했다. Helpdesk의 새 0005/0006 migration과 독립 generated model·OpenAPI client를 함께 갱신했다.
[ADR-0063](../docs/adr/0063-model-choices-and-metadata-only-migrations.md)이 장기 의미를 소유한다. 필수 로컬 normal·race·CGO0·양 DB·generated consumer·독립 reference 검증을 마쳤다. 실제 범위와 수정 이력은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록했다. Hosted ORM은 아직 완료 전이다.
기준 source의 GDJ-0075 Hosted ORM은 [별도 실행](https://github.com/progresshans/godj/actions/runs/35392098111)에서 완료했다. 이번 choices 변경을 그 결과에 합치지 않는다.
