# GDJ-0134: 명명된 계산식과 조회 결과

- 상태: 공통 AST·compiler·ORM·생성 facade·두 생성 소비자·독립 비교·Helpdesk 연결 작성; 원격 검증 진행 중
- 시작: 2026-10-19
- 기준: `90437ebc6b8508995de962660adc3b6cad66a839` — GDJ-0133 뒤 전체 실행 계획 포함; 제품 source `55928651`의 전체 통합 검증 완료
- 근거: [헌장](../docs/CHARTER.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md), [개발 기준](../docs/DEVELOPMENT_CRITERIA.md),
  [계산식](../docs/adr/0090-query-update-and-scalar-expressions.md), [집계와 결과 단계](../docs/adr/0091-grouped-results-and-having.md)

[전체 실행 계획](../docs/EXECUTION_PLAN.md)의 Q1이다. 전체 구현과 G-ORM 여섯 조건을 충족한 뒤 A1의
Form/Admin/API 조립으로 주 작업을 옮긴다. 일반 function/subquery/window의 전체 완성을 추가 선행 조건으로 넣지 않는다.

## 필요한 기반과 소비자

현재 계산식은 Go 변수로 projection·조건·정렬에 재사용할 수 있다. 그러나 query가 소유한 이름으로 계산 결과를
찾거나, 앞서 정의한 이름에 의존하는 계산식과 재정의를 표현할 수 없다. 모델의 Schema IR에 임시 필드를 추가하거나
애플리케이션에서 전체 행을 읽어 계산하는 방식으로 이 요구를 대신하지 않는다.

Helpdesk의 기한 미리보기에서 조정 기한·남은 시간을 명명된 결과로 공유하고, 선택·필터·정렬·페이지·입출력과
생성 client에 연결한다. 구조가 다른 생성 앱에서는 원 모델과 명명 결과, scalar 및 관계 집계의 의존성·재정의와
집계 전후 필터 순서를 함께 검증한다. 정확한 공개 API 형태는 이 소비자와 compile prototype에서 확인한 뒤 확정한다.

## 유지할 계약

- 이름과 값은 query가 소유한 불변 정의다. Schema IR·ModelDescriptor의 저장 필드와 명시적 ResultScope를 구분한다.
  SQL alias와 사용자 이름을 동일시하지 않는다. Typed/dynamic는 같은 공통 AST·source 검증·compiler를 사용한다.
- 정의는 앞에서 뒤로 bind한다. 미정의 이름·앞선 정의 없는 순환은 I/O 전에 오류다. 재정의는 새 정의를 만들고,
  기존 의존 계산식과 이미 만든 query·결과·cache를 소급 변경하지 않는다. 다른 분기의 참조를 이름만 같다는 이유로 허용하지 않는다.
- 원 필드·관계 이름과의 충돌, 선택되지 않는 alias와 선택되는 annotation, 같은 이름의 재정의·선택 순서를 명시한다.
  원본 metadata·NULL·Decimal precision·Duration fraction/native mean·명시적 시간대 의미를 이름 뒤에서도 보존한다.
- Scalar와 aggregate 정의를 함께 다룬다. 원 행의 조건과 집계 결과 조건을 구분하고, collection 집계가 정의되는
  시점의 JOIN 의미를 이후 Filter가 바꾸지 않게 한다. 여러 collection의 실제 행 곱·DISTINCT/FILTER·empty/NULL을 보존한다.
- 이름은 숨겨진 subquery나 새 evaluation cache를 뜻하지 않는다. 필요할 때 명시적 결과 stage를 사용하며 slice·정렬·
  grouping·Count/Exists·재집계·행 잠금·쓰기의 지원 및 거부 경계를 오류로 드러낸다. 일반 실패에 panic하지 않는다.
- 모든 dependency와 실제 SQL 확장의 깊이·노드·parameter 한도를 검사한다. 공유 map·slice·scanner·DTO가 caller 변경으로
  다른 query에 영향을 주지 않으며 context/session/cursor 종료·scan 실패·rollback 뒤 부분 결과를 게시하지 않는다.
