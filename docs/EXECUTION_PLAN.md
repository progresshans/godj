# 전체 범위 실행 계획

- 수립: 2026-10-20
- 대상: [헌장](CHARTER.md)과 [기능 카탈로그](CAPABILITY_CATALOG.md)의 전체 범위
- 기준: GDJ-0133까지 포함한 `b2a862766a298cf640dcf67c1c292f193aa77d27`
- 성격: 개발 순서·의존성·완료 판정 기준. 이 문서의 채택은 제품 구현이나 검증 완료가 아니다.
- 현재 실행 상태는 [CURRENT](status/CURRENT.md), 지원 범위는 [구현 현황](status/IMPLEMENTATION_MATRIX.md)과
  [Backend Matrix](BACKEND_MATRIX.md), 실행 상세는 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)가 소유한다.

범위와 품질을 유지하면서 다른 영역의 착수가 ORM의 전체 완성 뒤로 밀리지 않게 한다.
**GDJ-0134의 named annotation을 끝내고 아래 ORM 전환 기준을 충족하면, 주 작업을 A1의 모델 중심
Form/Admin/API 조립으로 옮긴다.** 일반 함수·subquery/window·고급 관계·추가 필드는 후속 작업으로
명시적으로 남기고 배정된 순서에 수행한다. 이 전환은 ORM 전체의 완료 판정이 아니다.

## 계획의 기준과 해석

1. 이미 구현·검증한 scalar, 관계, bulk, formset/inline, 템플릿 상속/include, 인증, 파일·메일 등의 단면은 재사용한다.
   같은 영역에 미완료 기능이 있다는 이유로 기존 구현을 모두 다시 만들지 않는다.
2. 현재의 `미지원`, 범용성 미검증, 필요한 환경의 미실행, 구체적 지원 목록 미확정은 모두 미완료다.
   다른 작업으로 이관할 때는 받는 작업 ID와 완료 조건을 남긴다. 기록 없이 사라지는 잔여 범위를 허용하지 않는다.
3. 표의 작업 ID는 계획 안의 묶음이다. 새 ADR·work·package를 모두 미리 만들지 않는다.
   실제 착수한 묶음만 기존 작업을 이어 쓰거나 필요한 work로 연결한다. Q1은 GDJ-0134를 이어 받는다.
4. 묶음 안의 제품 코드·IR/생성기·공개 API·소비자·실패 경로·검증은 함께 완성한다.
   구현을 나눌 수 있어도 묶음의 일부 성공으로 나머지를 완료 처리하지 않는다.
5. 우선순위는 구현 순서다. 낮은 순위도 필수 범위이며 품질 기준은 같다.
   MVP, 데모, 특정 예제의 성공, 테스트 수·파일 수를 전체 완료의 대체 기준으로 삼지 않는다.
6. [호환성](COMPATIBILITY.md)의 고정 Django/DRF 기준, Go-native 계약, 공식 protocol과 backend capability를 구분한다.
   Python 내부 구조·소스 실행 등 헌장의 비목표를 추가 구현 의무로 만들지 않는다.
   반대로 Go API가 다르다는 이유로 관찰 가능한 목표 기능을 삭제하지 않는다.
7. 자원 한도·명시적인 오류·DB 고유 제약은 안전 계약이다. 제품에 없는 기능을 `unsupported`로 반환하는 것만으로
   해당 기능을 완료했다고 하지 않는다. Native 제약에 따른 차이는 실제 capability와 검토한 deviation으로 증명한다.

### 기준 source와 재개 준비

기준 branch에는 GDJ-0130~0133의 구현 및 환경별 검증 기록이 들어 있다.
현재 기본 checkout의 이전 CURRENT만 읽어서 이 후속 작업들을 미완료로 되돌리지 않는다.
PR의 미병합 상태, 제품 코드의 존재, 실행 증거는 각각 별도로 판단한다.

GDJ-0134의 이전 미커밋 작업 사본은 계획 수립 시 원래 경로에서 확인되지 않았다.
남아 있는 `codex/named-query-annotations`의 commit은 기준 source와 같으며 annotation 구현을 포함하지 않는다.
따라서 이전 compile 기록을 현재 구현의 증거로 쓰지 않는다. B0에서 보존된 사본·증거를 확인하고,
복구할 수 없는 부분은 명확히 기록한 뒤 Q1에서 다시 구현한다. 복구 조사 자체가 장기 대기 사유가 되어서는 안 된다.

## 우선순위와 기본 실행 순서

기본은 제품 변경 묶음 하나를 끝까지 연결하는 방식이다. 아래 순서는 한 명의 통합 담당자가 실행해도 성립한다.
병렬 인력이나 상시 백그라운드 실행을 일정의 전제로 두지 않는다.

| 단계 | 우선순위 | 기본 실행 순서 | 단계가 끝나면 확보할 결과 |
|---|---|---|---|
| 0 | P0 재개 조건 | B0 | 기준 source·미커밋 작업의 보존 여부·즉시 작업과 환경을 확정 |
| 1 | P0 현재 작업 | Q1 | named annotation의 전체 계약과 실제 소비자, ORM 전환 기준 충족 |
| 2 | P1 첫 영역 전환 | A1 → F1 → C1 → I1 | 공통 CRUD 조립·Form/Admin 확장·새 프로젝트 시작·custom user 연결 |
| 3 | P1 앱 성장과 표현 | M1 → A2 → T1 → L1 | 안전한 모델/data 변경·일반 API 도구·템플릿 확장·지역화 |
| 4 | P2 공통 기능 폭 | Q2 → S1 → I2 → C2 → K1 → R1 | 함수/관계 폭·인증 provider·Web/contrib·단일 서버 Realtime |
| 5 | P2 DB와 서비스 확장 | Q3 → M2 → D1 → D2 → S2 → K2 → K3 | subquery/window·migration 도구·추가 DB·특수 필드·서비스/데이터 도구 |
| 6 | P3 운영과 복합 영역 | O1 → R2 → G1 → X1 | 운영·다중 process Realtime·GIS·기존 Django 데이터 이행 |
| 7 | P3 전체 완료 판정 | Z1 | 전체 카탈로그와 지원 환경의 누락 없는 최종 대조 |

