# 선택 계정과 catalog의 JSON 관리

`NewManagement(accounts, catalog, config)`는 생성 프로젝트의 실제 `AccountManagement[M]`와 `Catalog[G, P]`를
공통 JSON HTTP에 연결한다. 호스트가 모델마다 CRUD callback·SQL adapter·기본 User 사본을 만들지 않는다.
필드 노출과 저장 정책은 각 owner의 명시적 선언에서 가져오며 입력과 출력은 같은 Schema IR source를 유지한다.

```go
accounts, err := project.NewAccountManagement(sessionStore, verifier, authorizer, accountPolicy)
if err != nil { return err }
catalog, err := project.NewAccountCatalog(sessionStore, authorizer, catalogPolicy)
if err != nil { return err }
management, err := identityapi.NewManagement(accounts, catalog, identityapi.ManagementConfig{
    Namespace: "accounts",
    Authentication: jsonAuthentication,
})
if err != nil { return err }
// management.Routes()와 management.Middleware()를 같은 web.Config에 설치한다.
// management.OpenAPI()의 게시 URL과 접근 권한은 호스트가 정한다.
```

Config의 기본 Namespace는 선택 계정 모델의 app label, BasePath는 `/api/identity/`다. 별도 Namespace는 설치 앱에 맞게 지정하고
BasePath를 바꾸면 canonical static subtree를 사용한다. 생성자는 I/O·해싱·model validator를 실행하지 않는다.
선언에 실패하면 완성된 application을 게시하지 않는다. 같은 application과 owner는 동시 요청에 공유할 수 있지만,
요청·준비 입력·조회 결과와 현재 권한을 global cache에 보관하지 않는다.

## 경로와 입력

기본 prefix 아래 `accounts/`, `groups/`, `permissions/`에 같은 CRUD를 제공한다.

| 경로 | Method | 의미 |
|---|---|---|
| `accounts/` 등 | GET | `offset`(기본 0), `limit`(기본 20, 최대 100)의 현재 count와 scalar 목록 |
| `accounts/` 등 | POST | 명시한 실제 모델과 소속 생성 |
| `accounts/{id}/` 등 | GET | 선택 scalar·직접 소속의 상세 |
| `accounts/{id}/` 등 | PUT, PATCH | `If-Revision`에 묶인 수정 |
| `accounts/{id}/` 등 | DELETE | 전체 host 관계 효과·세션·감사를 포함한 삭제 |
| `accounts/{id}/history/` 등 | GET | 현재 domain의 감사 이력, `limit` 최대 100 |
| `accounts/{id}/password/` | POST | 별도의 비밀번호 교체/사용 불가 명령 |

`id` 경로는 0·음수를 포함한 canonical signed int64 decimal이다. `+1`, `01`, `-0`, overflow는 거부한다.
Router의 bounded string 경로에서 이 업무 문법을 검사하며 OpenAPI 설명에도 동일 제약을 둔다.
목록과 이력 외의 query, 알 수 없는/반복 query, 빈 `?`는 거부한다. 각 요청은 인증·CSRF·모델 권한을 먼저 검사하고,
쓰기에서는 경로/query와 revision을 body보다 먼저 확인한다.

모델 입력은 `{"fields": {...}}`다. 계정 생성은 credential 명령도 요구한다.

```json
{
  "fields": {"login": "member@example.test", "region": "KR"},
  "credential": {"password": "untrimmed password"}
}
```

예시의 `login`·`region` 대신 실제 선택 모델의 노출 이름을 사용한다. 모델에 `password`라는 프로필이 있으면
`fields.password`로 입력하며 `credential.password`와 구별한다. Credential 객체의 `password`는 필수이고,
명시적 null은 사용 불가 credential을 뜻한다. 비밀번호 명령 body는 `{"credential":{"password":...}}`만 받는다.
원문 비밀번호는 trim/NFKC하지 않으며 owner의 byte 한도·hasher·typed password policy를 그대로 적용한다.

JSON body는 64 KiB, 각 문자열은 UTF-8 4,096 bytes 이내다. 중첩 객체 전체가 한 parser 예산을 사용하고,
모든 child가 검증되기 전에는 setter를 실행하지 않는다. 선택 login/email은 `Binding.InputSpec`의 역할 정규화와
변환 전 원문 한도를 적용한다. 노출하지 않은 필드·principal identity·hash·revision·서버 값은 입력할 수 없다.

