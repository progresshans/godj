# 열린 질문

지금 답이 필요한 질문만 유지한다. 과거 조사·phase·CI 이력은 [Git 기록](https://github.com/progresshans/godj/blob/da1bfc524c4f205075fc7fac7f00b437473a5e1f/docs/OPEN_QUESTIONS.md)에 있다.
이미 선택한 개별 단면을 아래의 넓은 미결정 범위 때문에 다시 미구현으로 취급하지 않는다.

| ID | 현재 남은 질문 | 현재 기준 |
|---|---|---|
| Q-010 | project scaffold, installed version negotiation와 첫 지원 릴리스 이후 upgrade 정책 | 현재 CLI/generate/migration command는 구현됨 |
| Q-011 | 넓은 expression/aggregate/bulk/locking과 background 평가 소유권 | immutable AST와 cache/terminal 의미는 구현됨 |
| Q-012 | custom/data operation, destructive/general writer, repair/crash reconciliation | loaded definition, revision-fenced lifecycle와 bounded target/reverse는 구현됨 |
| Q-013 | OneToOne/ManyToMany, arbitrary target/depth/cycle와 relation 일반화 | AutoField-target FK와 현행 query/cache/delete 단면은 구현됨 |
| Q-016 | API schema/OpenAPI, viewset 일반화와 wider routing | bounded serializer/JSON CRUD/auth profile은 구현됨 |
| Q-017 | 현재 generated model의 사용성·namespace와 broader relation facade | whole-project publication과 current facade는 구현됨; consumer로 다음 제약을 찾음 |
| Q-019 | SQLite quarantine의 통합 검증과 운영 recovery 안내 | single retained handle·새 I/O 거부 구현은 기준 source에 포함 |
| Q-020 | 비협력 writer, 넓은 deployment topology·key distribution | 같은 normalized policy의 cooperative runtime만 지원 |
| Q-021 | token issuance/refresh, OAuth/OIDC/JWT, production BFF | injected Bearer resource-server와 Session 경계는 구현됨 |
| Q-022 | 더 넓은 multi-user/credential lifecycle | provision/open과 explicit permission CAS·session 폐기 구현; 나머지는 후속 |

## Q-017 — 모델 사용성과 생성물

Schema 선언은 generated model과 분리하고 project가 cross-app relation을 연결한다. 이 결정 자체는 다시 열지 않는다.
새 consumer에서 모델 method·field·relation의 자연스러운 사용, 복사와 cache ownership, Form/Admin/API metadata 연결을 확인한다.
과거 renderer version·파일 개수·companion 이름의 보존을 사용성 목표보다 우선하지 않는다.
안전한 전체 후보 검증과 실패 시 last-good preservation은 유지한다.

## Q-019 — SQLite unknown-outcome retained connection resource policy

첫 미확인 rollback/discard 뒤 retained connection을 하나로 제한하고 같은 Backend의 새 I/O를
`backend_recovery_required`로 닫는 구현이 존재한다. 이미 admitted된 작업과 explicit Close의 경계는
[CONCURRENCY](CONCURRENCY.md#sqlite-raw-transaction과-quarantine), 선택 이유는
[ADR-0057](adr/0057-sqlite-retained-connection-terminal-quarantine.md)에 있다.
기준 source의 local checkpoint와 아직 끝나지 않은 platform proof를 [Evidence](status/TEST_EVIDENCE.md)에서 구분한다.
보관 handle을 무조건 pool에 반환하거나 자동 retry/reopen하는 정책으로 바꾸지 않는다.

## 이미 선택한 기초

Schema IR 단일 원본, codegen/generic/runtime metadata 역할, 선언/generated import graph 분리,
nullable read와 explicit write intent, typed/dynamic AST 수렴, immutable query와 cache ownership,
historical model과 runtime model의 분리는 [ARCHITECTURE](ARCHITECTURE.md)와 해당 ADR에서 확인한다.
새 질문은 현재 코드의 구체적인 동작·실패·consumer 요구에서 출발한다. 과거 work를 읽고 단계 전체를 재개하지 않는다.