단계 0~7은 순서를 읽기 위한 묶음이며, 실제 요구와 의존성은 B0·Q1 등 작업 ID로 추적한다.
**필수 선행 관계는 아래 작업 표가 소유한다.** 모든 앞 단계가 끝나야 뒷 영역을 준비할 수 있다는 뜻은 아니다.
예를 들어 C1의 프로젝트 시작, T1의 템플릿, C2/R1의 Web·Realtime 준비는 ORM 전체를 기다릴 이유가 없다.

실행 선택은 다음 규칙을 따른다.

1. 현재 묶음을 완성하되 다른 묶음의 새 기능을 편의상 계속 덧붙이지 않는다.
2. 현재 묶음이 끝나면 표에서 가장 앞선 실행 가능한 미완료 묶음을 선택한다.
3. 외부 DB·서비스·계정 등 실제 환경 때문에 막혔다면 원인·필요한 입력·다음 확인 행동을 CURRENT에 적고,
   선행 조건이 갖춰진 다음 묶음을 진행한다. 막힌 묶음은 열린 상태로 유지한다.
4. 실행 가능한 다른 영역을 건너뛰고 같은 영역의 새 묶음을 먼저 수행하려면 실제 소비자의 실패 재현과
   의존성 변경 이유를 기록한다. 미래에 유용할 것 같다는 이유는 우선순위 변경 근거가 아니다.
5. 안전성·권한·데이터 보존 회귀는 순서와 무관하게 우선 해결한다. 해결 뒤 원래 다음 묶음으로 복귀한다.

## 전체 카탈로그의 미완료 범위 연결

아래 20개 행은 카탈로그의 영역을 빠짐없이 연결한다. `기반 있음`은 그 영역 전체가 완료됐다는 뜻이 아니다.
새 묶음의 완료 판정에는 아래 행과 해당 작업의 완료 조건을 모두 적용한다.

| ID | 카탈로그 영역 | 확인한 기존 기반 | 남은 범위 또는 추가 대조가 필요한 범위 | 완료 책임 |
|---|---|---|---|---|
| C01 | Project/Core | settings·app lifecycle, project/app scaffold, check·사용자 command, generate·migration·초기 사용자·runserver/reload, 설치 호환성·기본 logging·동기 event; C1 완료 근거는 TEST_EVIDENCE | 운영 환경의 설정·진단·배포 도구와 향후 지원 릴리스의 upgrade 정책 | C1, O1 |
| C02 | Web | request/response·middleware·오류·int64/str 경로와 reverse, streaming·파일 조건/Range·upload, C1의 개발 reload·요청 drain·종료 | namespace/converter 확장, static/media의 개발·운영 경로, HTTP 확장과 운영 관측/종료의 일반 구성 | C1, C2, O1 |
| C03 | Schema/Model | 정규화 IR, generated type/FieldSet/codec, 모델별 Go method, 현재 metadata | custom field/options/default/validator 확장, 모델 구성·상속 요구의 Go 표현, introspection과 더 넓은 key/관계 의미 | S1, S2, M2 |
| C04 | Fields | Auto/Integer/Float/Decimal/Boolean, Char/Text/Slug/Email/URL/UUID, Date/DateTime/Time/Duration, Binary/JSON, File/Image, choices의 현재 단면 | 기존 타입의 미지원 연산·default/constraint 조합, backend array/range/generated field, spatial 타입의 전 계층 연결 | Q2, Q3, S1, S2, G1 |
| C05 | Relation | FK/OneToOne/ManyToMany, forward/reverse manager, eager/prefetch, 할당·삭제와 유한 graph | AutoField 이외 target·relation-as-PK, 남은 cyclic/cross-app 조합, collection 값 projection/정렬·표현식과 관계 UI | S1, Q2, Q3, F1 |
| C06 | QuerySet | lazy/cache/iterator, Q/F, lookup/order/limit/distinct, projection/group/having/aggregate, 결과 stage | named annotation/alias, 남은 function·lookup·JSON/tuple/집합 연산, subquery/상관 참조/window, 각 조합의 평가·cache 수명 | Q1, Q2, Q3 |
| C07 | ORM writes | create/save/update/delete, get-or-create/update-or-create, bulk, 행 잠금·transaction/savepoint | Save/BulkUpdate expression, subquery 기반 쓰기, 넓어진 key/관계/잠금 target·batch와 실패/retry의 조합 | Q3, S1 |
| C08 | Migration | writer/autodetector·graph/history/state, 제한된 schema 변경·reverse·revision fence·재개 | 일반 schema/custom/data operation, populated 변경·복구/adoption, fake/squash/optimizer, multi-DB와 introspection | M1, M2 |
| C09 | Database | SQLite/PostgreSQL의 현재 compiler/editor·연결/transaction·schema 검사 | MySQL/MariaDB/Oracle, introspection, 확장 constraints/indexes, 새 기능별 capability와 각 DB의 실제 검증 | M2, D1, D2, S1, S2, O1 |
| C10 | Validation/Form | 공통 validation, Form/ModelForm, cleaned/errors/widgets, choices, formset/inline, 파일·cross-field의 현재 단면 | 지원 모델/관계 전반의 재사용 저장·검증, 확장점·widget/오류 흐름, 새 타입/지역화/identity/GIS 통합과 범용성 검증 | A1, F1, I1, L1, S1, S2, G1 |
| C11 | Template | escaping/trusted value, if/for, inheritance/include, default/length/lower 등 닫힌 필터 | 나머지 선택된 tag/filter와 안전한 Go 확장점, 지역화, loader/cache·오류·자원 한도의 조합 | T1, L1 |
| C12 | Admin | metadata 등록, list/search/filter/order/page, CRUD/actions/history, 권한·관계/inline·파일 UI의 현재 단면 | 기본 CRUD 조립, 넓은 관계 UI/선택·안전한 확장점, 새 모델/locale/custom user/GIS 연결과 범용성 검증 | A1, F1, I1, L1, S1, S2, G1 |
| C13 | Identity/security | User/Group/Permission, password/session/CSRF, login/logout/reset/revocation·audit, injected Bearer | custom user, token 발급/갱신/JWT/OAuth/OIDC, 넓은 사용자 수명·policy/key 분배, security header·운영 통합 | I1, I2, S1, C2, O1 |
| C14 | API | serializer/model serializer, typed 입력/출력/endpoint, JSON·filter/page, Session/Bearer, OpenAPI·고정 Go client | 일반 viewset/router/CRUD 조립, 출력 optional omission, browsable API/docs UI, 배포형·다언어 client, throttling/versioning, 새 인증 profile | A1, A2, I2, L1, O1 |
| C15 | Realtime | 유한 HTTP streaming은 존재; Realtime 완료 증거 없음 | WebSocket/SSE, consumer, groups/channel layer, presence, backpressure/cancel, 다중 process 전달 | C2, R1, R2, O1 |
| C16 | GIS | 현재 일반 scalar/관계 기반; GIS 완료 증거 없음 | spatial fields/lookups/geometry, capability 협상, projection/distance, GeoJSON·Admin/map과 migration | S2, G1 |
| C17 | i18n/지역화 | 고정 IANA timezone·지역 달력 계산 | gettext/catalog·locale, 표시/입력 format, 번역 validation/template/Admin, 사용자별 시간대와 naive/wall-time 입력 정책 | T1, L1, I1 |
| C18 | Contrib | storage·mail, query/API pagination과 일부 모델 serialization | content types·generic relation, sites/redirects/messages/cache, storage/mail 잔여, 공통 pagination, syndication/sitemaps, serialization/fixtures | K1, K2, K3, A2, O1 |
| C19 | Django 데이터 이행 | 일부 이름·password hash·operator 이전의 명시된 단면 | table/column/relation mapping, auth/content types, inspectdb, 기존 schema/data의 전체 이행·검증·복구 도구 | M2, I1, K1, S2, G1, X1 |
| C20 | 품질/운영 | differential/invariant/race·DB/process/security/failure 및 현재 플랫폼 CI | 확장 기능의 contract/property/fuzz, profiling/benchmark·관측·재현 가능한 packaging, 새 환경·운영 검증과 전체 조합 감사 | 모든 묶음, O1, Z1 |

