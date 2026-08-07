# GoDj

GoDj는 Django의 모델 중심 풀스택 개발 경험을 Go의 정적 타입, 제네릭, 코드 생성, 고루틴, 멀티코어 활용, 단일 바이너리 배포 방식에 맞게 재설계하는 웹 프레임워크입니다.

현재 지원 범위, 통과 contract, 활성 작업과 다음 단계는
[현재 상태와 다음 작업](docs/status/CURRENT.md)에서만 갱신합니다. 장기 범위가 문서에
있다는 사실은 구현을 뜻하지 않으며 pre-1.0 API와 성능 수치도 제품 약속이 아닙니다.

## 현재 기준

| 항목 | 기준 |
|---|---|
| 제품명 / CLI | `GoDj` / `godj` |
| Go module | `github.com/progresshans/godj` |
| Go 언어 / toolchain | Go 1.26 / 1.26.5 |
| Django 참조 프로필 | Django 6.1, CPython 3.14.3, SQLite 3.50.4, darwin/arm64 |
| 현재 Go backend | `modernc.org/sqlite v1.56.0`, SQLite 3.53.3 |
| Python 소스 호환 | 목표가 아님 |
| 핵심 방향 | Schema DSL → Schema IR → Codegen → Generic Core → Runtime Metadata → Query AST → Backend Compiler |

## 처음 읽을 문서

1. [문서 안내](docs/README.md)
2. [현재 상태와 다음 작업](docs/status/CURRENT.md)
3. [프로젝트 헌장](docs/CHARTER.md)
4. [아키텍처](docs/ARCHITECTURE.md)
5. [호환성 정책](docs/COMPATIBILITY.md)
6. [테스트 전략](docs/TESTING.md)

[작업 목록](work/README.md)에서 완료·활성 항목을 확인할 수 있습니다. 새 대화나 새
에이전트에서 작업을 이어갈 때는
[계속 작업 프롬프트](prompts/CONTINUE_WORK.md)를 사용할 수 있습니다.

## 중요한 구분

- 문서에 장기 기능이 적혀 있다는 사실은 구현 또는 지원을 의미하지 않습니다.
- API 예시는 설계 의도를 설명할 뿐, ADR이나 검증된 명세에서 확정되기 전에는 공개 계약이 아닙니다.
- 기능은 `제안됨 → 채택됨 → 구현됨 → 검증됨`을 구분해 기록합니다.
- Django와의 동일성은 인상이나 SQL 문자열이 아니라 버전이 고정된 동작 계약과 실행 증거로 판단합니다.

## 프로젝트 상태

정확한 브랜치, 커밋, 활성 작업, 검증 결과, 차단 요인은 [docs/status/CURRENT.md](docs/status/CURRENT.md)만 갱신합니다. README에는 진행률을 복제하지 않습니다.
