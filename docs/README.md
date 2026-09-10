# 문서 읽기

작업 재개는 [CURRENT](status/CURRENT.md)와 거기 연결된 활성 work에서 시작한다. 변경할 코드와 관련 문서만 읽는다.

| 필요한 정보 | 위치 |
|---|---|
| 제품 목적 | [CHARTER](CHARTER.md) |
| 실제 사용 시작 | [저장소 README](../README.md), [개발 흐름](DEVELOPER_EXPERIENCE.md) |
| 계층·소유권·공개 경계 | [ARCHITECTURE](ARCHITECTURE.md), [CONCURRENCY](CONCURRENCY.md) |
| 현재 기능과 제한 | [IMPLEMENTATION_MATRIX](status/IMPLEMENTATION_MATRIX.md), [BACKEND_MATRIX](BACKEND_MATRIX.md) |
| Django 비교 기준과 의도적 차이 | [COMPATIBILITY](COMPATIBILITY.md), [DEVIATIONS](DEVIATIONS.md) |
| 테스트 선택·실행 | [TESTING](TESTING.md), [conformance 안내](../conformance/README.md) |
| 실제 실행 증거 | [TEST_EVIDENCE](status/TEST_EVIDENCE.md) |
| 다음 방향·열린 결정 | [ROADMAP](ROADMAP.md), [OPEN_QUESTIONS](OPEN_QUESTIONS.md) |
| 장기 범위 | [CAPABILITY_CATALOG](CAPABILITY_CATALOG.md) |
| 왜 이 결정을 선택했는가 | [ADR 목록](adr/README.md)에서 해당 결정만 |
| 기준 출처와 라이선스 | [SOURCES](SOURCES.md), [LICENSING](LICENSING.md) |

## 한 사실은 한 곳에

현재 상태는 CURRENT, 구현 범위는 Matrix, 실행 상세는 Evidence, 장기 이유는 ADR이 소유한다.
CURRENT·ROADMAP·ADR에 CI run, 테스트 수, bytes, SHA 목록을 복사하지 않는다. 근거가 필요하면 소유 문서를 링크한다.

설계는 Proposed/Accepted/Superseded, 구현은 코드 유무, 검증은 source와 환경별 실행 결과로 구분한다.
Accepted는 설계 방향을 선택했다는 뜻이다. 제품 전체가 구현되었거나 모든 플랫폼에서 검증되었다는 뜻이 아니다.
설계의 실현 가능성이 불명확하면 작은 prototype을 먼저 수행하되, 전체 CI 성공을 모든 결정 채택의 조건으로 삼지 않는다.

현행 명세는 지금 코드의 의미를 설명한다. ADR은 당시 대안과 이유를 보존하므로 옛 API 예시나 파일 구성 자체를
현재 호환 계약으로 취급하지 않는다. 의미가 달라지면 관련 현행 명세와 결정의 대체 관계를 함께 고친다.

## 과거 작업

완료된 work와 대체된 ADR의 실행 과정은 Git에 남긴다. [정리 전 문서 트리](https://github.com/progresshans/godj/tree/da1bfc524c4f205075fc7fac7f00b437473a5e1f/docs)와
[과거 work 목록](https://github.com/progresshans/godj/tree/003afee4524a0294ada8f02c140781f3e1751a5c/work)을 필요한 경우에만 연다.
현재 제품에 필요한 안전성·결정 이유는 현행 문서에 남기며, 링크의 고정 commit은 `git show COMMIT:PATH`로도 읽을 수 있다.
역사 전체를 새 에이전트의 기본 입력으로 사용하지 않는다.