### 넓은 기능 이름을 닫힌 요구 목록으로 바꾸는 규칙

`나머지 함수`, `custom`, `확장`, `provider`, `다언어`는 구체 목록이 없으면 완료 판정할 수 없다.
각 묶음 착수 시 현재 코드·고정 비교 기준·이 표를 대조해 하위 요구 ID와 수용 시나리오를 work에 확정한다.
예를 들어 Q2는 text/numeric/형 변환/NULL/JSON/통계 함수군, A2는 client 언어·도구·배포 대상,
K2는 provider·backend별 기능, T1은 tag/filter/확장 계약을 각각 열거해야 한다.

이 준비는 필요한 묶음에서 수행한다. 모든 upstream 테스트 번역이나 전체 repository 재조사를 B0의 선행 작업으로 만들지 않는다.
목록을 정할 때 원래 카탈로그 항목을 삭제하거나 `미지원`으로 영구 제외하지 않는다.
하위 요구의 구현이 후속 묶음으로 넘어가면 기존 묶음의 미완료 부분과 받는 ID를 함께 남긴다.
구체 목록이 비어 있거나 소유자가 없는 항목이 하나라도 있으면 해당 카탈로그 행과 Z1은 완료할 수 없다.

## 작업 묶음별 의존성과 완료 조건

`필수 선행`은 제품 구현에 필요한 기반이다. 각 묶음의 범위 조사·공개 API 설계·환경 확인은 그 전에 할 수 있다.
아래의 완료 조건에는 뒤의 공통 품질 기준도 항상 포함된다.

### 재개와 첫 영역 전환

