---
id: GDJ-0096
status: active
updated: 2026-09-22
baseline_commit: "42ae95d3b1a891e6a0692fb0399968e483f4d907"
integration_owner: "root"
---

# 일대일 관계와 티켓 작업 보고서

## 결과와 선택 이유

Helpdesk의 각 티켓에 작업 보고서를 0..1건 연결하고 생성·조회·수정·삭제를 Form/Admin/API에서 사용한다.
티켓 자체와 보고서의 수명은 구분하며 없는 보고서는 정상적인 부재다. 기존 티켓 필드를 옮기거나 기존 데이터를 자동 삭제하지 않는다.
Category의 접근 범위와 명시적 읽기·쓰기 권한을 보고서의 관계 선택·역방향 조회·수정에도 유지한다.

[카탈로그](../docs/CAPABILITY_CATALOG.md)의 OneToOne을 GDJ-0095가 제공한 양 DB의 column uniqueness 위에 연결한다.
선언·이력·단일 reverse 객체의 기반 구현을 마쳤으며 작업 보고서의 전체 소비자 연결은 진행 중이다. FK에 Unique를 붙인 것만으로 reverse collection을 단일 객체로 바꾸지 않는다.
이 구분은 [고유성 ADR](../docs/adr/0072-column-uniqueness-and-constraint-ownership.md)과 독립 Django 관찰에 근거한다.

## 구현 조건

- [x] 양 DB 독립 기준: required/nullable·명시적/default/hidden reverse, 없는 관계·cache, unique FK와의 차이·입력·저장·삭제와 FK 변경
- [x] 명시적 선언과 IR cardinality, 생성 metadata·historical wire/digest·autodetect, unique FK와의 구분
- [x] 양 DB FK+UNIQUE의 생성/변경/reverse, 기존 중복 실패·행/제약/revision 보존과 명시적 수정 후 재시도
- [x] generated 단일 reverse 객체·exact typed/dynamic 조회·prefetch와 forward eager, 누락/중복·취소·cache 소유권·PROTECT/SET_NULL
- [ ] forward/reverse assignment의 객체·cache·저장 의미와 unsaved/required/nullable 실패 경로
- [x] 단일 reverse의 관계/필드 isnull·nullable/Boolean·비교/IN/검색·AND/OR/NOT, 다른 reverse/collection 조건과의 조합
- [x] 단일 reverse와 forward typed eager tree의 조합, 이어지는 관계 경로의 행·cache 소유권
- [x] facade reverse selector·문자열 mixed path, 지연 접근과 저장/형제 cache 보존
- [ ] 실제 작업 보고서의 migration·Form/Admin/API/OpenAPI/client, category·권한과 중복/실행 오류 구분
- [x] 기반 cross-app 생성 소비자·양 DB·race/CGO 비활성·기존 생성 소비자/외부 compile·generated drift checkpoint
- [ ] 위 소비자 전체를 연결한 source의 필요한 process·platform 통합 milestone

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

[ADR-0073](../docs/adr/0073-one-to-one-cardinality-and-reverse-objects.md)에 선언·migration과 단일 reverse 소유권을 기록했다.
기존 37개 관찰은 유지했고 조회 전용 독립 모델에서 41개 조건의 결과·SELECT 수·JOIN 형태를 양 DB와 비교했다.
이는 기존 관찰 전체의 제품 parity가 아니다. 12개 eager 경로의 row/presence·초기 SELECT·JOIN 형태도 양 DB와 비교했다.
Typed reverse/mixed eager tree와 generated selector/FromSelected bridge는 공통 runtime에 연결했다. Nullable child·ancestor 부재,
잘못된 FK·중복 child·전체 실패 후 재시도·동시 조회·owner 기준 Fresh를 검증했다. Django reciprocal path의 추가 SELECT와
GoDj warm cache 0 I/O 차이는 DEV-0018에 기록했다.

Facade의 `.Related` selector·문자열 mixed path도 같은 tree와 generated object factory에 연결했다.
부모 저장 후 reverse 조회, 지연 child SELECT 관찰, 정방향 FK 변경 후 reverse 형제 cache 보존, namespace·origin·외부 Go 타입 거부를 검증했다.
Incoming 관계가 있으면서 outgoing FK를 갖는 모델도 삭제할 수 있도록 binding을 정리하고, 독립 Django와 양 DB에서 PROTECT·삭제 후 부모 보존을 확인했다.
다음은 OneToOne assignment의 전체 의미와 작업 보고서의 실제 입력 소비자다.
전체 platform/cold-build 통합은 소비자 연결 milestone이 소유한다.
