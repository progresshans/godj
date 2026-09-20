---
id: GDJ-0095
status: active
updated: 2026-09-21
baseline_commit: "ef9b05c0ec829d5cb38c0944507a70f0a5e1f483"
integration_owner: "root"
---

# 모델 고유성과 외부 참조 중복 방지

## 결과와 범위

모델에 선언한 고유성을 실제 DB가 보장하고 Form/Admin/API의 명시적 오류로 연결한다.
Helpdesk의 외부 UUID 참조 중복 방지를 실제 소비자로 사용한다. 사전 조회만으로 동시 쓰기를 보호했다고 간주하지 않는다.
Schema IR이 고유성의 정본이며 생성 metadata·historical definition·autodetect·physical catalog와 같은 의미를 유지한다.
관련 현재 범위는 [Backend Matrix](../docs/BACKEND_MATRIX.md), 장기 목표는 [기능 카탈로그](../docs/CAPABILITY_CATALOG.md)가 소유한다.

일반 scalar와 현재 ForeignKey의 column uniqueness를 같은 선언으로 다룬다. 기본 PK의 고유성은 유지한다.
FK의 고유성만으로 relation facade를 one-to-one 객체 API로 바꾸지 않는다. 모델의 composite/조건부/expression constraint와
새 OneToOneField는 추가 표현·소비자를 갖춰 이어갈 전체 목표이며, column unique 지원으로 완료 처리하지 않는다.

## 기준 관찰

[독립 Django runner](../conformance/runners/django/unique_reference.py)는 SQLite와 native PostgreSQL의
12종 scalar 및 별도 JSON canonical 저장 profile, nullable 값·실제 중복·문자열 case·UUID 별칭·같은 numeric 값의 충돌을 관찰한다.
ModelForm의 duplicate·자기 instance 제외·blank/null·UUID 정규화, unique FK와 reverse manager도 구분한다.
Public migration state·autodetector·schema editor로 기존 데이터의 unique 추가/제거·실패·명시적 데이터 수정 후 재시도·재접속,
nullable unique AddField·constant default 충돌, inbound FK 보존을 관찰한다. Savepoint/outer rollback과 두 connection의 경쟁도 포함한다.
Raw와 실행 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유하며 GoDj 제품 구현의 PASS가 아니다.

참조 의미는 [Django unique](https://docs.djangoproject.com/en/6.1/ref/models/fields/#unique),
[PostgreSQL unique constraint](https://www.postgresql.org/docs/17/ddl-constraints.html#DDL-CONSTRAINTS-UNIQUE-CONSTRAINTS),
[SQLite unique index](https://www.sqlite.org/lang_createindex.html#unique_indexes)와 실제 pinned 관찰에 근거한다.
기존 JSON/Float/Decimal의 저장·정밀도 정책과 backend 차이는 해당 ADR·deviation을 유지한다.

## 구현과 검증

- [x] 양 DB 독립 관찰과 네 Python 버전의 fresh reference 검증
- [ ] Schema IR/선언·정규화·소유권·생성 metadata·historical wire/digest·unique-only 변경 분류
- [ ] 양 DB create/add/remove/alter와 sqlmigrate, 선언된 unique constraint/index의 정확한 catalog 검증
- [ ] 기존 중복 데이터의 실패/rollback·recorder/revision 보존·재시도·reverse·reopen·inbound FK/sequence 보존
- [ ] 실제 insert/update 충돌의 안정적 무결성 오류·취소·경쟁 쓰기와 정상 결과 보존
- [ ] 재사용 가능한 context/error 기반 검증과 Helpdesk Form/Admin/API·OpenAPI/client 연결
- [ ] 관련 생성 소비자·DB/process/race checkpoint와 고정 source의 필요한 통합 milestone

고유성 검사는 DB의 저장·equality·collation 의미를 따른다. SQL NULL의 기본 distinct 정책과 빈 값/JSON null/zero UUID를 구분한다.
누적 history와 실제 catalog를 함께 검증하고, 알 수 없는 index/constraint를 허용하거나 데이터 삭제로 migration을 통과시키지 않는다.
부분 수정이나 사전 조회 결과를 원자적 무결성의 대체물로 사용하지 않는다. 권한 검사는 조회·검증·쓰기보다 앞에 유지한다.
검증용 저장소·필수 실행·skip/실패/환경 범위는 이전 작업의 성공과 합치지 않는다.

## 현재 상태와 다음 행동

독립 기준을 확보했다. 아직 GoDj 모델에 unique를 선언하거나 실제 제약을 만드는 제품 구현은 없다.
`schema/ir/types.go`·`field_change.go`, `migrations/definition`, 생성 metadata에서 같은 속성을 먼저 연결한다.
이어 SQLite remake/index 소유권과 PostgreSQL constraint/index catalog를 구현하면서 기존 revision-fenced lifecycle을 유지한다.
현재의 physical catalog 거부를 단순히 제거하지 않고 새 선언이 정확하게 소유하는 물리 객체만 허용한다.