| ID | 작업과 필수 선행 | 구현할 결과 | 완료를 증명할 조건 |
|---|---|---|---|
| B0 | 기준 source·작업 복구 / 없음 | 최신 통합 기준을 명시하고 GDJ-0134 사본·보존 증거를 확인한다. 활성 branch/work, Q1 요구, 다음 A1 흐름, 필요한 CI/DB 환경을 연결한다. | 복구된 파일은 출처·diff를 확인하고 현재 source에서 재검증한다. 복구 불가 부분은 다시 구현할 목록으로 남긴다. 예전 compile이나 PR 상태를 새 PASS로 옮기지 않는다. 다른 작업의 변경은 보존한다. |
| Q1 | named annotation / B0 | scalar·관계 aggregate의 이름/alias, 의존·재정의, source/result scope, 선택/필터/정렬/집계와 typed/dynamic·생성 API를 공통 AST에 연결한다. | 정의 전후 filter/JOIN·재정의·forward/cycle/이름 충돌·NULL/type/budget를 검증한다. 모델 결과와 계산 결과의 권한을 구분한다. 두 구조의 생성 소비자, cross-app 관계, Helpdesk HTML/API/client·브라우저, 양 DB·독립 비교·필수 원격 실행을 완료하고 아래 전환 기준을 모두 충족한다. |
| A1 | 모델 중심 기본 조립 / B0; named 결과 연결은 Q1 | 같은 IR과 명시적 policy에서 Form/Admin/API의 일반 생성·조회·수정·삭제 및 업무 명령을 구성한다. 공통 준비·기본 저장·관계 선택·computed read-only 출력의 반복 연결을 줄인다. | Article형 단일 모델과 Helpdesk형 관계 모델 및 외부 소비자가 같은 공개 경계를 사용한다. 새 필드·권한·응답 변경에 같은 의미를 중복 선언하지 않는다. allowlist·PATCH/presence·현재 객체 권한·revision/audit·transaction·오류 진단을 유지하고 전후 조립/변경 비용을 기록한다. |
| F1 | Form/Admin 일반화 / A1 | 지원된 필드와 FK/OneToOne/M2M·formset/inline·파일을 공통 typed 준비/저장으로 묶는다. 관계 선택·검색/페이지·read-only·action/command와 widget/validation 확장 경계를 완성한다. | 기존 scalar/collection/file 저장의 원자성·부분 게시/rollback을 유지한다. 현재 권한과 서버 부모/PK/choice를 재검사한다. 구조가 다른 두 앱과 cross-app에서 입력 보존·복합 오류·위조/경합·브라우저 동작을 검증한다. S1/S2/G1의 새 타입 연결은 각 해당 묶음도 책임진다. |
| C1 | Project/Core와 새 앱 시작 / B0, A1 | settings/environment·app lifecycle을 이용한 project/app scaffold, custom management command, 설치 버전 협상·호환성 검사, 기본 logging·signal/event, generate/migrate/초기 사용자/runserver·reload 흐름을 연결한다. | 빈 디렉터리에서 공식 기본 경로로 앱을 만들고 필드 변경·기존 데이터 보존·재시작·실패 진단까지 외부 소비자가 수행한다. 잘못된 도구/runtime/생성 ABI 조합은 시작 전에 진단한다. 생성 실패는 last-good를 보존하고 reload는 실패한 후보를 게시하지 않는다. event의 순서·오류·취소·등록 해제와 process 종료/자손 회수를 검증하며 durable 전달은 R2와 구분한다. |
| I1 | custom user와 계정 수명 / A1, F1 | 기본 User와 custom user가 공통 credential/session·permission·관리 Form/API·login/reset 경계를 사용하도록 결합한다. 새 모델로의 이행과 설정 검증을 제공한다. | 별도 custom 모델의 생성·권한 변경·비활성화·삭제·password/reset·session 폐기·재시작을 실제 DB/HTTP로 검증한다. 모델 교체나 key/username 정책이 다른 계정의 권한·session을 승계하지 않는다. S1의 key 확장은 같은 계정 계약으로 회귀 검증한다. |

### 모델 성장과 사용자 표현

| ID | 작업과 필수 선행 | 구현할 결과 | 완료를 증명할 조건 |
|---|---|---|---|
| M1 | schema/data migration / B0 | 현재 bounded writer를 일반적인 앱 성장으로 확장하고 custom/data operation·historical model 접근·populated schema 변경·역방향/비가역 진단을 연결한다. | 채운 DB에서 필드/관계/제약 변경과 데이터 변환을 실행한다. plan과 실행의 fresh history·revision fence·durable prefix를 유지한다. 중간 실패·취소·재시작·재실행·reverse와 불가역 거부에서 행·catalog·recorder를 대조한다. |
| A2 | API 일반화와 client 도구 / A1, C1 | typed model/viewset/router·CRUD, 업무 endpoint, optional 출력, filter/page·throttling/versioning·browsable API/docs UI와 배포형/다언어 client를 연결한다. | 실제 HTTP·OpenAPI·생성 client의 입력/출력/오류·auth·body/header·null/omitted/exact numeric 의미가 일치한다. 최소 Go와 브라우저 TypeScript 흐름을 포함하고 확정한 언어·도구 목록 전체를 검증한다. UI에도 같은 권한/CSRF를 적용하고 version·rate 제한의 동시 요청/우회 경로를 검증한다. 다른 언어 요구를 목록 없이 완료 처리하지 않는다. |
| T1 | Template의 기능·확장 / F1 | 현재 inheritance/include와 닫힌 값 모델 위에 고정 기준의 남은 tag/filter, 명시적 Go 확장점·loader/cache·안전한 오류 위치를 제공한다. | 공개 요구 목록의 정상·실패·조합을 실제 렌더링에서 확인한다. escaping/trusted 경계·순환 include/상속·큰 입력·취소·동시 렌더링을 유지한다. 임의 Python callable/attribute 실행은 추가하지 않는다. |
| L1 | 번역·locale·표시 시간 / T1, C1 | gettext/catalog 생성·갱신, 요청별 locale, 날짜/숫자/시간 형식, validation/template/Admin 번역과 명시적 사용자 시간대·naive/wall-time 입력을 연결한다. | 두 locale와 시간대를 가진 동시 요청에서 설정이 섞이지 않는다. plural/context/fallback·missing catalog·format/parse round trip·DST gap/overlap·브라우저 표시를 검증한다. 저장된 UTC와 API canonical wire를 표시 정책이 암묵적으로 바꾸지 않는다. IANA 지원만으로 지역화 완료를 선언하지 않는다. |

### 공통 기능과 Realtime의 첫 사용

