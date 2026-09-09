---
id: GDJ-0066
status: active
updated: 2026-09-10
baseline_commit: "d6443fa048ffdf4e22e0d817f4e8d0aaeb83e8da"
integration_owner: "Codex"
---

# 실행 비용과 중복 책임의 측정 기반 정리

## 결과와 범위

전체 감사에서 확인한 비용과 추가 후보를 구현·측정·검증한다. 코드 줄 수를 목표로 삼지 않고,
불필요한 할당·컴파일·DB 왕복·행 전송과 같은 의미를 여러 곳에서 관리하는 비용을 줄인다.
현재 작업 브랜치와 Draft PR #1을 사용하며 전역 문서와 통합 검증은 한 소유자가 관리한다.

현행 기준은 [아키텍처](../docs/ARCHITECTURE.md), [동시성](../docs/CONCURRENCY.md),
[Backend](../docs/BACKEND_MATRIX.md), [검증 실행](../docs/TESTING.md)을 따른다.

## 구현과 검증

| 항목 | 완료 조건 | 보존할 위험 검증 |
|---|---|---|
| 쿼리 불변 소유권 | 파생에서 바뀌지 않는 metadata 복사를 제거하고 할당 비교 | 입력/반환 slice 격리, 파생 cache 분리, concurrent access |
| 외부 빌드 cache | 일반 fixture가 공용 빌드 정책을 사용하고 cold milestone은 별도 유지 | 실제 compiler 실행, private runtime/DB, offline dependency, timeout/reap |
| 쿼리 구성 API | 오류 반환을 하지 않는 호환용 AST 구성 경로 제거 | 잘못된 입력·budget·relation 위치·source membership의 명시적 오류 |
| PostgreSQL 관측 | 같은 catalog 관측을 공통화하고 독립 expected 유지 | 물리 컬럼/제약/index/sequence와 손상 대조, source binding |
| 세션 atomic access | Manager의 같은 행 Load/Touch 중복을 한 저장소 연산으로 통합 | 저장 전 record 검증, 시간 단조성, expiry/rotation/cancel, multi-runtime/restart |
| 감사 로그 prune | 꽉 찬 보존 한도에서 측정하고 DB에서 결과를 좁혀 행 전송 절감 | corruption/cardinality/rows close, 삭제 전 실패 보존, transaction rollback |
| 관계 Count | 현재 지원 관계 filter의 cold Count를 DB 집계로 실행 | JOIN multiplicity, NULL, distinct/slice/order, cache·cancel·close |
| migration 순서 | 조상 index의 반복 전체 탐색을 제거 | deterministic order, DAG/chronology, bounded resource와 cancellation |
| 추가 탐색 | 같은 경로의 남은 반복 할당·중복 helper를 검토하고 타당한 항목 처리 | 별도 의미/독립 oracle/실패 경계를 합치지 않음 |

- [x] 변경 전 비교 workload와 수치 확보
- [x] 위 구현 항목과 추가 후보 처리
- [x] 묶음별 compile, affected normal·race·CGO-disabled·generated 검증
- [ ] 최종 소스의 Hosted full platform·PostgreSQL·reference·cold build 검증
- [ ] 현행 명세·작업 상태·단일 [실행 증거](../docs/status/TEST_EVIDENCE.md) 정리

로컬 전체와 Hosted 전체를 중복하지 않는다. 로컬은 변경 경로와 비교 측정을 맡고, 최종 통합 milestone은
현재 소스의 Hosted full scope가 소유한다. 미실행·skip·다른 소스의 결과를 현재 PASS로 기록하지 않는다.

## 채택한 변경과 추가 탐색

- Query의 private 불변 storage를 공유하고 외부 slice 소유권은 유지했다. `WithConditions`는 `(Plan, error)`를 반환하며
  private unchecked AST 경로를 삭제했다. 실패 입력 검사는 생성자에서, 물리 metadata/SQL 오류는 각 compiler에서 계속 검증한다.
- 다섯 외부 fixture가 기존 `gobuild.Environment`를 사용한다. 실제 compiler·offline 실행·private DB/runtime과 명시적 cold opt-out은 유지한다.
- PostgreSQL migrate/target의 물리 catalog 수집을 `conformance/internal/dbstate`로 통합했다. 제품별 expected는 별도로 유지한다.
  raw SQL 정의까지 비교하는 showmigrations는 관측 의미가 달라 이번 공통 수집기로 축소하지 않았다.
- `Store.Access`와 불변 `AccessPolicy`로 Manager의 중복 Load/Touch를 한 원자적 조회·검증·갱신으로 바꿨다.
  만료·rotation·clock rollback·cancel·오류 분류를 유지하며, Clock/Random panic 후 source lock이 남는 추가 결함도 수정했다.
- 감사 prune은 기존 newest capacity+1 범위에서 COUNT/MIN을 구하고 결과 검증·rows 종료 후 최대 한 행을 삭제한다.
  공통 scalar MIN 및 typed `Min`을 추가하고 MIN/MAX의 field capability를 `OrderedField`로 정리했다.
- 관계 filter Count는 기존 JOIN row source를 감싸서 DB 집계한다. 일반 관계 projection/aggregate나 eager Count를 추가하지 않는다.
- Migration ancestor index는 반복 전체 검색 대신 deterministic queue와 연속 bitset storage를 사용한다. 독립 DFS와 대조한다.
- 추가 eager 후보는 target metadata/source를 query당 한 번 준비하도록 수정했다. 각 행의 predicate와 ready cache는 독립이다.
- Python AST 중복 탐색에서 확인한 관측 envelope와 SQL capture를 공통화했다. 결과 누락과 명시적 null, 계약별 statement 분류,
  recorder 접근 순서·primary-key 관측 차이는 보존했다. 고정 reference의 실제 생성 결과를 바이트 단위로 대조했다.

미배포 내부 API의 새 형태로 소비자를 함께 수정했으며 과거 API 모양을 유지하는 alias나 fallback은 두지 않았다.
공통 PostgreSQL 관측기는 migrate/target test 소스가 소비하며, 별도 live attestation observer의 구현을 가져오지 않는다.
현재 source-binding inventory도 확인했다. 기존 관측 경로 밖으로 attestation 동작을 옮긴 helper는 없다.

## 현재 상태와 다음 행동

구현, 비교 측정, 관련 로컬 회귀와 외부 다섯 명령의 실제 실행을 마쳤다. 다음은 최종 제품 커밋을 push하고
Hosted full scope로 PostgreSQL 17.10·모든 OS/arch/mode·cold build·reference·capture 생성/소비를 확인하는 것이다.
로컬 PostgreSQL 17.5의 raw catalog negative control은 통과했으며 제품의 17.10 profile 검증을 대신하지 않는다.
