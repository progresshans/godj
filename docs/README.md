# GoDj 문서 안내

이 디렉터리는 프로젝트의 장기 기억입니다. 하나의 거대한 설계서나 긴 프롬프트에 모든 내용을 복제하지 않고, 질문마다 하나의 정본을 둡니다.

## 어디에서 무엇을 찾는가

| 질문 | 정본 |
|---|---|
| 왜 이 제품을 만드는가, 무엇을 포함하는가 | [CHARTER.md](CHARTER.md) |
| 장기 기능 범위를 빠짐없이 어디까지 보는가 | [CAPABILITY_CATALOG.md](CAPABILITY_CATALOG.md) |
| 사용자가 어떤 개발 경험을 얻게 되는가 | [DEVELOPER_EXPERIENCE.md](DEVELOPER_EXPERIENCE.md) |
| 계층과 데이터 흐름은 어떻게 되는가 | [ARCHITECTURE.md](ARCHITECTURE.md) |
| 동시성·취소·resource lifecycle 원칙은 무엇인가 | [CONCURRENCY.md](CONCURRENCY.md) |
| Django와 무엇을 같게 또는 다르게 할 것인가 | [COMPATIBILITY.md](COMPATIBILITY.md) |
| upstream 코드와 테스트의 라이선스·출처를 어떻게 관리하는가 | [LICENSING.md](LICENSING.md) |
| 승인된 의도적 차이는 무엇인가 | [DEVIATIONS.md](DEVIATIONS.md) |
| 어떤 테스트로 주장하는가 | [TESTING.md](TESTING.md) |
| DB별 목표와 현재 상태는 무엇인가 | [BACKEND_MATRIX.md](BACKEND_MATRIX.md) |
| 어떤 순서와 gate로 개발하는가 | [ROADMAP.md](ROADMAP.md) |
| 아직 결정하지 않은 것은 무엇인가 | [OPEN_QUESTIONS.md](OPEN_QUESTIONS.md) |
| 외부 기준과 버전 근거는 무엇인가 | [SOURCES.md](SOURCES.md) |
| 지금 checkout의 실제 상태는 무엇인가 | [status/CURRENT.md](status/CURRENT.md) |
| 기능별 설계·구현·검증 상태는 무엇인가 | [status/IMPLEMENTATION_MATRIX.md](status/IMPLEMENTATION_MATRIX.md) |
| 실제로 어떤 검증을 실행했는가 | [status/TEST_EVIDENCE.md](status/TEST_EVIDENCE.md) |
| 왜 장기 결정을 내렸는가 | [adr/README.md](adr/README.md)와 개별 ADR |
| 작업 하나를 어떻게 재개하는가 | 루트의 [`work/`](../work/README.md) |

## 문서 계층

문서 종류는 서로 다른 책임을 가집니다.

1. `AGENTS.md`는 반복해서 지킬 작업 규칙입니다.
2. Accepted ADR은 변경 비용이 큰 결정과 그 이유를 기록합니다.
3. Charter, Capability Catalog, Developer Experience, Architecture, Concurrency, Compatibility, Testing은 통합된 현재 설계입니다. Developer Experience의 code sketch는 명시적으로 비규범적입니다.
4. `work/*.md`는 한 작업의 범위와 실행 상태이며 상위 설계를 임의로 덮어쓰지 못합니다.
5. `status/*`는 현재 checkout에서 관찰한 사실입니다. 설계를 새로 결정하는 문서가 아닙니다.
6. `prompts/*`는 위 문서를 읽게 하는 편의 도구일 뿐 정본이 아닙니다.

문서가 충돌하면 임의로 하나를 선택하지 않습니다. 관련 작업을 멈추고 Accepted ADR과 통합 명세를 같은 변경에서 일치시키거나, 새 ADR로 변경을 제안합니다.

## 상태 용어

| 상태 | 의미 |
|---|---|
| Proposed | 논의 또는 프로토타입이 필요한 안 |
| Accepted | 장기 방향으로 채택됐지만 코드 존재를 뜻하지 않음 |
| Implemented | 현재 checkout에 코드가 존재함 |
| Verified | 명시된 환경과 테스트에서 증거가 기록됨 |
| Superseded | 더 새로운 결정으로 대체됨 |

호환 계약의 실행 상태는 별도로 `draft → oracle_locked → red → passing`을 사용하며, 의도적 차이는 `deviation`으로 분류합니다.

## 갱신 원칙

- 설계 변경: 관련 ADR과 통합 명세를 함께 갱신합니다.
- 구현 변경: 활성 work 문서와 implementation matrix를 갱신합니다.
- 테스트 실행: test evidence에 명령과 결과를 추가합니다.
- 작업 전환: CURRENT의 활성 작업과 다음 작업을 갱신합니다.
- 완료한 work 문서는 실제 결과와 남은 문제를 채운 뒤 상태를 `completed`로 바꿉니다.
- 출처에서 파생한 테스트나 문서는 원문 버전, 경로, 라이선스를 남깁니다.

새 하위 시스템의 구현이 시작될 때에만 `docs/specs/` 또는 디렉터리별 `AGENTS.md`를 추가합니다. 빈 미래 문서를 미리 대량 생성하지 않습니다.
