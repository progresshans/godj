---
id: GDJ-0096
status: active
updated: 2026-09-21
baseline_commit: "42ae95d3b1a891e6a0692fb0399968e483f4d907"
integration_owner: "root"
---

# 일대일 관계와 티켓 작업 보고서

## 결과와 선택 이유

Helpdesk의 각 티켓에 작업 보고서를 0..1건 연결하고 생성·조회·수정·삭제를 Form/Admin/API에서 사용한다.
티켓 자체와 보고서의 수명은 구분하며 없는 보고서는 정상적인 부재다. 기존 티켓 필드를 옮기거나 기존 데이터를 자동 삭제하지 않는다.
Category의 접근 범위와 명시적 읽기·쓰기 권한을 보고서의 관계 선택·역방향 조회·수정에도 유지한다.

[카탈로그](../docs/CAPABILITY_CATALOG.md)의 OneToOne은 아직 구현되지 않았다. GDJ-0095가 제공한 양 DB의 column uniqueness를
기반으로 실제 단일 객체 관계를 연결한다. FK에 Unique를 붙인 것만으로 reverse collection을 단일 객체로 바꾸지 않는다.
이 구분은 [고유성 ADR](../docs/adr/0072-column-uniqueness-and-constraint-ownership.md)과 독립 Django 관찰에 근거한다.

## 구현 조건

- [x] 양 DB 독립 기준: required/nullable·명시적/default/hidden reverse, 없는 관계·cache, unique FK와의 차이·입력·저장·삭제와 FK 변경
- [ ] 명시적 선언과 IR cardinality, 생성 metadata·historical wire/digest·autodetect, unique FK와의 구분
- [ ] 양 DB FK+UNIQUE의 생성/변경/reverse, 기존 중복 실패·행/제약/revision 보존과 명시적 수정 후 재시도
- [ ] generated typed/dynamic forward·단일 reverse 객체, 누락/중복·취소·cache 소유권, assignment와 PROTECT/SET_NULL
- [ ] 단일 reverse lookup·eager/prefetch의 NULL/없는 행과 query 결과, 다른 forward/collection 관계와의 조합
- [ ] 실제 작업 보고서의 migration·Form/Admin/API/OpenAPI/client, category·권한과 중복/실행 오류 구분
- [ ] 구조가 다른 cross-app 생성 소비자와 양 DB 실제 실행, 필요한 race/process·generated drift 통합 checkpoint

Schema IR이 관계 의미의 정본이다. 단일 관계의 고유성은 선택적인 사전 조회에 의존하지 않는다.
실제 DB 제약과 transaction이 저장 무결성을 보장하며 사전 검증 뒤의 native 충돌도 처리한다.
정상 부재와 실행 오류를 구분하고 실패·취소의 부분 결과를 cache에 게시하지 않는다.
Python descriptor의 자동 I/O·상호 model cache를 그대로 옮기지 않고 Go의 명시적 context/error와 현행 handle 소유권을 유지한다.

우선 현재 AutoField target과 PROTECT/SET_NULL의 실제 관계 경로를 연결한다. Relation을 PK로 사용하는 모델·arbitrary target·상속·
ManyToMany 등 나머지 Q-013 범위를 완료로 합치지 않는다. 이 작업 중 드러나는 기반 결함은 해당 소비자를 연결하기 전에 해결한다.

## 현재와 다음

[독립 runner](../conformance/runners/django/one_to_one_reference.py)는 GoDj 코드나 기대 fixture를 읽지 않는다.
Public ORM/ModelForm/migration API로 양 DB에서 동작 37개와 FK 변경 경로 2개를 관찰했다.
이는 구현할 의미의 기준이며 GoDj 제품 PASS가 아니다. Source·네 Python 환경·hash seed와 DB 범위는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.

다음은 IR·생성기·historical state와 단일 reverse runtime을 함께 정리하는 설계 변경이다.
제품 코드와 생성 소비자·테스트가 연결된 checkpoint에서 affected 검증을 수행하고,
전체 platform/cold-build가 필요한 통합 milestone은 변경 범위를 보고 정한다.
