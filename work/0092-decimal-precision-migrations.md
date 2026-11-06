---
id: GDJ-0092
status: complete
updated: 2026-09-20
baseline_commit: "d106e73d5338cff107623351c48ac4f5778fff8c"
integration_owner: "root"
---

# Decimal 정밀도 변경과 기존 값 보존

Helpdesk의 예상 비용 한도를 늘리는 모델 변경을 기존 데이터가 있는 DB에 적용한다.
Precision·scale을 변경하고 되돌릴 때 값을 반올림하거나 부분 이력을 남기지 않도록 model difference와 실제 migration을 연결한다.
GDJ-0091의 모델·입력·소비자 연결 및 Hosted ORM 위에서 진행한다.

## 확인한 의미

고정 Django 6.1의 public migration·autodetector·schema editor로 12개 profile을 관찰했다.
SQLite는 precision 변경 때 table을 remake하지만 물리 숫자 값은 유지한다. 새 scale의 조회 결과는 반올림될 수 있고,
새 전체 자릿수를 초과하면 migration은 성공해도 조회에서 InvalidOperation이 발생한다. Reverse는 원래 조회 precision을 복구한다.
별도 native PostgreSQL 실험에서는 전체 자릿수 overflow가 원자적으로 실패하지만 scale 축소는 실제 값을 반올림하고 reverse로 복구되지 않았다.
이 native 실험은 Django PostgreSQL 실행과 구분한다. 원본과 실행 환경은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.

GoDj는 [ADR-0069](../docs/adr/0069-exact-decimal-values-and-storage.md)의 정확한 값과 반올림 거부를 precision 변경에도 적용한다.
저장된 값이 변경 전후 field에 모두 정확히 맞는지 검사해야 하며, 오류·취소는 값과 schema/history/revision을 보존해야 한다.

## 구현 연결

1. Choices-only AlterField의 기존 책임을 유지하면서 Decimal precision-only delta를 구분한다.
   Before/After의 정규화·default 유효성·copy·definition·state·graph·autodetect·capability를 함께 처리한다.
   이름·종류·nullability·default·관계 등의 다른 변경을 동시에 허용한 것으로 표시하지 않는다.
2. SQLite BEGIN IMMEDIATE와 PostgreSQL의 정렬된 ACCESS EXCLUSIVE NOWAIT·OID/catalog 재검증을 재사용한다.
   같은 transaction에서 기존 값을 순차 검증하고, 맞지 않는 값·외부 malformed 값·취소·실패는 publication 전에 거부한다.
3. SQLite의 field-scale 독립 BLOB은 precision-only 변경에 remake나 data rewrite가 필요하지 않다.
   PostgreSQL은 값 검증 뒤 NUMERIC(p,s)을 변경하고 실제 typmod를 확인한다. Reverse에도 같은 데이터 적합성 검사가 필요하다.
4. SQL projection의 backend별 차이를 operation별로 보존한다. 현재의 flat statement 수를 단순히 느슨하게 만들어
   혼합 Create/Add/Alter의 누락을 숨기지 않는다. DB-free·자원 한도·복사·진단 redaction·한 번의 출력 조건을 유지한다.
   순수 SQL projection은 live 데이터 사전 검사를 실행하거나 실제 적용 성공을 보증하지 않는다.
5. 실제 Helpdesk 선언 변경·생성·makemigrations·기존 DB migration·Form/Admin/API·독립 client를 연결한다.
   Generated 양 DB 소비자에서 scale 변경·큰 새 값 뒤 reverse 실패와 복구·rollback·재접속·잠금/취소를 검증한다.

제품·생성기·테스트 묶음을 정리한 뒤 affected test와 필요한 DB/race checkpoint를 실행한다.
제품·historical definition·양 DB·SQL projection·Helpdesk 0013·독립 client를 연결했다. 관련 일반·race·선택 CGO=0 로컬 통합 checkpoint를 완료했으며 Hosted full milestone은 별도로 확인한다.
SQL renderer는 operation별 string slot을 유지하여 SQLite의 metadata-only와 PostgreSQL의 physical ALTER를 구분한다. 상세 설계는 ADR-0069와 ADR-0055의 현행 절에 반영했다.

## 통합 milestone

이번 변경은 historical lifecycle·backend SQL renderer 계약·PostgreSQL prepared result cache와 실제 web 소비자를 함께 바꾼다.
로컬은 affected 21 package와 필요한 race/CGO=0만 실행한다. 최종 동일 source의 Hosted full을 한 번 통합 milestone으로 사용하여
ORM·CLI를 별도 중복 실행하지 않고 지원 플랫폼과 Linux 전용 process 회귀를 확인한다. 로컬 전체/cold-build는 반복하지 않는다.

Hosted full은 제품 source 06f601e에서 62개 job을 모두 완료했다. 후속 목록 감사에서 빠진 직접 PostgreSQL 회귀 세 root를
필수 선택에 보강했고, 제품 바이트가 같은 CI-only source 153bf08의 reference scope에서 PostgreSQL 세 mode와 관련 owner의 검증을 완료했다.
전체 플랫폼 실행과 후속 선택 보강의 범위를 구분한다. 정확한 source·job·실행 수는 TEST_EVIDENCE가 소유한다.