| ID | 작업과 필수 선행 | 구현할 결과 | 완료를 증명할 조건 |
|---|---|---|---|
| Q2 | 일반 함수·lookup·조회 폭 / Q1 | text/numeric/형 변환/NULL·JSON·통계 함수군, tuple membership·집합 연산·collection projection/정렬과 남은 lookup/group 조합을 공통 AST로 넓힌다. | 함수·연산 목록별 타입/NULL/cardinality·원본 검사·source 소유권을 양 DB·typed/dynamic에서 대조한다. Named 모델 결과와 eager/prefetch의 조합도 Q1에서 남긴 미완료 조합으로 이어 받아 검증한다. 문자열 SQL 우회 없이 값/관계/그룹/후속 결과에 조합되며 지원하지 않는 native 조합은 capability로 진단한다. 같은 값의 HTML/API 소비자와 저장 보존을 확인한다. |
| S1 | 모델·관계 의미 확장 / M1, Q2 | custom field/options/default/validator, 모델 method·상속 요구에 대응하는 Go 구성, 추가 key/관계 target·relation-as-PK, conditional/expression constraint/index와 남은 cyclic graph를 연결한다. | 선언→IR→생성→migration→ORM→Form/Admin/API가 같은 의미를 사용한다. populated schema·원본 metadata·constraint ownership·cross-app/cycle·삭제/할당·기존 데이터/sequence를 실제 DB에서 검증한다. 새 field/key는 외부 consumer의 compile 및 runtime, 인증·관계 입력의 부정 대조를 갖춘다. |
| I2 | token과 OAuth/OIDC / I1, C1 | token 발급/refresh/revocation·JWT 검증·key rotation, OAuth/OIDC 계정 연결·callback·로그아웃과 API profile, server-owned token을 사용하는 production BFF 구성을 제공한다. | 선택한 공식 protocol/profile의 실제 독립 provider와 상호 운용한다. issuer/audience·redirect/state/nonce·PKCE·만료/재사용·동시 refresh·키 교체·계정 연결/해제·현재 권한을 검증한다. BFF의 cookie/CSRF·server-side token 수명과 logout/revocation을 검증한다. secret·token이 로그/진단/UI에 노출되지 않으며 Session과 Bearer의 경계를 유지한다. |
| C2 | Web의 개발·운영 경계 / C1 | namespace/reverse·typed converter 확장, static/media pipeline, HTTP 표현·보안 header·middleware·관측과 장기 연결 준비를 공통 서버에 연결한다. | 경로 충돌/encoding·HEAD/conditional/range·캐시·인가된 media·업로드 한도·클라이언트 종료·graceful shutdown을 실제 HTTP/process에서 확인한다. static 공개 경로와 권한 있는 media의 노출 정책을 검증한다. Realtime은 별도 연결 수명 계약을 따른다. |
| K1 | content types·공통 contrib / S1, C2 | content types/generic relation, sites/redirects/messages, 공통 pagination과 cache API/선택한 backend를 연결한다. | 모델 식별·역방향·migration/삭제·권한 범위가 유지된다. redirect/open-redirect, message 수명, cache key/tenant·직렬화·TTL·동시성·invalidation·실패를 실제 소비자로 검증한다. 공유 cache는 stale 권한을 승인하는 근거가 되지 않는다. |
| R1 | 단일 서버 Realtime / C2 | WebSocket/SSE·consumer API·연결별 context/자원 소유권, 인증/인가·취소·backpressure와 재접속 계약을 구현한다. | 실제 브라우저/독립 client의 양방향 WS와 SSE 수신, slow reader·burst·연결 단절·권한 폐기·서버 종료를 검증한다. 버퍼/메모리·goroutine/FD가 회수되며 HTTP 요청 종료와 연결 수명을 혼동하지 않는다. 다중 process 전달 완료는 R2에서 별도로 증명한다. |

### Query·DB·외부 서비스의 확장

