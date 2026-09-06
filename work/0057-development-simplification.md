---
id: GDJ-0057
status: active
updated: 2026-09-06
baseline_commit: "da1bfc524c4f205075fc7fac7f00b437473a5e1f"
integration_owner: "primary agent"
---

# 개발 구조 정리

사용자는 미배포 프로젝트의 내부 API·형식·개발 원칙을 변경하고 불필요한 코드와 문서를 제거하도록 승인했다.
기존 작업별 경로 제한과 과거 inventory/문서 byte 보존은 이번 작업의 제약이 아니다.
동작 무결성, 인증·비밀 보호, 실제 실행 여부에 대한 정확한 보고는 유지한다.

## 결과와 확인 목록

- [x] 문서: 중복 프롬프트·완료 work·대체된 ADR 정리, 고유 결정과 출처 보존, 현행 설계·사용법 재작성
- [x] 작업 안내: 짧은 CURRENT, 한 곳의 증거 기록, 작업 완료와 플랫폼 검증 상태 분리
- [x] 진단: Go build/test JSON과 내부 빌드 실패의 유용한 원인 보존, 출력 상한·비밀 제거 유지
- [x] CI: 빠른/관련/전체 실행 범위, 문서-only 전체 재실행 제거, dependency/build cache, 필수 실행 누락 검사
- [x] 증거: 전체 테스트 이름·길이·digest 잠금 제거, 현재 실행 actual의 생성·소비를 CI artifact로 연결
- [x] 테스트: 실제 scenario의 실행 소유자 정리, 중복 cold helper build 제거, 격리/프로세스/DB 회귀 보존
- [x] 생성기: 프로젝트·필드 해석 공유, 모델과 무관한 생성 runtime 로직을 일반 Go로 이동
- [x] CLI: 공통 실행 절차·빌드 재사용 경계 정리, 명령별 durable outcome·secret lifecycle 보존
- [x] 제품 연결: 모델 기반 Form/Admin/API 설정과 변환 중복 축소, 필드 선택·읽기 전용·관계 입력 검토 및 구현
- [x] 앱 성장: operator 권한 변경의 명시적 경로와 audit 조합 폭증 제거
- [x] 소비자: 구조가 다른 모델과 공개 API의 실제 조합으로 추가 수정 필요 여부 확인
- [x] 전체 감사: 나머지 package 경계·오류·지원 제한·옛 구현 잔재 확인, 필요 수정 또는 구체적 비대상 근거 기록
- [ ] 통합 검증: 관련 회귀, generated drift, 전체 compile/vet, 필요한 race/CGO0/backend/platform 실행
- [ ] 최종 정리: 기존 Draft PR 사용, 성공/실패/미실행 범위 명시, 작업 브랜치와 임시 산출물 정리

## 작업 소유권

- 통합 담당: AGENTS, CURRENT, 이 work, Makefile/CI, 진단·attestation 소비, CLI
- 문서 담당: 나머지 Markdown과 역사 문서 링크, 문서 문구를 검사하던 소수 테스트
- 생성기 담당: codegen·공통 ORM runtime, 관련 생성물·회귀
- 제품 담당: forms/admin·Article/새 소비자·systemstate의 앱 연결과 권한 변화, 관련 회귀

공개 API 변경은 담당자가 먼저 경계를 공유하고 다른 lane의 파일을 임의 수정하지 않는다.
각 변경은 affected 검증부터 실행한다. 무거운 외부 build/DB suite는 통합 담당이 조정한다.
전체 검증은 통합 소스에서 수행하며 문서 정리 때문에 제품 전체 검증을 반복하지 않는다.

## 기준 상태와 증거

- Baseline은 clean da1bfc5이다. Q-019 quarantine 구현은 포함하지만 기존 GDJ-0056의 최종 Hosted 완료는 주장하지 않는다.
- 이전 작업의 상세 증거는 baseline Git 이력에 보존한다. 이번 작업의 명령·결과는 TEST_EVIDENCE의 새 단일 기록에 모은다.
- 구현 checkpoint와 실제 명령은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록했다. 최종 통합/Hosted는 아직 실행 전이다.

## 현재 결정과 검증 소유권

- 현재 source actual은 같은 CI attempt의 PostgreSQL producer artifact로 전달한다. 고정 JSON은 codec fixture로만 유지한다.
- `make quick`은 core feedback이다. platform 그룹(cmd/project/projectcheck/실제 runner)은 4 OS/arch × normal/race/CGO0 matrix가 소유한다.
- Portable core/integration/conformance/products는 별도 shard다. 제품 CLI/protocol·DB의 무결성 검증 범위는 유지한다.
- 전체 이름·개수·serialized length/hash 잠금은 제거했다. 필수 sentinel과 package completion/skip/truncated log 및 independent oracle 검사는 유지한다.
- 생성기 lowering/프로젝트 graph를 공유하고 generic evaluation/cache cell을 ORM에 모았다. eager Offset/Distinct/Fresh를 추가했다.
- Eager First/Count와 다중 관계 탐색은 기존 joined row/cardinality 의미를 별도 설계·검증하기 전까지 지원 범위로 확대하지 않는다.
- GDJ-0056의 Q-019 구현과 미완료 플랫폼 검증은 이 통합에 포함한다. 이전 work의 실행 일지는 기준 Git 이력에 보존했다.

