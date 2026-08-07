# GoDj Repository Instructions

## 프로젝트 정의

GoDj는 Django의 Python 구현을 포팅하는 프로젝트가 아니다. Django의 모델 중심 풀스택 개발 경험과 외부 동작을 Go에 맞게 재설계한다.

Python 소스, Python 서드파티 앱, Django 내부 객체 구조의 호환은 목표가 아니다. 장기 제품 범위에 Admin, API, Realtime, GIS, 다중 DB가 포함되더라도 현재 지원을 뜻하지 않는다.

## 작업 시작 시 읽기 순서

모든 문서를 무조건 읽지 않는다. 다음 순서로 필요한 범위만 읽는다.

1. 이 파일
2. `docs/status/CURRENT.md`
3. CURRENT가 가리키는 활성 `work/*.md`
4. 그 작업이 링크한 명세, 호환 계약, ADR

문서의 역할과 정본 위치는 `docs/README.md`를 따른다.

## 상태와 범위

- `Proposed`, `Accepted`, `Implemented`, `Verified`를 구분한다.
- 문서나 예제에 존재한다는 이유로 구현됐다고 표현하지 않는다.
- 활성 작업 문서의 목표·비목표·수정 허용 경로를 지킨다.
- 공개 API, Schema IR, Query AST, Migration State, 패키지 의존 방향을 바꾸기 전에는 작업 문서와 ADR 상태를 확인한다.
- 장래 사용을 이유로 빈 패키지나 추상화를 대량 생성하지 않는다.
- 기존 사용자 변경과 작업 범위 밖 파일을 보존한다.

## 아키텍처 불변 조건

GoDj의 기본 파이프라인은 다음과 같다.

`Schema DSL → Schema IR → Codegen → Generic Core → Runtime Metadata → Query AST → Backend Compiler`

- Schema IR은 모델 의미의 정규화된 단일 원본이다.
- Codegen은 모델별 정적 타입, FieldSet, Descriptor, Codec 연결을 만든다.
- Generics는 모델 공통 동작과 타입 정보를 유지한다.
- Runtime Metadata는 문자열 lookup, Admin, Historical Model 등 런타임 동적 기능을 처리한다.
- 타입 안전 API와 동적 API는 동일한 Query AST 의미로 수렴한다.
- DB별 차이는 backend capability와 compiler/schema editor 경계에서 처리한다.
- 지원하지 않는 기능은 조용히 무시하지 않고 구조화된 오류를 반환한다.
- 기존 ORM을 핵심 실행 엔진으로 감싼 구현은 채택하지 않는다.

API 예시는 검증 전 가설일 수 있다. 특히 codegen bootstrap, 앱 간 관계, nullable 표현, QuerySet 캐시, migration 형식은 `docs/OPEN_QUESTIONS.md`를 확인한다.

## Go 규칙

- 모든 실제 I/O는 `context.Context`와 `error`를 전달한다.
- 일반 오류 경로에 `panic`을 사용하지 않는다.
- generic type에는 메서드를 정의할 수 있지만, 메서드가 receiver에 없는 새 type parameter를 선언할 수 없음을 지킨다. 추가 type parameter가 필요하면 최상위 generic function 또는 별도 generic type을 검토한다.
- Query plan은 불변 값으로 다룬다. goroutine 안전성이나 결과 cache 공유는 명시적 계약 없이는 가정하지 않는다.
- hot path의 reflection 사용은 측정과 근거 없이 도입하지 않는다.
- 생성 결과는 결정적이어야 하며 `gofmt`, schema hash, generator version, 실패 시 기존 산출물 보존을 검증한다.
- 사용자 프로젝트의 생성 Go source는 Git에 커밋하는 방향이며, 생성기가 구현되면 CI에서 `godj generate --check`로 drift를 검증한다.

## 호환성과 테스트

- 기준 프로필과 contract ID는 `docs/COMPATIBILITY.md`와 `docs/status/IMPLEMENTATION_MATRIX.md`에서 확인한다.
- Django 결과를 oracle로 쓰기 전에 버전, DB, timezone, locale을 고정한다.
- 비교 우선순위는 결과·부작용, 오류 의미, transaction 의미, 공개 동작, SQL 의미 순이다. SQL 문자열 동일성은 기본 목표가 아니다.
- Django 테스트를 번역할 때 출처와 라이선스 정보를 남기고 Python 내부 구조를 복제하지 않는다.
- 테스트를 실행하지 않았으면 통과했다고 쓰지 않는다. 테스트 증거에는 명령, 날짜, checkout 식별자, 결과를 남긴다.
- false green을 금지한다. 미구현 계약은 명시적으로 미구현 상태여야 한다.

## 작업 종료와 인수인계

작업이 끝나거나 중단될 때 다음을 함께 갱신한다.

- 활성 `work/*.md`: 체크리스트, 변경 파일, 결정, 테스트 증거, 다음 정확한 작업
- `docs/status/CURRENT.md`: 현재 단계, 활성 작업, blocker, 다음 작업
- `docs/status/IMPLEMENTATION_MATRIX.md`: 설계/구현/검증 상태가 바뀐 기능만
- `docs/status/TEST_EVIDENCE.md`: 실제 실행한 검증만
- 관련 ADR 또는 `docs/DEVIATIONS.md`: 장기 결정이나 의도적 차이가 생긴 경우

완료 보고에는 변경 파일, 구현 동작, 결정, 실행한 테스트, 실행하지 못한 테스트, 알려진 제한, 다음 작업을 포함한다.

멀티 에이전트 작업에서는 동일한 공개 API, 안정 명세, CURRENT 파일을 여러 에이전트가 동시에 수정하지 않는다. 통합 담당 한 명이 최종 상태를 반영한다.
