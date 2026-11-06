---
id: GDJ-0075
status: complete
updated: 2026-09-19
baseline_commit: "8fd8936d634b5038a534936c15a2b1cfac4b853b"
integration_owner: "root"
---

# Scalar IN과 빈 조회의 의미

## 목표

사용자가 선택한 여러 ID·상태·시각을 하나의 typed/dynamic query로 조회할 수 있게 한다. 내부 prefetch에 한정됐던 scalar IN을
현재 scalar 타입의 공개 FieldSet과 dynamic lookup에 연결하고, 빈 목록·NULL·부정 조건·실제 I/O의 의미를 함께 구현한다.
장기 목표는 기능 카탈로그의 전체 완성이며 이 작업은 그중 query 표현력을 넓히는 한 범위다.

## 구현 상태

- Auto/Integer·Char/Text·Boolean·DateTime의 `.In(...)`과 root dynamic `__in`, 입력 snapshot·정확한 타입·policy precedence를 구현했다.
- 공통 immutable AST가 빈 목록·NULL member를 보존한다. 양 compiler와 empty analysis가 nullable negation의 같은 의미를 사용한다.
- 전체 backend plan 검증 뒤 빈 source의 SQL을 생략한다. Model/projection은 zero rows, COUNT는 0, MIN/MAX는 NULL이다.
- 일반·관계·coordinated session의 취소·종료 수명을 보존하고, synthetic cursor도 transaction 종료 뒤 사용할 수 없게 했다.
- 독립 Django 기준, 생성된 외부 모델 소비자, 실제 SQLite/PostgreSQL과 관련 normal/race/CGO0 checkpoint를 완료했다.

장기 의미와 남은 범위는 [ADR-0062](../docs/adr/0062-scalar-membership-and-empty-query-execution.md), source·명령·실패 수정·실행 수는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다. Relation-path·tuple/composite-key·subquery IN과 대규모 목록의 분할은 미완료다.

## 통합 완료

통합 source `adb3ea62f7c8f9a57c623634e2c11b60f04374bc`를 기존 draft PR에 반영했다.
[Hosted ORM](https://github.com/progresshans/godj/actions/runs/35392098111)의 선택된 owner가 모두 완료됐고 최종 scope report를 확인했다.
전체 platform을 요청한 full 실행은 아니다. 환경별 상세·비대상 skip은 TEST_EVIDENCE에 기록했다.
후속 [GDJ-0076](0076-model-choices-and-metadata-migrations.md)의 별도 구현을 이 source의 PASS로 합치지 않는다.