Blank/nullable scalar는 생략할 수 있고 문자열의 빈 값 정책은 IR Blank를 따른다. Full 입력은 IR default를 적용하며,
partial 입력은 생략 값을 만들지 않는다. PUT에서도 생략한 optional collection은 기존 소속을 유지하고 `[]`는 해제한다.
Default를 제출값처럼 재검증하거나 정규화하지 않는다. 생성 owner는 필수 login·추가 profile과 최종 전체 상태를 다시 확인한다.

## 권한과 결과

조회/이력은 view 또는 change와 모든 추가 권한을 요구한다. 계정 생성은 add와 change 및 모든 추가 권한을,
catalog 생성은 add와 모든 추가 권한을 요구한다. 수정·password·삭제는 각 operation과 모든 추가 권한을 요구한다.
`api.PolicyAuthentication`으로 한 번의 인증/CSRF 경계와 같은 OpenAPI 정책을 구성한다. 권한 선언의 canonical·중복·
합집합 한도는 공통 PermissionPolicy를 따른다. API는 Admin의 staff admission을 암묵적으로 요구하지 않는다.
최종 owner는 현재 actor·domain·target·권한·소속/유효 권한 한도를 native snapshot/transaction에서 다시 검사한다.

쓰기의 `If-Revision`은 1..9223372036854775806의 단일 canonical positive decimal이다. 누락은 428, 문법/한도 오류는
400, 현재 revision과 충돌하면 412다. 목록·wildcard·leading zero·반복 header도 거부한다. 이 조건은 ETag가 아니다.

생성은 201, 수정·password·삭제는 200으로 `{"id":...,"revision":...}`와 같은 `Revision` header를 반환한다.
삭제 revision은 삭제한 행의 마지막 관측 값이다. No-op 수정은 revision과 감사를 늘리지 않는다.
응답은 owner가 확정한 두 정수만 표현한다. Commit 뒤 model getter·DB 조회·권한 재검사나 request context 작업을
실행하지 않아 자기 비활성화·password 교체·세션 폐기 뒤에도 이미 확인된 성공을 유지한다. 모델 표현을 새로 읽지 않는다.

상세는 `id`, `revision`, `fields`이고 계정에는 같은 snapshot에서 관측한 `password_usable`도 있다.
목록은 `items`, `count`, `offset`, `limit`이며 각 item의 `fields`에는 선택 scalar만 둔다. Collection-only 읽기를
선택하면 목록의 `fields`는 빈 객체다. 실제 ModelView의 전체 source·key·선택 값 로드를 확인하며 hidden/zero/default
값으로 누락된 출력을 채우지 않는다. 저장된 principal identity·encoded password·세션 material은 보관/출력하지 않는다.
이력은 sequence·action·changed field names·time만 제공하고 actor principal ID나 이전/현재 field 값은 공개하지 않는다.

입력 진단은 JSON Pointer를 사용한다. 모델 오류는 `/fields/...`, credential 오류는 `/credential/...`다.
Owner의 `PasswordInputDiagnostics`는 직접 반환된 확정 password 거부만 식별하므로 실제 모델의 `password` 필드와
혼동하지 않는다. 출처는 `validation.RejectionOrigin`의 진단 metadata이고 실행 오류 원인으로 보관하지 않는다.
전체 host 관계의 PROTECT 거부는 동일한 collector 오류로 롤백이 확정된 뒤 400 `validation_error`와
`/fields/__all__`의 `protected` 진단으로 전달한다. 보호한 행의 값이나 DB 진단은 응답에 포함하지 않는다.
Wrapped/joined 오류·rollback/cleanup 실패는 일반 입력 오류로 낮추지 않는다.
Unknown outcome은 503 `outcome_unknown`이며 Retry-After나 자동 재시도가 없다. 클라이언트가 durable 상태를 확인해야 한다.
출력과 오류는 완전하게 준비하고 일부 JSON이나 잘린 진단을 성공으로 게시하지 않는다. Cache-Control은 no-store다.

기본/사용자 정의 일곱 생성 변형을 실제 TCP·SQLite/PostgreSQL 소비자에 연결했다. 공개 CLI·Article/scaffold는
선택 계정 runtime을 조립하며, 별도 ogen module의 Session/Bearer 관리 소비자도 같은 소유자·22개 경로와
새 receipt/선택 필드 계약을 사용한다. 구현·최소 compile과 실제 환경별 실행 범위는
[TEST_EVIDENCE](../../docs/status/TEST_EVIDENCE.md)를 따른다.
남은 고정 Directory/Manager host 교체, profile 관계/파일·채운 DB 전환·복구와 전체 환경 수용은 I1에 남아 있다.
