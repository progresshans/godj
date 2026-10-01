---
id: GDJ-0109
status: active
updated: 2026-10-02
baseline_commit: "f6e95bb81f80605491bbf04feab49017f79f49e4"
integration_owner: "root"
---

# Native bulk 생성과 여러 티켓 생성

## 결과와 범위

모델의 여러 생성 입력을 native 다중 행 INSERT와 명시적인 batch/transaction 경계에 연결한다.
Schema IR의 타입·default·nullable·고유성·관계 의미와 생성 ID·입력 순서·오류 및 반환 값의 소유권을 유지한다.
기존의 단건 저장 API와 각각의 save/clean/감사 정책을 bulk가 암묵적으로 호출하는 것으로 해석하지 않는다.
Helpdesk에서 같은 Category에 여러 티켓을 만드는 흐름으로 Form/Admin/API와 독립 client까지 소비한다.

행 잠금과 update-or-create의 기반 source `f6e95bb8`는 Hosted full 36900514942의 65개 job·필수 owner·
새 capture 결합과 소비·최종 집계까지 완료했다. Markdown 완료 기록 `b96f9833`을 합쳤다.
그 실행 결과를 이 작업의 새 source 검증으로 사용하지 않는다. 구현 사본을 분리했고 root가 통합 문서와 검증을 소유한다.
Bulk update·query update expression과 나머지 query 범위는 다음 기반으로 이어가며 전체 기능 카탈로그에서 제거하지 않는다.
이 작업은 bulk 전체나 장기 목표의 완료 선언이 아니다.

## 구현과 검증

- [x] 고정 Django의 다중 생성·배치·반환 key·default/관계·충돌 정책·부모/실패 의미를 정식 독립 fixture와 Go 비교로 연결
- [x] 불변 다중 행 INSERT AST와 타입/입력/출력/자원 한도의 DB 독립 계약
- [x] SQLite/PostgreSQL native multi-row·returning·충돌 처리·parameter 예산과 native session/cursor 수명
- [x] generic ORM·생성 입력 및 root facade·caller cache/metadata 소유권과 전체 실패의 원자성
- [x] 양 DB의 독립 생성 소비자·잘못된 model/field compile 거부·실제 오류/취소/경쟁
- [x] 현재 권한·Category 범위·원자 audit를 유지하는 Helpdesk 여러 티켓 생성과 실제 Form/Admin/API/client
- [ ] 완성된 변경 묶음의 영향 검증·정식 기준 대조·생성 drift·필요한 통합 범위 기록

## 현재 상태와 다음 행동

Django 6.1/Python 3.14.3과 SQLite/PostgreSQL에서 authored bulk 사례 32개를 각각 두 새 프로세스로 관찰했다.
입력 순서와 반환 key, batch별 statement/transaction, 실패 후 전체 행, duplicate key·ignore/update conflict,
selected-field bulk update와 부모 rollback을 포함한다. SQLite의 ignore가 CHECK/NOT NULL까지 생략하는 것과
PostgreSQL의 같은 statement 내 중복 upsert key 거부를 구분한다. 이는 native 기준 조사이며 Go 지원이나 PASS가 아니다.

여러 행의 입력·결과를 표현하는 AST와 native backend 경계, generic ORM의 전체 입력·metadata·batch/transaction과
typed 생성 root facade를 구현하고 각 변경 묶음의 normal/race/CGO=0 checkpoint를 완료했다.
실제 두 연결의 충돌 경쟁·모든 native session의 부모 commit/rollback·뒤쪽 배치 실패·scalar 왕복 저장을 확인했다.
정식 bulk-create 23개 사례를 각 DB의 두 새 native 프로세스로 관찰하고 원 출력과 Go 소비자의 직접 대조를
세 mode에서 완료했다. Helpdesk는 현재 Category·라벨·고유성을 검사한 여러 후보를 generated BulkCreate로 저장하며,
모든 티켓·라벨 연결·저장된 JSON/digest·완성된 출력·행마다의 audit를 같은 transaction에 포함한다.
Admin의 typed 생성 formset과 배열 API·독립 생성 client를 연결했고 기존 티켓 편집기의 새 행도 같은 writer를 사용한다.
완성된 업무 묶음과 공통 parser/serializer/Admin·ORM을 실제 양 DB의 normal/race/CGO=0에서 확인했다.
실제 브라우저에서 동적 행/오류 재표시와 기존 행 변경·새 행 생성의 저장을 확인했다.

현재 public API·지원/오류 정책을 ADR와 소비자 문서에 반영한다. PostgreSQL CI의 명시적 root 선택과 relation 필수
목록에 새 bulk 경로를 연결했다. 첫 full source `d2d85182`는 macOS ARM/race package timeout으로 실패했다.
모든 테스트를 유지하는 분할을 적용한 source `18199083`으로
[Hosted full 36932376723](https://github.com/progresshans/godj/actions/runs/36932376723)을 요청했다.
전체 플랫폼·cold/process·기능 간 조합과 새 capture source 결합은 이 실행이 소유하며 아직 완료가 아니다.
로컬 전체 검증을 중복하지 않으며 GDJ-0108 결과를 전이하지 않는다.
장기 의미는 [ADR-0088](../docs/adr/0088-bulk-creation-and-native-batch-ownership.md), 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에만 기록한다.
