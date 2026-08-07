# GoDj 프로젝트 헌장

- 상태: Accepted
- 마지막 검토: 2026-08-07
- 설계 상태와 구현 상태는 별개입니다.

## 한 줄 정의

GoDj는 Django의 모델 중심 풀스택 개발 경험을 Go의 정적 타입, 제네릭, 코드 생성, 고루틴, 멀티코어 활용, 단일 바이너리 배포 방식에 맞게 재설계하는 웹 프레임워크입니다.

## 해결하려는 문제

Go 웹 애플리케이션은 보통 router, ORM 또는 SQL builder, migration, validation, template, auth, Admin, OpenAPI, WebSocket, CLI를 따로 선택하고 연결합니다. 이 조합은 유연하지만 다음과 같은 Django식 일관성을 기본으로 제공하지 않습니다.

```text
모델 의미를 한 번 선언
→ ORM과 Migration
→ Form과 Admin
→ Serializer와 OpenAPI
→ Permission과 데이터 호환 도구
```

GoDj의 제품 가치는 개별 기능 수보다 **하나의 모델 의미와 앱 시스템을 공유하는 통합 개발 경험**에 있습니다.

## 제품 목표

### 개념 호환

Django 개발자가 Project, App, Settings, Model, Field, Manager, QuerySet, Migration, Form, ModelForm, Admin, Management Command와 같은 개념을 빠르게 대응시킬 수 있어야 합니다.

### 동작 호환

QuerySet 지연 평가와 체이닝, 모델 저장 수명주기, 관계와 역참조, migration 역사 상태, Form/Admin의 모델 메타데이터 사용 등 사용자에게 관찰되는 의미를 버전이 붙은 계약으로 정의하고 검증합니다.

### 구조 친숙성

Go 관례를 해치지 않는 범위에서 `models.go`, `views.go`, `urls.go`, `forms.go`, `admin.go`, `migrations/`, `management/commands/`, `templates/`, `static/`, `locale/`처럼 Django 사용자에게 익숙한 경계를 유지합니다.

### 데이터 이행 가능성

Django 기본 table/column/relation 관례, auth와 content types, password hash 문자열, 기존 DB introspection을 장기 호환 대상으로 둡니다. 구체적인 지원은 계약과 backend matrix로만 주장합니다.

### Go다운 운영 모델

정적 타입, 명시적 오류, `context.Context`, goroutine concurrency, 멀티코어 실행, 단일 바이너리 배포를 활용합니다. Django의 역사적 WSGI/ASGI 경계나 sync/async API 모양을 그대로 복제하지 않습니다.

## 비목표

- Python `models.py`, migration 또는 Django 앱을 직접 실행하기
- CPython이나 Python 메타클래스 체계를 포함하기
- Python 서드파티 앱의 바이너리·소스 호환
- Django 내부 Python 객체 계층을 Go로 복제하기
- SQL 문자열을 모든 backend에서 byte 단위로 동일하게 만들기
- Django의 알려진 버그를 무조건 재현하기
- GORM 등 기존 ORM에 Django 이름만 씌운 얇은 wrapper 만들기
- 모든 장기 기능을 첫 릴리스에 동시에 구현하기

## 장기 제품 범위

다음은 **제품 비전**이며 현재 지원 목록이 아닙니다.

| 영역 | 장기 범위 |
|---|---|
| Core | settings, app registry, routing, middleware, request/response, signal/event, management command |
| Models/ORM | schema, fields, relations, manager, QuerySet, expression, aggregate, subquery, prefetch |
| Migration/DB | autodetector, historical model, graph, rollback, introspection, multi-DB |
| Full stack | form, template, session, auth, Admin, CSRF, messages, static/media |
| API | serializer, view set, router, auth/permission, OpenAPI, browsable API |
| Realtime | WebSocket, SSE, consumer, group, channel layer, presence |
| GIS/i18n/contrib | spatial fields/lookups, locale/timezone, Django-style contrib capabilities |
| Quality | differential conformance, backend conformance, race/fuzz, security and performance baselines |

기능 범위는 일찍 보존하되, Go API 시그니처는 수직 프로토타입과 계약 테스트를 통과한 뒤 확정합니다.

세부 장기 기능 목록은 [CAPABILITY_CATALOG.md](CAPABILITY_CATALOG.md), 목표 사용자 흐름은 [DEVELOPER_EXPERIENCE.md](DEVELOPER_EXPERIENCE.md)에 있습니다.

## 제품 구성 원칙

- 하나의 제품, 저장소, 릴리스, CLI, 문서 체계를 지향합니다.
- 내부 패키지는 명확한 책임과 의존 경계를 가집니다.
- 초기에는 하나의 Go module로 시작합니다.
- Oracle, GIS 등 무거운 의존성이 핵심 빌드와 배포를 실제로 방해할 때만 공식 module 분리를 검토합니다.
- Admin, API, Realtime은 나중에 붙이는 무관한 CRUD 도구가 아니라 공통 metadata/validation/auth 기반의 상위 모듈입니다.

## 성공 기준

GoDj의 진행률은 파일 수나 패키지 수가 아니라 다음 증거로 판단합니다.

- compatibility profile에 연결된 계약이 실행 가능함
- Django oracle과 GoDj 결과의 차이가 분류됨
- 지원 기능은 compile test와 runtime test를 통과함
- backend별 차이가 명시적 capability 또는 deviation으로 드러남
- codegen과 migration이 반복 실행·실패·rollback 상황에서도 결정적임
- concurrency, context 취소, transaction 수명주기가 race/error 테스트로 검증됨
- 현재 상태, 활성 작업, 테스트 증거가 같은 checkout을 가리킴
