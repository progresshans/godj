---
id: GDJ-0061
status: complete
updated: 2026-09-07
baseline_commit: "ddb8c5135533f9fb7fc280d0446f2688b7b6b649"
---

# 검증 fixture와 CI 실행 소유권 정리

테스트·검증 코드의 중복과 반복 실행을 함께 줄인다. 테스트 이름·파일 수를 보존하는 대신,
실제 실패를 찾아내는 검증과 필요한 환경을 유지한다. 사용자가 불필요한 테스트의 삭제·통합을 승인했다.

## 범위

- 같은 Author/Post 선언을 복제한 관계 product의 생성 코드를 공통 프로젝트로 모은다.
- 중복 generated drift·앱 의존성 검사와 반복되는 oracle 대조·결정성 준비를 합친다.
- 관계별 실제 DB·cache·취소·rollback·typed/dynamic·잘못된 생성 조합 회귀는 계속 실행한다.
- CI의 동일 환경·package·mode 반복을 제거하고 matrix 선언의 반복도 정리한다.
- 제품 runtime·공개 API·고정 Django/DRF oracle은 변경하지 않는다. 장기 fixture 소유권 설명은 현행 문서에 반영한다.

## 검증 소유권

| 정리 대상 | 남는 위험 검증 |
|---|---|
| 관계별 동일 생성 앱·프로젝트 복제 | 공통 whole-project 생성·drift·bootstrap, codegen의 기능별 외부 consumer compile |
| 각 복제본의 동일 의존성 검사 | 공통 앱 간 import/dependency 경계, 각 observer의 oracle-blind 검사 |
| 이전 미배포 facade v2 복제본과 고정 파일 수·private 필드 수 검사 | 현재 full union의 모든 generated file을 다른 snapshot과 섞은 compile 실패, feature prerequisite 실패와 bundle 소유권 검사 |
| 별도 oracle 일치·결정성의 반복 준비 | 독립 actual 두 번 생성, 고정 expected 대조와 canonical byte 비교 |
| SQLite/migration의 겹친 matrix 실행 | 관계 matrix의 같은 OS/CPU/normal·race·CGO-disabled 및 vet, scope 누락 거부 |

## 진행

- [x] 공통 fixture 이관과 불필요한 복제·중복 테스트 정리
- [x] CI의 중복 실행과 반복 선언 정리, scope와 필수 sentinel 연결
- [x] 변경 묶음의 compile·관련 normal/race/CGO-disabled·generated drift와 부정 회귀
- [x] 고정 소스의 Hosted 통합 확인, 실제 코드 양·실행 범위·제한 기록

편집 중에는 필요한 compile만 확인한다. 관련 검증은 변경 묶음이 완성된 뒤 실행하고,
전체 플랫폼 검증은 마지막 Hosted 통합 시점이 소유한다. 실행 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 한 번 기록한다.

## 다음 행동

이번 정리는 완료했다. 후속 기능은 별도 의미와 범위를 정한 뒤 시작하며 현재 blocker는 없다.