| ID | 작업과 필수 선행 | 구현할 결과 | 완료를 증명할 조건 |
|---|---|---|---|
| Q3 | subquery/window와 쓰기 표현식 / Q2, S1 | 상관 참조·EXISTS/scalar/IN subquery, window partition/order/frame·순위/집계, Save/BulkUpdate expression·subquery 쓰기와 남은 잠금 조합을 연결한다. Q1에서 거부하는 named 표현식의 QuerySet UPDATE·Save/BulkUpdate도 포함한다. 백그라운드 평가의 별도 context·cache·결과 소유권도 확정한다. | source/result/correlation 권한·중복/NULL/empty·frame 경계·순서·집계 단계·실제 batch/경합을 대조한다. 같은 모델/다른 모델의 잘못된 참조를 거부하고 원자성·native 오류·savepoint/cache 수명을 보존한다. 요청 종료 후 평가와 취소가 다른 소비자의 cache/session을 훼손하지 않는다. 독립 생성 소비자와 실제 업무 조회/쓰기를 양 DB에서 확인한다. |
| M2 | migration 도구·introspection·multi-DB / M1, S1 | schema introspection/inspectdb, adoption·repair/crash reconciliation, fake/squash/optimizer, DB alias/router와 DB별 graph/history를 제공한다. | 읽기 introspection은 DB를 바꾸지 않고 선언으로 round trip한다. 채운 DB의 fake/adopt는 물리 schema를 검증한다. optimizer/squash 전후 schema/data가 같고 잘못된 history·drift·응답 유실·재개를 검증한다. DB별 라우팅/권한/transaction·금지된 cross-DB 관계를 명시하며 분산 원자성을 암묵적으로 약속하지 않는다. |
| D1 | MySQL와 MariaDB / M2, Q3 | 각각의 driver·capability/compiler/editor·introspection과 전체 공통 모델/ORM/migration/상위 소비자를 연결한다. | MySQL와 MariaDB를 별개 native 환경으로 실행한다. collation·정밀도/시간·JSON·DDL commit·잠금/deadlock·오류/취소·history/복구를 검증하고 모든 적용 가능한 공통 계약과 앱 흐름을 확인한다. 한 제품의 성공을 다른 제품의 PASS로 옮기지 않는다. |
| D2 | Oracle / M2, Q3 | Oracle driver·capability/compiler/editor·introspection, 값/식별자·반환값·migration·상위 소비자를 연결한다. | 실제 Oracle 환경에서 NULL/빈 문자열·숫자/시간·identity·bind/returning·DDL/transaction·lock/cancel·권한과 복구를 검증한다. 실행 가능한 환경·배포 의존성·라이선스 확인을 준비 작업으로 기록하며 mock/compile로 완료하지 않는다. |
| S2 | backend 특수 필드 / M2, Q2, D1, D2 | array/range/generated field의 IR·codec·query·constraint/index·migration·Form/Admin/API와 spatial 확장점을 제공한다. | 필드×backend별 native 지원/제한을 먼저 열거한다. 지원 좌표에서 round trip·NULL/empty/bounds·generated read-only·재계산·기존 데이터 변경을 실제 검사한다. 선언만 있고 compiler/저장/입출력이 없는 필드는 완료가 아니다. D1/D2의 해당 native 좌표도 확인한다. |
| K2 | Storage/Mail 잔여 / C1, F1, C2 | 실제 AWS/S3 운영 경로·추가 storage provider, multipart·회수/reconciliation, SMTP 외 선택한 mail backend/provider와 운영 설정을 연결한다. | provider 목록별 native 환경의 게시/미게시/unknown·중단/재시도·부분 업로드 정리·DB rollback 뒤 참조를 확인한다. mail은 접수/거절/unknown·인증/암호화·timeout·중복 가능성을 구분한다. MinIO나 loopback 성공을 AWS/운영 provider 검증으로 전이하지 않는다. |
| K3 | 직렬화·fixtures·배포 콘텐츠 / K1, T1, M1 | 모델 serialization/fixtures·관계 식별과 순환 load/dump, syndication/sitemaps와 sites/pagination 연결을 구현한다. | exact 숫자/날짜/bytes·NULL·관계·natural key 등 확정한 형식의 독립 round trip을 검증한다. 잘못된 fixture·부분 load·권한·secret 제외·원자성/재개를 확인한다. feed/sitemap은 escaping·URL/origin·페이지/증분·캐시 의미를 실제 HTTP/독립 parser로 검사한다. |

### 운영·복합 기능과 전체 완료

| ID | 작업과 필수 선행 | 구현할 결과 | 완료를 증명할 조건 |
|---|---|---|---|
| O1 | 운영·관측·재현 가능한 배포 / C1, C2, I2, M2 | 설정/secret·logging/tracing/metrics·health, migrate/serve/종료, policy/key 분배, backup/restore·quarantine/recovery 안내와 packaging을 연결한다. | 선언한 운영 topology에서 실제 시작/중지·장애·복원·키 교체·다중 runtime·외부 writer 충돌을 검증한다. durable outcome 확인 전 자동 재시도를 하지 않는다. 고정 source/dependency의 재현 가능한 산출물·설치/외부 빌드, workload별 latency/allocation/RSS/query 수의 측정 기준과 회귀 예산을 갖춘다. |
| R2 | 다중 process Realtime / R1, K1, O1 | groups/channel layer·presence와 process 사이 전달, 재연결·실패/중복/순서/만료·수신 권한 계약을 연결한다. | 독립 process와 실제 channel backend에서 group 입퇴장·presence 만료·장애/재시작·느린 수신자·재전달을 검증한다. 전달 보장과 손실/중복 조건을 명시하고 권한 폐기·tenant 격리를 유지한다. in-memory 성공만으로 다중 process 기능을 완료하지 않는다. |
| G1 | GIS 전체 흐름 / S2, Q2, F1, A2 | spatial field/geometry·lookups·projection/distance, backend 협상·migration, GeoJSON/Form/Admin/map을 연결한다. | 선언한 spatial backend/extension의 실제 native 결과와 독립 geometry 기준을 대조한다. SRID·축/단위·유효하지 않은 도형·empty/NULL·index·경계/대용량·취소를 검사한다. 지도 표시와 입력의 권한·오류·저장 일치까지 검증하고 backend별 미실행을 남긴 채 GIS 완료로 표시하지 않는다. |
| X1 | Django 데이터 이행 / M2, I1, K1, D1, D2, S2, G1 | 기존 table/column/relation·auth/content types·password hash와 추가 모델 타입을 introspection하여 GoDj 선언/이력/데이터로 이행하는 도구와 안내를 제공한다. | 고정 Django가 만든 채운 DB에서 이름/key/sequence·관계·권한/hash·값을 대조한다. inspect/plan은 읽기 전용이며 실제 이행·실패/재개·복원·전환 뒤 Form/Admin/API/로그인이 동작한다. Python migration 실행이나 서드파티 앱 소스 호환으로 범위를 바꾸지 않는다. |
| Z1 | 전체 요구·환경 대조 / 나머지 모든 묶음 | 카탈로그 20개 영역과 모든 하위 요구, 지원 backend/platform/provider·상위 소비자 조합을 같은 통합 기준에서 대조한다. | 열린 필수 요구·근거 없는 완료·필수 환경 누락이 0이다. 실제 공개 경로로 새 앱 시작→모델 성장→권한 있는 HTML/API→client→데이터 이행/운영 복구와 Realtime/GIS/locale 흐름을 확인한다. 범용성·성능·실패·출처/라이선스·packaging 근거가 있고 Matrix/Evidence/CURRENT가 일치해야 전체 목표 완료를 검토한다. |