## 제거한 실험의 위험 소유자

| 제거 대상의 위험 | 현재 제품 검증 |
|---|---|
| codegenbootstrap의 broken target와 선언 package 분리 | `cmd/godj.TestActualGodjGenerateProcess`, relationdeleteproduct의 실제 declaration bootstrap |
| candidate compile/validation 실패와 last-good 보존, no user init/test 실행 | `internal/projectgenerate.TestGoCandidateVerifierCompilesVirtualNewPackagesWithoutExecutingUserCode`, candidate rejection/publication/recovery 회귀 |
| 별도 relationbinding binder의 missing/duplicate/역관계 충돌, partial publish | `orm.TestBindProjectErrorsAreTypedDeterministicAndPublishNothing`, IR normalization/current project binding 회귀 |
| 두 앱 상호/자기 참조·app import cycle 방지 | `codegen.TestGeneratedMutualAndSelfRelationProjectHasZeroAppImportEdges`의 실제 생성 external module |
| typed/dynamic relation 의미·join/nullability/prefetch | `orm` dynamic_relation/reverse_prefetch/select_related와 actual relation product 관찰 |
| SET_NULL 중간 실패의 rollback | relationdeleteproduct `TestREL008AdversarialDeleteFailureRollsBackAndPreservesCaller`, SQLite relation transaction 회귀 |

모든 제거 대상 원문은 기준 commit의 Git 트리에 보존한다. 폐기한 대안 IR/별도 모형의 byte·field-count 동일성은 현행 제품 위험이 아니다.

Lifecyclefence 실험도 실제 제품 검증으로 대체했다. Stale/BUSY/ABA/corruption/cutover/two-process/cleanup은 기존 SQLite 회귀가 소유하고,
`db/sqlite/migration_lifecycle_regression_test.go`에 두 복합 사례를 이관했다. 정상·race에서 확인한 뒤 prototype을 삭제했다.
과거 DirectExecutor가 stale을 받아들이게 하던 gap과 fake backend source 호환성은 현재 제품 계약이 아니므로 유지하지 않았다.

## 전체 경계 감사

- DB/Migrations/Query/Schema: Q-019 admission/retention/Close, commit outcome, durable prefix와 additive writer 경계를 확인했다.
  Query typed-nil 오류 검사에서 발생하던 panic을 nil guard와 errors.Is/Join 회귀로 수정했다.
  IR/AST/backend 검증은 서로 다른 신뢰 경계이므로 무리하게 합치지 않는다.
- Auth/Session/API/Web/Template/Settings/Apps/Validation: 입력·인증·CSRF·escaping·출력 경계와 미구현 잔재를 확인했다.
  추가 보안 결함을 발견하지 않았다. 동기 borrowed request/buffered response와 closed template subset은 명시적 제한이다.
- 새 compiler/helper, Form/Admin/serializer, cached policy refresh와 CI/actual 소비는 독립 lane 검토를 받았다.
  오류·출력·source identity·cache·scope/skip/truncation 경계를 점검하고 실제 발견만 수정했다.

Projectcheck의 별도 CLI 모형도 제거했다. 실제 MIG-065..074 adapter/oracle 비교는 유지한다.
선택·discovery·source precedence는 실제 `selection_test.go`와 `linked` 회귀로, temp parent 교체와 cancel/interrupt/launch failure는
실제 `workspace_modcache_test.go`/`process_test.go`로 이관했다. Parent root 교체와 malformed/oversized descriptor가 겹칠 때
관찰한 identity race가 우선하지 않던 결함을 재현하고 `selection_unix.go`의 parse 전 identity 재검증으로 수정했다.
관련 normal/race를 통과한 뒤 prototype와 전용 CI matrix를 삭제했다.

## 통합 대기

제품·테스트·도구 source 편집을 마쳤다. 최종 제출 commit에서 로컬 빠른/compile/vet/generated/adapter 검증 후 기존 Draft PR의 `ci:full`로
PostgreSQL과 모든 플랫폼을 한 번 실행한다. CI artifact의 실제 생성·소비 성공은 Hosted 결과가 나와야 검증 완료로 기록한다.
일반 CI·로컬 quick의 module/build cache는 재사용하며 실제 CLI cold 경로는 전체 milestone의 Linux amd64 normal 한 subtest가 소유한다.
