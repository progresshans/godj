# 핵심 미결정 사항

- 상태: Active register
- 마지막 검토: 2026-08-07

이 문서의 항목은 초안 예시를 확정 API로 오해하지 않도록 관리합니다. 결정이 나면 개별 ADR로 옮기고 여기에는 결과 링크만 남깁니다.

| ID | 우선순위 | 결정 시점 | 질문 |
|---|---|---|---|
| Q-001 | P0 | M0 | 오래된 generated code가 compile되지 않을 때 codegen이 어떻게 새 출력을 만드는가 |
| Q-002 | P0 | M0 | Python/SQLite/timezone/locale과 dependency를 정확히 어떻게 pin하는가 |
| Q-003 | P0 | M0 | 초기 contract manifest와 runner protocol의 최소 형식은 무엇인가 |
| Q-004 | P0 | M0 | GoDj 저장소 license와 upstream Django 파생물의 attribution/licensing 검증을 어떻게 정하는가 |
| Q-005 | P1 | M1 | `ModelDescriptor[M]`는 interface인가 generated concrete type인가, 언제 freeze되는가 |
| Q-006 | P1 | M1 | `NULL`, zero value, omitted/unchanged를 어떤 Go 표현으로 구분하는가 |
| Q-007 | P1 | M1 | QuerySet result cache와 동시 평가의 정확한 의미는 무엇인가 |
| Q-008 | P1 | M1 | dynamic lookup validation/error를 chaining과 어떻게 조화시키는가 |
| Q-009 | P1 | M1 | package별 interface 소유권과 dependency direction을 어떻게 자동 검증하는가 |
| Q-010 | P1 | M1 | 전역 CLI와 프로젝트 library/generator 버전 불일치를 어떻게 처리하는가 |
| Q-011 | P1 | M1 | request, QuerySet, transaction, hook의 goroutine safety 계약은 무엇인가 |
| Q-012 | P1 | M2 | migration 파일 형식, ABI, recorder, lock, DDL transaction 정책은 무엇인가 |
| Q-013 | P1 | M3 전 | cross-app relation의 source/target type, import, reverse path, loader는 어떻게 구성하는가 |
| Q-014 | P2 | M5 전 | DTL parser/runtime 호환 수준과 method exposure 정책은 무엇인가 |
| Q-015 | P2 | M6 전 | Admin에서 보존할 흐름과 새로 설계할 UI/DOM/CSS 경계는 무엇인가 |
| Q-016 | P2 | M7/M8 전 | DRF와 Channels의 정확한 reference version과 호환 범위는 무엇인가 |
| Q-017 | P2 | API freeze 전 | pre-1.0 공개 API와 generated code upgrade 정책은 무엇인가 |
| Q-018 | P2 | 공개 배포 전 | Django trademark와 비공식 프로젝트임을 어떤 이름·고지 정책으로 다루는가 |

## Q-001 — Codegen bootstrap

초안의 임시 runner import 방식은 schema package가 오래된 generated type 때문에 compile되지 않으면 동작하지 않습니다. 후보는 선언/생성 package 분리, Go AST 또는 `go/packages` 기반 제한적 추출, bootstrap package, 별도 선언 포맷입니다. rename/delete와 사용자 메서드 의존 실패를 포함한 compile prototype으로 비교합니다.

## Q-006 — Nullable와 변경 추적

후보는 `*T`, `sql.Null*`, `godj.Nullable[T]`입니다. 단순 조회뿐 아니라 zero value, 명시적 NULL set, partial update에서 생략, JSON/Form 의미를 함께 평가합니다.

## Q-007 — QuerySet cache

불변 plan과 instance result cache를 분리해야 합니다. chain 후 cache, 동시 `All`, error cache, iterator/Count/Exists 간 공유, goroutine sharing을 Django contract와 race/benchmark로 결정합니다.

## Q-008 — Dynamic lookup API

`FilterKw`가 `(QuerySet, error)`를 반환하면 `.FilterKw(...).Update(...)` chaining 예시는 Go에서 컴파일되지 않습니다. error-bearing builder, 즉시 검증, terminal validation 등 후보를 compile usability와 안전성으로 비교합니다.

## Q-013 — 관계 API

초안의 `RelationField[Post]`에는 target type이 없지만 `PostFields.Author.Name`을 사용합니다. symbolic relation binding, target descriptor, generated loader, reverse relation과 import cycle을 한 설계로 검증해야 합니다.

새 작업이 이 표의 질문에 의존하면 추측으로 확정하지 말고 작업 문서에 명시하고 필요한 ADR/prototype을 먼저 만듭니다.
