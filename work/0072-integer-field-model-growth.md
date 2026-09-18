---
id: GDJ-0072
status: active
updated: 2026-09-19
baseline_commit: "8ade467afe918474e9c42fd066edbfe7972ee600"
integration_owner: "root"
---

# 일반 정수 필드와 기존 모델의 성장

## 결과와 범위

완성 목표는 [헌장](../docs/CHARTER.md)과 [기능 카탈로그](../docs/CAPABILITY_CATALOG.md)의 전체 기능·기반이다.
이 작업은 그 목표를 향한 다음 구현 묶음이며 전체 완료를 뜻하지 않는다. 직전 사용자가 승인한 문서 변경을 보존한다.

현재 정수는 AutoField와 ForeignKey에만 쓰여 일반 업무 필드로 선언·입력·수정할 수 없다.
GoDj `IntegerField`는 플랫폼에 독립적인 signed int64를 사용하고, nullable과 명시적 정수 default를 지원한다.
외부 저장·범위 의미의 참조는 고정된 Django 6.1 `BigIntegerField`다. Django의 모든 정수 종류를 하나의 이름으로
호환한다고 주장하지 않으며 JSON의 현행 canonical integer 규칙은 유지한다.

- Schema IR → historical migration → 양 DB → generated model/CRUD/query → Form/Admin/serializer/OpenAPI를 연결한다.
- Auto primary key와 수정 가능한 일반 정수의 typed capability를 구분한다. nullable 정수는 null과 zero를 구분한다.
- Helpdesk에 nullable priority를 새 migration으로 추가한다. 기존 0001과 데이터를 보존하고 기존 row는 NULL로 남긴다.
  default가 있는 정수의 신규 모델 생성·생략/명시 값 의미는 별도 fixture에서 검증한다.
- 명시적 업무 범위 제한은 application validation이 소유한다. 숫자의 저장 표현을 문자열이나 float로 우회하지 않는다.
- schema/current format은 미배포 현행 형식을 확장한다. 호환만을 위한 과거 내부 API·생성 ABI를 유지하지 않는다.

## 구현과 검증

- [x] IR/DSL·migration decode/write/replay·SQLite/PostgreSQL 물리 schema
- [x] typed/dynamic query·nullable projection/aggregate·생성 CRUD와 read-only primary key
- [x] Form/Admin·serializer/OpenAPI·Helpdesk 새 migration과 기존 데이터 보존
- [ ] fixed Django source/실제 비교, 관련 normal/race/CGO0 및 생성 client 갱신
- [ ] 통합 검증 소유 범위·미실행 환경·현행 문서 갱신

편집 중 compile만 확인하고 설계 변경 묶음이 준비되면 영향을 받는 검증을 통합한다.
ORM·migration·generator의 공통 경계를 변경하므로 통합 시점에는 해당 Hosted scope와 source를 확인한다.
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 현재 상태와 다음 행동

정수 field와 별도 Auto selector, Form/Admin/API, 0002 migration과 generated consumer를 함께 구현했다.
로컬 관련 normal 검증·고정 Django 관찰·생성물 drift를 완료했다. 단독 관계 product 생성물 누락과 새 관계 테스트가
지원 밖 비교 연산을 사용한 오류를 수정한 뒤 해당 패키지를 다시 통과했다. [ADR-0059](../docs/adr/0059-signed-integer-field-and-model-growth.md)가 의미를 소유한다.

이번 통합 milestone은 GDJ-0070/0071/0072의 누적 제품 소스에 Hosted full을 한 번 적용한다.
로컬 전체 matrix를 반복하지 않으며 OS·race·CGO0·고정 PostgreSQL·process/reference 검증은 그 실행을 소유자로 삼는다.
필수 job과 source SHA가 일치하는 최종 결과를 확인하기 전까지 이 work의 통합 검증은 미완료다.

첫 Hosted 실행에서 이전 API middleware 변경의 conformance/process consumer 네 곳이 누락된 것을 발견해 같은 API 인스턴스로
수정했다. 전체 compile/vet와 관련 63개 실제 테스트를 통과했으며 수정 source의 full 통합을 다시 확인한다.
