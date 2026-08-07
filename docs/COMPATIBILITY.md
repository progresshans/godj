# Django 호환성 정책

- 상태: Accepted
- 초기 프로필: `django-6.1`
- 기준 태그: Django `6.1`, commit `fe0a859f537d4238cf49fca39073513206f83122`
- 마지막 검증: 2026-08-07

GoDj의 호환성은 Python 코드를 실행하는 능력이 아니라 **사용자가 관찰할 수 있는 개념, 결과, 부작용, 오류, transaction 의미**를 Go API에서 재현하는 정도입니다.

## 프로필 범위

초기 정본은 Django 6.1 final입니다. 현재 로컬 `/Users/hanhyeonjin/Documents/django`의 checkout은 Django 6.2 alpha가 포함된 `main`이므로 그대로 oracle로 사용하지 않습니다. M0에서 tag `6.1` 또는 정확히 고정된 `Django==6.1` 환경을 별도로 재현합니다.

DRF와 Channels에 해당하는 `api`, `realtime` 모듈은 장기 제품 범위에 포함되지만, 참조 프로젝트와 정확한 버전은 아직 정하지 않았습니다. Django 6.1 호환이라고 해서 DRF/Channels 호환까지 자동으로 주장하지 않습니다.

## 호환성 차원

| 차원 | 목표 |
|---|---|
| 개념 | Django 사용자가 대응 개념과 수명주기를 이해할 수 있음 |
| 동작 | 결과, 순서, 부작용, 오류 범주, transaction 의미가 계약과 일치 |
| 구조 | Go 관례 안에서 앱·파일·명령 구조가 친숙함 |
| 데이터 | table/column/relation/auth 관례와 이행 경로를 단계적으로 지원 |
| 내부 구현 | 호환 목표가 아님 |
| Python source/API | 호환 목표가 아님 |

## 비교 우선순위

1. 반환 결과와 DB/외부 부작용
2. 오류 분류와 발생 시점
3. transaction, locking, rollback 의미
4. Model과 QuerySet의 공개 동작
5. 프로젝트와 출력 구조
6. Query AST의 GoDj 내부 불변 조건
7. SQL의 의미
8. 필요한 좁은 경우에만 SQL 문자열

두 backend가 같은 의미의 SQL을 다르게 생성할 수 있으므로 SQL 문자열 동일성은 기본 합격 조건이 아닙니다.

## 계약 단위

각 동작에는 안정적인 ID를 부여합니다.

```text
META-xxx Profile, provenance, harness protocol
SCH-xxx  Schema와 model metadata
GEN-xxx  Code generation
QRY-xxx  QuerySet과 lookup
MOD-xxx  Model lifecycle
REL-xxx  Relation과 loading
MIG-xxx  Migration
FRM-xxx  Form/validation
ADM-xxx  Admin
AUT-xxx  Auth/session/permission
WEB-xxx  Routing/middleware/template
API-xxx  Serializer/API
RTM-xxx  Realtime
GIS-xxx  Spatial
I18N-xxx Locale/timezone/translation
CTR-xxx  Contrib와 infrastructure
DB-xxx   Backend 공통 계약
```

계약에는 최소한 다음을 기록합니다.

- contract ID와 설명
- 정확한 reference profile
- Django 문서 또는 테스트 경로
- 입력, fixture, 연산
- 결과, 순서 보장, 부작용
- 안정적인 오류 범주
- 대상 backend와 환경
- 관련 GoDj 테스트/runner 경로
- 의도적 차이와 ADR
- 현재 상태와 마지막 검증 checkout

초기 계약 목록과 상태는 [status/IMPLEMENTATION_MATRIX.md](status/IMPLEMENTATION_MATRIX.md)가 관리합니다. 구현이 시작되면 machine-readable manifest를 추가하되 Markdown 정본과 자동 검증으로 drift를 막습니다.

## 계약 상태

