---
id: GDJ-0068
status: completed
updated: 2026-09-10
baseline_commit: "6e826a1acc41720b1df99acb7e488c71f24c2841"
integration_owner: "existing Draft PR #1"
---

# 불변 값 전달과 검증 비용 정리

## 결과와 범위

전체 감사의 개선 항목과 추가 탐색에서 타당한 항목을 구현한다. 반복 복사·오류 누적·준비 비용과
같은 책임의 중복을 줄이며, 외부 mutable 입력/출력, callback, DB·process·oracle 경계를 유지한다.
제품 의미는 [아키텍처](../docs/ARCHITECTURE.md)·[동시성](../docs/CONCURRENCY.md),
검증 책임은 [TESTING](../docs/TESTING.md)을 따른다. 전후 측정과 실제 결과는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0068--불변-값-전달과-검증-비용-정리)에 기록한다.

| 변경 묶음 | 보존할 경계와 검증 |
|---|---|
| Validation 오류 누적 | field/cross/unknown 순서, parameter snapshot, append 원본 불변성 |
| Session Record·ID 전달 | 외부 값 map 복사, 변경 시 분리, strict ID 입력, atomic access·expiry·rotation |
| Auth Principal 전달 | 권한 snapshot·getter 분리, invalid permission, dummy hash·현재 DB policy 확인 |
| Form·Serializer 결과/준비 | callback 관측 불변성, 초기값 overlay, omitted/null/default와 오류 순서 |
| Template 반복과 출력 | nested/include scope·forloop 의미, 정확한 escape·output/depth/cancel 한도 |
| Migration ProjectState | zero/empty 의미, 정규화·상태 분리, mutable replay builder·revision fence |
| JSON decode·출력 예산 | empty/duplicate key·Unicode·정수·순서·자원 한도·실패 시 미게시 |
| App 생성 namespace | 실제 선행 AST, package/member 충돌, standalone/bundle·실패 보존 |
| Binding 출력과 필드 metadata | 결정적 출력, typed 모델·관계 canonical metadata·입력 snapshot |
| DB assignment·LIKE | SQLite ASCII column key·PostgreSQL validation/quoting·인자 순서 |
| Route·API Page 전달 | 요청별 소유권, mutable public getter, 페이지 shape·한도 |
| Test fixture·Django helper | 환경 policy·독립 fixture·oracle/actual 분리·source binding |
| ORM 쓰기 준비 | 크기별 측정, full field identity·callback snapshot·omitted 검증 |
| External compile 실행 | 각 정상/실패 fixture별 결과·진단, 실제 모드·별도 소비자 경계 |
| CI 작업 배치 | 모든 OS/arch/mode·개별 gate·capture producer·cold/32-bit·실패 전파 |
| 추가 후보 | 근거와 변경 범위를 먼저 확인하고 같은 통합 검증에 포함 |

## 구현과 검증

- [x] 변경 전 측정과 영향 범위 확정
- [x] 제품·생성기·관련 회귀·추가 후보 구현
- [x] 전후 측정, affected normal, 필요한 generated drift·소비자 compile
- [x] 통합 race·CGO-disabled·SQLite·실제 command와 reference/CI 도구 검사
- [x] 같은 제품 소스 Hosted full scope·실제 PostgreSQL capture 및 aggregate 확인
- [x] 현행 계약·CURRENT·증거와 Draft PR 마감

로컬 전체와 Hosted 전체를 중복하지 않는다. 로컬은 변경 묶음과 통합 위험을 검사하고,
최종 Hosted milestone이 전체 OS/arch/mode·PostgreSQL·cold build·capture를 소유한다.
조건부 성능 후보는 측정 후 설계를 정하며, 검증을 생략해 얻은 속도를 개선으로 기록하지 않는다.

## 완료 상태

제품·생성기·DB·환경 helper와 CI 배치 변경을 구현했다. 외부 ABI의 각 정상/오용 결과를 보존하면서
compile 준비를 공유하고, CI command 묶음 안에서도 선택된 각 제품의 완료 결과를 따로 요구한다.
추가 탐색에서 JSON 직접 bounded 출력·LIKE 공통 escape·단일 field 생성·callback별 복사·필수 실행
누락 검사도 포함했다. Affected normal, 실제 command·race·CGO-disabled 통합 검증과 최종 측정을 마쳤다.
누락됐던 별도 metadata fixture도 재생성해 drift와 세 mode 소비자 검사를 통과했다.
첫 Hosted에서 통합 전 job 이름을 기대하던 구성 검사 두 개를 찾고 보정했다. 각 제품 step의 실행 조건·모드·필수 항목을
개별 확인하며 해당 protocol package의 세 mode 검사를 통과했다.
보정 소스 `ffe384492bee0b98aec918d594f6c17531797280`의 Hosted full scope와 현재 capture 검증을 완료했다.
전후 측정·첫 실패와 최종 완료 근거는 TEST_EVIDENCE 한 곳에 기록한다. 기존 PR #1은 Draft로 유지한다.
