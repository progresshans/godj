---
id: GDJ-0093
status: complete
updated: 2026-09-20
baseline_commit: "06f601ed4d939d4f60b2313b4c70b33eaf4a5939"
integration_owner: "root"
---

# UUID 모델과 외부 연동 참조

Helpdesk Ticket에 외부 시스템의 UUID 참조를 저장하고 Form/Admin/API로 편집하는 흐름을 연결한다.
UUID 값과 NULL, 같은 UUID의 다른 입력 표기, 128-bit 전체 범위와 저장·비교 의미를 보존한다.
GDJ-0092 제품 source를 기반으로 독립 기준을 준비한다. GDJ-0092의 Hosted 결과와 이 작업의 구현/검증을 합치지 않는다.

## 독립 기준

고정 Django 6.1·DRF 3.18.0의 public model/Form/serializer와 SQLite schema editor·ORM을 실행한다.
[Runner](../conformance/runners/django/uuid_reference.py)는 GoDj 코드나 expected fixture를 읽지 않는다.
[Raw](../internal/uuidtest/testdata/django61.json)는 model 62·Form 86·serializer 252·JSON token 13개와 네 representation profile,
SQLite nullable add·literal default·root/forward/F query·집계·rollback·재접속·remove를 보존한다.
Python 네 버전의 fresh 비교를 마쳤으며 hash와 환경별 실행은 통합 시 TEST_EVIDENCE에 기록한다.

기본 출력은 lowercase hyphenated UUID다. Hex·uppercase·braces·URN 등 입력 표기는 원래 public API 결과대로 구분한다.
Form의 공백 정리와 serializer의 입력은 같지 않다. Integer의 128-bit 범위와 JSON의 integer/float token 구분도 유지한다.
Python `UUID(int=bool)`의 public `.int`에 bool이 남는 관찰과 기본 UUID 문자열의 0/1 정규화를 원본에 그대로 남겼다.
Go의 값 설계는 Python 객체의 내부 표현을 복제하는 목표가 아니다.

별도 native PostgreSQL 17.5 probe는 native UUID 정렬과 canonical 출력, 원래 MIN/MAX(uuid)의 SQLSTATE 42883을 확인했다.
`MIN/MAX(value::text COLLATE "C")::uuid`는 현재 profile에서 같은 unsigned UUID 순서의 경계값을 보존했다.
이 probe를 Django PostgreSQL 또는 GoDj 제품 PASS로 표시하지 않는다.

## 연결할 구현

1. 복사 가능한 16-byte UUID 값과 canonical text를 정의하고 IR의 독립 kind/default·wire·hash·resource·clone에 연결한다.
   모든 128-bit pattern과 nil UUID를 지원하고 nullable pointer의 NULL과 구분한다. Typed write/query는 UUID 값을 사용한다.
2. SQLite canonical lowercase 32-hex 저장과 PostgreSQL native UUID parameter/scanner를 연결한다.
   외부 malformed/noncanonical SQLite 저장이 숫자 순서·equality를 깨뜨리지 않도록 검사한다.
   DB별 query·집계 지원과 compiler의 책임을 명시한다. 저장을 float/int64 경유로 변환하지 않는다.
3. Generated model·nullable/default·root/forward query·projection/집계와 historical create/add/remove·autodetect를 함께 연결한다.
   실제 기존 DB에 nullable UUID를 추가하고 실패·취소·rollback·재접속에서 값과 이력을 보존한다.
4. Form/Admin의 원문 검증·canonical 초기값·값 기준 변경 감지, serializer의 null/생략·정확한 integer token·기본 UUID 문자열,
   OpenAPI와 실제 고정 client를 같은 metadata로 연결한다. 권한/CSRF·실패-before-transaction·관계 격리를 유지한다.
5. 실제 Helpdesk `external_reference` 선언·생성·migration·CRUD/PUT/PATCH 및 양 DB의 독립 소비자로 검증한다.

UUID 값의 생성 전략·callable default, UUID primary/foreign key와 uniqueness는 각각 별도 모델/제약 의미가 필요하다.
이 연결만으로 해당 기능이나 나머지 field/ORM/API 범위를 완료로 표시하지 않는다.
제품·생성기·테스트 묶음이 정리된 뒤 영향 범위의 실행 checkpoint를 선택한다.

## 진행과 통합 checkpoint

값·IR·query·양 DB·historical create/add/reverse·생성 외부 소비자의 기반 연결과 관련 일반/race/CGO=0 로컬 checkpoint를 완료했다.
[ADR-0070](../docs/adr/0070-uuid-model-values-and-storage.md)에 모델 값·storage·입력 계층의 책임을 기록했다.
Form/Admin·serializer·OpenAPI·Helpdesk/client의 입력·소비자 연결을 구현했다. 고정 Python Unicode 16의 숫자 입력과 구버전 Python 차이도 독립 관찰에 추가했다. 실제 실행 상세는 TEST_EVIDENCE만 소유한다.

최종 UUID 수직 단면의 Hosted full 통합 checkpoint를 완료했다. 입력 작업에서 Python Unicode profile의 차이를 추가로 확인했으므로
기존 ORM-only 계획을 넓혀 exact/compatibility reference와 지원 플랫폼의 관계·portable·PostgreSQL·명령 소비자를 같은 source에서 확인한다.
로컬 전체는 중복 실행하지 않고 영향 패키지·실제 Helpdesk/client checkpoint를 실행한다.
필수 PostgreSQL 선택에는 UUID adapter와 generated consumer를 포함한다. 기존 Decimal 전체 검증을 새 UUID source의 전체 platform PASS로 옮기지 않는다.

Source `7da91ad5fbd6284622fb372e7d8051120584424e`의 Hosted full 62개 작업·실제 checkout·필수 owner와 cold-build를 확인했다.
이 작업의 모델·입력·Helpdesk/client 연결을 완료하며, UUID PK/FK·uniqueness·generation/callable default와 다른 카탈로그 요구는 계속 남아 있다.