설계·구현 상태와 실행 상태를 구분합니다.

```text
계약 실행 상태:
draft → oracle_locked → red → passing
                         └→ deviation
```

- `draft`: 계약 내용이나 환경이 아직 바뀔 수 있음
- `oracle_locked`: Django 결과와 provenance가 재현 가능하게 고정됨
- `red`: GoDj가 실행되지만 계약을 통과하지 못함
- `passing`: 명시된 profile/backend에서 통과 증거가 있음
- `deviation`: 차이를 의도적으로 수용했고 deviation/ADR이 연결됨

미구현 기능을 skip하여 녹색으로 만드는 것은 금지합니다. 미구현은 명시적 상태와 실패/미지원 결과로 드러나야 합니다.

## 초기 계약 후보

M0에서는 범용 Django 테스트 전체를 옮기지 않고 8~12개의 작은 계약을 고정합니다.

- exact lookup
- ASCII `icontains`
- 여러 filter의 AND 결합
- QuerySet 체이닝과 원본 query plan 불변성
- `order_by`와 limit
- 빈 결과
- nullable 값과 `isnull`
- 잘못된 field/lookup의 오류 의미
- 실제 I/O 전 지연 평가
- 결과 cache 의미는 Q-007 결정 후 추가

M1에서 `Article` 한 모델의 typed/dynamic lookup이 같은 AST와 SQLite 결과로 이 계약을 통과하도록 만듭니다.

## 데이터 호환성

다음은 장기 대상이며 구현 전에는 지원을 주장하지 않습니다.

- Django 기본 table naming과 primary key 관례
- ForeignKey column과 ManyToMany join table
- auth, permission, content types 데이터
- Django password hash 문자열
- 기존 Django DB introspection과 import/export
- timezone, collation, decimal, UUID, bytes 직렬화 의미

데이터 호환은 destructive fixture가 아닌 disposable database와 migration round-trip으로 검증합니다.

## 의도적 차이

다음과 같은 차이는 합리적일 수 있습니다.

- Python exception hierarchy를 Go error category와 `errors.Is/As`로 표현
- Python의 동적 속성 대신 generated typed field와 explicit metadata API 사용
- WSGI/ASGI 또는 sync/async API 쌍 대신 Go request/goroutine/context 모델 사용
- Django Admin의 흐름은 보존하되 DOM/CSS를 GoDj 자체 UI로 제공
- backend별 unsupported feature를 명시적 error로 반환

차이는 “Go니까 다르게 했다”로 끝내지 않습니다. 관찰 가능한 영향, migration 비용, 관련 계약, 대안을 [DEVIATIONS.md](DEVIATIONS.md)에 기록하고 필요한 ADR을 연결합니다. Mismatch가 발생했다는 사실만으로 deviation이 승인되지는 않습니다.

## Oracle 변경 정책

- reference version, DB, timezone, locale 변경은 별도 diff로 검토합니다.
- Django oracle 출력은 같은 환경에서 반복 생성 시 byte 단위로 같아야 합니다.
- oracle 변화는 자동 승인하지 않습니다.
- mismatch는 `GoDj bug`, `scenario bug`, `intentional deviation`, `Django bug`, `environment drift` 중 하나로 분류합니다.
- 정렬로 차이를 숨기지 않습니다. 순서가 계약이면 원래 순서를 비교합니다.

## 출처와 라이선스

Django 문서와 테스트에서 파생한 시나리오는 upstream version, 파일 경로, 원래 테스트 이름을 기록합니다. Django의 BSD 3-Clause 조건에 따라 복사·번역한 코드에는 필요한 저작권 고지와 라이선스 정보를 보존합니다. 동작을 독립적으로 기술한 계약과 upstream 코드를 번역한 파생물을 구분합니다.

공식 기준 링크와 로컬 검증 정보는 [SOURCES.md](SOURCES.md)에 있습니다.