## ORM에서 다른 영역으로 넘어가는 기준

이 절의 G-ORM은 **주 작업의 전환 조건**이다. ORM 전체 완료 조건은 Q1/Q2/Q3/S1/S2/M1/M2 및 관련 backend 작업이
모두 끝나고 해당 카탈로그 행이 닫힐 때 충족된다. 둘을 혼동하지 않는다.

G-ORM은 다음 여섯 조건을 모두 요구한다.

| 기준 | 필요한 증거 |
|---|---|
| H1. 현재 기능의 완결 | Q1의 scalar와 aggregate, annotation과 alias, 의존/재정의·JOIN 순서·결과 반환까지 구현·실행. 이름 저장소와 compile만으로 통과하지 않음 |
| H2. 공개 경계의 일관성 | typed/dynamic가 같은 AST와 source/type/NULL 의미를 사용. 생성 facade와 외부 소비자가 private SQL·가짜 field metadata·모델 쓰기 권한 우회 없이 사용 |
| H3. 실제 소비자와 일반성 | Helpdesk의 computed 결과·필터/정렬/DB 페이지·HTML/API/client와 서로 다른 구조의 생성 앱, 필요한 cross-app 흐름이 같은 기능을 사용 |
| H4. 안전성과 실패 | 권한·현재 scope·원 데이터/관계/audit 보존, 잘못된 참조/타입·재정의·NULL·자원 한도·취소·rollback/cache/동시 분기의 부정 대조 |
| H5. source에 결합된 실행 | 양 DB·독립 기준·실제 소비자·생성 drift·관련 race/process와 현재 원격 CI의 필수 실행/원본을 대조. 다른 source 성공·skip·누락으로 대신하지 않음 |
| H6. 다음 소비자의 준비 | A1의 단일 모델 CRUD, 관계 모델 CRUD, 계산 결과 목록 흐름에 필요한 기존 공개 기능과 policy를 확인. 실제로 막히는 결함만 재현·담당·수정 범위로 등록하고 해소 |

H1~H6을 충족하면 CURRENT의 다음 주 작업은 A1이다. 다음을 추가 선행 조건으로 끼워 넣지 않는다.

- 함수 전체, subquery/window, 모든 JSON/collection 조합의 완성
- 모든 PK/관계/constraint 형태, 모든 migration 도구, 모든 DB의 완성
- Realtime/GIS에 언젠가 필요할 수 있는 가정상의 ORM 기능
- 새 아키텍처 조사, 내부 모양 통일, 테스트 이름·파일 수 보존

A1 이후 실제 결함이 나타나면 소비자·재현·영향 기능·필요한 검증을 적고 그 결함을 해결한다.
이미 정상인 소비자가 요구하지 않는 인접 기능까지 함께 확대하지 않는다. 해결 후 A1 또는 원래 진행하던
상위 묶음으로 돌아간다. 새 ORM 요구는 Q2/Q3/S1/S2 중 담당을 정해 추적한다.
이 규칙은 Form/Admin/API의 기본 연결을 위해 ORM 전체를 선행 구현하는 반복을 막는다.

## 모든 묶음에 적용할 품질·완료 기준

다음 조건을 빠뜨린 작업은 구현 중 또는 검증 대기로 남긴다.

1. **요구와 설계:** 기능 목록·공개 동작·실패/소유권·소비자·필수 환경·완료 증거를 착수 시 적는다.
   Schema IR 단일 원본, codegen/generic/runtime metadata와 DB capability의 책임을 유지한다.
2. **기능의 전체 연결:** 해당 기능의 제품·생성물·기본 사용 경로·구조가 다른 소비자·문서를 함께 연결한다.
   기존 모델/관계에 영향을 주면 migration과 기존 데이터 처리도 포함한다.
3. **보안과 무결성:** allowlist·생략/null/zero·인증/CSRF·목록/객체/관계 권한, revision·source binding·audit,
   실제 DB 제약·transaction/savepoint·rollback·durable/unknown outcome을 유지한다.
4. **Go 실행 의미:** context/error 전파, 공유/복사/cache·callback/stream 수명, 자원 예산·정리·동시성을 검증한다.
   일반 오류를 panic·암묵 재시도·결과 누락으로 바꾸지 않는다.
5. **독립 비교:** 해당 계약의 고정 Django/DRF 또는 native/공식 protocol 관측과 대조한다.
   Go 고유 의미는 독립 native/property/fuzz 등 적합한 방법으로 검증한다. 같은 구현으로 expected/actual을 만들지 않는다.
6. **생성과 외부 사용:** 결정적 재생성·drift·실패 시 last-good 보존, 공개 API의 정상/오용 compile 및 runtime,
   외부 module·필요한 cross-app·배포 산출물을 확인한다.
7. **실행 환경:** [TESTING](TESTING.md)의 실제 DB·platform/mode·process·browser·service owner를 따른다.
   새 backend/provider/protocol은 필요한 native 환경과 negative control을 추가한다.
   필수 실행 누락·skip·잘린 증거·mock/compile만 있는 상태는 해당 환경의 PASS가 아니다.
8. **사용성과 운영:** 기본 구성으로 실제 작업이 끝나고 잘못된 입력/권한/인프라 실패를 진단할 수 있어야 한다.
   조립·필드 변경·정책 변경의 비용을 같은 조건으로 비교한다. 성능·개발 시간 수치는 직접 측정한 경우에만 쓴다.
