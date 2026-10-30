---
id: GDJ-0065
status: complete
updated: 2026-09-08
baseline_commit: "341659d290ed0d344e1db51de086c5d4baac5f4e"
integration_owner: "root"
---

# 코드와 검증 체계의 중복 정리

## 결과와 범위

전체 코드 감사에서 확인한 오류, 불필요한 복사·할당, 중복된 규칙과 검증을 정리한다.
사용자 승인에 따라 미배포 내부 API·생성 ABI의 하위호환 계층은 제거할 수 있다.
DB 무결성, 권한, 취소, transaction·복구, 독립 oracle과 실행 누락 검증은 유지한다.

Schema/ORM/codegen, DB/migrations/systemstate, conformance/CI, Web/Admin/API와 CLI를
파일 소유자로 나누고 전역 상태·최종 증거는 통합 담당이 기록한다.
현행 의미는 [아키텍처](../docs/ARCHITECTURE.md), 실행 원칙은 [검증](../docs/TESTING.md)을 따른다.

## 구현과 검증

- [x] Template 오류·불변 값, Audit·ORM·serializer의 불필요한 작업 정리
- [x] 생성 namespace·IR 검증·hash·budget·journal·CLI 공통 규칙 단일화
- [x] Migration/Article의 과거 내부 호환 계층 제거와 소비자 갱신
- [x] 미사용 검증·과거 inventory·fixture·reference·실행 catalog와 CI 중복 정리
- [x] 관련 normal/generated/consumer 검사와 최종 통합 검증
- [x] 현행 계약·검증 소유권·실행 증거와 남은 한계 기록

편집 중에는 compile만 확인하고 변경 묶음 완성 후 affected 검증을 실행한다.
전체 backend/race/platform/reference 검증은 최종 통합 소스 한 곳에서 실행하고 로컬 전체와 중복하지 않는다.
실제 source·명령·결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 현재 상태와 다음 행동

구현·관련 회귀·독립 리뷰와 최종 Hosted full scope를 완료했다. 초기 PostgreSQL 빈 이력 검사 실패를 수정한 제품 소스와
모든 실행 범위·관측 자료는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0065--코드와-검증-체계의-중복-정리)에 기록했다.
추가 제품 기능은 이 작업에서 새로 선택하지 않는다.

## 감사 항목의 처리

2026-09-08 기준 `341659d` 감사의 번호를 따른다. 실행 결과는 TEST_EVIDENCE에만 기록한다.

| 항목 | 구현과 남긴 경계 |
|---|---|
| 1, 13 | `forloop.last`를 원래 목록 길이로 계산한다. Private immutable child 공유와 render-local loop scope로 재귀·전체 map 복사를 줄이며 외부 container 격리와 중첩 scope를 보존한다. |
| 2, 3 | 도달하지 않는 Admin observer를 제거하고 과거 부분 inventory를 현행 전체 검사로 통합한다. 계약별 상태와 독립 artifact hash, 모든 cross-binding 거부를 남긴다. |
| 4 | Audit prune은 보관 ID 배열 없이 초과 한 건만 기억한다. Bounded 결과의 모든 ID·iteration·Close 검증 후 삭제하며 과잉 row를 거부한다. |
| 5 | 실제 생성 AST에서 package/import/receiver namespace를 수집한다. Standalone prerequisite와 promoted source 충돌·whole-candidate compile은 계속 검사한다. |
| 6 | Forward/reverse dynamic input parser와 scalar 변환을 공유한다. 방향별 resolver·nullable 의미·오류 우선순위를 보존한다. |
| 7 | `irresource`에 IR 구조 순회를 모으고 `MigrationIntent.Clone`으로 소유권을 명시한다. Boundary별 budget·오류·derived limit은 분리한다. |
| 8 | DB history의 canonical hash/order와 relation policy fingerprint 각각에 단일 소유자를 둔다. 독립 golden과 서로 다른 hash domain을 유지한다. |
| 9 | `identifiers`의 SQL·exported Go·portable path/import 어휘를 공유해 synthetic Schema 정규화를 제거한다. 모델 전체 정합성과 각 경계의 길이·예약명 정책은 유지한다. |
| 10 | CLI interrupt/cancel barrier·공통 failure code·linked 응답 게시를 공유한다. Read/mutate/credential/server의 terminal 정책은 명령이 소유한다. |
| 11 | Journal uniform scanner를 `wirejson`으로 통합한다. Manifest의 경로별 상한, typed decoding과 canonical byte/state 검증은 별도로 둔다. |
| 12, 14 | Eager field count를 scan 밖에서 준비하고 At는 표현 가능한 OFFSET/LIMIT 1을 사용한다. 큰 index fallback·기존 limit·cache·rows 오류 의미를 유지한다. |
| 15 | Serializer/Admin metadata를 encoder/projector 생성 때 준비한다. Reader field 복사와 객체별 타입·nullability·길이·identity 검증은 유지한다. |
| 16 | Loaded graph 기반 reconstructor와 Detect의 base state를 공유한다. Catalog 오류 분류와 candidate/durable prefix의 새 strict load/replay를 유지한다. |
| 17 | PostgreSQL 현재 operation 전체와 transition을 SQL 전에 seal 검사한다. 전체 intent는 preflight·완료 시 검사하고 physical 검증 전 recorder 성공을 금지한다. |
| 18 | 독립 SQL snapshot·bounded process/buffer·cleanup을 test support에 모은다. 제품별 expected state와 mutation, oracle 독립성은 공유하지 않는다. |
| 19 | `conformance/suites.json`과 실행 plan으로 manifest/profile/reference/product 선택을 단일화한다. Checker는 한 번 build하고 각 suite는 별도 실행한다. 테스트는 선택·누락·중복·binding을 검증한다. |
| 20 | Linux 기준 이미지를 Ubuntu 24.04로 맞추고 같은 OS/arch/mode에 실제 owner가 있을 때만 중복 package/subset 실행을 생략한다. Sentinel·no-skip·실패/취소/누락 aggregate를 owner로 옮긴다. |
| 21, 22 | Operation embedding 호환과 Article Admin repository wrapper를 제거했다. Built-in만 실행하며 Admin not-found 변환은 등록 callback에서 원인을 보존해 수행한다. |

작은 후보도 함께 정리한다: bundle finalization·facade constructor·consumer fixture 조립, Article Update/Patch transaction,
Django 순수 schema normalization, attestation traversal/framing, package 내부 rows 수명이다. DB별 DDL과 template/serializer/form의
다른 입력·출력 의미를 하나의 범용 추상화로 합치지는 않는다.
