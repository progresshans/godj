# 의도적 호환 차이 원장

- 상태: Active ledger
- 마지막 갱신: 2026-08-07
- 현재 승인된 deviation: 없음

이 문서는 Django reference contract와 다른 GoDj 동작을 의도적으로 수용한 경우의 정본입니다. 단순 mismatch, 미구현, bug, 환경 drift를 deviation으로 바꾸어 테스트를 녹색으로 만들면 안 됩니다.

## 승인 절차

1. Differential mismatch를 `GoDj bug`, `scenario bug`, `Django bug`, `environment drift`, `deviation candidate`로 먼저 분류합니다.
2. Candidate에는 사용자에게 보이는 차이, 대안, migration 비용, backend 영향, 보안/성능 영향과 contract ID를 기록합니다.
3. public behavior, data, error, transaction 의미를 바꾸면 Proposed ADR을 연결합니다.
4. review와 필요한 사용자 결정을 거쳐 상태를 `Accepted`로 바꿉니다.
5. manifest의 contract 상태를 `deviation`으로 바꾸고 GoDj 고유 expected test를 추가합니다.
6. 대체되면 원래 기록을 지우지 않고 `Superseded`와 새 ID를 연결합니다.

## 상태

- `Proposed`: 차이를 검토 중이며 compatibility pass로 계산하지 않음
- `Accepted`: 의도적인 제품 결정으로 승인됨
- `Implemented`: 현재 checkout에 다른 동작이 구현됨
- `Verified`: deviation 전용 expected test가 기록된 환경에서 통과함
- `Superseded`: 새 deviation 또는 원래 Django 동작으로 대체됨

## 기록 형식

```markdown
## DEV-NNNN — 제목

- Status: Proposed
- Date:
- Contracts:
- Reference profile/backend:
- Related ADR/work/evidence:

### Django의 관찰 가능 동작
### GoDj에서 제안/채택한 동작
### 이유와 고려한 대안
### 사용자·데이터·migration 영향
### backend/concurrency/security 영향
### 구현과 검증 조건
### 복귀 또는 supersede 조건
```

## 원장

아직 기록된 deviation이 없습니다.