- Helpdesk의 현재 Category·ViewTicket 인가·하나의 읽기 snapshot·DB pagination·전체 저장/관계/audit 보존을 유지한다.
  이름을 외부 입력으로 받는 경로는 명시적 allowlist를 쓰며 임의 저장 필드·관계·SQL 접근을 허용하지 않는다.

## 구현과 검증 순서

1. 공통 AST에 명명 정의·참조와 query별 불변 소유권·재정의·자원 예산을 연결한다.
2. 양 compiler와 scalar/집계·원 행/결과 stage·JOIN 순서의 실행 의미를 연결한다.
3. Generic ORM·typed/dynamic·원 모델/명명 결과 decoder·생성 facade를 함께 구현한다.
4. 구조가 다른 생성 소비자·고정 Django/native 비교·실패/소유권/compile 부정 대조와 Helpdesk의 HTML/API/client/browser를 연결한다.
5. 한 변경 묶음의 포맷·최소 compile 후 push하고, 영향·DB·race·process·platform 검증은 원격 CI에서 수행한다.

## 완료 조건

- [ ] 이름·참조·재정의·소유권과 자원 한도가 공통 AST와 양 backend에 연결된다.
- [ ] Scalar/집계·필터 순서·선택/alias·원 모델 및 결과 단계의 typed/dynamic·생성 API와 실패 의미가 구현된다.
- [ ] 구조가 다른 생성 소비자와 Helpdesk의 실제 흐름·독립 비교·인가/저장 보존을 검증한다.
- [ ] 같은 source의 필수 실행과 원본을 대조하고 현행 지원·차이·미완료 범위를 갱신한다.

## 현재 근거

2026-10-21, 원래 작업 디렉터리는 없지만 보존된 Git snapshot에서 코드·검사·필수 목록·이 작업 문서를 복구했다.
코드 세 파일과 필수 목록의 bytes는 이전 compile receipt의 SHA-256과 일치한다. 복구는 제품 동작 검증과 구분하며,
현재 파일·source와 검증 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.

`query.Plan`은 원 필드와 선택 결과를 구분하며 `ResultScope`는 pointer identity로 선택된 열의 권한을 소유한다.
`DynamicExpression`의 이름은 현재 concrete metadata와 명시적인 project relation binding으로만 해석된다.
모델의 추가 열 decoder에는 기존 generated `ProjectionDescriptor`/`ProjectionScan`을 사용할 수 있으며,
이 capability와 일반 `ModelDescriptor`의 지원 경계도 구현에서 검증한다.

`query.AnnotationRef`와 Plan의 불변 정의에 `ScalarAnnotation`을 연결했다. 재정의는 새 identity를 만들고 이전
조건·계산식의 참조를 보존한다. CASE 양 operand·함수·산술·결과 stage의 source 권한과 집계/NULL/Duration
provenance를 전파하며 실제 dependency 확장을 scalar 한도에 포함한다. Field 목록이나 ResultScope만으로
명명 참조를 승인하지 않는다. Alias 직접 선택·중첩 집계·명명 query에서의 쓰기는 명시적으로 거부한다.

공통 compiler는 각 정의가 볼 수 있던 collection Filter까지만 JOIN을 재사용한다. 첫 명명 정의 뒤의
Filter가 unfiltered occurrence를 재사용하지 않도록 scope를 분리했다. 첫 aggregate가 만든 grouping 경계는
projection·재정의 뒤에도 유지한다. 집계 결과와 의존 계산식을 물리적으로 분리해 PostgreSQL의 정확한 Duration
fraction accumulator를 완료한 뒤 참조하며, 명시적 result stage의 소유권이나 slice 의미를 바꾸지 않는다.

