# 현재 상태

- 마지막 갱신: 2026-08-07
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 commit: `fe7bf44` (`Initial commit`)
- remote: `https://github.com/progresshans/godj.git`, remote refs 없음
- 현재 단계: 문서 기반 완료, M0 시작 전
- 활성 작업: 없음
- 다음 준비 작업: [GDJ-0001 Compatibility Lab](../../work/0001-compatibility-lab.md)

## 현재 checkout에서 확인된 사실

- 이 작업 전에는 초기 commit의 `README.md`만 존재했습니다.
- `go.mod`, Go 코드, 테스트, CI, release artifact는 아직 없습니다.
- GoDj의 구현 또는 Django 호환 기능은 아직 하나도 `Implemented`나 `Verified`로 주장할 수 없습니다.
- 로컬 Go는 1.26.5입니다.
- 로컬 Django 저장소의 현재 `main`은 6.2 alpha이며, 별도 tag `6.1`이 존재합니다. M0 oracle은 정확한 6.1 환경을 사용해야 합니다.

## 이번 작업에서 만든 기반

- 제품 목표/비목표와 장기 범위 분리
- 아키텍처 계층과 역할, Go generics 제약 기록
- Django compatibility profile과 contract lifecycle 정의
- contract-first vertical slice 로드맵 정의
- ADR lifecycle과 resumable work item 형식 정의
- 새 에이전트용 재개 프롬프트 정의
- 원 초안에서 확정하지 말아야 할 API/architecture 문제를 open question으로 분리

## 현재 차단 요인

구현 시작을 막는 외부 blocker는 없습니다. 다만 GDJ-0001 완료 전에 다음 P0 항목을 해결해야 합니다.

1. Django/Python/SQLite/timezone/locale exact reference lock
2. 최소 contract/runner/oracle protocol
3. codegen bootstrap 실패 사례와 후보 검증
4. Django-derived test provenance와 license 정책의 실행 가능한 형태

## 다음 정확한 작업

사용자가 구현 시작을 요청하면:

1. `work/0001-compatibility-lab.md` 상태를 `active`로 변경합니다.
2. 해당 work 문서의 `필수 읽기` 목록을 읽습니다. 여기에는 Architecture와 관련 ADR도 포함됩니다.
3. `go.mod`를 module `github.com/progresshans/godj`로 만들기 전에 remote를 다시 확인합니다.
4. M0 환경 lock과 첫 contract manifest 형식을 작은 계획 diff로 구체화합니다.
5. Django oracle 하나를 재현하고 comparator가 의도적 mismatch를 탐지하는 데까지 구현합니다.

## 작업 재개 체크포인트

- 미커밋 변경: EVID-20260807-001에 기록된 32개 신규 Markdown 파일, 모두 untracked
- 실행된 코드 테스트: 없음 — 코드가 없음
- 건드리면 안 되는 범위: `/Users/hanhyeonjin/Documents/django` checkout은 reference이며 사용자 승인 없이 branch 변경/수정 금지
- 공개 API: 아직 freeze된 항목 없음
- 가장 위험한 추측: 첨부 초안의 임시 codegen runner와 relation/dynamic lookup/cache API를 확정안으로 구현하는 것

문서 검증은 [EVID-20260807-001](TEST_EVIDENCE.md#evid-20260807-001--documentation-foundation-validation), 기능 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md)에 기록되어 있습니다.
