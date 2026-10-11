---
id: GDJ-0106
status: done
updated: 2026-10-01
baseline_commit: "99ac532a2cfc5e694bfcad4f0d64a6d42c09857a"
integration_owner: "root"
---

# Binary 모델 필드와 모델 입력 정책

임의 바이트를 문자열이나 파일 이름과 구분하여 모델에 저장한다. BinaryField의 기본 입력 제외와
명시적 `Editable(true)`를 공통 모델 입력 정책으로 구현하고 Form/Admin·JSON/OpenAPI에 연결한다.
Helpdesk의 외부 payload 지문은 서버가 실제 저장된 JSON을 읽어 같은 transaction에서 계산한다.
이 작업의 영향 검증과 source `8e5c2b3e`의 Hosted full 통합을 완료했다. GDJ-0105 source의 결과와 구분한다.

## 구현과 검증

- [x] 고정 Django/DRF의 base64·빈 바이트/NULL·길이·입력 제외와 실제 양 DB 저장/DDL을 독립 관찰
- [x] 소유권이 명확한 바이트 값·정규화 IR·기본값·편집 가능 여부와 엄격한 history
- [x] SQLite BLOB/PostgreSQL bytea·양 DB migration·index/unique·typed/dynamic query와 생성 소비자
- [x] Form/ModelForm·Admin·JSON/OpenAPI의 입력/출력과 서버 소유 필드 보호
- [x] Helpdesk의 실제 저장 payload 지문·인가·CSRF·rollback·독립 client
- [x] 완성된 변경 묶음의 영향 검증·실패 대조·생성 drift와 현행 문서
- [x] 새 scalar와 공통 입력 정책의 Hosted full 통합

선택한 의미와 명시적 차이는 [ADR-0085](../docs/adr/0085-binary-fields-and-model-input-policy.md)가 소유한다.
관찰은 설계 근거이며 Go 구현의 성공 주장이 아니다. 실행 상세는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다. 편집 중에는 필요한 compile 확인만 하고,
완성된 제품·생성기·소비자·테스트 묶음에서 영향 검증과 실제 양 DB의 세 mode를 모아 실행한다.
로컬 전체/cold 검증을 Hosted 전체와 중복하지 않는다.