AST의 참조/예산 부정 대조와 양 DB의 독립 seed 기반 검사 본문을 필수 실행 목록에 연결했다. 필터 전후 JOIN,
여러 collection 행 곱·DISTINCT, 혼합 OR, 숨긴 그룹 키, 이름 재정의, 명시 stage/slice/재집계와 native mean·fraction을
포함한다. Typed/dynamic facade·원 모델 및 명명 결과 decoder를 작성하고 scalar 및 collection 구조의 두 생성
소비자를 연결했다. 모델 wrapper/DTO·타입·NULL·선택/재정의·DB Page·cache/session/실패·저장 분리를 검사한다.
Helpdesk는 `adjusted_due_at`에 의존하는 `remaining`을 같은 명명 query에 bind하고 기존 필터·정렬·DB Page와
HTML/API/client 계약을 사용한다. 고정 Django observer의 양 DB 77개 결과·오류와 명시한 Go 차이를 외부 생성
소비자에 연결했다. 모든 비교는 rollback과 네 모델의 전체 저장 행·관계 보존을 확인하며, 의도한 차이의 Django
결과도 고정 원본과 일치해야 한다. 원격 runtime·race·실제 Helpdesk 흐름의 검증 완료는 아직 남아 있다.
코드와 검사 본문의 존재나 compile-only를 runtime PASS로 계산하지 않는다.

기존 독립 Django 6.1 annotation observer와 보존된 양 DB 77개 관측을 읽었다. 앞선 정의 참조, forward reference 오류,
재정의 후 기존 dependency 보존, 필드/관계 이름 충돌, alias 선택 거부, 집계 전후 Filter와 nested aggregate 오류를
이번 요구의 근거로 사용한다. 새 runner는 같은 observer를 소유한 DB에서 다시 실행하고 source·runtime·정리 상태와
전체 목록을 대조한 뒤 Go 결과와 비교한다. 과거 관측이나 GDJ-0133의 PASS를 새 기능의 실행 성공으로 계산하지 않는다.
실제 검증 상태는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## G-ORM의 다음 소비자 준비

A1의 기존 공개 경로를 source `8325c30f`에서 확인했다. 아래 준비는 새 자동 CRUD의 구현이나 사용자 실험을 뜻하지 않는다.

| 소비자 | 현재 사용 가능한 공개 경계 | A1에서 함께 유지할 책임 |
|---|---|---|
| Article 단일 모델 CRUD | `Manager.Metadata/ModelValues/ApplyValues`, typed create/patch·선택 열 Save, `forms/model.BindInstance/PrepareInstance`, model serializer·typed endpoint | 입력 생략/NULL/default와 PUT/PATCH, 대상 조회·body 해석 순서, 저장 안의 응답 준비·commit 결과, publish 명령·감사 기록 |
| Helpdesk 관계 모델 CRUD | 같은 모델 준비, project relation binding·현재 scope 조회, `SaveCollections/SaveManyToMany`, 관계 transaction과 model Admin | Category·객체/관계 선택의 현재 권한, update-only와 제외 열, revision·unique·관계/audit 원자성, 파일 게시의 별도 outcome |
| 계산 결과 목록 | 명명 query·명시 projection·단일 statement Page, typed output/endpoint와 기존 Helpdesk 미리보기 | 명명 셀의 source 권한·read-only, 원 저장 모델과 분리, 현재 읽기 인가·snapshot 및 입력/응답 allowlist |

실제 연결은 [Article Admin](../examples/article/adminapp/registration.go),
[Article 수정 endpoint](../examples/article/apiapp/typed_update.go),
[모델 Form 준비/저장](../forms/model/README.md),
[Helpdesk Form 저장](../examples/helpdesk/form_save.go),
[Label 수정 endpoint](../examples/helpdesk/label_update_endpoint.go),
[명명 기한 조회](../examples/helpdesk/deadline_preview.go)에 있다.
Source 검토에서 이 세 흐름을 위해 추가로 먼저 구현해야 할 ORM 기능은 찾지 못했다.
A1은 이미 있는 public 기능의 반복 DTO·snapshot·입력 매핑과 저장 연결을 줄이는 작업으로 시작할 수 있다.
권한·transaction·응답 준비를 일반 CRUD로 바꾸면서 생략하지 않으며, G-ORM의 현재 실행 조건은 별도로 끝낸다.