9. **기록:** 설계 채택, 코드 구현, source/환경별 검증을 구분해 Matrix/Evidence/CURRENT에 반영한다.
   기능과 무관한 옛 증거·새 문서의 존재를 제품 완료 증거로 사용하지 않는다.

로컬은 포맷·필요한 최소 compile·실패 재현에 사용한다. 완성된 제품 변경 묶음을 push하고 원격 CI에서 검증한다.
PR 생성·갱신·재개와 main push의 전체 CI, 새 source에 의한 이전 실행 취소 정책은 유지한다.
문서-only 계획 변경은 링크·상태·diff·계획의 누락/의존성 검사를 적용한다. 제품 전체를 로컬에서 다시 실행하지 않는다.

## 의존성·환경과 미결정 사항 관리

| 입력 또는 결정 | 담당 | 필요한 시점·해소 기준 |
|---|---|---|
| Q-010 project scaffold·설치 버전 협상/업그레이드 | C1, O1 | 도구/runtime/생성 ABI의 호환성 검사와 향후 지원 릴리스의 upgrade 정책을 구분. 출시 결정은 별도이며 현재 개발을 기다리게 하지 않음 |
| Q-011 expression/평가·Q-013 relation·Q-017 generated 사용성 | Q1, Q2, Q3, S1, A1 | 해당 묶음의 실제 소비자와 하위 요구 목록으로 범위를 고정. 모든 질문을 Q1의 선행 조건으로 합치지 않음 |
| Q-012 custom/data·repair | M1, M2 | native DDL/transaction 특성과 historical 상태·재개/복구 수용 사례 확정 |
| Q-016 API·client 언어/배포·routing | A2, C2 | 지원 언어·generator·문서 UI·version/throttle·경로 확장 목록과 각 client의 실제 실행 기준 확정 |
| Q-019 quarantine·Q-020 topology/policy/key | O1, R2 | 실제 장애 후 durable outcome 확인·복구 절차와 topology별 보장·권한 모델 검증 |
| Q-021 token/provider·Q-022 사용자 수명 | I1, I2 | custom 모델·issuer/provider·계정 연결/폐기·session 관계를 확정하고 독립 상호 운용 환경 준비 |
| DB 버전/실행 환경·Oracle 의존성 | D1, D2, G1 | 구현 전에 version·권한·driver/extension·CI 제공 방법·필요한 라이선스를 확인. 환경 부재는 blocker이며 완료 아님 |
| AWS/추가 storage·mail·channel provider | K2, R2 | 필요한 provider 목록·test account/endpoint·CI secret 소유자를 확정. 실제 값은 문서/로그에 기록하지 않음 |
| locale/catalog·GIS 지원 좌표 | L1, G1 | locale/format과 spatial backend/extension 목록·공식 기준·native 검증 대상을 확정 |

위 질문의 해결은 해당 묶음의 산출물이다. 답이 없다는 이유로 전체 카탈로그를 막거나 그 항목을 범위에서 빼지 않는다.
환경이나 사용자가 결정해야 할 입력이 실제로 필요할 때만 해당 결정을 요청하며, 실행 가능한 다른 묶음은 계속한다.

## 실행 상태·일정과 계획 갱신

- CURRENT에는 현재/다음/막힌 일과 이 계획의 작업 ID·근거 링크만 둔다. 전체 잔여 목록을 복사하지 않는다.
- 실제 착수한 work에는 하위 요구, 담당 코드/소비자, 선행 조건, 완료 증거를 기록한다.
  계획 수립 단계에서 모든 하위 API 시그니처·파일 개수·테스트 개수를 확정하지 않는다.
- 한 묶음을 닫을 때 카탈로그 행에 남은 하위 요구가 있는지 확인하고 다음 ID를 선택한다.
  새 요구·실제 blocker·독립 실행 가능성이 발견되면 의존성과 순서를 이 문서에서 고친다.
- 수용 기준을 줄여 일정을 맞추지 않는다. 계획 변경 시 이전 요구가 어느 ID로 이동했는지 남긴다.
  묶음을 나눠도 부모 요구의 완료 조건은 그대로 유지한다.
- 달력상의 완료일은 현재 확정하지 않는다. 재개 시각·실제 작업 가능 시간·native 환경 준비·측정된 구현/CI 재작업
  시간이 없으므로 날짜를 약속할 근거가 부족하다. 위 단계는 착수 순서와 전환 시점을 명시한 실행 계획이다.
- 착수한 묶음은 요구 분해와 환경 확인 뒤 예상 범위·불확실성을 기록하고, 실제 완료/재작업 시간을 얻으면
  가까운 다음 묶음의 예상 범위를 갱신한다. CI 대기 시간과 개발 작업 시간을 구분하고 기존 작업 수로 전체 기간을 단순 환산하지 않는다.

## 전체 목표의 최종 완료 판정

Z1에서는 각 카탈로그 행마다 요구 ID → 구현 위치 → 공개 소비자 → 관련 environment → 실제 증거를 추적한다.
20개 행을 표에 적었다는 사실이나 계획상의 묶음 수는 진행률이 아니다.
필수 기능·하위 목록·native 환경·실패/보존 조건 중 하나라도 미완료·미정·미검증이면 전체 목표는 계속 미완료다.

새 앱 시작, 기존 앱의 모델 성장, 권한 변경, HTML/API·client, migration/데이터 이행, 운영 복구,
locale·Realtime·GIS가 통합된 제품 경계에서 동작해야 한다. 기존 제한을 무조건 없애는 대신,
안전한 자원 한도와 실제 backend capability는 유지하면서 카탈로그의 기능을 충족했는지 확인한다.
출시일·외부 지원 정책·릴리스 결정은 이 개발 완료 판정과 별도로 사용자가 정한다.
