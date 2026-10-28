# ADR-0076 — Credential snapshot과 서버 세션의 결합

- 상태: accepted
- 날짜: 2026-09-27
- 관련 작업: [GDJ-0100](../../work/0100-multi-user-credential-and-session-lifecycle.md)

## 결정과 이유

다중 사용자·비밀번호 변경을 구현하려면 로그인에서 확인한 자격 증명과 다음 요청의 현재 자격 증명을 구분해야 한다.
사용자 ID만 담은 세션으로는 같은 사용자의 비밀번호 교체·재해싱을 판별할 수 없다.
`auth.CredentialAuthenticator`는 Authenticate와 Resolve에서 불변 `Credential`을 반환한다.
`Principal()`은 그 관찰에 속한 권한 snapshot이다. 권한과 비밀번호 상태를 각각 읽어 서로 다른 시점의 값을 조합하지 않는다.
Resolve는 현재 상태를 반환하며 이전 요청에서 허용한 immutable Principal을 소급 변경하지 않는다.

`Credential.SessionStamp()`는 버전 구분자·principal ID·이미 salt가 포함된 encoded password의 SHA-256이다.
ID와 encoded password는 NUL을 허용하지 않아 각 구성 요소를 NUL로 구분한다.
서버 세션의 `_godj_principal_id`와 `_godj_credential_stamp`를 함께 생성하거나 회전한다.
다음 요청은 한 번 Resolve한 credential의 ID·active 상태·stamp를 검증한다. 비교에는 constant-time equality를 쓴다.
Stamp는 password verifier나 bearer credential이 아니며 client cookie·응답·로그에 노출하지 않는다.
원문 비밀번호·encoded password는 세션에 저장하지 않는다. Credential의 기본 표현·JSON은 계속 비공개다.

비밀번호 교체와 재해싱은 stamp를 변경한다. 사용자 이름과 권한은 stamp에 포함하지 않는다.
현재 권한은 매 요청의 Principal에서 읽는다. 사용자·그룹·권한의 durable 저장은 [identity 앱](0077-reusable-app-models-and-host-relation-ownership.md)의
Directory가 한 DB snapshot으로 제공한다. 저장 인증과 기존 operator의 전환/관리는 아래처럼 구분한다.
누락·위조·stale stamp, 다른 ID, 비활성·없는 사용자는 서버 세션을 폐기하고 anonymous로 처리한다.
폐기 실패나 backend 오류는 익명 성공으로 숨기지 않는다. 이전 형식의 ID-only 세션을 자동 승격하지 않는다.

공통 인증기는 이미 검증한 credential을 반환하며 자동 재로그인하지 않는다. 저장 Identity composition은 아래의
명시적 LoginPersistence를 연결해 현재 credential/admission·세션·last_login을 같은 write fence에서 확정한다.
이 binding이 없는 memory/legacy/custom 인증은 다음 요청의 stamp 검사 경계를 사용한다.

## 기존 operator와 Django 기준

기존 operator의 policy CAS·전체 세션 폐기·다른 정책 runtime 거부는 유지한다.
Resolver는 coordinated read에서 읽고 검증한 현재 credential을 반환한다. 로그인은 startup verifier가 검증한 stamp와
현재 username/stamp도 일치해야 한다. 저장된 credential이 달라졌다면 과거 비밀번호의 검증 결과를 새 credential로 승격하지 않는다.
Startup password verifier의 교체는 여전히 명시적 reopen을 요구한다. 이 방어는 범용 password maintenance API나
비협력 writer 지원의 구현을 뜻하지 않는다.

고정 Django 6.1의 실제 User·session/auth middleware를 사용하는
[독립 runner](../../conformance/runners/django/credential_session_reference.py)가 양 DB에서 7개 흐름을 관찰한다.
비밀번호·재해싱·권한·username·비활성·누락/위조 session hash의 외부 인증 결과를 비교한다.
Django의 비활성 사용자 처리에는 기존 DB session 행이 남을 수 있다. GoDj는 기존 invalid-principal cleanup 정책대로 폐기한다.
HMAC/SECRET_KEY fallback을 포함한 Django session hash의 wire 형식이나 key rotation을 이 stamp와 동등하다고 주장하지 않는다.
이 차이와 지원 범위는 [DEV-0013](../DEVIATIONS.md#dev-0013--credential-session의-go-표현과-invalid-identity-정리)에 명시한다.
실제 구현·환경별 실행은 [Matrix](../status/IMPLEMENTATION_MATRIX.md)와 [Evidence](../status/TEST_EVIDENCE.md)가 소유한다.

## 저장 인증과 role admission

`auth.CredentialStore`는 종료된 읽기 범위에서 얻은 현재 immutable Credential을 제공한다.
`StoredAuthenticator`와 `identity.NewAuthenticator`는 password work를 DB snapshot 밖에서 수행한다.
Unknown·잘못된 username은 bounded dummy hash를 사용하고, 존재하는 inactive 사용자도 같은 검증 경계를 지난 뒤 거부한다.
저장 hash는 현재 hasher의 bounded work profile이어야 한다. 저장/설정 오류를 잘못된 비밀번호 성공/거부로 감추지 않는다.
Memory와 stored 인증기는 password 오류 정규화와 dummy profile 검사를 공유한다. 취소가 다른 hasher 오류와 결합됐어도
기본 진단에 비밀번호 material을 출력하지 않으며 errors.Is/As 원인은 보존한다.

검증 성공 뒤 같은 ID를 다시 읽어 active·username·stamp를 확인한다. 변경/삭제된 credential로 인증을 승격하지 않고,
일치할 때 그 두 번째 관찰의 권한·role을 반환한다. 검증 중 grant/role이 바뀌면 현재 값을 사용한다.
이후 다른 transaction이 commit하는 것을 무기한 봉쇄하거나 이미 진행 중인 요청을 소급 취소하는 보장은 아니다.

Principal은 active·staff·superuser를 구분한다. Inactive는 어떤 permission도 허용하지 않는다. Active superuser는
canonical permission을 암묵적으로 허용하며 `Permissions()`는 명시적 grant만 반환한다. 전역 permission catalog를 꾸며 내지 않는다.
Admin Site는 active staff를 요구하고 LoginStaff가 session/cookie 생성 전에 검사한다. Superuser나 별도 site grant는 staff를
대체하지 않는다. SiteConfig.AccessPermission은 선택적인 추가 gate이고, model 권한 및 설정한 Authorizer의 거부는 유지한다.
기존 DefaultAccessPermission이라는 기본 admission은 제거한다. 이 의미는 고정 Django의 active/staff/superuser 관찰을 따른다.
비정규 permission 문자열에는 [DEV-0014](../DEVIATIONS.md#dev-0014--superuser와-canonical-permission-경계)의 좁은 Go 정책을 적용한다.

기존 operator table은 role 필드를 저장하지 않는다. 그 policy에 새 role을 넣으면 명시적으로 거부하며 `WithPermissions`가
role을 조용히 지우지도 않는다. 기존 operator의 데이터 이전·durable 전환·소비자 재연결을 완료하기 전에는 핵심 라이브러리의
checkpoint를 전체 제품 완료로 표시하거나 변경 묶음을 게시하지 않는다.

## Identity 소유권의 명시적 전환

기존 system `0001_initial` bytes와 데이터 형식은 역사로 보존한다. 새 system `0002_identity_transition`은 identity `0001_initial`에
의존하고 전환 기록을 추가한다. `AdoptOperator`는 정확한 옛 policy를 검증한 뒤 같은 principal ID·username·encoded password·active와
직접 grant를 User로 옮긴다. Staff/superuser는 호출자가 명시한다. 이미 있는 Permission의 표시 이름과 관계없는 User는 덮어쓰지 않는다.
새 User/권한/기록 생성, 옛 row를 비활성·사용 불가능한 password·빈 grant·고정 전환 digest로 만드는 작업은 같은 coordinated transaction이다.
옛 row는 삭제하지 않는다. 새로운 구조 migration을 되돌려도 이전 binary의 provisioning/authentication이 옛 credential을 재활성화할 수 없다.
새 도메인의 `ProvisionIdentity`도 동일한 비활성 표시를 만들며 legacy operator를 묵시적으로 채택하지 않는다.

기존 session/audit 행은 byte 단위로 보존한다. Session stamp의 입력인 ID·encoded password가 같으므로 유효한 이전 세션은 계속 동작한다.
일반 AppendAudit의 capacity 정리가 기존 기록을 삭제할 수 있어 전환은 독립된 immutable receipt로 기록한다.
Receipt의 target user ID에는 삭제 cascade를 두지 않는다. 계정 편집·삭제 후에도 역사적 전환을 조회할 수 있다.
전환 instant는 UTC·microsecond이며, 이전 형식에는 가입 시각이 없어 이 시각을 초기 DateJoined로 사용한다.
Source fingerprint는 전체 source credential의 원본 bytes를 길이로 구분해 SHA-256으로 결합하며 공개 accessor/JSON을 제공하지 않는다.

실패·불확실한 commit에는 성공 receipt를 반환하지 않는다. 자동 재시도하지 않고 `InspectIdentityTransition`으로 저장 결과를 조회한다.
확정된 commit 뒤의 늦은 context 취소를 일반 rollback 실패로 바꾸지 않는다. 취소 중 rollback 종료를 확정할 수 없으면 원래 backend의
transaction-outcome-unknown 분류를 유지한다. 이미 완료한 전환을 다시 수행하거나 role을 다시 덧씌우지 않는다.

`OpenIdentity`는 migration과 전환 기록·비활성 표시·bounded session/audit를 native snapshot에서 확인하고 나서 저장 인증기를 구성한다.
기록·legacy credential·User·session·audit가 모두 없는 경우에만 정확한 credential-absent를 반환한다. 기존 operator가 있으면 명시적
전환을 요구하고, orphan state·잘못된 migration·corruption·읽기 종료 실패를 public-only 성공으로 바꾸지 않는다.
이 현재 startup 결정은 SYS-028의 GoDj 결정 관찰과 연결하며, legacy OpenExisting의 policy 검증은 SYS-027에 계속 남는다.

Credential·Account·ModelInput·선택 ModelView/Profile·인증기·Directory·전환 receipt는 내부 상태를 불투명한 pointer 뒤에 둔다. Formatter가 일반 진단 형식을 가리고,
Formatter보다 먼저 처리되는 잘못된 `%p`/`%w`의 reflection fallback도 비밀 필드에 도달하지 않는다.
사용자가 명시적으로 선택한 Profile JSON에는 공개 계정 정보만 포함되며 비밀번호 해시·stamp는 나오지 않는다.

Permission의 변경 충돌 검사를 위해 `godj_identity.0002_permission_revision`을 명시적 migration으로 추가한다.
기존 `0001_initial` bytes는 유지한다. 기존 Permission에 revision 1을 채우며 PK/code/name·연결된 그룹/사용자·호스트 참조와
credential·session·audit를 보존한다. 새 runtime의 `OpenIdentity`·전환 작업은 이 migration까지 정확히 적용돼야 시작한다.
Startup이 DDL이나 backfill을 대신하지 않는다. 이 schema 기반의 추가는 Group/Permission 관리 service의 완료와 구분한다.

## 관리자 비밀번호 교체

`identity.Manager.SetPassword`는 인증된 actor와 대상 User ID·expected revision을 받는다. 먼저 native snapshot에서
actor의 현재 active/직접·그룹/superuser 권한과 대상 revision을 확인한다. `godj_identity.change_user`와 Authorizer의
deny overlay를 모두 요구하며 요청에 실린 오래된 권한을 현재 권한 대신 쓰지 않는다. 해시는 읽기 종료 뒤 최대 한 번 생성한다.
같은 coordinated write fence 안에서 현재 인가·revision·이전 credential stamp를 다시 확인한 후 hash와 revision을 갱신한다.
Hash가 기존 credential과 같으면 새 stamp를 만들지 못하므로 거부한다. 같은 원문 비밀번호의 정상적인 새 salt는 허용한다.

대상 principal의 durable session만 폐기하고 값 없는 `password` 변경 audit를 같은 transaction에 쓴다. 세션은 전체 bounded
inventory를 확인한 뒤 256행 keyset batch로 읽으며 SELECT를 종료한 뒤 삭제한다. 뒤 batch의 손상·삭제 실패·감사 저장 실패에도
앞의 User/session 변경을 rollback한다. 다른 사용자와 anonymous session의 bytes는 보존한다.
`auth.SessionPrincipalIDKey`·`SessionCredentialStampKey`가 로그인과 maintenance의 같은 서버 저장 키를 소유한다.

관리 상태를 변경하는 협력 writer는 같은 fence와 revision 증가를 따른다. 관찰 필드 last_login은 같은 fence에서
해당 열만 갱신하며 관리 revision을 증가시키지 않는다. 저장 Identity 로그인은 현재 credential을 write 안에서 재검사한다.
임의 SQL이나 이미 승인된 요청까지 소급 통제하는 보장은 아니며 다음 요청의 resolver 검사도 유지한다.
자기 계정을 관리자 권한으로 교체해도 현재 세션을 특별히 유지하지 않는다. 자기 비밀번호 확인·현재 세션 회전을 제공하는
self-service와 reset service, 메일 요청·Form/API 소비자는 아래 별도 계약을 사용한다. 관리 Form/Admin/API는 이 정책을 사용한다.

실패나 unknown outcome에는 Profile을 게시하지 않고 자동 재시도하지 않는다. Unknown rollback/commit 분류는 일반 callback
오류보다 우선하며 `errors.Is/As`로 확인한다. 정상 commit 뒤 늦은 취소는 이미 확인된 성공을 뒤집지 않는다. 현재 revision과
audit 조회만으로 특정 요청의 성공을 증명한다고 주장하지 않으며, 이 서비스는 일반적인 command receipt/idempotency API를 제공하지 않는다.

## 사용자 생성·조회·편집·삭제

`identity.Manager`는 현재 저장 인가를 사용하는 `CreateUser`, `User`/`Users`, `UpdateUser`, `DeleteUser`를 제공한다.
생성에는 `add_user`와 `change_user`를 모두 요구한다. 고정 Django `UserAdmin`의 실제 add view가 두 권한을 요구하기 때문이다.
조회에는 `view_user` 또는 `change_user`, 편집에는 `change_user`, 삭제에는 `delete_user`가 필요하다.
각 permission의 Authorizer deny overlay도 적용한다. 이 service의 인가는 Admin의 active staff 진입 조건과 별개다.
호스트는 이미 인증한 actor를 전달하며 요청에 담긴 과거 권한 대신 현재 User·직접/그룹 grant·role을 같은 snapshot에서 확인한다.

`UserCreate`의 principal ID는 호스트가 선택하고 이후 불변이다. Password는 별도 인자로 받는다.
`UserPatch`는 공개 profile·role·그룹/직접 권한만 허용하며 hash·principal ID·revision·가입/최근 로그인 시각 setter를 제공하지 않는다.
입력과 반환 collection은 복사본이다. 관계 입력 생략은 유지, 명시적 빈 collection은 전체 해제다.
삭제 결과에는 ID·revision·삭제 건수만 포함한다. 삭제 권한만으로 전체 Profile을 공개하지 않는다.

Username의 NFKC와 email의 마지막 `@` 뒤 domain에 대한 full Unicode 소문자 변환은 고정 Django manager를 따른다.
정규화 전후 username에는 auth의 UTF-8·1,024-byte·NUL/바깥 공백 제한을 적용하고, 정규화 결과는 User의 Schema IR 256자 한도를 검사한다.
Byte envelope는 모든 UTF-8 폭의 256자를 운반하는 상한이며 모델의 글자 수 제한을 대신하지 않는다. 저장 profile 조회에도 IR 한도를 적용한다.
Email은 변환 전 잘못된 UTF-8·NUL·4,096-byte 초과를 거부하고, 최종 email 254자와 이름 각 150자 제한을 적용한다.
단순 `strings.ToLower`는 `İ` 확장과 문맥에 따른 Greek sigma가 달라 사용하지 않는다. 고정 Unicode 16의 full lowercase와 Final_Sigma를 사용한다.
이는 Django `UserCreationForm` 전체 호환 선언이 아니다. Form의 현재 지원 범위와 Unicode/credential의 계층별 경계는 아래에 명시한다.
대소문자를 무시하는 생성 중복 정책은 아래의 명시적 옵션으로 선택한다.

생성은 현재 인가·unique 값·선택한 관계·유효 권한 한도를 읽기 snapshot에서 먼저 검사한다.
읽기를 종료한 뒤 hash를 한 번 생성하고 coordinated relation transaction에서 전부 재확인한다.
편집은 expected revision을 요구하며 scalar·그룹/직접 grant의 원하는 전체 집합·revision·감사를 원자적으로 반영한다.
실제 변화가 없으면 revision과 감사를 늘리지 않는다. Profile/grant 변경은 hash와 stamp를 유지하고 다음 요청이 현재 권한을 읽는다.
Active에서 inactive로 바뀌면 현재 저장된 대상 session을 같은 transaction에서 폐기한다. 이미 허용된 요청에 대한 제한은 password 교체와 같다.
그룹 입력과 저장 집합은 최대 256개, 직접/그룹 유효 권한 합집합은 기존 auth 한도인 256개다. 초과를 자르지 않고 거부한다.

삭제에는 호스트가 생성한 전체 `orm.RelationDeleter[models.User]`를 명시한다. Identity 앱만의 정책을 자동 선택하지 않는다.
`DeleteInSession`은 native `SessionValidator`가 있는 borrowed relation session에서 전체 CASCADE·PROTECT·SET_NULL을 실행한다.
새 transaction을 열거나 caller model의 PK를 지우지 않으며 반환 건수는 바깥 commit 전까지 잠정 값이다.
호스트 관계 삭제·대상 session 폐기·감사는 같은 transaction이고 뒤의 실패도 전부 rollback한다.

읽기 preflight의 예상 입력 거부는 읽기 데이터로 보관하고 scope가 정상 종료한 뒤에만 공개한다.
읽기 종료·취소 실패가 겹치면 실행 오류이며, write의 입력 거부도 확정 rollback 뒤에만 renderable 결과가 된다.
실행 실패·unknown outcome에는 성공 DTO가 없고 자동 재시도하지 않는다. 확정 commit 뒤 늦은 취소는 성공을 뒤집지 않는다.
전용 Form/Admin과 self-service/reset의 전체 동작은 별도 소비자가 소유한다. 관리 API 계약은 아래에 명시한다.

## Group·Permission 관리와 직접 관계의 revision

`identity.Manager`는 Group·Permission의 생성·단건/페이지 조회·편집·삭제를 제공한다. 조회에는 각 모델의 현재 view 또는 change,
생성에는 add, 편집에는 change, 삭제에는 delete 권한을 요구한다. 생성에 change를 추가로 요구하는 UserAdmin과 구분한다.
이 기준은 고정 Django의 GroupAdmin과 명시적으로 등록한 표준 Permission ModelAdmin 관찰을 따른다. Django가 Permission을
기본 admin에 등록한다는 뜻은 아니다. 실제 Admin의 active staff 진입 조건은 별도 경계이며 service는 현재 저장 권한과 deny overlay를 확인한다.

조회에서 대체 change 권한을 검사하는 조건은 프레임워크가 확정한 권한 거부뿐이다. Actor 읽기·인가 callback 오류는 실행 실패로
유지한다. 오류의 cause에 permission-denied 분류가 들어 있다는 이유로 정상 조회에 도달하지 않는다. User 조회에도 같은 경계를 적용한다.

Group name은 150자, Permission name은 255자까지의 비어 있지 않은 UTF-8/NUL 없는 값이다. Service는 trim·대소문자 변환을
하지 않으며 Form의 입력 표현과 구분한다. Permission code는 기존 auth의 canonical lowercase dotted name이다. Code 변경은
Permission ID와 직접/그룹 할당을 보존하고 다음 credential resolution에 반영한다. 생성 입력과 patch는 caller collection을 복사하며,
Group permission 입력 생략은 유지, 명시적 빈 집합은 해제다. 중복 ID는 같은 선택으로 정규화하지만 입력 개수 한도는 정규화 전에 검사한다.
실제 변화가 없으면 revision·audit를 늘리지 않는다. List는 ID 순서의 최대 100개 scalar page이고 상세 collection을 행마다 조회하지 않는다.

Group permission 편집은 같은 coordinated write fence에서 영향을 받는 모든 사용자의 제안된 권한 합집합을 검증한다.
직접 권한과 다른 그룹 권한을 유지하고 편집 대상 그룹의 새 집합을 합치며, 256개를 넘으면 전체 변경을 거부한다.
사용자 행은 256개 keyset batch로 읽고 rowset을 닫은 뒤 각 사용자의 bounded membership/permission 조회를 수행한다.
이는 일정한 query 수를 보장하는 방식이 아니며, 전체 사용자를 메모리에 모으거나 첫 batch만 검사하지 않는다.
User의 그룹 가입과 Group 권한 확장도 같은 fence에 참여하므로 두 동시 변경이 함께 한도를 넘길 수 없다.

Revision은 해당 row와 직접 관리 collection의 변경 충돌을 소유한다. Group 삭제는 직접 소속 User의 revision을,
Permission 삭제는 직접 할당 User와 해당 Permission을 가진 Group의 revision을 같은 transaction에서 증가시킨다.
Owner 행도 256개씩 처리하며 overflow·손상·뒤 batch 실패는 앞에서 갱신한 revision과 삭제를 모두 rollback한다.
그룹을 통해 권한만 상속하는 User의 revision은 이 이유로 바꾸지 않는다. Group permission 편집이나 Permission code 변경도
User의 직접 collection을 바꾸지 않는다. Credential/hash·session stamp와 session bytes는 유지하고 다음 요청에서 현재 권한을 평가한다.

삭제는 호스트가 제공한 전체 typed relation deleter로 CASCADE·PROTECT·SET_NULL을 실행하며 identity-only 정책을 자동 선택하지 않는다.
Owner revision·관계 삭제·값 없는 audit는 같은 transaction이다. 삭제 응답에는 ID·revision·건수만 담아 delete 권한으로 profile을 공개하지 않는다.
삭제 audit의 대상은 삭제한 resource다. 영향을 받은 owner마다 별도의 편집 audit를 만들지는 않는다.
실패·unknown outcome에는 성공 DTO가 없고 재시도하지 않는다. 확정 commit 뒤 늦은 취소는 성공을 뒤집지 않는다.
이 서비스의 구현과 환경별 검증은 실제 관리 Form/Admin/API·독립 client, 전체 identity product milestone의 완료와 구분한다.

## 관리 JSON API와 수정 조건

`identity/api`는 User·Group·Permission의 목록/상세·생성·PUT/PATCH·삭제와 관리자 password 교체를
기존 Manager에 연결한다. Schema IR에서 선택한 field의 serializer·응답·OpenAPI를 구성한다.
외부 입력이 principal ID·encoded password·시간·revision을 지정하지 못하게 하고, 새 principal ID는 서버가 생성한다.
응답에는 profile과 직접 관계 ID만 선택하며 principal ID·hash·session material은 포함하지 않는다.
List는 scalar만 최대 100개씩 ID 순서로 반환하고 count와 items가 같은 snapshot을 사용한다.

조회는 view 또는 change, User 생성은 add와 change, 나머지 변경은 해당 모델의 add/change/delete를 요구한다.
`api.AlternativeAuthentication.RequireAny`는 인증과 CSRF를 한 번 수행하고 확정 거부일 때만 다음 권한을 확인한다.
인가 실행 오류는 다른 권한으로 우회하지 않는다. 공통 `api.PermissionPolicy`는 모든 필수 권한과 대안 중 하나를
결합한다. Session/Bearer의 `RequirePolicy`는 인증·CSRF를 한 번 수행하고 필수 권한 뒤에 대안을 평가한다.
오류·취소에는 뒤 검사를 하지 않으며, deny overlay는 principal이 갖지 않은 grant를 추가할 수 없다.
`endpoint.Permissions`가 같은 불변 선언을 실행과 OpenAPI에 연결한다. OpenAPI의 `x-godj-any-permissions`는 대안,
`x-godj-additional-permissions`는 함께 필요한 전체 권한이며 두 그룹 사이의 중복도 거부한다.
API의 모델 권한과 Admin의 active staff admission은 별개다.
Manager는 HTTP에서 받은 principal의 과거 grant를 쓰기 권한으로 신뢰하지 않고 현재 저장 상태를 다시 확인한다.

선택 계정의 typed JSON 입력은 모델 field와 credential 명령을 별도 객체로 결합할 수 있다. 공통 `api/input.Object`는
기존 `NewModel`의 완전한 source와 명령 Body를 보존하며 객체 구조에서 저장이나 권한을 추론하지 않는다.
한 parser 예산이 중첩 객체와 JSONField 전체를 포함하고, 모든 child의 검증/변환이 끝나기 전에는 setter를 실행하지 않는다.
이 결합 API의 바깥 객체 member는 full/partial 모두 필수이며 null을 허용하지 않는다. Mode는 child 내부 field에 적용한다.
오류 위치는 escape된 JSON Pointer로 구별한다. 기본/custom 외부 계정 생성 소비자는 `fields`와 `credential`을 사용하고
add+change/CSRF를 본문보다 먼저 확인한다. 공통 관리 JSON CRUD는 아래 선택 계정 관리 JSON 조립을 사용한다.
이 입력 기반이나 조립 코드만으로 I1 JSON의 환경별 제품 수용을 완료하지 않는다.

선택 계정의 `Binding.FormDefinition`과 `Binding.InputSpec`은 명시한 field의 전체 모델 source를 보존하고 login/email
쓰기 정규화를 길이·문법·static choice 검사 전에 적용한다. Login은 raw UTF-8 1,024 bytes, 별도 email은 4,096 bytes를
trim·정규화 전에 제한한다. 두 역할이 같은 field면 더 좁은 login 한도와 NFKC 다음 email-domain 정책을 함께 사용한다.
JSON의 명시적 한도는 좁힐 수 있지만 넓히거나 다른 정규화로 바꿀 수 없다. Read-only 출력·생략 default·stored IR·choice
선언은 이 변환으로 다시 쓰지 않는다. Form의 raw/initial도 유지한다. 관리 Form과 최초 계정 command가 같은 정의를 사용하며,
command description/fingerprint에도 원문 한도를 포함한다. 이 정의는 입력 준비이며 저장 owner의 보호 field·현재 권한·
domain/revision·최종 typed 상태 검사를 대신하지 않는다. 공개 CLI/scaffold의 선택 runtime 조립은 별도 요구다.

수정·삭제·password 명령은 `If-Revision`에 대상 row의 canonical positive decimal revision을 요구한다.
응답의 `revision`과 `Revision` header가 같은 값을 제공한다. 이 값은 profile·직접 관계의 충돌을 다루는
application 조건이다. 특히 password 명령은 User의 revision을 사용한다. HTTP representation의 ETag로 표현하지 않는다.
[HTTP representation validator와 변환된 PUT의 규칙](https://www.rfc-editor.org/rfc/rfc9110.html#section-9.3.4)을
row revision 계약으로 오인하지 않기 위한 선택이다.
누락은 [428](https://www.rfc-editor.org/rfc/rfc6585.html#section-3), 잘못된 값은 400, stale revision은 412다.
여러 header·목록·wildcard·leading zero와 증가시킬 수 없는 int64 최댓값은 거부한다.
OpenAPI는 두 header를 required int64로 기술한다. `If-Revision`은 1..9223372036854775806,
`Revision`은 1..9223372036854775807이며 생성 client가 문자열 변환 없이 row revision을 전달할 수 있다.
No-op은 기존 revision을 반환한다. 자동으로 최신 revision을 조회해 덮어쓰지 않는다.

PATCH의 생략은 유지, 명시적 빈 collection은 해제다. PUT은 선언된 scalar default를 적용하고 optional collection 생략은 유지한다.
Password는 생성 또는 별도 관리자 교체 명령에서만 받고 공백을 보존한다. 입력은 JSON 64 KiB·문자열 4 KiB 한도를 가지며
password hash profile의 추가 제한은 유지한다. Hasher가 직접 반환한 cause 없는 password input 거부만
400 validation으로 표현한다. 취소·entropy·wrapped error는 실행 실패이며 입력 오류로 낮추지 않는다. UserCreationForm 전체 validation·password confirmation·self-service/reset을
이 JSON API의 구현으로 주장하지 않는다.

호스트가 제공한 전체 typed relation deleter의 binding을 startup에서 검사한다. identity-only 정책을 자동 선택하지 않는다.
실제 relation PROTECT는 backend가 같은 callback 오류를 돌려주어 rollback을 확인한 경우만 `__all__/protected` 입력 거부로 표현한다.
Cleanup·storage 오류는 입력 거부로 낮추지 않는다. Unknown commit/rollback은 성공 DTO·version 없이 503 `outcome_unknown`을 반환하며
Retry-After를 제공하지 않는다. 호출자는 durable state를 재확인해야 한다. 관리 응답은 `Cache-Control: no-store`다.

Article의 authenticated composition은 실제 관리 API와 권한으로 보호된 `/api/identity/openapi.json`을 게시한다.
독립 Session/Bearer generated client를 실제 HTTP에 연결했다. 관리 Form/Admin의 연결은 아래에 명시하며,
GDJ-0100 전체 platform/process milestone은 별도로 남아 있다.


## 관리 Form과 공통 Admin 기반

ModelConfig의 공통 Form은 편집을 소유하고 CreateForm은 생성의 표현 정책을 소유한다.
`forms/model.Definition`이 model field 선택·override·비저장 입력·pure cross validator를 함께 투영하며
`admin.FormConfig.Definition`과 재사용 Identity Form이 이를 공유한다. 비저장 입력은 선택하지 않은 저장 필드를 포함해
Schema IR의 어떤 field도 가리지 못한다.
CSRF token과 expected_revision도 입력 field 이름으로 사용할 수 없다. 생성에는 모델의 add와 명시한 추가 권한을 모두 요구한다.
생성·편집 RelatedChoices는 각 action이 지정한 permission과 loader를 사용한다. Loader는 현재 저장 인가를 확인하고,
최종 관계 검증은 write service의 transaction에서 수행한다. Helpdesk의 별도 target-view 정책은 유지한다.

List/Get/History에는 인증 actor를 전달한다. Site와 registry의 조회는 view 또는 change를 허용하며
확정 거부에서만 대체 권한을 확인한다. 인가·읽기 오류를 권한 거부로 낮추지 않는다.
List snapshot에는 목록 field와 revision만 요구해 편집 relation 전체를 매 행 조회할 필요가 없다.
편집 snapshot과 Initial의 일치 검사는 유지한다. Admin의 삭제 preview는 모델 read와 delete를 모두 요구한다.
이는 profile을 노출하지 않는 Manager/API의 delete-only 권한 계약과 구분한다.

RevisionField는 입력에서 제외한 nonnullable Integer다. 수정·삭제·object command의 HTML은 읽은 revision을
expected_revision으로 제출하며, validation 재표시에도 같은 조건을 보존한다. Canonical positive int64만 허용하고
증가시킬 수 없는 최댓값은 수정 조건으로 거부한다. Site의 선행 조회 검사는 write transaction의 CAS를 대체하지 않는다.
Mutation callback은 조건을 다시 확인하며, 확인된 충돌은 409로 반환하고 현재 값을 검토하도록 안내한다.
Unknown outcome은 503으로 반환하며 성공 redirect·Retry-After·자동 재시도를 제공하지 않는다.
Callback이 성공을 알린 뒤 결과 계약이 틀리면 reconciliation-required 실행 오류로 남긴다.

Commands는 별도의 Form·권한·callback을 갖고 `/command/<name>/?id=<id>`에 GET/POST를 게시한다.
폼에는 profile initial을 주입하지 않는다. Callback은 확정된 ID/revision만 반환할 수 있어 자기 session을 폐기한 직후
성공 profile을 다시 읽기 위해 추가 인가나 I/O를 수행할 필요가 없다. 감사의 password 같은 논리 이름은
AdditionalAuditFields에 명시하며 snapshot이나 입력 필드로 승격하지 않는다.

PasswordInput은 Char 입력의 표현이다. 공백 보존은 WithTrimWhitespace(false)로 명시한다.
Password default와 choices는 시작 시 거부하며, 정상/오류 화면에 입력값을 넣지 않는다.
Form의 raw text와 Value의 문자열 payload는 불투명한 내부 상태에 둔다. 일반 fmt와 잘못된 %p/%w fallback도
비밀번호에 도달하지 않는다. 명시적 getter만 값을 반환하고 Value.Equal이 문자열 내용의 동등성을 소유한다.
HTML 입력은 요청 전체 64 KiB·개별 값 4 KiB·총 1,024개 값으로 제한해 두 개의 256-member 선택 집합과 scalar 조건을 수용한다.
이 공통 기반의 범용성은 실제 Identity 관리 소비자와 구분한다. Identity 연결과 입력 정책은 아래에 명시한다.

## 문자열 iexact와 사용자 생성 중복 정책

`StringField`·`NullableStringField`·관계 문자열 field의 `IExact(string)`과 dynamic `__iexact`는
같은 Query AST의 literal string 조건을 사용한다. Char와 Text에 적용하며 JSON·다른 scalar kind·NULL RHS·F RHS는
명시적으로 거부한다. NULL 조회는 IsNull을 사용한다. 입력 문자열을 Go에서 정규화하거나 casefold하지 않는다.
Nullable 조건의 부정, optional 관계의 AND/OR/NOT, reverse collection의 multiplicity와 NOT EXISTS는 기존 공통 planner가 소유한다.

고정 Django 6.1의 `IExact`, backend operators/operations를 따라 SQLite는 `%`·`_`·`\`를 이스케이프한
완전한 literal을 `LIKE ? ESCAPE '\'`에 바인딩한다. 부분 검색용 `%`를 추가하지 않는다.
PostgreSQL은 `UPPER(column::text) = UPPER($n)`을 사용하고 RHS를 LIKE escape하지 않는다.
Unicode 비교는 DB에 남기며 현재 PostgreSQL 17 profile의 UTF8·libc·C collation/ctype 제한을 유지한다.
두 DB를 공통 Unicode 소문자 변환으로 대체하거나 임의 collation까지 지원한다고 주장하지 않는다.

`UserCreate.WithCaseInsensitiveUsernameCheck()`는 고정 Django `UserCreationForm.clean_username()`의 추가 중복 정책이다.
NFKC 정규화한 candidate를 현재 인가 뒤, hash 전 읽기 snapshot과 저장 직전 coordinated relation transaction에서 검사한다.
Duplicate는 username의 `unique` 오류이며 lookup/읽기 종료 실패는 실행 오류로 남긴다.
두 cooperating writer가 대소문자만 다른 후보로 경쟁하면 마지막 검사를 같은 fence에서 수행해 하나만 생성한다.
이 옵션은 별도 case-insensitive DB 제약이나 로그인 정책이 아니다. 기본 Manager/API 생성과 로그인은 기존 case-sensitive 의미를 유지한다.
비협력 SQL writer, 이후 기본 정책의 생성·이름 변경까지 전역적으로 case-insensitive unique로 만드는 기능이 아니다.
이 조회/중복 정책은 UserCreationForm 전체 validation을 소유하지 않는다. Confirmation과 strength 정책은 아래 소비자에 연결한다.

독립 [Django observer](../../conformance/runners/django/iexact_reference.py)는 같은 synthetic 입력만 읽으며
GoDj 코드/기대 결과를 import하지 않는다. Django 소스는 저장소 고정 6.1과 BSD-3-Clause를 따르며,
각 결과에 lookup·auth forms·backend operations 파일 hash와 DB profile, 입력 hash를 남긴다.
실행 source·환경·실패/검증 범위는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.

## 실제 Identity Admin 소비자와 입력 정책

`identity/admin.Register`는 호스트가 제공한 backend·hasher·authorizer·전체 관계 삭제 정책으로 User/Group/Permission을
등록한다. Startup에는 I/O가 없으며, opaque Config의 옵션과 validator slice는 복사본이다. Article의 실제 Admin composition이
이 등록을 호출한다. Browser는 principal ID·encoded password·revision 값을 모델 필드로 지정하지 못한다.
Revision은 앞서 정의한 별도 조건으로만 제출한다. 생성 principal ID는 호스트가 선택하는 불투명한 값이다.

User 생성은 username·password1·password2와 명시적 unusable_password 선택을 받는다. Profile/role·그룹·직접 권한은 편집 Form이 소유하고,
별도 password command는 Manager가 확정한 ID/revision을 바로 반환한다. Profile 변경과 삭제는 선행 snapshot의 revision을
제출 조건과 비교한 뒤 Manager의 마지막 CAS를 수행한다. 삭제 결과는 확정 성공 뒤에만 선행 snapshot을 게시한다.
확정 PROTECT rollback만 기존 protected 화면으로 표현하고 cleanup failure·unknown은 실행 실패로 남긴다.

User 관계 선택은 현재 `change_user`, Group 생성/편집의 Permission 선택은 각각 현재 `add_group`/`change_group`을 요구한다.
Target catalog의 view 권한을 별도로 요구하지 않는다. 인가와 선택 목록은 같은 native read snapshot에서 읽고,
ID 순서의 완전한 목록만 반환한다. 4,096개를 초과하면 명시적 오류로 거부하며 저장된 선택을 조용히 잘라내지 않는다.
이 선택 목록 한도와 실제 사용자 그룹/유효 권한의 256개 한도는 다른 조건이다. 큰 catalog의 검색/페이지 선택 UI는 별도 확장이다.

History도 현재 view/change 인가·대상 존재·감사를 같은 read snapshot에서 읽는다. Runtime의 별도 Atomic 조회를 중첩하지 않고
`AuditHistoryInSnapshot`에 살아 있는 borrowed reader를 전달한다. Reader lifetime·결과 identity·오름차순 sequence·한도를 확인하고,
query/읽기 종료/취소 오류에는 결과를 게시하지 않는다. History backend의 permission 모양 원인을 실제 권한 거부로 낮추지 않는다.

View-only actor는 change GET 주소에서 선택된 model field의 읽기 전용 상세를 볼 수 있다. Password widget은 제외하고
relation은 저장된 ID를 표시한다. 선택 가능한 target catalog를 조회하지 않으며, 같은 주소의 POST는 change 권한을 요구한다.
Helpdesk의 편집용 target-view 정책과 데이터 접근 전 write admission은 유지한다.

Form의 `WithStringNormalizer`는 whitespace 처리 뒤 required/length/validator 전에 pure 변환을 수행한다.
Initial과 raw redisplay를 변경하지 않으며 changed 비교에도 같은 변환을 사용한다. Model override의 max length는 IR의 저장
한도를 넓히지 않고 입력 한도를 좁힐 수 있다. Static choice는 암묵적으로 trim하지 않고 명시적 normalizer를 적용한 값으로
기존 선택값을 검사한다. `WithMaxInputBytes`는 callback 전에 원문 한도를 확인한다.
기존 기본 identity Form의 Username 생성은 Python whitespace strip·NFKC·150자·문자 문법을 적용한다.
편집은 IR의 256자 한도를 사용해 API/CLI에서 정상 생성한 긴 이름도 Form initial과 저장을 왕복할 수 있다.
기존 기본 identity Form은 raw 글자 수가 해당 한도를 넘으면 NFKC를 생략하는 UsernameField의 비용 경계를 유지한다.
선택 계정 Form은 위의 역할 정책·원문 byte 한도와 실제 선택 모델의 IR 길이를 사용한다.
Email은 고정 Django EmailValidator의 quoted local/Unicode BMP domain/IP literal 문법을 적용한다. Password 두 값은 strip/NFKC
없이 비교하고 mismatch를 password2에 표시하며 모든 재표시에서 원문을 비공개로 유지한다.

`identity.WithPasswordValidators`는 pure·동시 사용 가능한 host 정책을 설정한다. 기본은 Django의 빈
AUTH_PASSWORD_VALIDATORS와 같이 strength 검사가 없다. Create/SetPassword는 hash 전과 마지막 write fence 안에서 정책을
검사하며 각 validator에 Profile의 복사본을 준다. Password 또는 non-field 오류만 허용하고, 취소/cleanup/unknown을 확인된
입력 거부로 바꾸지 않는다. Form adapter는 password 오류를 password2로 연결한다. 내장 strength 정책은 아래에 정의한다.

`internal/unicode16`은 고정 CPython 3.14.3과 같은 Unicode 16.0.0의 NFKC·full lowercase·문자 분류를 소유한다.
Go/compiler/x/text 버전 변경으로 기준을 바꾸지 않는다. 공식 UCD의 hash/크기를 검증한 오프라인 생성물이 원본이며,
독립 Python 관찰 결과를 생성 데이터로 사용하지 않는다. NFKC는 stream-safe 변환을 추가하지 않아 긴 결합 문자 뒤에 CGJ를 삽입하지 않는다.
일반 오류 입력의 잘못된 UTF-8을 replacement character로 고치지 않고 호출 계층에서 거부한다.
공식 normalization corpus, 모든 Unicode scalar의 독립 Python NFKC/소문자/분류 및 casing context 비교가 검증 경로다.
[고정 데이터와 라이선스](../../internal/unicode16/NOTICE.md), [입력 출처](../../identity/admin/NOTICE.md)를 함께 관리한다.

새 `ProvisionIdentity`는 같은 NFKC와 IR 경계를 적용한다. 기존 operator의 명시적 adoption은 이전 username의 정확한 바이트를
보존하며 자동으로 정규화하거나 이름을 바꾸지 않는다. 로그인은 저장된 username의 정확한 비교 의미를 유지한다.
Private createsuperuser protocol의 username 상한은 1,024바이트, password는 기존 1,024바이트, 전체 frame은 2,060바이트다.
Wire grammar/version은 유지하며 malformed·truncated·과대 입력은 기존처럼 거부한다. Legacy operator의 저장 256자 한도는 유지한다.
이 전송 상한은 Go-native SYS-023 결정이며 Django 기본 User의 150자 저장 스키마와 같다는 주장이 아니다.

기본 UserCreationForm의 재사용·저장 수명은 아래에 정의하며 custom user model은 별도 범위다.
[독립 observer](../../conformance/runners/django/identity_admin_reference.py), 실행 source/환경/범위는 TEST_EVIDENCE를 따른다.

### 생성 Form의 복합 오류와 읽기 검증

`ModelConfig.ValidateCreate`는 공통 Admin의 선택적 read-only post-clean callback이다. Site는 CSRF·현재 staff 및
생성/추가/선택 목록 권한을 통과한 뒤 Form을 bind하고, field 오류가 있어도 이 callback을 실행한다.
Callback은 불변 Form의 오류를 먼저 확인하고 성공한 입력만 검사한다. 기존 field 오류를 지우거나 Form을 교체하지 못한다.
확인된 `validation.Reject`만 `WithErrors`로 합치며 미선택 field 진단, wrapped/cleanup 오류와 취소는 실행 실패로 남긴다.
Pure `forms.Spec`에 DB I/O를 넣지 않는다. 이 읽기 검증은 저장 권한이나 재사용할 수 있는 성공 증명이 아니다.

Identity 생성은 `Manager.CheckUserCreation`으로 같은 read snapshot의 현재 add_user/change_user 권한과
DB `iexact` 중복을 검사하고, 남은 password2를 host 정책에 전달한다. 이름의 required/길이/NUL 오류나 중복은
candidate username을 비운다. 모델 문자 문법 오류는 candidate 이름을 유지한 채 strength 검사도 수행한다.
따라서 중복 이름과 짧은 password, 누락된 password1과 유효한 password2의 정책 오류를 한 번에 표시한다.
Confirmation mismatch 또는 password2의 field 오류는 strength 검사를 생략한다. 사용 불가 password 선택은
required/confirmation/strength를 생략하지만 입력 field의 NUL 오류까지 지우지는 않는다.

고정 Django `UserCreationForm.clean_username` → Form clean → model post-clean → password validation의 외부 결과를
[독립 양 DB observer](../../conformance/runners/django/user_creation_reference.py)로 관찰했다. Go Profile의 문자열은
native 후보의 None/빈 username을 빈 문자열로 표현한다. DB 조회 수와 Python 내부 instance 구조를 복제하지 않는다.
읽기 종료가 성공한 뒤에만 진단을 게시하며 ID 발급·hash·write는 하지 않는다. 최종 CreateUser는 현재 인가·중복·policy를
다시 preflight하고 hash 뒤 coordinated write fence에서 재검사한다. 저장은 기존 원자적 user/관계/audit 계약을 유지한다.
Admin과 아래 재사용 Form은 같은 정의와 검증 단계를 사용한다. Custom user model이나 Python 객체 내부 구조의
호환을 이 경로의 지원으로 주장하지 않는다.

### 재사용 Form과 저장 전 준비

`NewUserCreationForm`과 `NewAdminUserCreationForm`은 기본 User IR에 대한 불변 Form이다. 후자는 명시적 사용 불가 password
선택을 추가한다. `Bind(ctx, actor, data)`는 입력 정리와 read-only 검사를 합쳐 bound Form을 반환한다.
Admin도 같은 `Definition`과 `Check`를 사용한다. 일반 재사용 Form에는 Site의 staff 조건이 없으며 Manager의 현재 add/change
조건은 유지한다. 익명 signup이나 role 입력을 자동으로 공개하지 않는다. 초기화되지 않은 Form의 Definition은 명시적 빈 field
선택으로 실패하며 nil 선택의 '전체 model field' 의미로 되돌아가 private 저장 필드를 노출하지 않는다.

`Prepare`는 cleaned 값의 선택·타입·confirmation·문자 문법을 다시 검사한 뒤 현재 Manager에서 credential을 준비한다.
`Manager.PrepareUserCreation`은 정규화·현재 인가·관계·중복·policy 읽기를 끝내고 hash를 한 번 수행한다.
사용 불가 준비는 정책/hash를 호출하지 않는다. 이 단계에서는 user·관계·session·audit를 쓰지 않는다.
결과 `PreparedUserCreation`은 비밀번호·encoding을 공개하지 않는 불변 값이며 원래 Manager와 actor ID에 결합한다.
준비 시각에 date_joined와 profile 입력을 고정한다. 작업 종료 후 참조를 버리며 암호 메모리의 물리적 삭제를 보장한다고 하지 않는다.

`CommitUserCreation`은 hash 없이 현재 권한·중복·관계·policy를 coordinated write fence에서 다시 검사하고 기존 user/관계/audit의
원자 저장을 수행한다. 다른 Manager·actor의 후보는 거부하며, 준비 후 권한 회수·중복·관계 삭제도 최종 결과에 반영한다.
실패나 unknown에는 UserDetails를 게시하지 않고 자동 재시도하지 않는다. 후보의 모든 복사본은 한 번의 저장 시도를 공유한다.
해당 시도에 진입하면 확정 실패·unknown에도 소비 상태를 유지하며 재시도에는 새 Prepare가 필요하다.
성공한 사용자가 삭제된 후에도 과거 credential을 재생성할 수 없다. 소유자 거부·진입 전 취소는 후보를 소비하지 않는다.
기존 CreateUser/CreateUserWithUnusablePassword와 Form.Create도 이 prepare/commit 경로를 사용한다.

고정 Django의 `save(commit=False)`에서 hash 시점·저장 없음·나중 save의 외부 결과를 양 DB에서 관찰한다.
Python unsaved model의 mutable 속성·password getter·암묵적 권한은 복제하지 않는다. 추가 profile/role/관계는 typed UserCreate로
준비 전에 선택하고, 그 선택 전체를 마지막 write에서 검사한다. 기본 User Form과 준비/확정의 구현은 모든 ModelForm 저장이나
custom user model의 완료가 아니다. [사용 흐름](../../identity/USER_CREATION.md)과 실제 환경별 Evidence를 함께 따른다.

## 내장 password validator와 공통 정책 선택

`DefaultPasswordValidators()`는 고정 Django startproject의 similarity·최소 8자·common·numeric 순서를 반환한다.
자동으로 전역 정책을 켜지 않으며, policy 생략은 기존 빈 AUTH_PASSWORD_VALIDATORS 의미를 유지한다.
Manager의 WithPasswordValidators, Admin의 동일 옵션, API Config.PasswordValidators는 caller slice를 분리해 소유한다.
Article의 WithPasswordValidators는 두 HTTP surface에 같은 정책을 전달한다.

길이는 Unicode 글자 수, 숫자 판정은 Unicode 16 isdigit이다. Similarity는 선택한 public profile 문자열의 full lowercase,
Python `\W+` 분리(양 끝의 빈 부분 포함), 전체 값에 대한 quick_ratio의 multiset overlap과 inclusive threshold를 따른다.
Common은 pinned Django의 19,640개 사전과 lower/strip한 후보를 비교한다. 입력 secret을 NFKC/trim하거나 hash 전에 바꾸지 않는다.
모든 진단은 password field의 안정적인 code와 비공개 값이 없는 params이며 구성 순서대로 누적한다.
현재 인가·revision 검사, hash 전과 마지막 fence의 현재 profile 검사, session/audit 원자성과 rollback/unknown 구분은 유지한다.

Go 구성은 최소 길이 nonnegative, finite similarity >= 0.1(0은 기본 0.7), Profile의 네 public 문자열 속성을 지원한다.
Nil 속성은 기본 네 개, 명시적 empty는 검사 대상 없음이다. 사전은 constructor가 복사하는 문자열 목록이며 nil은 고정 사전,
empty는 빈 목록이다. 사용자 사전은 UTF-8/1,000,000개/16 MiB를 검증하며 외부 파일 I/O는 caller가 구성 전에 소유한다.
Malformed UTF-8을 고쳐서 허용하지 않는다. Source·license·정확한 parameter 의미는
[Password validation](../../identity/PASSWORD_VALIDATION.md)에 기록한다. 임의 Python user 속성 반사는 지원하지 않으며 Form·self-service·reset은 각 소비자 계약을 따른다.

## 사용 불가능한 password와 관리 lifecycle

비밀번호 사용 불가 상태는 User의 active/staff/superuser와 권한에 독립적이다.
`auth.MakeUnusablePassword(ctx)`는 예약된 `!` prefix와 32바이트의 새 암호학적 난수로 opaque 저장 값을 만든다.
`Credential.HasUsablePassword()`와 `Account.HasUsablePassword()`는 인코딩을 노출하지 않고 상태를 읽는다.
해당 prefix를 가진 기존 bounded 값도 사용 불가로 읽으며, 일반 encoded envelope의 빈 값·NUL·개행·크기 거부는 유지한다.
Django의 내부 marker 길이나 password hash 형식의 소스 호환은 약속하지 않는다.
출처·라이선스와 독립 기준은 [auth NOTICE](../../auth/NOTICE.md)를 따른다.

Memory/StoredAuthenticator는 사용 불가 값을 hash parser로 보내지 않는다. 비밀번호 인증은 알 수 없는 사용자와 동일한
현재 dummy hash로 한 번 검증한 뒤 반드시 거부한다. Dummy password를 알아도 성공하지 않는다.
Verifier 실행 실패·취소와 실제 malformed hash는 기존 오류 분류를 유지하며 잘못된 비밀번호로 숨기지 않는다.
현재 active identity의 Resolve와 다른 신뢰된 인증 수단으로 수립한 현재 stamp 세션은 허용한다.
사용 불가 설정도 매번 stamp를 바꾸므로 반복 설정은 그 사이의 세션을 무효화한다.
PasswordHasher.Hash가 예약된 사용 불가 값을 반환하는 잘못된 구성은 허용하지 않는다.

`Manager.CreateUserWithUnusablePassword`는 기존 생성의 현재 add/change 권한·profile·관계·unique와 감사 transaction을 사용한다.
`Manager.SetUnusablePassword`는 SetPassword와 동일한 현재 인가·expected revision·credential 재검사,
대상 session 폐기·revision 증가·값 없는 password 감사의 원자성을 사용한다. Marker 생성은 preflight 후 DB scope 밖에서
한 번만 수행하며 raw password validator/hasher를 호출하지 않는다. 반복 설정도 새 변경이고 no-op으로 축약하지 않는다.
SetPassword로 복구할 때는 기존 정책·원문·hash 작업을 모두 적용한다. 실패/unknown은 성공 Profile을 게시하지 않으며 재시도하지 않는다.
기존 generic login과 관리 변경의 경쟁은 다음 요청의 stamp 검사 경계를 유지한다.

관리 JSON API의 생성/password command는 required `password` member를 사용한다. 명시적 JSON null만 사용 불가 요청이고,
생략·빈 문자열·다른 타입은 거부한다. 문자열은 기존 비공개 입력과 원문 보존·선택된 정책을 따른다.
별도 Session/Bearer OpenAPI client도 nullable string을 사용하며 zero-value 빈 문자열을 null로 해석하지 않는다.
생성 client의 인코딩은 서버 검증을 대신하지 않는다.
Admin은 required 확인 checkbox·CSRF·revision·change_user 권한을 갖는 `disable-password` object command를 제공한다.
생성 폼의 `unusable_password` checkbox는 기본 false다. 선택하지 않으면 두 비밀번호의 required/confirmation과 host 정책을 적용한다.
선택하면 두 입력의 내용·일치 여부와 password 정책을 사용하지 않고 사용 불가 계정을 만든다. 비공개 렌더링과 transport/field 자원 검사는 계속 적용한다.
Username 정규화·중복 검사와 현재 add/change 권한·감사 transaction은 같은 Manager가 소유한다.
이 선택은 고정 Django AdminUserCreationForm의 외부 결과를 따른다. Boolean checkbox와 Django radio/field 내부 구조의 소스 호환은 약속하지 않는다.
기본 UserCreationForm·self-service·reset은 이 ADR의 각 계약을 따르며 custom user model·다른 인증 provider는 별도 범위다.

## 현재 password 상태의 읽기 전용 표현

`Profile.PasswordUsable`은 profile을 만드는 동일한 User 행의 encoded password에서 계산한다.
별도 저장 필드·조회·hasher 작업·캐시를 추가하지 않는다. Directory/Account와 Manager의 목록·상세·성공 결과가 같은 값을 가진다.
`auth.IsPasswordUsable`은 `!` prefix의 의도적인 사용 불가 상태를 구분하는 표현용 predicate다.
지원 hash 알고리즘·인코딩 검증·active/staff/권한·로그인 성공의 판정에 사용하지 않는다.
Go의 빈/zero credential은 false이며 정상 저장 credential의 기존 envelope 검증은 유지한다.

관리 API의 User/UserSummary는 required non-null boolean `password_usable`을 반환한다.
목록·상세·생성·PUT/PATCH·password 명령의 성공 응답과 두 독립 OpenAPI client에 같은 계약을 적용한다.
모든 입력에서는 이 이름을 거부하고, 응답에 raw/encoded password를 포함하지 않는다.
표시를 위해 Schema IR에 저장되지 않는 가짜 model field를 넣지 않는다.
`ModelEncoder.WithComputed`가 명시적 read-only field와 순수 snapshot reader를 소유하며 output Spec을 제공한다.
저장/관계 이름의 대체·중복·nil reader는 구성 오류다. 파생 encoder는 원본과 형제의 field/reader 집합을 변경하지 않는다.
읽기 결과의 부재·잘못된 타입/null·문자열 길이·decimal precision은 실행 오류이고 부분 응답을 게시하지 않는다.
계산한 값에도 입력 trim/default/coercion을 적용하지 않으며 OpenAPI는 같은 encoder Spec에서 생성한다.

Admin은 기존 객체의 편집 및 view-only 상세에 `Password login: Enabled/Disabled`를 표시한다.
`ReadOnlyFields`는 이미 인가된 typed 객체 snapshot의 bounded plain text만 받는 명시적 표시 선언이다.
Reader는 I/O·인가·객체 보관을 하지 않는다. 생성 화면에는 표시/평가하지 않고 input·CSRF/revision 조건 이름과 충돌할 수 없다.
구성과 descriptor는 caller slice에서 분리하며 label/value는 HTML로 신뢰하지 않고 escape한다.
확인된 입력 거부로 form을 다시 표시할 때도 같은 snapshot의 상태를 유지한다. 실패·취소 때 부분 화면을 게시하지 않는다.
이 표시는 Django의 `has_usable_password()` 외부 의미를 따른다. hash summary를 렌더링하는 Python widget의 복제가 아니다.
목록/history만 지원하는 Admin `ReadOnly` 등록에 상세용 field를 주면 명시적 구성 오류로 처리한다.


## 로그인 관찰과 세션 수립

`last_login`은 실제 password 로그인에서 서버 세션을 확정할 때 기록한다. Authenticate/Resolve, bearer 검증,
기존 세션 Access, logout, provision과 관리 생성은 기록하지 않는다. UTC microsecond로 정규화하며 API의 기존 nullable
DateTime에 노출한다. Admin의 `Last login`은 Never 또는 UTC 시각의 읽기 전용 표시이고 입력으로 받지 않는다.
이 관찰 때문에 관리 revision·audit를 늘리지 않으며 profile 편집·password 변경은 최신 관찰 값을 보존한다.

`Runtime.LoginPersistence(manager)`는 그 Identity runtime의 durable session store를 사용하는 정확한 Manager에
명시적으로 결합한다. 구성 중 I/O는 없으며 다른 manager/domain과 typed-nil binding은 거부한다. Article·process worker와
저장 Identity 소비자는 이 binding을 전달한다. 기존 operator에는 User 필드가 없으므로 이 capability를 제공하지 않는다.
공통 sessionauth는 memory/custom 인증도 사용하므로 Identity를 암묵적으로 탐지하거나 전역 hook에 연결하지 않는다.

Password 검증과 첫 active/staff/permission admission, 시계·entropy는 DB write scope 밖에서 준비한다.
`auth.SessionLogin`은 검증한 credential·이전 detached session·시각·동일 admission callback을 전달한다.
해당 callback은 write owner를 재진입하지 않아야 한다. 저장 owner는 native coordinated transaction에서 세션을 생성/교체하고,
현재 User·credential stamp·grant union을 다시 읽어 admission을 확인한 뒤 last_login을 갱신한다.
현재 권한을 담은 Credential과 session 결과는 commit이 확정된 후에만 게시한다. 실패 시 세션과 시각을 함께 rollback한다.
로그인 전에 `Peek`로 읽어 idle expiry를 별도로 늘리지 않는다. 읽기 뒤 만료된 세션의 cleanup도 실패한 로그인과 함께
rollback하며, 다음 일반 Access/정리에서 처리한다.

익명 또는 같은 principal/stamp의 재로그인은 payload와 원래 absolute lifetime을 보존하며 ID를 항상 회전한다.
다른 principal, stale stamp 또는 partial authentication key가 있으면 `Store.Replace`가 이전 ID를 새 lifetime·auth key만
갖는 세션으로 원자 교체한다. 이전 계정의 값은 이어받지 않는다. 일반 Store.Replace의 만료·누락·충돌 의미는 Rotate와 같다.
Manager.WithStore는 immutable 정책과 직렬화한 clock/entropy 소유권을 공유하고 원래 store binding을 바꾸지 않는다.

실패/취소/unknown outcome은 credential·session DTO와 cookie를 게시하지 않는다. 확인된 admission 거부만 정확한
`ErrInvalidCredentials`이고 cleanup/unknown 원인에 이 sentinel이 포함되어도 정상 인증 거부로 낮추지 않는다.
Unknown transaction/commit 분류와 cancellation을 보존하면서 private driver 진단은 제거한다.
오류의 직접 비교는 알려진 sentinel/pointer에 한정한다. Slice/map을 가진 host error도 panic 없이 실행 오류로 반환한다. 확인된 세션 ID 충돌만
새 entropy로 bounded retry한다. 확정 commit 뒤 발생한 cancellation은 이미 성공한 결과를 취소하지 않는다.

[독립 Django 관찰](../../conformance/runners/django/login_lifecycle_reference.py)은 양 DB에서 인증만 수행한 경우,
거부·익명 로그인·재로그인·계정 교체·접근·logout과 저장 실패/인증 뒤 변경을 관찰한다. 기준과 다른 부분은 명시한다.
Django는 같은 계정 재로그인에서 ID를 유지할 수 있지만 GoDj는 기존 fixation 정책대로 항상 회전한다.
Django에서 last_login 저장 뒤 session middleware 저장이 실패하면 시각만 남을 수 있고,
인증 뒤 비밀번호/활성 상태가 바뀌면 먼저 로그인한 뒤 다음 요청에서 거부할 수 있다.
GoDj 저장 binding은 이 두 경계를 같은 transaction과 현재 admission 재검사로 강화한다.
정확한 Django 부작용 parity로 합산하지 않으며 [DEV-0013](../DEVIATIONS.md#dev-0013--credential-session의-go-표현과-invalid-identity-정리)과
실행 evidence에 관찰/차이/검증 범위를 구분한다.

## 자기 비밀번호 확인과 현재 세션 유지

`StoredAuthenticator.ConfirmPassword`는 mutable username 대신 고정 principal ID로 현재 password를 확인한다.
현재 관찰의 read scope를 끝낸 뒤 bounded Verify를 한 번 수행하고, 현재 credential/active/권한을 다시 읽는다.
Username 변경은 같은 ID의 확인을 무효화하지 않는다. Hash/credential 변경이나 비활성은 확인을 거부한다.
사용자 없음·사용 불가 password는 기존 bounded dummy 작업을 공유한다. Hasher의 직접적이고 원인이 없는 password 입력 거부만
일반 인증 거부가 된다. 이를 감싼 실행 오류·취소는 원인과 private diagnostic 경계를 유지한다.
확인 자체는 password/last_login/session을 쓰지 않는다.

Self-service는 명시적인 `PasswordChangePersistence`로 준비하며 정확한 session manager와 native identity runtime에 결합한다.
현재 세션 ID가 본인을 식별하고 target ID·관리 change_user 권한은 입력으로 받지 않는다. Legacy operator에는 이 capability가 없다.
`PasswordChanger`는 이전 credential stamp와 old password를 확인하고, 별도로 주입한 password policy를 현재 profile에 적용한다.
새 hash는 종료된 read scope 밖에서 한 번 생성한다. 불변 prepared 값은 raw/encoded password getter가 없고 fmt/JSON에 값이 나오지 않는다.
호출자는 이 값을 request 이후 보관하지 않는다. 관리 비밀번호 정책과 같은 정책을 주입하는 책임은 composition에 있다.

최종 native write fence 안에서 현재 User의 active/credential과 실제 저장 session의 ID/principal/stamp를 다시 확인한다.
Profile policy도 현재 행으로 다시 검증한다. 현재 세션 payload와 원래 absolute lifetime을 보존하며 ID와 stamp를 회전하고,
다른 principal session을 완전한 bounded inventory 검사 후 폐기한다. 익명/다른 사용자의 session은 그대로 둔다.
User의 password와 **현재 revision + 1**만 patch하며 본인을 actor로 한 값 없는 password audit를 함께 쓴다.
Password 확인은 revision 자체를 선행 조건으로 사용하지 않으므로 그 사이 username/profile/grant/last_login 편집을 덮어쓰지 않는다.
동시에 확인된 두 변경은 이전 credential/session fence를 함께 통과할 수 없고 하나만 성공한다.

실패하면 User/session/audit 전체를 rollback한다. 만료 session cleanup도 실패와 함께 rollback하며, 확인된 ID collision만 새 entropy로 재시도한다.
Hash 작업을 반복하지 않는다. Callback zero/nil/twice/swallow·wrapped validation·취소·unknown commit/rollback은 성공 결과를 게시하지 않는다.
확인된 commit 이후 취소는 성공을 뒤집지 않는다. Web runtime은 성공 시에만 session cookie를 교체하고 기존 CSRF secret을 유지한다.
호출 route가 CSRF·입력 confirmation을 먼저 검증한다. 원인이 붙은 validation은 취소를 포함해 실행 오류 wrapper를 유지하며,
일반 입력 거부로 다시 노출하지 않는다. Runtime은 Form/JSON/OpenAPI의 완성된 사용자 화면을 대신하지 않는다.

독립 [Django observer](../../conformance/runners/django/password_change_reference.py)는 실제 고정 PasswordChangeView/Form과
session middleware를 사용한다. 일반 사용자의 허용, old password/공백/confirmation/strength, 현재 session 회전·payload 보존,
다른 session의 다음 접근 거부, 같은 원문의 새 salt, last_login 불변이 공통 관찰이다.
Django의 다른 session은 다음 접근까지 DB에 남지만 GoDj는 같은 transaction에서 폐기한다.
Django native form.save()는 전체 User를 저장해 검증 뒤 비활성/다른 password 변경을 덮어쓸 수 있고,
session cycle/final save 실패에서 password만 바뀐 부분 상태를 남길 수 있다. Fixture는 이 결과를 그대로 기록한다.
GoDj는 현재 credential/active 재검사와 field patch·원자 저장을 채택한다. 동일한 실패 부작용이라고 주장하지 않는다.

## 일반 계정의 Form·JSON 소비자

`identity/account`는 일반 계정의 `/account/login/`, POST logout, password Form과 완료 화면을 제공한다.
Article은 저장 Login/PasswordChangePersistence와 기존 Admin/API의 명시적 password validator를 같은 runtime에 연결한다.
Admin은 active staff와 모델 권한을 계속 요구한다. 공유 runtime의 redirect 목록은 Admin 경로와
`SiteConfig.AdditionalNextPaths`의 명시적 앱 경로가 정확히 일치해야 하며, 추가 경로가 그 route의 접근 권한을 부여하지 않는다.
Account 로그인은 자기 fallback을 사용하며 외부/미등록 next는 거부한다. Username은 고정 Unicode 16 trim/NFKC와 IR 길이를,
password는 원문 공백 보존을 사용한다. Form은 password 값을 다시 렌더링하지 않는다.

Password Form은 `old_password`, `new_password1`, `new_password2`를 받는다. 기존 password 오류와 new confirmation/policy
오류를 필드 순서로 함께 표시하는 고정 Django 결과를 따른다. 필수/confirmation 오류가 있는 제출도 성공적으로 clean된
필드의 검사를 수행한다. `CheckPasswordChange`는 선택한 필드의 진단만 반환하고 새 hash나 저장·재사용할 쓰기 권한을 만들지 않는다.
유효한 Form 제출은 최종 원자 변경 경로에서 다시 확인한다. 같은 원문의 password도 새 hash/stamp/ID를 생성한다.
Form은 64KiB body/4KiB query/입력값 4096 bytes/최대 16개 값으로 제한하며 unknown 입력·중복 보안 값을 거부한다.
Django QueryDict의 last-value 동작을 복제하지 않고 중복 password는 필드 오류로 처리한다.

`api.PrincipalAuthentication.RequireAuthenticated`는 모델 permission 없이 현재 active principal만 요구하는 명시적 capability다.
기존 `Require("", ...)`는 구성 오류를 유지한다. Session의 CSRF와 Bearer 검증/challenge는 그대로 적용하고,
OpenAPI는 이 선택을 `AuthenticatedOnly`/`x-godj-authenticated-only`로 기록하며 permission과 혼합하지 않는다.
이 account API는 Session profile만 게시한다. `InspectPrincipal`/API `WithReadOnlyResolution`으로 admission을 수행해
유효 session의 idle 시간을 갱신하거나 만료·stale 행을 청소하지 않는다. 변경은 최종 persistence fence가 소유한다.
일반 `Principal`의 touch/cleanup은 유지하며 verifier의 wrapped denial·취소·unknown은 실행 오류로 보존한다.

`GET /api/account/csrf/`는 204와 masked header를, `POST /api/account/password/`는 required non-null 문자열
`old_password`/`new_password`를 받는다. 인증·CSRF 후 parser가 입력을 검사하며 target/revision/confirmation을 받지 않는다.
성공은 204와 replacement session cookie이고 기존 CSRF cookie는 유지한다. 확정 입력 오류 400, 인증/CSRF 403,
body 상한 413, media 415, unknown outcome 503을 구분한다. JSON 문자열 byte 상한 위반은 400이다.
실패에는 cookie·Retry-After·자동 write retry가 없으며 unknown은 durable 상태를 확인한 뒤 후속 작업을 판단한다.
인증된 `/api/account/openapi.json`은 실제 handler/serializer 선언을 게시한다. Account 응답은 no-store다.

별도 ogen account client는 제품 Form 로그인 후 실제 Session/CSRF JSON 변경, required replacement cookie,
같은 password 교체, other-session 폐기·제품 logout을 호출한다. 부모는 reopen한 SQLite의 password/revision/last_login,
unused/foreign session과 값 없는 audit를 별도로 확인한다. Synthetic 503 decoder/no-retry와 실제 양 DB native HTTP
rollback/unknown 검증을 구분한다. Reset과 기본 UserCreationForm은 이 ADR의 별도 계약을 따르며 custom user model·token issuer/JWT/OAuth/OIDC는 남은 구현이다.
실행 범위와 환경별 완료는 TEST_EVIDENCE에서 별도로 확인한다.

## 비밀번호 재설정 token과 원자 저장

`identity.PasswordResetter`는 `ManagementBackend`와 명시적 hasher·`PasswordResetConfig`에 결합한다.
서버 내부의 `IssueToken(ctx, principalID)`는 현재 active/usable 계정을 읽어 수신자에게만 전달할 값을 만든다.
이 함수를 identifier 기반 공개 조회 API로 노출하지 않는다. 수신자 선택은 reset 저장 owner가 소유하고,
공통 email 정규화·메일 전달은 아래 계약을 따른다. 실제 HTTP 소비자는 `identity/account`에 연결한다. `CheckPassword`는 token과 선택적인 새 password를 읽기 전용으로 검사하며,
새 hash·write admission·session을 만들지 않는다. Raw token은 명시적인 `Encoded()`로만 꺼내고 fmt/JSON에서는 감춘다.

`auth.PasswordResetKeyRing`은 active 1개와 validation 전용 최대 7개의 서로 다른 32-byte 키를 소유 복사한다.
키의 생성·보관·배포는 host가 맡고 CSRF/session 키와 공유하는 API를 제공하지 않는다. 검증은 모든 등록 키를 확인한다.
Token은 domain-separated HMAC-SHA256의 전체 MAC과 canonical base-36 Unix 초를 raw URL-base64와 함께 인코딩한다.
HMAC 입력은 길이 framing한 저장 row ID·stable principal ID·credential stamp·email 원문·UTC last_login·발급 시각이다.
URL에는 password·stamp·email을 넣지 않으며 wire 형식은 Django와 호환되지 않는다.
기본 만료는 Django와 같은 72시간이고 host는 양의 정수 초 duration을 선택한다. 경계 초는 유효하며 미래 발급은 거부한다.
Key ring과 validator 목록은 생성 후 불변이고 clock/validator callback은 pure·concurrency-safe여야 한다.

공통 서명은 모델에 의존하지 않는 `auth.PasswordResetState`를 사용한다. 저장 owner는 같은 snapshot의 실제
credential·row key·email·LastLogin을 넘긴다. State는 LastLogin을 UTC 전체 정밀도로 복사하고 credential domain을
포함한 stamp에 서명을 결합한다. 같은 ID/hash라도 다른 모델/domain 세대나 unscoped credential의 token을 승인하지 않는다.
모든 저장 int64 key를 표현하며 optional Email/LastLogin의 부재도 명시적으로 표현한다. Email은 UTF-8/NUL과 4,096 byte
자원 한도를 검사한다. State·key·syntax signature·delivery token의 fmt/JSON은 비공개이고, 발급 token은 hash/credential을
보유하지 않는다. 이 순수 값은 DB 소유권이나 최종 write 권한을 증명하지 않으며 실제 owner의 현재 상태 재검사를 대체하지 않는다.

`ResetPassword`는 preflight read scope를 닫은 뒤 password policy·새 hash를 처리하고, native write fence 안에서
현재 행과 token의 credential/email/last_login·active/usable·만료를 다시 검사한다. 현재 profile로 policy도 다시 적용한다.
변경되지 않은 encoded password를 돌려주는 hasher는 거부한다. Password와 **현재 revision + 1**만 patch하며,
대상의 모든 session 폐기와 본인을 actor로 한 값 없는 password audit를 같은 transaction에 저장한다.
사용자를 로그인시키거나 현재 브라우저 session을 보존하지 않는다. Username/profile/role/revision만 바뀌면
token을 불필요하게 거부하지 않되 최신 값을 덮어쓰지 않는다. 두 연결에서 같은 token을 제출하면 하나만 성공한다.

없는/비활성/사용 불가/변경된 계정, malformed/만료/다른 서명은 원인 구분 없는 `auth.ErrInvalidResetToken`이다.
Malformed token은 I/O 전에 거부한다. 저장 단계의 거부는 확정 rollback이 직접 반환한 경우에만 그 sentinel이나
원인 없는 validation으로 게시한다. Snapshot/transaction callback 위반·cleanup·취소·unknown은 실행 오류이며
secret-free 진단과 원인을 유지하고 재시도하지 않는다. 확정 commit 뒤 취소는 성공을 실패로 바꾸지 않는다.

독립 [native reset 기준](../../conformance/runners/django/password_reset_reference.py)의 두 DB 결과는 수정하지 않는다.
Django는 last_login의 초 아래 부분을 버리고 active 변경만으로는 token을 무효화하지 않으며 미래 timestamp도 허용한다.
GoDj는 실제 저장된 UTC 시각의 전체 정밀도와 현재 active/usable을 확인하고 미래 발급을 거부한다.
Django SetPasswordForm이 두 stale snapshot을 순서대로 저장하거나 최신 email/active를 복구하는 관찰과도 다르게,
GoDj는 token을 최종 재검사하고 field patch·원자 저장을 사용한다. 이 차이는 [DEV-0013](../DEVIATIONS.md#dev-0013--credential-session의-go-표현과-invalid-identity-정리)에 기록한다.
메일 실패 정책·수신자 선택·reset HTTP view·Form/API의 구현 및 검증은 이 service의 완료로 추론하지 않는다.


공통 [mail](../../mail/)의 Message·Memory·SMTP 기반은 별도 [ADR-0078](0078-mail-message-ownership-and-delivery.md)을 따른다.
전송의 확정 접수/거절/unknown과 message의 소유권을 구현했고 reset 수신자 선택·발급 값 연결도 아래 service가 소유한다.
공개 응답·Form/API는 후속 소비자가 소유한다. Reset 전송 실패를 그대로 공개 HTTP 오류로 바꾸어 계정 존재 여부를 드러내지 않는다.
실제 I/O 오류의 내부 보고 방식까지 소비자 연결에서 검증한다.


## 재설정 메일 요청과 수신자 결합

`identity.PasswordResetMailer`는 하나의 `auth.PasswordResetMailSource`, 명시적 `mail.Sender`와 mail 설정에 결합한다.
Source는 reset 저장 owner이며 선택·서명·수신자 관찰을 소유한다. Mailer는 고정 User 조회나 별도 token 발급을 수행하지 않는다.
Email 역할을 제공하지 않는 source와 초기화되지 않은 authority는 시작 시 거부한다.
`auth.CleanPasswordResetEmail`은 고정 PasswordResetForm의 strip·required·EmailValidator·254자·NUL 진단을 공유한다.
Raw UTF-8와 4,096 byte 한도는 Unicode 작업 전에 검사한다. EmailValidator 문법은 공통 `validation.ValidEmail`이 소유하며
기존 Identity Admin도 같은 함수를 사용한다. Model 의미는 Schema IR에 남고 254자 입력 제한은 native reset form의 계약이다.

수신자 선택은 한 native read snapshot에서 `active=true`와 DB의 `Email.IExact`로 후보를 찾은 뒤,
고정 Unicode 16의 NFKC + full casefold 및 usable password를 검사한다. CPU 정규화로 전체 사용자를 검색하지 않는다.
Case folding은 공식 Unicode 16 CaseFolding C/F data에서 생성하고 모든 scalar의 독립 CPython 결과와 비교한다.
SQL 단계에서 다른 이메일인 값은 Unicode 정규화 결과가 같더라도 선택하지 않는다.
후보는 ID 순서이며 active/DB-iexact 후보 256개까지 전부 읽는다. 초과·읽기 실패·callback 위반·cleanup 오류이면 아무 메일도 보내지 않는다.

각 수신 주소와 token은 **같은 계정 snapshot**에서 만든다. Source는 native borrowed reader와 callback/cleanup을 확인하고
scope를 닫은 뒤 전체 `auth.PasswordResetToken` 목록만 전달한다. 나중에 계정을 다시 읽어 새 token만 발급하면
변경 전 이메일로 현재 상태의 유효한 token을 보낼 수 있으므로 그렇게 하지 않는다. Snapshot 종료 후 경쟁 변경이 있으면
메일의 이전 token은 기존 reset service의 현재 credential/email/last_login/active 검사에서 거부된다. 일반 profile 변경은 보존한다.
요청 과정은 password hash·User·session·last_login·audit를 변경하지 않으며 callback과 mail I/O 전에 DB scope를 닫는다.

링크 origin은 host가 명시한 HTTPS origin이다. HTTP는 literal loopback IP의 개발 환경에만 허용한다.
요청의 Host/forwarding header를 사용하지 않고, query·fragment·userinfo 없는 origin과 canonical confirm path를 사용한다.
Principal ID는 canonical base64url path segment로 담는다. `PasswordResetMail`은 username·site name·link만 명시적으로 제공하며
fmt/JSON은 비공개다. 기본 plain text가 있고 host renderer는 Subject/Text/HTML만 만든다. From/To는 framework가 소유한다.
메시지별 내용 64 KiB, 전체 후보 256개 범위에서 모든 render·메시지 검증을 마친 뒤 전송한다. 두 번째 render 실패도
첫 번째 메시지를 보내지 않으며 renderer가 자기 내부에서 만드는 자원의 한도와 captured state의 동시성은 host 책임이다.

전송은 대상마다 한 번이며 일반 sender 실패 뒤에는 다음 대상도 시도한다. Context가 취소되면 남은 대상을 보내지 않는다.
실패 원인은 private cause chain에 보존해 `delivery_failure`로 반환하며, 접수 불명 mail 원인은 `outcome_unknown`으로 유지한다.
확정된 마지막 접수 뒤 늦은 취소로 성공을 되돌리지 않고 어떤 오류도 자동 재시도하지 않는다.
이는 DB transaction이 아니므로 일부 전송 성공을 되돌릴 수 없다. 없는/비활성/사용 불가 계정은 전송 없이 nil이다.

Native Django는 send 오류를 기록하고 삼키며, Go service는 내부 호출자에게 명시적 오류를 반환한다.
제품 HTTP 소비자는 유효 email 요청에 같은 공개 안내를 주고 이 오류를 별도로 기록해야 한다. 현재 service의 동기식
DB/메일 실행을 계정 존재에 대한 일정한 응답 시간 보장으로 표현하지 않는다. 실제 공개 응답·CSRF·token 숨김과
Form/API·독립 client는 후속 통합에서 검증한다. [DEV-0019](../DEVIATIONS.md#dev-0019--메일-소유권과-명시적-smtp-접수-결과)와 환경별 Evidence를 따른다.

## 재설정 HTTP 관찰과 세션 저장의 원자 결합

고정 Django의 실제 ResetView/ConfirmView를 CSRF·인증·DB session middleware와 함께 SQLite/PostgreSQL에서 관찰했다.
유효 email은 대상 유무·active/usable 여부·메일 send 실패에 같은 완료 redirect를 사용한다.
Raw token URL은 token을 server session에 저장하고 `set-password` URL로 redirect한다. Confirmation HTML과
redirect에는 raw token이 없으며, 다른 브라우저·다른 계정의 hidden URL·삭제된 proof session은 사용할 수 없다.
필수 입력·confirmation mismatch·password policy의 오류 순서와 password 미반사도 native 기준에 포함한다.

기본 native reset은 자동 로그인하지 않고 last_login을 바꾸지 않는다. 현재 브라우저가 다른 사용자로 로그인한 경우
그 인증과 payload를 유지하며, 대상 사용자로 로그인한 기존 session은 다음 인증 접근에서 무효화된다.
서버 session 저장이 실패하면 HTTP 500이어도 password는 이미 바뀌고 저장된 reset token은 남을 수 있다.
Native HTTP 관찰은 이 결과를 감추지 않는다. GoDj는 기존 reset 계약의 최종 active/usable 재검사와 즉시 세션 폐기를 유지하며,
확인에 사용한 session/proof 정리도 password·revocation·audit와 같은 native transaction에 결합한다.

그 결합을 위해 `PasswordResetter.Prepare`는 읽기 종료 뒤 hash를 한 번 준비하고 owner에 결합한 불변
`PreparedPasswordReset`을 반환한다. `ApplyIn`은 호출자의 같은 저장 영역에 속한 coordinated transaction 안에서
현재 token·account·만료·password policy를 다시 확인하고 password/현재 revision·대상 session 폐기·audit를 쓴다.
자체 transaction을 열거나 commit하지 않으며, 이후 proof 저장이 실패하면 호출자가 전체 변경을 rollback해야 한다.
이 값은 호출 작업 동안만 보유한다. 실패 또는 unknown 결과를 자동 재시도하지 않고, 확인된 rollback 뒤 명시적으로 재사용해도
최종 인가를 생략하지 않는다. 다른 resetter의 준비 값은 거부한다. 기존 `ResetPassword`도 같은 Prepare/ApplyIn 경로를 사용한다.

`CheckTokenIn`은 같은 저장 영역의 빌린 coherent read/write scope에서 현재 token만 관찰한다. 새 snapshot·hash·write를 만들지
않으며 세션에 proof를 저장할 때 같은 fence에서 admission을 확인하기 위한 것이다. 이 관찰은 나중의 write 권한이 아니다.
실제 proof session persistence와 Web runtime은 아래 계약을 사용하며 Form/API·독립 client는 마지막 소비자 계약을 따른다.
Native fixture나 transaction/runtime 검증과 실제 공개 HTTP 검증의 범위를 구분한다.

`Runtime.PasswordResetPersistence(manager, config)`는 해당 Identity runtime의 정확한 durable store와 manager에 결합한다.
생성은 I/O 없이 수행하고, `Resetter()`로 같은 key ring·policy owner에 메일 requester를 연결한다.
공통 `auth.PasswordResetPersistence` 계약을 Web runtime에 명시적으로 주입하며 Web 설정의 manager와 다르면 startup에서 거부한다.
Persistence는 초기화된 `auth.PasswordResetAuthority`를 제공한다. 이는 immutable reset owner 하나의 프로세스 내 식별이며
서명 키·durable credential domain·bearer capability가 아니다. Mail source와 완료 persistence는 같은 식별을 공유해야 한다.
키나 경로가 우연히 같아도 다른 owner를 조립하면 `identity/account` 시작 시 거부한다. 이 검사는 DB·해싱·메일 I/O를 수행하지 않는다.
Legacy operator runtime은 이 기능을 제공하지 않는다.

Entry는 현재 token을 읽기 전용으로 검사한 다음 같은 write fence에서 다시 확인한다. 기존의 활성 session이 있으면
최신 payload·인증 값·생성 시각·absolute lifetime을 보존하면서 ID를 회전하고 reset target/token을 서버 값에 추가한다.
없거나 만료된 기존 session이면 anonymous session을 만든다. Cookie에는 256-bit session ID만 담고 target 인증을 만들지 않는다.
Native의 기존 session ID 유지와 달리 Go는 새 proof 권한을 저장할 때 fixation을 막기 위해 ID를 회전한다.
Malformed/duplicate cookie는 Web 경계에서 거부한다. `CheckPasswordReset`은 session touch·expiry cleanup·새 hash 없이 확인한다.

완료는 저장된 proof에서 token을 읽고 DB scope 밖에서 hash를 준비한다. 최종 fence 안에서 현재 session의 만료·target/token과
기존 인증 binding을 다시 검사한다. DB 잠금 전에 샘플링한 rotation 시각으로 만료된 proof를 승인하지 않는다.
같은 session ID의 payload가 바뀌어도 최신 값에서 proof를 제거하며, proof나 인증 binding이 바뀌면 이전 제출을 거부한다.
`Manager.CheckRecord`는 clock/entropy/I/O 없이 manager의 한도를 검사한다. 저장소보다 좁은 manager 한도도 commit 전에 적용한다.

현재 session이 reset 대상의 인증 session이면 다른 대상 session들과 함께 폐기하고 cookie를 지운다.
Anonymous 또는 다른 계정의 session은 proof를 제거한 최신 payload와 원래 lifetime으로 회전한다.
Password/현재 revision·대상 session 폐기·현재 proof 정리·audit는 한 native transaction에 속한다.
자동 로그인과 last_login 변경은 없으며 다른 계정의 유효한 인증을 유지한다. 실패는 별도 session cleanup이나 cookie를 게시하지 않는다.
확정된 ID collision만 새 entropy로 제한 재시도하고 hash를 반복하지 않는다. Unknown commit/rollback은 재시도하지 않는다.
확정 commit 이후의 늦은 취소는 이미 확정된 결과를 뒤집지 않는다.

Web runtime은 확정된 새/회전/삭제 cookie만 적용하며 별도 CSRF cookie를 유지한다. Zero·같은 ID·남은 proof·충돌하는 clear 결과 등
잘못된 persistence 결과는 cookie 없이 오류로 처리한다. Cause 없는 직접 proof/validation 거부와 cleanup/unknown 오류를 구분한다.
공개 Form/API 소비자는 entry의 token-free redirect·no-store/no-referrer와 제출 전 CSRF/confirmation을 소유한다.
제품 route·Form/JSON·OpenAPI·독립 client는 아래와 같이 이 runtime을 소비한다. 검증 환경과 실행 상태는 Evidence에서 구분한다.

## 공개 재설정 Form·JSON과 Article 구성

`identity/account.Config.PasswordReset`은 같은 persistence의 resetter로 구성한 mailer와 내부 오류 reporter를 요구한다.
여기서 reset service는 framework 내부의 token·password·transaction 코드이며 별도 서버 프로세스를 뜻하지 않는다.
정확한 reset owner·메일 링크 prefix와 cookie 범위를 startup에서 검사한다. 설정하지 않으면 기존 account route와 인증된 schema 조회를 유지한다.
Article의 `WithPasswordReset`은 같은 resetter/key/policy owner를 persistence와 mailer에 공유하고,
명시적 sender·origin·From·reporter와 공통 password 정책 및 reset 전용 정책을 함께 구성한다.

Reset email의 GET/POST Form, sent/complete page와 uid/token GET/POST route를 설치한다. 유효 email 입력은 대상 존재 여부,
active/usable·전달 거절/unknown·DB 오류·취소·sender panic과 무관하게 같은 Form redirect/JSON 204를 반환한다.
오류는 별도의 내부 reporter로 전달하고 reporter panic도 공개 응답을 바꾸지 않는다. 이는 실제 전달 성공이나 동일 처리 시간의
보장이 아니다. 전송은 자동 재시도하지 않는다. 주소 정규화·required/length/NUL/문법 검사는 기존 공통 cleaner를 사용한다.

`api.CSRFAuthentication.RequireCSRF`는 익명 요청에서도 origin/CSRF를 검사하되 principal resolution·session touch/cleanup·
model permission을 실행하지 않는다. Reset root의 GET은 cookie/header pair를 발급하고 POST는 email만 받는다.
Proof GET과 completion POST는 명시적 `CSRFOnly`와 `SessionCookieRequired` OpenAPI 계약을 사용하며,
handler가 현재 서버 proof를 검사한다. Schema 조회도 reset 활성화 시 익명 safe admission을 사용한다.
기존 password change API의 authenticated-only admission은 그대로 유지한다.

UID는 최대 128-byte principal ID의 canonical unpadded base64url이다. Raw token entry는 최종 proof 저장 뒤 곧바로
`set-password` URL로 redirect한다. Raw URL의 POST도 CSRF 뒤 proof만 교환하며 그 요청으로 password를 바꾸지 않는다.
Confirmation Form은 required·mismatch·policy 순서를 고정 Django와 대조하며 password를 재표시하지 않는다.
JSON completion은 `new_password`만 받고 raw token이나 confirmation 입력을 받지 않는다. 현재 proof 없는 hidden link는
일반 invalid-link page 또는 JSON 403 `invalid_reset_link`로 거부한다. 완료는 자동 로그인이나 last_login 변경을 하지 않는다.

Reset subtree와 활성 schema의 응답은 routing/negotiation 실패까지 no-store/no-referrer를 유지한다. 알려지지 않은
handler 오류·panic은 내부 보고 뒤 고정 500으로 처리한다. 최종 Web response budget 오류의 500도 같은 privacy header를 유지한다.
Unknown 완료는 503과 무쿠키/무재시도를 유지하며 typed JSON은 `outcome_unknown`을 반환한다. 상대·본문·query·중복/미지 필드와
cookie/CSRF 한도는 실제 HTTP 소비자가 검증한다. 독립 generated client는 실제 email의 URL을 따라 proof를 수립한 뒤
JSON으로 끝내며 부모가 별도 native runtime에서 password/revision/session/audit를 확인한다.

Form/API의 양 DB 재접속은 별도 연결/runtime의 재개 검증이다. 별도 server process restart, 운영 mail provider와 Hosted 전체
검증을 이 로컬 소비자 결과에서 추론하지 않는다. 실행 상세는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 둔다.

## 프로젝트가 소유하는 계정 역할과 생성 binding

[GDJ-0138](../../work/0138-custom-account-lifecycle.md)의 계정 선택은 `codegen.ProjectSpec.Account`에 놓는
`ir.AccountRole`이다. 설치한 model·logical field·M2M의 실제 IR에 principal/login/password/revision/last_login,
active/staff/superuser와 permission catalog 역할을 대조한다. 기본 User도 같은 선언을 사용한다.
Email과 login은 같은 unique field를 사용할 수 있다. Boolean 역할은 저장된 필드 또는 명시적인 상수 중 하나이며,
상수의 false·true와 미선택은 구분한다. 저장할 login·공통 로그인 Form·인증 조회의 정규화 정책을 각각 명시한다.
기본 계정은 저장과 Form에 NFKC, 인증 조회에 exact를 선언한다. Form을 거치지 않는 인증 호출에 Form 정책을 적용하지 않는다.
선택적인 `CreatedAt`은 nullable이 아닌 DateTime의 서버 생성 시각을 가리키며 기본 계정은 `date_joined`를 선택한다.
사용자 정의 모델은 이 역할을 생략할 수 있고 이름으로 추론하지 않는다.

Email 역할에는 저장 정책 `exact` 또는 `unicode16_email_domain`을 명시한다. 후자는 변환 전 UTF-8·NUL·byte 한도를
검사한 뒤 고정 Unicode 16 공백 처리와 마지막 `@` 뒤 domain의 전체 소문자 변환을 사용하며 local part는 보존한다.
Email이 없으면 이 정책도 없다. Login과 email이 같은 필드면 login 변환 뒤 email 변환을 적용하고 결과 한도를 다시 검사한다.
어느 정책도 암묵적인 대소문자 무시 조회나 unique 비교를 만들지 않는다. 저장·Form·인증 정책은 계정 역할 fingerprint에
포함되므로 정책 변경 시 기존 저장 domain을 그대로 사용할 수 없다.

계정 역할은 프로젝트 source identity이며 model의 물리 DDL history가 아니다. 역할 변경은 생성 snapshot과
프로젝트 명령 digest를 바꾸지만 이미 적용한 migration 정의를 고쳐 쓰지 않는다. Private wire와 사본은 역할과
상수의 소유권을 유지하며, 닫힌 JSON shape·문자열·aggregate node 한도를 decode/clone 전에 검사한다.

계정을 선택한 프로젝트의 `BindAccount`는 생성된 실제 모델 타입, descriptor와 전체 host 관계 그래프를
`identity/model.Binding[M]`에 묶는다. 개별 필드를 SQL이나 reflection으로 다시 해석하지 않는다. Profile의
추가 관계와 외부 앱도 이 그래프에 포함한다. 앱 설치만으로 계정이 암묵적으로 선택되지 않는다. 선택이 없는
프로젝트도 현재 생성 roster와 source marker를 유지하며, typed account 함수는 선택한 프로젝트에만 생긴다.

이 binding은 모델 설정과 타입의 일치를 확인한다. 인증·인가·계정 생성·저장된 domain 전환의 승인이 아니다.
일반 typed ORM 접근 권한과 identity runtime의 현재 권한·credential·revision·session/audit fence를 구분한다.
사용자 정의 모델을 인증 runtime에 연결할 때는 저장된 domain과 선택을 확인하고, 모델 또는 identity 정책 변경은
명시적 mapping·receipt·session/reset 정책을 갖춘 전환으로 처리해야 한다. 그 runtime·이행·Form/API·실제 환경의
완성 여부는 GDJ-0138과 TEST_EVIDENCE에서 따로 추적한다.

### 선택 모델의 공통 로그인 Form

`LoginFormNormalization`은 `exact` 또는 `unicode16_nfkc`를 필수로 선언한다. 정규화 원본·private wire의 닫힌 shape와
실제 byte 예산·생성 snapshot/ABI·역할 fingerprint에 같은 선언을 넣는다. 누락된 과거 선언에 기본 정책을 보충하지 않는다.
DB가 있는 프로젝트에서 이 선언을 바꾸는 것은 단순 화면 변경이 아니며 기존 domain의 명시적 전환 경계를 따른다.

`Binding.LoginInput`은 선택한 실제 login field의 GoName과 MaxLength를 불변 `auth.LoginInput`으로 투영한다. 길이가 없는
Text 또는 공통 인증 한도보다 큰 필드는 1,024의 Form 문자 한도를 사용하고 인증의 1,024-byte 한도도 유지한다.
`CredentialStore`→`StoredAuthenticator`/선택 인증기→`sessionauth.Runtime`은 같은 credential source의 metadata만 전달한다.
Metadata 제공은 DB 조회나 password work를 하지 않는다. 일반 인증기에는 이 추가 capability를 강제하지 않지만,
공통 계정 Application과 Admin Site를 조립할 때 누락·잘못된 metadata는 시작 오류이며 기본 User로 대체하지 않는다.
기존 Directory 인증기는 자신의 명시적인 기본 계정 binding으로 이 capability를 제공한다. MemoryAuthenticator는
Form을 제공하는 소비자가 생성 시 넘긴 불변 선언만 반환하며 이를 생략한 일반 인증에 Form 정책을 추론하지 않는다.

`forms/auth.NewLoginSpec`은 공통 계정 Application과 Admin의 같은 AuthenticationForm 정의다. Wire field 이름은
`username`을 유지하되 label·문자 한도·Form 정규화는 선택 인증기에서 받는다.
고정 Unicode 16 공백 정리 후 exact는 원문을 유지한다. NFKC는 변환 전 byte·문자 한도를 확인하고 변환 후 문자 한도를
다시 적용한다. 두 Form은 정리된 username의 1,024-byte 한도와 password의 명시적 byte 한도도 인증 전에 검사한다.
Password 공백은 보존한다. 선택 필드가 Email이거나 생성 choices를 가지더라도 로그인에 해당 모델의
생성 validation을 암묵적으로 적용하지 않는다. 이후 인증기의 exact/NFKC 조회·현재 credential/active/권한 재검사와
동일 session owner의 원자적 login persistence가 최종 인증을 담당한다.

Admin은 같은 Form을 시작 시 준비하고 실제 login field의 label과 maxlength를 HTML에 이스케이프해 표시한다.
엄격한 요청 파싱과 CSRF 검사 뒤에 Form을 bind하며, 입력 실패와 인증/입장 거부는 같은 invalid_credentials 화면으로
응답한다. Password를 다시 렌더링하지 않는다. Form 통과 뒤의 `LoginStaff`는 active staff와 명시적 AccessPermission을
검사하며 선택 LoginPersistence가 저장 fence에서 같은 admission을 다시 확인한다. 이 Form 조립이 account/catalog
전체 Admin 등록, 관리/reset·scaffold runtime의 완성을 대신하지 않는다.
실제 생성 소비자와 환경별 실행 상태는 GDJ-0138 및 TEST_EVIDENCE에서 추적한다.

## 계정 도메인을 구분하는 credential과 actor

`auth.NewCredentialForDomain`은 명시적인 `CredentialDomain`을 session stamp의 v2 입력에 포함한다.
같은 principal ID·encoded password여도 다른 도메인이나 도메인이 없는 credential의 stamp를 받아들이지 않는다.
Username·현재 role/grant의 행 값만 바뀌면 같은 도메인의 stamp를 유지한다. 새 생성 API가 입력받는 도메인은
비밀값이나 인증 수단이 아니라 소유권 식별자이며, 구성만으로 저장 상태의 일치나 계정 전환을 승인하지 않는다.
기존 독립 memory/legacy 인증의 도메인 없는 constructor와 의도된 operator adoption은 별도 계약을 유지한다.

도메인은 credential이 반환하는 Principal에도 보존한다. 이미 묶인 Principal로 credential을 만들 때 도메인을
지우거나 다른 도메인으로 덮지 못한다. 관리 writer는 현재 저장 도메인과 `MatchesPrincipal`을 대조한 뒤 같은 ID의
현재 권한을 읽어야 한다. 세션 검사만으로 인증과 최종 write 사이의 모델 전환 경합을 막았다고 보지 않는다.
이 일치 검사는 active·staff·permission·Authorizer의 현재 판단을 대신하지 않는다.

프로젝트 binding은 정규화된 계정 역할의 fingerprint와 32-byte 소유권 세대로 도메인을 유도한다.
일반 profile 필드 추가는 계정 소유권 변경이 아니며 schema/migration 검증을 별도로 유지한다. 계정 선택·필드 역할·
상수 보안 정책의 변경은 fingerprint를 바꾼다. 명시적 전환은 새 세대를 저장해야 하며 이전 모델로 돌아갈 때도 재사용하지 않는다.
`PrepareCredential`은 실제 generated descriptor의 typed 행과 완전한 권한 snapshot에서 필드 역할·IR 문자열 한도·
revision·저장 key의 존재를 검사한다. 명시적으로 저장한 0/음수 AutoField key를 미저장 값과 혼동하지 않는다.
이 순수 준비 단계는 I/O·해싱·정규화·profile validation·권한 관계 조회나 durable domain 검증을 수행하지 않는다.

독립 `godj_account_domain` 앱은 계정 타입에 FK를 두지 않는 추가 전용 전환 이력을 소유한다.
선언·생성 root는 `account/domain`이며 기본 계정의 `identity` 생성 root 안에 중첩하지 않는다. 각 프로젝트는 자기
manifest의 파일만 게시하며 다른 프로젝트의 generated 파일을 소유하거나 무시하는 예외를 추가하지 않는다. Schema IR에서 생성한
모델과 독립 초기 migration을 사용하며 기본 User와 옛 system migration에 의존하지 않는다. 이미 적용한 identity/system
migration bytes를 수정하지 않는다. Sequence·operation ID·generation은 각각 unique이며 이전 receipt의 FK는 PROTECT다.
첫 기록은 predecessor가 없고 이후 기록은 직전 sequence를 가리킨다. 현재 조회는 최신 두 기록의 이 연결과 필드 형식을
검사한다. 임의 SQL writer의 역사 삭제·변조까지 방어하는 저장소 무결성 증명으로 표현하지 않는다.

`domain.Prepare`는 선택 역할과 전환 근거의 SHA-256에 새 256-bit operation ID·generation을 결합한다. 준비는
인가나 mapping 실행 증거가 아니다. `InitializeIn`/`TransitionIn`은 호출자가 소유하는 native coordinated transaction에
참여하며 commit하지 않는다. 초기화는 비어 있는 domain 이력을 요구하고, 전환은 예상 receipt 전체를 현재 이력과 비교한다.
이미 기록한 operation ID의 재실행은 stale 오류이며 과거 receipt를 새 작업의 성공으로 반환하지 않는다. 다른 모델과
이전 모델로의 복귀 모두 새 generation을 갖는다. 순서가 timestamp에 의존하지 않도록 sequence를 사용하고 시각은 UTC microsecond로 저장한다.

호스트는 같은 transaction에서 최초 계정 생성 또는 검증한 mapping, source retirement, session/reset 정책과 audit를
처리해야 한다. 중간 실패는 함께 rollback하며 반환된 receipt는 commit 전 잠정 값이다. Commit/rollback unknown은
자동 재시도하지 않는다. 보존한 operation ID로 `Lookup`하면 이미 supersede된 기록도 확인할 수 있다. 기록 부재만으로
unknown write의 rollback을 확정하지 않고 원래 write fence와 작업 상태를 함께 조정한다. 근거 digest의 원문과 mapping의
실행·대조는 상위 전환 작업이 소유한다. 이 API만으로 임의로 채운 DB의 전환을 승인하지 않는다.

`RequireIn`과 generated binding의 `RequireDomainIn`은 계정 조회와 같은 stable snapshot/write handle에서 선택 역할을
확인한다. `RequirePrincipalDomainIn`은 그 세대로 actor를 검사한 후 현재 ID/권한을 읽게 하는 경계이며 active·permission
판단을 대신하지 않는다. `Read`/`Lookup`은 정확히 한 snapshot callback과 cleanup이 성공한 뒤에만 값을 게시한다.
이전에 얻은 receipt를 현재 권한 cache로 쓰지 않는다. 기존 Directory/Manager·startup·첫 계정 명령·Form/Admin/API와
채운 DB의 명시적 mapping 연결은 GDJ-0138에 남아 있으며, 이 기반을 전체 모델 전환 보호 완료로 표시하지 않는다.


## 선택 계정과 권한을 읽는 공통 저장소

생성 프로젝트의 `NewAccountCredentialStore`는 선택한 실제 계정·Group·Permission descriptor를
`identity/model.NewCredentialStore`에 묶는다. caller가 SQL이나 권한 loader를 다시 작성하지 않으며, 기본 User와
이름·필드·catalog가 다른 모델에 같은 runtime을 사용한다. Backend는 native `ReadSnapshot`을 제공해야 한다.
구성은 I/O 없이 같은 project owner와 정확한 IR 역할을 검사한다.

한 번의 읽기는 저장된 domain·계정 행·직접 권한·소속 그룹을 통한 권한을 같은 snapshot에서 관측한다.
권한은 선언된 M2M을 따르는 typed query와 scalar projection으로 읽고 정렬·중복 제거한다. 선택 계정의 권한을
256개보다 많이 발견하면 잘라서 반환하지 않는다. 소속 그룹 집합도 공통 `MaximumAccountGroups`의 256개 한도를
따른다. 그룹 key와 revision을 함께 최대 257개 읽으므로 같은 권한이나 같은 revision을 가진 여러 그룹을 하나로
세지 않는다. 그룹 입력·저장 집합·유효 권한 합집합은 독립된 제한이며 초과한 현재 계정이나 관리 대상을 거부한다.
잘못된 계정·소속 그룹·권한 revision과 비정규 권한 code도 거부한다.
명시적으로 저장한 0/음수 account key를 허용하며, 다른 계정이나 미할당 catalog 항목을 대신 읽지 않는다.
계정·query 결과·권한의 전역 cache는 없고 이전 Principal의 detached 권한은 다음 관측과 독립적이다.

`orm.BindInverseManyToManyQuery`는 명시적인 forward collection owner와 target을 받아 그 선언을 역방향으로 조회한다.
공개 reverse accessor가 없는 관계도 같은 physical hop·Query AST를 사용한다. 이는 reverse namespace나 mutation
권한을 만들지 않으며 다른 project snapshot·모델·필드와 섞지 못한다. Runtime이 역방향 이름을 추측하지 않는다.

`CredentialByUsername`은 canonical login을 정확히 조회한다. 생성 `NewAccountAuthenticator`와 공통
`identity/model.Authenticator`가 선택된 인증 입력 정책을 적용한다. Unicode 16 NFKC 선택 시 입력의 원래 byte/UTF-8
한도를 먼저 확인하고 변환 뒤에도 공통 인증 한도를 적용한다. Exact 정책이나 대소문자를 암묵적으로 바꾸지 않는다.
Hash·dummy 검증·encoded profile 검사는 DB snapshot 밖에서 실행하고, 기존 StoredAuthenticator가 검증 뒤 현재
도메인·credential·active와 권한을 새 snapshot에서 다시 읽는다. 한 번의 callback 및 read cleanup·취소 확인이
성공하기 전에 credential을 게시하지 않으며 저장 오류의 private cause는 진단 문자열에 노출하지 않는다.
공통 `ReadSnapshotResult`는 callback의 reader에 live `SessionValidator`를 요구하고 root backend·쓰기/transaction
capability를 거부한다. Queryer를 감싸는 관측 도구도 실제 borrowed scope의 검증을 전달해야 한다.

이 저장소는 credential 관측 경계다. Typed profile 관리·인가된 catalog 변경·첫 계정 명령·session/reset/audit
write fence·채운 DB mapping은 같은 binding에 연결해야 하며 GDJ-0138에서 계속 추적한다.


공통 저장소의 `AuthorizeIn`은 이미 인증된 Principal을 live borrowed snapshot/write scope에 전달받는다.
현재 저장 domain이 claim과 일치하는지 먼저 확인하므로, 다른 세대 또는 domain 없는 actor의 같은 ID를 새 계정으로
조회하지 않는다. 이어 같은 scope에서 현재 active·role·직접/그룹 권한을 다시 읽고 모든 필수 권한을 확인한다.
Authorizer에는 이 현재 Principal만 전달하며 저장 grant를 늘릴 수 없는 deny overlay로 사용한다.

확정 거부만 anonymous Principal·false·nil로 반환한다. 잘못된 선택 역할, I/O, 만료 scope, 취소, overlay 오류는
Principal 없이 error로 반환하며 다른 권한의 대안으로 넘어갈 수 없다. Required permission은 정규·비중복·한도 내의
비어 있지 않은 conjunction이어야 한다. Root backend와 만료된 handle은 거부하며 callback 뒤에도 scope와 취소를 확인한다.
이 API는 transaction이나 password 작업을 시작하지 않는다. Overlay는 owner를 재진입하거나 scope를 변경하면 안 된다.

반환 Principal은 해당 scope의 잠정 관측이며 다음 쓰기에 재사용하는 권한 증표가 아니다. 관리 owner는 같은 scope에서
대상 revision·typed 입력·관계·session/audit를 처리하고 commit 또는 read cleanup이 확인된 뒤에만 결과를 게시한다.
아래 typed profile owner는 이 경계에서 실제 수정과 audit를 묶는다. 기존 기본 Directory/Manager의 교체와 공개
관리 소비자 전체의 연결은 GDJ-0138에 남아 있다.

## 선택 계정의 typed profile 수정

생성 `NewAccountProfileEditor`와 공통 `identity/model.ProfileEditor[M]`은 선택한 모델의 scalar·FK·OneToOne profile 수정을 소유한다.
서버가 명시한 field 목록만 허용하며 새 필드를 자동 노출하지 않는다. Password·principal identity·revision·선택한
서버 시각·active/staff/superuser·권한 관계와 NonEditable 필드는 입력에서 제외한다. FK·OneToOne은 아래의 명시적
`ProfileRelation` 정책을 요구한다. 파일은 아래의 명시적 `ProfileFile`·`UpdateForm` 계약을 따른다.
M2M·생성·삭제와 role/catalog 변경은 별도 owner의 작업이다.

입력은 Form/JSON의 cleaning을 거친 `orm.ModelInput[M]`이다. 준비 단계에서 현재 모델 source·허용 필드·기존 target
binding과 collection 의도를 확인한다. 이 owner는 transport parsing을 반복하지 않는다. 쓰기 전 고정 login/email
정규화와 IR 문자열 한도를 검사하며, 필요하면 typed `ProfileValidator`가 같은 scope에서 현재 애플리케이션 정책을
확인한다. Validator에는 분리된 현재/후보 모델 값을 전달한다. 빌린 reader를 보관하거나 중첩 transaction·쓰기·외부
부작용을 일으킬 수 없으며 callback 뒤 취소·scope 수명을 다시 검사한다. 구성과 callback은 동시 호출에 안전해야 한다.

`Update`는 같은 native coordinated transaction에서 현재 domain·actor 권한·deny overlay를 확인하고 대상 행을 새로
읽는다. 대상의 expected revision과 credential·실제 권한 관계 무결성도 검사한 뒤 typed CRUD의 unique 검증과 강제
update를 사용한다. 사라진 행을 insert로 대체하지 않는다. 실제 값이 바뀔 때만 revision을 증가시키고 domain에 묶인
audit를 추가한다. No-op도 현재 권한·대상·revision 검사를 생략하지 않으며 revision/audit는 추가하지 않는다.

`db.CoordinatedAtomicResult`는 조정 없는 Atomic으로 대체하지 않으며 정확히 한 callback과 확정 commit 뒤에만
ID·revision·변경 필드 이름을 반환한다. 원본 계정·hash·principal identity는 결과에 없다. 일반 오류는 private cause를
포함한 진단 문자열을 숨기되 `errors.Is`/`As` 연결을 유지한다. 직접 발생한 원인 없는 validation 거부만 확정 rollback
뒤 renderable 결과로 돌려주고 cleanup 실패나 unknown outcome으로 덮인 거부를 정상 입력 오류로 축소하지 않는다.
Commit/rollback unknown은 자동 재시도하지 않는다. 확정 commit 뒤 도착한 취소는 성공을 되돌리지 않는다.

## 선택한 프로필 조회와 현재 이력

생성 `NewAccountProfileReader`와 `identity/model.ProfileReader[M]`는 선택한 모델의 scalar 조회·목록·현재 domain
감사 이력을 소유한다. `ProfileReadConfig`의 명시적 필드만 `ModelView[M]`에 담으며 원 계정 행·hash·principal
identity·권한 집합을 보관하지 않는다. Password와 principal identity는 선택 자체를 거부한다. Key·revision·서버
시각·role 값은 읽기 정책에서 의도적으로 선택할 수 있지만 자동 노출하지 않는다. 이것이 ProfileEditor의 쓰기
금지를 해제하지 않는다. 선택 계정 역할의 Groups/Permissions만 명시적으로 상세 조회에 넣을 수 있으며,
`Fields`는 전체 선택을, `ListFields`는 scalar 선택만 반환한다. 그룹과 직접 권한의 ID는 실제 source에 묶이고
빈 집합과 미로드를 구분한다. Collection만 선택한 상세도 유효하며 scalar 값은 노출하지 않는다.
File/Image도 명시적으로 선택한 저장 이름만 읽으며 저장소를 열거나 URL·다운로드 권한을 만들지 않는다.
그 밖의 M2M 표현과 과거 domain 조회는 각각 별도의 정책이 필요하다.

View permission 또는 명시한 change permission 중 하나와 모든 추가 권한이 필요하다. 현재 actor·domain·실제
권한을 같은 native snapshot에서 읽으며, 확정된 view 거부에서만 change 대안을 검사한다. 조회/overlay 오류는
대안 인가나 일반 permission denial로 바꾸지 않는다. `Get`과 `History`는 대상의 실제 key·credential·권한 관계
무결성을 확인한다. `List`는 같은 snapshot의 count와 기본키 순서의 제한된 page를 읽는다. 대상별 권한 집합을
반환하지 않으므로 N+1 권한 조회는 하지 않고 scalar credential 상태를 확인한다. 존재하는 0/음수 key도 유지한다.

`Profile`의 ID·revision은 후속 작업을 준비하는 관측 값이다. 다음 조회나 수정은 이를 권한 증표로 사용하지 않는다.
`History`는 현재 domain으로만 기록을 선택하므로 이전 세대의 같은 모델/ID 기록을 물려받지 않는다. 확정 거부와
not-found도 snapshot 종료가 성공해야 반환한다. 종료 실패·취소·scope/callback 소유권 위반에서는 선택 데이터·
count·이력을 모두 버리고 비공개 원인을 보존한 persistence 오류를 반환한다. 새 읽기에서 취소를 확정 write의
성공으로 해석하거나 unknown commit으로 보고하지 않는다.

## 선택 계정의 typed Group·Permission 관리

생성 `NewAccountCatalog`와 `identity/model.Catalog[G, P]`는 선택 계정의 정확한 SessionStore와 현재 권한을 실제 Group·Permission 타입의
CRUD에 연결한다. 생성 팩토리의 예상 role·전체 host source가 session owner와 같아야 한다. `Groups()`와 `Permissions()`의 `CatalogResource[M]`는 각각 명시적 읽기·쓰기 필드, view/add/change/delete와
추가 권한을 소유한다. 조회는 확정된 view 거부에서만 change를 검사하고 추가 권한은 두 경로 모두 필요하다.
Metadata용 `Manager()`는 Form/API 선언에 쓰는 순수 typed source이며 backend나 관리 권한을 포함하지 않는다.

기본 name/code 역할은 쓰기 목록에 필요하고 추가 scalar 필드는 실제 생성 타입과 IR default·unique 검증을 사용한다.
Key·revision·NonEditable·파일·FK와 선택한 그룹 권한 이외의 M2M은 쓰기에서 제외한다. 읽기 필드는 자동으로 늘어나지 않는다.
`ModelInput[M]`의 전체 모델 source·target 존재와 허용 필드를 I/O 전에 검사하고, Form/JSON의 생략·NULL·명시적 빈 집합을
보존한다. Group permission의 raw 개수는 중복 제거 전 256개로 제한한다. Custom validator에는 같은 borrowed scope의
분리된 실제 모델을 전달하며 생성 때 current는 nil이다. 쓰기·scope 보관·중첩 transaction·외부 부작용을 허용하는 callback이 아니다.

현재 actor/domain, 최신 대상과 expected revision, scalar·관계·unique·custom validation, 저장과 값 없는 audit는 같은
native coordinated relation transaction에서 처리한다. `CoordinatedRelationAtomicResult`로 관계 삭제와 conflict insert 기능을
같은 scope에 보존하며 일반 쓰기 scope로 대체하지 않는다. 생성 revision은 1이고 수정은 실제 변경 때만 증가한다. No-op도 현재 인가와
대상 무결성을 확인한다. 결과는 확정된 ID·revision·변경 필드 또는 삭제 건수뿐이다. Profile과 같은 공통 결과 경계에서
cleanup/unknown 원인을 보존하며 실패 결과 게시와 자동 재시도를 막는다.

권한 확장은 선택 계정의 모든 해당 회원을 256개 keyset batch로 읽어 현재 상태와 제안된 유효 권한 합집합을 확인한다.
첫 page는 ID 하한을 두지 않아 명시적으로 저장된 0/음수 회원을 포함한다. 직접 grant·다른 그룹·새 grant를 합친 256개 한도는
서로 다른 그룹의 동시 변경에도 같은 fence로 보호한다. Group 삭제는 직접 회원, Permission 삭제는 직접 할당 계정과 소유
Group의 revision을 증가시킨다. 전체 삭제 영향과 합쳐 한 owner의 revision을 한 번만 증가시킨다. 간접 회원의 revision이나
credential/hash/session stamp를 변경하는 작업으로 확대하지 않는다. 명시적 through의 중복 링크 일부만 사라져 실제 소속 집합이
유지되면 해당 owner의 revision을 증가시키지 않는다.

상세 조회는 출력에서 collection을 제외했더라도 저장된 전체 그룹 grant의 무결성을 검사한다. 명시한 collection만 선택
`ModelView`에 넣으며 로드한 빈 집합과 미로드를 구분한다. 목록은 같은 snapshot의 count와 최대 100개 scalar page이고
행마다 grant를 읽지 않는다. 이력은 현재 domain의 존재하는 대상에 한정한다. 읽기 종료 실패에는 선택 값·page·이력이 없다.

전체 host source의 collection과 삭제 기능을 생성 단계와 runtime 시작 시 함께 확인한다. Catalog 행 삭제는 아래의 공통
선택 계정 수명 owner로 추가 계정/Group/Permission의 CASCADE·SET_NULL과 session·revision·audit를 조정한다.
`CatalogConfig.AccountPermissions`는 직접 영향을 받는 선택 계정의 delete/change·추가 권한이다. Group/Permission에는 각
resource의 기존 권한을 사용한다. Catalog 생성/수정/삭제가 계정도 지우면 반환값의 `RevokedSessions`에 같은 domain의 실제 폐기 수를 담는다.
Group grant 교체는 아래의 공통 전체 수명 조정을 사용한다. Incoming CASCADE/SET_NULL이 추가 계정/catalog이나 다른 소속의
through를 바꾸는 조합도 전체 최종 상태로 검사한다. Through 모델 자체가 선택 계정/Group/Permission이면 새 링크 생성에도
별도의 identity 생성 owner가 필요하므로 해당 collection을 쓰기로 노출할 때 `unsupported`로 거부한다. 이 저장/생성 조립은
잔여 요구이며 읽기나 기존 전체 수명 owner의 행 삭제를 함께 차단하지 않는다.
공통 Admin과 JSON 조립은 아래의 선택 Catalog/Account 등록과 관리 JSON을 사용한다. 기존 고정 모델 Manager host 교체는 남아 있다.

## 선택 계정과 catalog 삭제의 전체 수명 조정

생성 `NewAccountDeleter`와 `AccountDeleter[M]`는 정확한 SessionStore·계정 binding·typed catalog descriptor·전체 host의
삭제 정책을 조립한다. 현행 account-project 생성 ABI를 사용하며 과거 backend-only catalog 팩토리를 위한 호환 경로를 두지 않는다.
`Delete`는 현재 root 삭제 권한·추가 권한을 확인한 뒤 실제 대상과 expected revision을 검사한다. 명시적으로 존재하는
0/음수 key를 허용하며, 반환값은 확정된 ID·revision·삭제 행 수·폐기 session 수뿐이다. 삭제 권한으로 profile을 공개하지 않는다.

공통 관계 삭제기의 전체 수집·PROTECT·rowset 정리 뒤 한 번의 callback에서 선택 계정/Group/Permission의 효과를 조정한다.
직접 삭제되는 각 선택 모델에는 명시한 `DeletionPermissions.Delete`, 살아남아 SET_NULL되는 모델에는 `Change`와 모든 추가
권한이 필요하다. Actor의 현재 domain·저장 grant와 deny overlay를 쓰기 전에 다시 검사한다. 일반 host 행은 선언한 관계 정책을
따른다. Django Admin의 삭제 대상별 권한 검사는 [공식 get_deleted_objects 계약](https://docs.djangoproject.com/en/6.1/ref/contrib/admin/#django.contrib.admin.ModelAdmin.get_deleted_objects)을 참조한다.
GoDj의 선택 모델 SET_NULL에 대한 명시적 change 권한과 revision·domain session 처리는 이 수명 owner의 추가 계약이며,
일반 host 모델의 Admin 등록·삭제 preview 전체 권한 대조를 완료했다는 의미가 아니다.

모든 직접 효과와 실제로 달라지는 직접 소속을 `(model, key)`별로 합친다. Through는 정규화 IR의 정확한 source/target 필드를
사용하여 같은 Query AST로 읽는다. 전체 key를 bounded batch로 읽고, owner별 전체 링크를 keyset으로 순회하여 삭제 전/후의
서로 다른 target 집합을 비교한다. 공개 reverse accessor·pair uniqueness를 가정하지 않고 중복 링크나 첫 batch만으로 결론내리지 않는다.
같은 account/catalog에 여러 NULL·소속 변경이 겹쳐도 revision은 한 번만 증가한다. 삭제될 행에는 revision 증가를 적용하지 않는다.

현재 typed 계정 credential·소속/권한 한도, catalog name/code·revision·grant를 모두 확인하고 모든 audit를 준비한 뒤 동반 쓰기를
시작한다. 살아남는 owner의 revision overflow나 뒤 owner의 손상도 앞선 변경을 남기지 않는다. Collector가 PK/FK 변경을 소유하므로
callback은 revision만 수정하고 수집한 topology를 변경하지 않는다. 삭제되는 계정의 현재 domain session만 폐기하며 익명·다른 계정·
과거 domain의 bytes를 유지한다. LoginPersistence와 같은 native write fence를 사용해 동시 login이 삭제 뒤 session을 남기지 못하게 한다.

직접 삭제되는 각 선택 모델 행에는 값 없는 delete audit, 직접 NULL되는 생존 행에는 전체 NULL·함께 바뀐 소속 필드의 change audit를
한 번 기록한다. 소속 변화만 받는 owner에는 기존 계약대로 별도의 편집 audit를 만들지 않는다. 삭제와 일시적 NULL이 겹치면 delete가
우선한다. 현재 actor가 자기 삭제나 cascade 대상이어도 쓰기 전의 검증된 actor/domain으로 감사를 남기고 성공 뒤 재조회로 결과를 뒤집지 않는다.

조회/정리·인가·revision·session·audit·뒤 관계 DML 실패는 하나의 transaction에서 rollback한다. Callback의 정상적인 거부는 scope가
유효할 때 원래 오류 identity를 보존하고, 취소/만료가 겹치면 이를 함께 유지한다. Unknown/cleanup에는 성공값을 게시하거나 자동 재시도하지
않는다. 공개 계정 Admin/HTTP 조립과 통합 restart 수용은 I1의 잔여 요구다.

## Collection 편집의 전체 최종 상태와 생성 효과

`AccountEditor.Update`의 모든 명시적 collection과 catalog Group grant 교체는 하나의 `PrepareCollectionChangesInSession`에
모인다. 현재 선택과 같아 보이는 입력도 준비에 포함하여 다른 collection의 cascade로 사라질 보존 링크를 다시 삽입할 수 있게 한다.
공통 수명 owner는 전체 삭제/NULL과 typed 삽입 값을 합쳐 각 owner의 삭제 전/후 distinct target 집합을 계산한다. 현재 모든
계정 credential·소속/유효 권한·catalog scalar/revision과 최종 typed 후보·소속/권한 한도를 모두 검사한다. 그룹 grant만 바뀌는
간접 회원도 완전한 keyset 순회로 확인하지만 그 계정의 직접 소속이 같으면 revision과 별도 audit를 늘리지 않는다. 최종적으로
삭제될 권한을 중간 합집합에 남겨 정상적인 256개 후보를 거부하지 않으며, 반대로 간접 회원의 최종 257개 확장도 허용하지 않는다.

편집 root가 자기 의도의 cascade로 삭제되는 경우에는 `conflict`로 전체를 거부한다. Root의 scalar·NULL·실제 collection 변경을
한 번 합쳐 revision과 audit를 만들고, 다른 직접 선택 모델 효과는 각각 명시한 delete/change 및 추가 권한을 요구한다.
`AccountEditConfig.RelatedPermissions`에서 생략한 operation은 그 효과를 허용하지 않는다. Root 자체의 NULL은 editor의 전체
change 정책에 포함된다. 기본 인가와 deny overlay를 모두 적용하고, 삭제되는 계정과 비활성화되는 root의 현재 domain session을
같이 폐기한다. 다른 계정·익명·과거 domain session은 보존한다.

Typed validator는 분리된 현재 값과 전체 NULL·다음 revision을 반영한 최종 후보를 받으며 계정 validator는 최종 소속 사본도 받는다.
이는 임의 `full_clean` 실행을 추가하는 의미가 아니다. 수집한 graph의 실행과 동반 revision/session/audit 뒤 root의 실제 최종 unique
검증과 typed 저장을 수행한다. 최종 내부 재조회는 모든 관련 typed scalar·revision·소속·유효 grant, 삭제 행 부재를 검사하며,
편집자의 자기 비활성화·권한 상실·cascade 뒤 새 public view 권한을 요구하지 않는다. Unique 또는 최종 재조회 실패도 앞선 모든 DML을
rollback한다. PK/FK topology는 공통 collector/collection engine이 소유하며 callback이 별도 변경하지 않는다.

Catalog 생성은 먼저 같은 transaction에서 provisional INSERT로 generated key를 얻고, 그 뒤의 실제 링크 상태를 수집한다.
최종 root revision은 1이고 add audit는 한 번이다. Validator의 current는 nil이며 candidate에는 실제 key와 최종 NULL이 있다.
준비·인가·validation·쓰기/읽기 거부는 이 provisional row와 뒤 효과를 모두 rollback한다. 생성 전 같은 fence에서 확인한 actor의
권한을 효과 인가에도 사용하며, INSERT에 따른 DB 상태 변경으로 새로 얻은 grant를 추가 효과 권한으로 사용하지 않는다. Domain과
deny overlay는 같은 scope에서 계속 확인한다. 이 생성 의미와 별도의 선택 모델-as-through identity 생성 지원은 구분한다.

## 선택 계정의 typed 생성과 해시 준비

생성 `NewAccountCreator`와 `identity/model.Creator[M]`는 명시적으로 받은 동일 `SessionStore[M]`의 전체 host source·역할 정책·
실제 account Groups/Permissions capability를 사용한다. `CreationPolicy.Fields`는 login·scalar/FK/OneToOne/file profile·editable role flag와
직접 소속을 명시한다. 관계 프로필에는 명시적 `Relations`, 파일에는 `Files` 정책과 아래의 Form 생성 경로가 필요하다.
Principal identity·encoded password·revision·선택된 서버 시각은 입력으로 받지 않는다. 일반 ModelInput은 파일 참조·이미지 크기를 대입하지 못한다.
새 필수 profile을 쓰기 목록에서 빠뜨려 초기 값이 필수 NULL/빈 문자열로 남으면 구성 시 거부한다. 나머지 생략 값은
Schema IR default를 사용한다. 전송 계층은 먼저 실제 ModelForm/JSON cleaning을 수행하고 서비스에 `ModelInput[M]`을 넘긴다.

`Prepare`는 원 입력의 password byte 한도(기본 1024, 설정 가능 1..4096)와 소속 개수(중복 제거 전 각각 256), 모델 source·
선택 필드·unsaved target intent를 확인한다. ProfileEditor와 동일한 명시적 login/email write 정규화를 사용한다. Principal ID는
서버 source이며 기본값은 256비트 난수의 `user-`/hex 문자열이다. Revision은 1, 선택 CreatedAt은 서버 UTC microsecond,
선택 LastLogin은 NULL로 시작한다. 실제 typed 추가 필드와 상수 역할을 기본 User 모양으로 바꾸지 않는다.

현재 domain·add **및** change·추가 권한·deny overlay, 관계 선택과 저장된 catalog revision/code·유효 합집합·unique와
application/password policy를 한 native snapshot에서 검증한다. 해시가 아직 없는 사전 검사에서는 `PreparedModel.ValidateUniqueExcluding`
으로 password 필드와 그 필드를 포함한 **전체** 제약을 미룬다. 파일 게시 전의 추가 단계는 아래 계약을 따른다.
미지정·중복 제외 이름은 I/O 전에 거부하며, 임시 hash 값으로
다른 계정과 충돌시키지 않는다. 선택적인 생성 IExact 중복 정책은 write/authentication 정규화와 별개의 명시적 설정이다.

Snapshot 종료 성공 뒤 DB 밖에서 한 번만 hash하고 encoded profile·credential usability·저장 길이를 검증한다. Hasher의 직접적이고
원인이 없는 password input 오류만 일반 입력 거부로 반환한다. 취소·wrapped error·entropy·잘못된 해시는 원인을 비공개로 보존한다.
`PrepareUnusable`은 별도 명시적 진입점이며 hasher/strength policy를 호출하지 않는다. 빈 raw password는 이 경로를 선택하지 않는다.

`PreparedCreation[M]`은 비공개 모델·비밀번호를 보유하며 해당 creator pointer와 actor ID **및 domain**에 묶인다. 값 복사도 하나의
commit 시도를 공유한다. 다른 owner/actor/domain 또는 진입 전 취소는 시도를 소비하지 않는다. `Commit`은 현재 권한·선택지·전체
unique(실제 hash 포함)와 typed/password policy를 native write fence 안에서 다시 검사한다. Callback은 별도 모델 사본을 받고
password 필드는 비워 전달하며, 소속 getter도 사본을 반환한다. Callback은 reader를 보관/변경/재진입하거나 외부 효과를 내지 않는다.

Force insert로 얻은 실제 key는 잠정 값이다. 두 membership을 하나의 collection batch로 준비하고, INSERT 직후 존재한
관계까지 포함한 삭제·NULL·삽입 효과를 편집과 같은 수명 owner에서 검증한다. 추가 selected-model 효과에는
`CreationPolicy.RelatedPermissions`의 해당 change/delete·추가 권한이 필요하다. 같은 fence에서 INSERT 전에 확인한
actor만 효과 인가에 사용하며, INSERT가 새로 부여한 권한으로 추가 효과를 승인하지 않는다.

실제 typed 계정 행이 catalog 권한 관계의 through인 경우 그 행을 논리적인 이전 소속에서 제외하고 최종 소속에 반영한다.
현재/최종 권한 합집합·필요한 owner revision·값 없는 audit를 대조한다. 동일한 물리 link가 하나 늘어도 논리 소속이 같으면
추가 membership revision/audit를 만들지 않으며, 해당 owner의 현재 change 권한은 계속 필요하다. 이 endpoint의
`ProfileRelation`은 생성에서만 사용할 수 있고 ProfileEditor/AccountEditor의 scalar 편집에서는 구성 시 거부한다.
일반 collection setter가 선택 계정/catalog identity 행을 자동 생성하는 조립은 별도의 미완료 요구다.

관계·동반 scalar·세션 폐기·audit를 적용한 뒤 typed/password policy와 unique를 최종 그래프에서 다시 확인한다. 이 시점의
candidate에는 실제 key가 있고, 자기 행은 unique/IExact 조회에서 제외한다. Hash는 늘 한 번이며 validator에는 비밀번호를
비운 사본을 전달한다. 최종 scalar 저장과 전체 그래프 readback까지 같은 native transaction에서 확인한다.
공개 결과에는 ID·revision·변경 필드·폐기한 세션 수만 있고 implicit profile 조회 권한은 없다.
생성과 편집의 변경 필드 및 감사에는 같은 Schema IR 선언 순서를 사용한다. 저장 field 뒤에 collection을 두며,
강제 역할·password도 그 위치의 필드 이름만 기록한다. Password 값을 감사에 복사하지 않는다.
Cleanup/rollback/unknown 실패는 결과를 버리고 자동 재시도하지 않는다.
확정 commit 뒤 취소는 성공을 지우지 않는다. 시도를 가져올 때 공개 준비 객체의 비밀 참조를 해제하고 실제 callback 소유의
지역 값으로 넘겨, 잘못된 backend가 callback 종료 전에 반환해도 cleanup과 callback의 데이터 경합을 만들지 않는다.
Go 문자열 메모리의 강제 소거를 보장하는 API는 아니다. 준비 결과를 포기하면 참조를 버린다.

`CreationConfig`는 공통 `CreationPolicy`에 add/change·추가 권한을 결합한다. 이 owner는 이미 설치된 현재 domain의
인가된 생성 경로이며, 최초 domain 초기화는 아래의 별도 operator owner가 담당한다. 공개 `createsuperuser`와 추가 profile
prompt·공통 Admin/HTTP 설치·password/reset owner는 별도 I1 요구다. 외부 소비자의 전송 조립만으로 명령/설치를 완료 처리하지 않는다.

## 선택 계정의 최초 소유권과 계정 생성

생성 `NewFirstAccount`와 `FirstAccount[M]`는 명시적으로 조립하는 privileged operator 기능이다. 일반 사용자 인가의
우회 경로로 공개하지 않는다. 동일한 `CreationPolicy`·typed 필수 profile/default·정규화·unique·password policy·해시 준비와
실제 account Groups/Permissions 기능을 재사용하되 add/change 권한이나 임시 actor를 조작해 생성 권한을 만들지 않는다.
Active·staff·superuser는 서버가 모두 true로 고정한다. 해당 역할이 상수 false면 I/O 전에 구성 오류로 거부하며,
상수 true는 허용한다. 실제 boolean 필드는 입력으로 받지 않고 강제 값으로 준비하며 감사의 변경 필드에 포함한다.

`Prepare`와 최종 native write scope 양쪽에서 domain history와 선택 계정의 모든 행이 비어 있는지 확인한다.
정상 조회가 반환한 정확한 `ErrUninitialized`만 초기화 가능 상태다. 같은 sentinel을 포함한 저장/종료 오류는 초기화 허가가
아니며 기존 같은/다른 role history, 0/음수 key를 포함한 기존 계정도 거부한다. 기존 행 adoption·이력 초기화·모델 전환은 하지 않는다.

호스트가 지정한 canonical 비밀값 없는 declaration digest와 새 generation·operation ID는 DB 밖에서 준비한다.
`PrepareOperation`은 상위 process가 먼저 보관한 operation ID를 받지만 매번 새 generation을 만들며 자동 조회·재시도·복구를
의미하지 않는다. `Change.Generation()`은 잠정 namespace metadata일 뿐 current domain이나 인증 권한이 아니다.
비밀번호는 필수이고 usable credential이어야 한다. DB 밖 해시가 끝난 준비 객체의 복사본은 모두 한 번의 저장 시도를 공유한다.

`Commit`은 fresh-state 및 실제 hash를 포함한 최종 검증, domain 초기화, typed account insert·소속 저장·첫 audit를
하나의 coordinated relation transaction에 묶는다. 처음 생성한 domain-bound active/staff/superuser가 첫 감사의 actor다.
같은 생성 collection/lifecycle 경로로 through endpoint의 catalog 소속도 검증한다. 기존 계정이 없는 새 domain의
operator owner는 session revocation capability를 만들지 않으며, 예상 밖의 계정 폐기가 필요하면 실패한다.
관계 선택 callback에는 최종 검증에서도 인증 actor를 전달하지 않고 명시적 FirstAccount origin을 유지한다.
확정된 계정 metadata와 ownership receipt만 게시하며 실패/unknown 결과에는 잠정 계정이나 receipt가 없다.
준비 객체의 operation ID는 시도 뒤에도 남는다. Unknown 뒤에는 그 receipt와 write fence를 명시적으로 대조해야 하며,
조회에서 receipt가 없다는 사실만으로 rollback을 확정하거나 같은 입력을 자동 재실행하지 않는다.

이 기능은 fresh domain의 typed 저장 owner다. 공개 명령의 필수 profile prompt·비밀 입력 process·설치/startup과 기존 데이터의
mapping/retirement·password/reset·공통 Admin/HTTP 조립 및 독립 양 DB 수용은 I1의 남은 작업이다.

### DB 연결 전 선언과 명령 입력 준비

생성 `NewFirstAccountDefinition`은 실제 계정·Group/Permission descriptor, 전체 host 관계 source와 typed 생성 정책을
DB 없이 검증한다. `FirstAccountDefinition[M]`은 필드와 불변 source를 소유하며 `Bind`가 실제 snapshot/native write
backend를 받은 뒤 독립적인 최초 생성 owner를 만든다. 선언을 읽기 위한 가짜 backend·임시 actor·숨은 기본 User는 없다.
기존 생성 `NewFirstAccount`도 이 선언과 `Bind`를 합성한다. 일반 생성과 같은 내부 정책 검사를 사용한다.

`ProjectBinding.SourceSHA256()`은 정규화된 전체 모델과 incoming/forward/reverse/M2M 정책을 식별한다.
독립 binding과 schema 입력 순서가 달라도 같은 source는 같은 digest를 갖는다. 이 metadata digest는 인증이나 capability가
아니며 실제 typed 기능의 완전한 `ValidateSource` 검사를 대체하지 않는다. 선언 fingerprint는 이 source와 account role,
선택 필드·password 한도·대소문자 생성 정책·관련 효과 권한·호스트의 `EvidenceSHA256`을 묶는다. Go closure의 실제 동작을 주소로
해시하거나 검증했다고 주장하지 않는다. 호스트는 callback 정책이 바뀌면 그에 해당하는 evidence도 갱신한다.

`project/firstaccount.Command`는 생성 선언에서 실제 ModelForm을 만들어 DB 연결 전 입력을 준비한다. Description은
login을 첫 필드로, 나머지를 IR 선언 순서로 제공하며 필수/nullable/default 여부·입력 kind·정적인 선택값과 한도를
보존한다. 값은 선택값의 정확한 Form 표현이며 문자열 trim과 구분한다. Description은 복사본이고 제출 profile이나
password를 포함하지 않는다. 최대 31개 필드·scalar 각 4,096 bytes·총 입력 65,536 bytes, 필드당 256개 정적 선택값과 description
65,536 bytes의 한도를 적용한다. 이 전송 상한은 모델의 더 좁은 validation을 대신하지 않는다.

입력은 같은 description fingerprint와 정확한 필드 순서를 요구한다. 생략과 명시적인 빈 문자열을 구분하고 실제
`BindInstance`와 `BoundForm`을 거쳐 typed 입력을 보존한다. 임의 `map[string]any`나 기본 User DTO로 바꾸지 않는다.
FK/OneToOne key는 IR 기반 integer/null/default 규칙으로 파싱하고, 아래의 `ProfileRelation` 정책이 실제 DB의
현재 선택을 검증한다. 파일은 명시적 한도·보고 owner와 아래 수명 계약을 사용한다. Collection은 아래의 명시적 정수 목록 계약을 사용한다.
준비 객체는 하나의 Command에 속하며 복사본도 한 번의 실행 시도를 공유한다. `Clear`는 아직 시작하지 않은 준비만
포기하고 진행 중인 typed 입력을 변경하지 않는다. 실패 원인의 보존과 진단 비노출을 함께 유지한다.

Command는 명시적인 opener를 한 번 호출하되 실제 자원과 오류를 함께 반환할 수 있다. 호출한 process가 그 자원을
정확히 한 번 닫고, 확정된 생성 결과와 뒤따른 close/응답 오류를 구분한다. 준비·실행은 암묵적으로 연결하거나 닫거나
재시도하지 않는다. `Provision`은 상위 process가 보관한 operation ID와 실제 backend로 최초 owner를 호출한다.
이 Command만으로 공개 터미널·process·설치/startup의 CLI 수용을 주장하지 않는다.

### 선택 계정의 runtime process owner

`command.Config.FirstAccount`는 생성 모델을 import할 수 있는 별도 명령 runtime에 위 선언을 연결한다.
`Registry.Serve`는 `project/firstaccount/runner`의 private 선언 조회와 최초 생성 dispatch를 일반 command handler,
argv 값 파싱·로그·출력 proxy와 분리한다. 선언용 project runner에 생성 package를 import시키지 않는다.
선언 조회는 실제 typed Description을 반환하며 stdin을 읽거나 DB를 연결하지 않는다. 아래의 상위 process가 이 경로를
사용한다. 공개 `createsuperuser`와 Article/scaffold의 현재 조립은 아래의 공개 통합 경계를 따른다.

현재 private v4 wire의 단일 binary frame은 전체 길이·선언 fingerprint·상위 process가 먼저 보관한 operation ID·정확한 순서의
scalar 이름/존재 여부/값, collection 개수/signed int64 key, 구분된 파일 basename/길이/bytes와 raw password를 포함한다. 전체 bytes·개별 길이·중복·UTF-8/NUL scalar·EOF를 검사한 뒤
분리된 가변 버퍼를 만들며, 제출 값을 JSON·argv·환경에 싣지 않는다. 생략과 빈 값, 정확한 choice 표현을 유지한다.
Raw password의 전송 한도는 4,096 bytes이고 실제 선언의 더 좁은 한도와 정책을 다시 적용한다. 전송 단계가 별도의
문자 정리나 password 정책을 만들지 않는다. JSON Description/결과는 canonical closed shape와 한도를 검사한다.

Runner는 `Command.Prepare`로 실제 ModelForm을 DB 전에 검증하고 profile 버퍼를 해제한다. 실제 opener가 반환한 자원은
오류를 동반해도 한 번 닫으며, 비밀번호는 한 번의 `Provision` 호출 경계에서만 string으로 바꾼다. 읽기·opener·provision·
close·writer와 오류 분류 callback은 panic/Goexit를 포함한 defer 종료까지 기다린다. 입력과 panic/오류 원인은 출력하지 않는다.
호출이 자원을 반환하기 전까지는 callback이 그 자원의 정리를 소유한다. 자동 재시도·adoption·receipt 기반 재실행은 없다.

응답은 같은 operation의 `not_created`, `created`, `unknown`과 별도 `cleanup_failed`/`file_report_failed`를 게시한다. 실제 source/receipt와
일치하는 확정 commit은 이후 취소·close 실패로 지우지 않는다. Provision 중 비정상 callback 종료나 outcome-unknown은
잠정 성공을 게시하지 않으며 operation ID를 retained parent에 남긴다. Close는 유일한 결과 쓰기보다 먼저 끝난다.
확정 commit 뒤 응답을 못 쓰면 process는 private exit 86, close까지 실패하면 87을 보존한다. 파일 보고도 실패했으면
각각 88/89로 두 결과를 보존한다. Entry point는
`command.ExitCode`를 사용하고 Serve가 반환할 때까지 SIGPIPE 처리를 유지한다. 상위 process는 완전한 단일 request
전달·프로세스 종료 근거와 함께 판정해야 하며 이 exit 값만으로 임의 child의 성공을 만들지 않는다.

### 선택 계정의 터미널과 상위 process

`internal/projectcheck.RunFirstAccount`는 보관한 프로젝트 descriptor의 `commands_package`를 사용한다.
선언용 runner와 생성 모델을 사용하는 runtime을 각각 빌드하고 ABI를 확인하며, 전체 생성 bundle의 현행 source를
입력 전에 검사한다. 실제 runtime Description의 전체 schema source와 정규화한 선택 role fingerprint를 선언용
`ProjectSpec`과 대조한다. `identity/model.RoleFingerprint`는 이 대조를 위한 순수 metadata이며 typed binding이나
쓰기 권한을 대신하지 않는다. 일치하지 않으면 TTY를 읽거나 기본 User로 우회하지 않는다.

TTY는 실제 선언 순서의 scalar·static choice·명시적 local file path와 비밀번호를 한도 안에서 읽는다. 기본값/선택 필드의 생략은 별도 질문으로
명시하여 빈 문자열을 제출한 경우와 구분한다. 터미널에서 값의 정규화나 model validation을 다시 구현하지 않으며,
runtime의 실제 ModelForm이 이를 수행한다. 같은 터미널로 보낼 때만 profile을 echo하고 비밀번호는 echo하지 않는다.
비밀번호 확인은 원문 bytes를 비교한다. 삭제한 rune 저장 영역·실패 입력·전달 frame을 비우고 terminal 상태를 복구하며,
redirect된 진단 출력에는 입력값을 보관하지 않는다.

상위 process는 입력 후 새 operation ID를 확보하고 동일 runtime에 private frame을 한 번 전달한다. Pipe 용량보다 큰
frame도 취소/interrupt와 고정된 쓰기 기한을 관측한다. Pipe를 닫아 쓰기를 해제한 뒤 writer를 join하고 그 다음 bytes를
비운다. 선언 조회와 생성 child의 출력은 각각 한도를 가진 별도 process 단계다. 원문 진단은 버리고 direct child와
process group·출력 reader의 종료 및 project/workspace 정리를 끝낸 뒤 닫힌 JSON 결과 한 번을 게시한다.

완전한 한 번의 전달과 같은 operation의 엄격한 응답만 결과를 확정한다. 전달 완료 후 응답 유실·종료/정리 관측 불일치는
`unknown`으로 유지하며, canonical runtime의 정상 private exit 86/87은 확정 생성과 응답/정리 실패를 보존한다.
이미 확정된 결과 뒤에 취소·source 검사·계정 재조회로 성공을 지우지 않는다. 결과에는 operation ID·상태·코드와 별도
cleanup 여부만 남긴다. 자동 재시도나 임의 adoption을 하지 않는다. 공개 CLI는 이 owner를 사용한다.
공개 결과도 한 번만 쓰며 stdout 실패 뒤 stderr에 두 번째 결과를 쓰지 않는다. 게시 실패는 exit 3으로 보고한다.

### 선택 계정의 공통 runtime과 비밀번호 정책

생성 `NewAccountRuntime`과 `identity/model.Runtime[M,G,P]`는 같은 typed 계정·catalog·전체 host 관계 source와
하나의 credential/session store에 Authenticator, session manager, LoginPersistence, 자기 비밀번호 변경,
AccountManagement와 Catalog를 묶는다. 새 DTO·CRUD callback·SQL adapter로 모델을 대신하지 않는다.
Backend는 성공/실패 모두 호출자가 소유하며 runtime은 DB를 열거나 닫거나 migration·첫 계정 생성을 실행하지 않는다.
생성 시 Authenticator의 bounded dummy hash 준비만 실행하고 DB·모델 validator·권한 callback·session entropy는 쓰지 않는다.
Startup은 명시적 `InspectStartup`으로 관측하며 각 요청의 현재 domain/credential 검사와 구분한다.

계정에서 파생되는 catalog 효과와 catalog에서 파생되는 계정 효과는 해당 직접 수정/삭제의 grant와 모든 추가 권한이
같아야 한다. 불일치와 다른 host source의 collection/deletion은 준비 시 거부한다. Session manager와 durable store도
정규화한 같은 한도를 사용한다. 각 owner는 노출/권한 배열을 복사하며 공유 clock/entropy/callback의 기존 수명 계약을 유지한다.
Runtime의 PasswordResetter는 명시한 key ring·timeout·clock과 기존 manager/verifier를 사용하며 계정 생성/변경과 같은
typed password 정책·byte 한도를 자동으로 공유한다. 별도 reset 기본값으로 정책이 느슨해지지 않는다.

모델 독립 `auth/passwordpolicy`가 고정 Django의 similarity·최소 길이·공통 사전·Unicode numeric 알고리즘과 사전 bytes를
소유한다. 기존 기본 Profile과 선택 typed 모델은 이 한 구현에 실제 속성을 공급한다. `Binding.DefaultPasswordValidator`는
명시한 문자열 필드/정적 label을 검사하고 실제 detached M의 값을 사용한다. 인자가 없으면 현재 선택 login/email 역할만
선택하며 추가 profile은 명시적으로 넣는다. Credential·principal identity·revision·서버 시각은 선택할 수 없다. 일반 profile의
이름이 password여도 실제 credential 역할과 다르면 별개다. Nullable NULL·원문 비밀번호·고정 Unicode와 진단 순서를 유지하고,
취소와 잘못된 모델 표현을 강도 거부로 바꾸지 않는다. 사전 원본/라이선스와 기존 독립 Django 비교 근거는 유지한다.

Article/scaffold의 공개 조립은 다음 경계를 사용한다. 명시적 채운 DB 전환과 환경별 실제 수용이 끝나기 전에는 I1 전체 완료로 계산하지 않는다.

### 공개 CLI와 Article/scaffold 통합

공개 `godj createsuperuser`는 `RunFirstAccount`와 프로젝트의 별도 commands runtime을 사용한다. 계정 없는 ProjectSpec,
commands 미설정·ABI/source 불일치와 실제 typed Description의 불일치를 입력 전에 거부한다. 기본 User로 우회하지 않는다.
`modeldef.MigrationSources`는 기본 모델 migration과 독립 account-domain·audit·session 설치를 선언하며 기존 system history를 바꾸지 않는다.

호스트의 `internal/accounts/policy.go`가 최초 생성과 관리·자기 변경/reset에 사용하는 typed 비밀번호 정책을 소유한다.
최초 생성의 callback evidence는 이 선언 파일의 내장 SHA-256이며 전체 schema/role/field source는 생성 팩토리가 별도로 묶는다.
이 파일 hash는 선언 식별자이며 임의 callback의 행동을 증명하지 않는다. Opener는 실제 backend를 획득한 뒤 startup을 검사하며,
검사 실패에도 획득한 자원을 반환해 process owner가 정확히 한 번 닫는다. Constructor/Description은 DB를 열지 않는다.

서버는 같은 `NewAccountRuntime`의 SessionStore·Authenticator·LoginPersistence·관리/catalog·password owner를 사용한다.
자동 Account/Catalog Admin과 관리 JSON을 연결하며 저장된 현재 domain과 권한을 요청마다 확인한다. `Runtime.Credentials()`는
그 runtime의 동일한 scoped reader/authorizer다. 업무 Article도 전체 Principal을 native snapshot/write fence에서 재인가한다.
읽기 거부는 scope 종료가 확인된 뒤 게시하며, mutation과 domain audit는 같은 native transaction이다. API 업무 CRUD에는
Admin 감사가 추가되지 않는다. 감사는 현재 domain·actor·모델/key·변경 field와 시각만 보관하며 display label/값은 저장하지 않는다.

Fresh startup은 공개 페이지를, Ready는 명시적으로 허용한 개발 loopback에서 Admin/API를 조립한다. 생성·migration·HTTP 기동은
각각 명시적 동작이다. Public CLI와 독립 새 모듈의 재시작·응답 유실·양 DB 검증은 TEST_EVIDENCE에서 실행 source별로 판정한다.

## 선택 계정의 프로필과 역할 통합 편집

생성 `NewAccountEditor`와 `identity/model.AccountEditor[M]`는 기존 `SessionStore[M]`를 받아 같은 계정
binding·backend·현재 domain의 session 정책을 사용한다. `AccountEditConfig.Fields`로 scalar·FK·OneToOne profile과
active/staff/superuser의 실제 editable 필드, Groups/Permissions를 명시한다. 새 필드를 자동 노출하지 않는다.
Credential·principal identity·revision·선택 서버 시각·이미지 소유 크기와 다른 collection은 독립 입력으로 거부한다.
파일은 아래의 별도 게시 단계로 편집하며 일반 `ModelInput`으로 저장 이름을 대입하지 않는다. 역할을 상수로 선언하면 그 역할의
입력 필드는 없다. 같은 이름의 일반 Boolean을 명시적으로 profile에 선택해도 상수의 인증 의미를 바꾸지 않는다.
생성 factory의 예상 role fingerprint와 실제 collection의 완전한 host source가 세션 owner와 맞아야 한다.
이 통합 owner가 기존 RoleEditor를 대체한다. Profile만 선택하며 session 변경 권한을 갖지 않는 ProfileEditor는
별도 용도로 유지한다. ProfileEditor와 역할 저장을 차례로 호출하는 방식으로 통합 수정을 구현하지 않는다.

### 단일값 프로필 관계의 선택 정책

`NewProfileRelation[M,T]`는 선택 계정 binding·명시적 FK/OneToOne field·실제 typed target manager와
`ModelChoiceConfig[T]`를 받는다. 완전한 project source와 role fingerprint에 묶이며 backend·actor·조회 결과를
보관하지 않는다. 선택한 관계마다 정확히 하나의 정책을 요구하고 중복·외국 source·미노출 field의 정책을 거부한다.
선택 계정이 권한 collection의 through인 경우 그 endpoint FK는 일반 프로필로 변경할 수 없다.

필수 `Filter` callback은 현재 인가된 actor·첫 생성/일반 생성/수정 operation·명시적 대상 key와 borrowed native
reader를 받는다. Predicate가 없는 반환은 모든 현재 대상 행을 허용한다는 명시적 정책이다. Callback은 읽기만 하며
scope 보관·중첩 owner·쓰기·외부 효과를 수행하지 않는다. 0·음수 대상 key의 존재를 boolean과 구분한다.

공통 AccountManagement의 bounded Admin 검색/Resolve와 partial-invalid Form 검사, JSON·직접 typed 쓰기는
같은 정책을 사용한다. 조회 결과는 이후 쓰기의 허가가 아니다. Creator/FirstAccount는 해싱 전과 최종 transaction에서,
ProfileEditor/AccountEditor는 현재 domain·actor·revision·전체 후보를 확인한 transaction에서 선택을 다시 읽는다.
생략한 기존 FK와 no-op도 현재 선택 정책을 확인한다. Nullable clear는 NULL을 유지하며 scope/predicate 오류를 숨기지 않는다.
OneToOne의 unique 검사·native 제약, 전체 필드/collection·revision·audit·session 효과는 기존 owner의 원자성을 따른다.

첫 계정 명령은 DB를 열기 전에 실제 IR의 integer/null/default/static-choice 의미로 관계 key를 파싱한다.
이 단계는 대상 선택의 허가가 아니며 실제 FirstAccount owner가 별도 첫 계정 operation과 빈 actor로 정책을 검사한다.
선택 거부 뒤 부분 계정/domain을 남기거나 해싱·write를 자동 재시도하지 않는다. 파일 편집은 아래 계약을 따르고,
파일의 Form 생성과 공개 첫 명령의 입력/전송은 아래 계약을 따른다. 추가 M2M profile은 다음 절의 명시적 정책을 사용하며,
공개 첫 명령도 아래 collection 정책을 사용한다. 일반 setter의 selected-model through identity 생성은 I1에 남는다.
환경별 실행 결과는 TEST_EVIDENCE를 따른다.

### 추가 프로필 collection

`NewProfileCollection`은 선택 계정의 실제 typed `ManyToMany`와 target manager, 명시한 field를 전체 project source/role에
묶는다. `ProfileCollectionConfig`는 현재 선택 Filter·bounded 선택의 label/search/order와 양수 `MaximumItems`를 요구한다.
그룹/직접 권한은 기존 권한 정책을 계속 사용하며 프로필 정책으로 바꾸지 못한다. 잘못된 source·역방향 accessor·누락/중복
정책과 선택하지 않은 정책은 구성 단계에서 거부한다. 일반 setter가 계정/catalog identity 자체를 through 행으로 생성하는
조립은 별도 owner가 필요하므로 계속 명시적으로 거부한다.

계정 관리의 추가 collection은 필드별로 전체 소속을 최대 1,024개까지 관측한다. 이를 넘는 저장 소속은 잘린 목록으로 게시하지 않고 오류로 처리한다.
필드별 입력 정책은 1~1,024 사이에서 더 작은 한도를 정할 수 있다. JSON/typed 입력은 정렬·중복 제거 전 길이를 검사하며,
Form은 원 제출의 반복 key 수도 함께 검사한다. 실제 최종 집합도 필드별 한도를 지킨다. 필수 M2M은 빈 생성/편집을 거부하며,
기존 추가 필수 필드를 선택하지 않은 Creator/FirstAccount 선언이 이를 빈 집합으로 숨기지 못한다.

Creator/FirstAccount·AccountEditor는 프로필과 권한 collection을 하나의 collection batch로 준비한다. 현재 선택은 해싱 전과
최종 생성 scope, 편집의 현재 인가·revision 확인 뒤 최종 그래프에서 다시 검사한다. 생략은 이전 값을 유지하고 명시한 빈 목록은
선택적인 관계를 비운다. No-op도 현재 선택 정책을 확인한다. 선택 Filter는 FK 정책과 같은 origin·빌린 scope 계약을 사용하고
첫 계정은 인증 actor 없는 명시적인 operator origin을 유지한다. 파일을 함께 편집할 때는 게시 전에도 제안 선택을 검사한다.

수명 projection은 입력에서 빠진 계정 M2M을 포함하여 삭제/NULL/삽입이 만드는 논리 소속 변경을 추적한다. 대칭 관계의 다른
계정, 여러 collection이 공유하는 through와 연쇄 삭제, 중복 physical pair의 남은 논리 소속을 함께 계산한다. 영향을 받은
계정의 revision·현재 권한/credential·session 폐기와 직접 효과의 audit는 기존 native owner가 처리한다. 순수 간접 소속 변경은
owner revision을 한 번 올리며, 별도의 직접 편집 audit를 꾸며내지 않는다. 모든 생존 대상의 추가 소속을 예상 집합에 포함하고
최종 scalar 저장 뒤 실제 through/target을 다시 조회하여 불일치·뒤 단계 오류를 원자적으로 rollback한다.

`AccountMemberships`는 validator에 그룹/직접 권한과 추가 M2M의 최종 제안을 제공한다. 추가 field의 부재와 존재하는 빈 집합을
구분하며 accessor는 사본을 반환한다. 다른 collection의 연쇄 삭제로 바뀐 생략 필드도 관측한다. 진단에 key를 출력하지 않으며,
일반 ProfileEditor의 scalar 책임은 바꾸지 않는다. FirstAccount 선언 fingerprint는 필드별 collection 한도도 포함한다.

ProfileReader의 명시적 Fields 선택은 추가 M2M의 저장 key를 노출할 수 있다. 실제 canonical through와 target의 존재·한도·
scope 종료를 확인하며 0·음수 key를 보존한다. 쓰기의 선택 Filter로 기존 저장값을 숨기지 않는다. 목록은 scalar만 읽는다.
공통 AccountManagement의 Form/Admin 선택·부분 유효 Form 검사와 관리 JSON/OpenAPI는 같은 정책과 생성/편집/조회 owner를 쓴다.
FirstAccount의 typed/Form과 공개 `createsuperuser` 경로는 같은 기능을 사용한다. 명령의 입력/전송은 다음 계약을 따른다.
이 구현 계약과 실제 환경별 실행·수용은 구분한다.

### 공개 첫 명령의 collection 입력

`FirstAccountDefinition.CollectionLimits()`는 선택한 프로필과 그룹/직접 권한의 개수 한도를 사본으로 반환한다.
Command Description은 `many_to_many` kind·`max_items`와 `21 * max_items` 원문 byte 한도를 fingerprint에 묶는다.
Collection에는 NULL·default·upload·정적 표시 선택을 허용하지 않으며 login은 scalar로 유지한다. 최대 1,024개 key,
scalar bytes와 key당 8 bytes를 합한 65,536-byte 전송 예산을 검사한다. 터미널 원문도 별도의 같은 합계 예산을 지킨다.

TTY는 쉼표로 구분된 canonical int64와 구분자 주변 공백을 받는다. 생략 질문과 명시적 빈 줄을 구분하고, 입력 반복 수를
중복 제거 전에 검사한다. `Value.Keys`는 순서·반복·0/음수 key를 보관하는 별도 가변 버퍼다. 비어 있는 non-nil slice가
명시적 빈 collection이며 nil은 목록 입력이 아니다. 종류 혼합·개수/byte 초과·순서/source 불일치는 DB 획득 전에 거부한다.

명령은 문법상 key로만 ModelForm의 후보 choice를 구성하여 같은 필수/빈 집합·모델 준비 경로를 사용한다. 이 후보는 DB 조회나
선택 허가가 아니다. 실제 FirstAccount가 같은 native 범위에서 target 존재·현재 Filter를 해싱 전과 최종 저장 때 재검사한다.
Caller buffer를 지워도 준비한 입력은 독립적이며 한 번의 시도·소비/취소·파일 결과·unknown/확정 receipt의 기존 소유권을 유지한다.
Private v4는 목록 개수와 signed big-endian int64를 전달하고, 전체 frame을 검증한 뒤 분리된 key 버퍼를 만든다.
새 ABI는 이전 frame을 명시적으로 거부하며 runtime의 선언과 실제 source를 확인하기 전에는 터미널을 읽지 않는다.

입력의 source·선택 필드·target binding과 login/email의 고정 저장 정규화·문자/byte 한도를 I/O 전에 확인한다.
`Update`는 전체 수정에 대해 현재 change 권한과 모든 추가 권한·deny overlay를 요구하며, 대상의 현재
revision/credential/소속 무결성을 먼저 확인한다. Scalar만 또는 collection만 보냈어도 같은 권한 정책을 적용한다.
생략한 소속은 유지하고 명시한 빈 집합은 지운다. 그룹/직접 권한은 정렬/중복 제거 전 원 입력을 256개로 제한하며 현재와 제안 그룹 수,
직접 권한 수와 그룹/직접 권한의 유효 합집합을 각각 검증한다. 비활성 후보도 한도를 우회하지 않는다. Typed validator는
분리된 현재 모델과 프로필·역할·수집된 NULL·다음 revision을 함께 반영한 최종 후보, 사본을 반환하는 최종 소속을 받는다. Callback 뒤 scope 수명과
취소를 재검사하고 typed 모델의 unique 검증을 같은 scope에서 수행한다.

프로필·역할 scalar와 권한/추가 프로필 collection·한 번의 revision 증가·전체 변경 필드의 한 audit는 같은 native coordinated
relation transaction에 속한다. 모델/정책 거부가 프로필 또는 역할만 먼저 남겨서는 안 된다.
권한과 flag 변경은 principal/hash/session stamp를 바꾸지 않는다. Active가 true에서 false로 바뀔 때만 같은 scope에서
현재 domain의 대상 principal 세션을 모두 폐기하며 다른 계정·익명·역사 domain의 세션은 유지한다. 재활성화는 삭제한
세션을 되살리지 않는다. LoginPersistence도 같은 DB fence와 현재 active 재검사를 사용하므로 동시 로그인이 비활성화 뒤
세션을 남길 수 없다. No-op도 현재 권한/무결성/validation을 확인하고 revision·감사는 늘리지 않는다.

### 프로필 파일의 편집과 게시 결과

`NewProfileFile[M]`은 선택 계정 binding·파일 field와 명시적 capability를 완전한 project source/role에 묶는다.
파일을 선택한 ProfileEditor/AccountEditor는 필드마다 정확히 하나의 정책을 요구하며 missing/duplicate/foreign source와
미노출 정책을 구성 시 거부한다. 일반 업로드는 빌린 storage backend와 pure Name callback을 사용한다. Callback은
다른 준비 값과 기존/default 파일 참조를 가진 분리된 모델을 받되 password hash·principal identity는 비운다.
생성 후보에는 아직 저장된 PK가 없으며 이를 임의로 예약하거나 꾸며내지 않는다. Backend를 닫거나
global storage를 찾아 주지 않는다. 파일 choices는 canonical 이름 선택이며 업로드 saver를 선언하지 않는다.
Image choices에는 명시적 inspector가 필요하고 `FormDefinition`은 그 capability와 bounded image 정책을 같은 ModelForm에 넣는다.

`UpdateForm`은 성공한 BoundForm의 완전한 source·선언된 선택·target key와 이미지에서 파생한 값을 확인한다.
읽기 projection이 로드한 revision은 expected revision과 같아야 하며, revision을 로드하지 않은 ModelView에는 그 값을
만들어 넣지 않는다. 어떤 경우에도 호출자가 expected revision을 직접 전달하고 실제 DB에서 이를 확인한다.
저장 전 native snapshot에서 현재 domain/actor/change 권한·target credential/revision·관계 선택과 제안 grant 한도를
검사한 뒤 scope를 닫는다. 파일 I/O는 DB 밖에서 실행하고 실제 storage 반환 이름을 가진 `ModelInput`으로 최종 편집을 요청한다.

최종 native fence는 현재 인가·revision·전체 관계 효과·typed validator·실제 이름의 unique를 다시 검사한다.
파일 참조·이미지 크기·일반 profile·role/collection·revision·session 폐기·audit의 **DB 변경**은 하나의 기존 owner에서
확정한다. 이미지 소유 크기는 일반 Form/JSON/계정 입력으로 노출하지 않으며, ProfileEditor의 CRUD 경계에서도 server 값으로
적용한다. 업로드 생략은 이전 참조를 유지하고 clear는 nullable 파일도 빈 문자열, 소유 nullable 크기는 NULL을 저장한다.
기존 참조를 보여 주거나 유지하는 작업에 숨은 파일 Open을 넣지 않는다.

파일 게시와 DB commit은 분산 transaction이 아니다. 저장 후 권한 회수·revision 경합·unique/validator 거부·감사 실패가
발생할 수 있고 DB rollback이 이미 게시한 파일을 되돌리지는 않는다. 반환한 모든 `FilePublication`은 마지막 오류와 함께
보관한다. 일부 파일만 게시되거나 저장 응답이 불확실하면 DB 단계로 진행하지 않는다. DB 결과가 unknown이면 확정 계정
metadata는 0이지만 파일별 결과는 남는다. Clear·교체·실패에서 이전/새 파일을 자동 삭제하거나 작업을 재시도하지 않는다.
요청 업로드 capability의 수명을 늘리지 않으며 동일 준비의 수동 재호출을 멱등 작업으로 취급하지 않는다.

공통 Admin 등록은 업로드를 받는 편집 필드가 있을 때 `ReportFileEdit`를 요구한다. 기존 이름만 선택하는 choices에는 요구하지 않는다.
실제 파일을 시도한 모든 요청에 대해 DB scope가
끝난 뒤 파일 결과와 계정의 확정 성공/실패를 전달하며, HTTP 오류 변환 전에 application이 보관할 수 있게 한다.
Callback은 취소된 context도 받을 수 있다. 보관·복구와 그 채널의 내구성은 application 책임이며 보고 실패는 별도 실행 오류다.
보고가 실패했다고 이미 확인한 DB commit이나 파일 게시를 실패 전 상태로 바꾸지 않는다.

관리 JSON의 파일·이미지 크기는 명시적인 read-only 출력이다. 생성/편집 요청 schema에서는 제외하고 문자열/크기 입력은 거부한다.
파일의 내용·URL·다운로드 권한을 JSON 저장 이름에 결합하지 않는다. 필수 파일이 있는 모델은 해당 파일을 공급하는 Form
경로를 사용해야 한다. 필수 파일을 생략한 JSON 생성은 입력 거부이며 저장 이름을 임의로 만들어 쓰지 않는다.

### 프로필 파일을 포함한 계정 생성

`CreationPolicy.Files`는 선택 파일마다 동일 source의 명시적 `ProfileFile`을 요구한다. `AccountManagementConfig.Files`는
CreateFields/EditFields의 합집합에 대해 검증하고 각 owner에는 해당 선택만 전달한다. 이미지 소유 크기는 독립 입력이 아니며,
그 이미지를 선택하면 파생 저장/감사 필드로 포함한다. `Creator`와 `FirstAccountDefinition`/`FirstAccount`의 FormDefinition은
같은 정규화·이미지 검사 정책을 제공한다. 생성은 새로운 Form이므로 기존 key나 조작된 초기 파일 참조를 받아들이지 않는다.
IR default와 canonical choices의 선택은 새 업로드와 구분한다.

`Creator.CreateForm`/`CreateUnusableForm`은 현재 요청 안에서 한 번의 준비·게시·commit을 수행한다. 유효한 Form에서
pending file만 제외한 typed 입력으로 서버 identity·시각·고정 역할과 다른 값을 준비한다. 첫 native snapshot은 현재
domain·add/change·추가 권한·관계/소속과 실제로 준비된 값의 unique를 검사한다. Pending 파일 및 그것을 포함한 전체 unique
제약은 여기서 미루고, 업로드 client filename을 DB 저장 이름으로 검사하지 않는다. Admin의 부분 unique 검사도 같은 경계를 따른다.

Snapshot 종료 뒤 모든 이름 정책을 검사하고 DB 밖에서 파일을 게시한다. 이름 callback에는 정규화된 생성 후보의 사본을
전달한다. 실제 반환 이름과 파생 크기만 후보에 합쳐, 일반 scalar 정규화와 서버 identity를 이전 Form 값으로 덮지 않는다.
다시 새 snapshot에서 현재 인가와 **실제 이름**의 unique·typed/password policy를 확인한 뒤 한 번만 hash한다.
최종 native write fence에서 전체 권한·선택·unique·policy를 재검사하고 계정·소속·audit를 함께 확정한다. 파일이 없는
경로는 기존 사전 검사/해싱 단계를 유지한다. Unusable 생성은 hash/strength policy를 호출하지 않는다.

파일 의존 validator는 실제 이름이 준비된 뒤, 여전히 해싱 전에 호출한다. 따라서 파일 게시 뒤 validation·hash 실패가 날 수
있으며 모든 `FilePublication`을 반환한다. 불완전/불확실 게시에서 DB 단계로 진행하지 않는다. 업로드 수명을 준비 객체로
연장하지 않으며 자동 재시도·파일 삭제·DB/파일 분산 원자성을 제공하지 않는다.

`FirstAccount.CreateForm(ctx, operationID, bound, password)`는 호출자가 게시 전에 보관한 명시적 operation ID를 요구한다.
파일 게시 전·후와 최종 transaction에서 계정/domain의 부재를 확인한다. 마지막 단계의 domain·계정·첫 감사는 원자적이며,
unknown에서는 확정 계정 결과를 반환하지 않고 게시 결과를 보존한다. 보관한 operation ID와 `domain.Lookup`으로 실제
receipt를 대조한다. 다른 첫 생성이 게시 사이에 완료되면 원래 요청은 새 domain/계정을 쓰지 않고 파일 결과만 돌려준다.

Admin 생성에 업로드가 있으면 `ReportFileCreate`가 필수다. `AccountFileCreationReport`는 확정 Creation, 파일별 결과와
오류를 별도로 소유한다. Callback·오류 변환·취소·요청 수명과 보고 실패는 편집 보고와 같은 계약이며, 확정 commit 뒤 보고
실패는 성공한 DB 결과를 취소하지 않는다. Choices-only 생성에는 파일 게시 보고 callback을 요구하지 않는다.
공개 첫 명령의 파일 입력·private process 전송은 아래 계약을 따른다. 채운 DB 전환과 새 경로의 환경별 수용은 활성 I1 요구에 유지한다.

### 공개 첫 명령의 파일 입력과 보고

`firstaccount.New`는 upload 필드가 있으면 `Config.MaxFileBytes`/`MaxFilesBytes`와 `ReportFiles`를 요구한다.
두 한도는 양수·파일당 ≤ 합계 ≤ 64 MiB이며 실제 Description digest에 포함된다. 파일 choices는 같은 Form의 정적인
문자열 선택이고 임의 저장 이름·local 경로를 받는 upload와 구분한다. 이미지 소유 크기는 독립 CLI 입력으로 노출하지 않는다.

TTY의 파일 경로는 명령 작업 디렉터리 기준으로 한 번 열고 그 regular file handle에서 제한된 bytes를 읽는다.
FIFO/device/directory·한도 초과·관측한 크기/시각 변경·읽기/닫기 실패는 입력 실패다. 읽기 사이에 context/interrupt를
확인하며 local path를 child에 전달하지 않는다. 새 frame은 선언된 전체 길이를 확인한 뒤 해당 크기만큼 할당한다.
Scalar-only 요청 때문에 파일 상한 전체를 할당하지 않는다. 파일의 존재·basename·bytes와 scalar/password 예산을 따로 검사한다.

`Command.Prepare`는 `uploads.NewBuffer`로 복사한 입력과 실제 파일 Form을 연결한다. 이미지 검사는 DB 획득 전이며
Form 정책과 원문/default 의미를 유지한다. 준비를 포기하거나 한 번의 시도가 끝나면 소유 bytes와 이미 열린 reader까지
만료된다. 호출자 원본 변경이 준비를 바꾸지 않고, 복사한 준비를 재실행하거나 기존 요청 upload 수명을 연장하지 않는다.

`Command.Provision`은 같은 `FirstAccount.CreateForm`을 사용한다. `Result`는 확정 FirstAccountCreation·모든 파일 시도와
별도 ReportErr를 보관한다. 파일 시도가 있으면 upload를 만료시킨 뒤 `FileReport`를 한 번 보고하며 DB 오류도 함께 전달한다.
이 보고는 native DB scope 밖에서 같은 context를 사용한다. 애플리케이션이 operation ID·실제 이름·게시 outcome·생성 outcome을
보관하고 명시적 오류를 반환한다. Callback은 동시 호출에 안전해야 하며 panic/Goexit/오류를 closed 보고 실패로 분류한다.
보고 실패가 확정 commit을 취소하지 않는다. CLI/runner는 파일 이름이나 원인 오류를 공개 JSON에 복제하지 않는다.

부모는 `file_report_failed`와 DB/cleanup 결과를 따로 유지한다. 응답 유실의 unknown에서 false flag를 보고 성공으로
해석하지 않는다. 보관한 operation receipt와 파일 보고로 명시적으로 대조하며 자동 재시도·파일 삭제는 없다.
기본/custom의 실제 생성 module과 TTY/process/DB 재접속, 양 DB command 소비자는 이 계약의 검증 대상이며
작성/최소 compile/확인된 실패 재현을 전체 환경 수용으로 바꾸지 않는다. [공개 API](../../project/firstaccount/README.md)를 따른다.

반환값은 확정된 ID·revision·변경 필드와 폐기 수뿐이다. 후속 relation/session/audit 실패는 앞선 변경과 함께 rollback하고,
unknown 결과는 공개 값을 버리고 자동 재시도하지 않는다. 확정 commit 뒤 취소는 성공을 되돌리지 않는다.
Collection의 실제 through 행을 지우는 경로도 전체 host 삭제 정책에 묶는다. Endpoint FK가 PROTECT여도 직접적인 through
삭제는 별도로 검사한다. 추가 계정/catalog과 다른 through의 CASCADE/SET_NULL은 위 전체 수명 조정에서 처리한다.
Through 자체가 선택 계정/catalog인 저장의 identity 생성 조립은 남아 있으며 해당 collection을 편집 구성에 선택할 때 거부한다.
해당 소속을 노출하지 않은 scalar 역할 편집에는 이 생성 경로를 요구하지 않는다.

## 선택 계정 도메인의 독립 DB 세션과 로그인

`sessions/database`는 기본 User·기존 operator schema 없이 설치하는 `godj_sessions` 앱과 초기 migration을 소유한다.
Namespace·bearer digest·기존 canonical v1 payload를 생성 ORM으로 저장하고 원문 bearer는 저장하지 않는다.
Digest에는 별도의 버전 구분자·namespace·bearer를 포함한다. 기존 systemstate digest·migration·wire bytes를 재해석하거나
새 테이블로 자동 복사하지 않는다. 설치·채운 DB 이행·역사 세션 폐기는 명시적인 별도 작업이다.

`Database.Store(namespace)`는 인가가 없는 서버 저장 기능이다. Namespace는 UTF-8/NUL/길이 제한을 받으며 요청 입력으로
인증 namespace를 선택하는 API가 아니다. 기본 전체 용량은 4,096개, 설정 상한은 65,536개이며 모든 namespace를 합쳐
검사한다. 포화 시 전체 payload를 검증한 뒤 만료된 가장 작은 storage key 한 개만 회수한다. 활성 다른 domain을 지우지
않는다. Metadata inventory와 256개 keyset payload batch를 사용하고 잘못된 key·digest·payload·초과 용량은 거부한다.
`Inspect`는 한 snapshot에서 전체 inventory/payload를 검증하며 설치나 정리를 수행하지 않는다.

기본 Store 연산은 자체 native snapshot/write fence를 사용한다. `LoadIn`은 live read/write scope를 빌리며,
`InSession`은 이미 조정된 write scope에서만 임시 Store를 만든다. Root owner·만료 scope·중첩 binding을 거부하고
scope 안에서 중첩 transaction을 열지 않는다. `DeleteMatching`은 해당 namespace의 모든 payload를 검사한 뒤 정확한
서버 값에 일치하는 행을 지운다. 보존할 ID도 검증 대상이며 뒤 batch 오류는 앞선 삭제와 함께 rollback되어야 한다.
`DeleteAll`은 명시한 namespace만 폐기한다. Borrowed 결과는 바깥 owner가 확정하기 전까지 잠정 값이다.

생성 `NewAccountSessionStore`와 `identity/model.SessionStore[M]`는 credential reader의 같은 backend에서 매 연산의
현재 durable role/generation을 읽고 `CredentialDomain.Identifier()`로 namespace를 결정한다. Domain과 세션 조회는
같은 native snapshot이고 쓰기도 같은 fence에 속한다. Domain을 전환하면 동일 ID/hash/bearer의 역사 세션을 읽거나
새 세션으로 승격하지 않는다. `RevokeIn`은 현재 domain의 특정 principal 세션을 같은 write scope에서 폐기하는 서버 기능이며,
호출자가 계정 변경의 현재 인가·revision·audit를 먼저 소유해야 한다. 과거 domain의 정리나 전환 승인 기능은 아니다.

`SessionStore.LoginPersistence(manager)`는 정확히 그 Store를 사용하는 manager만 받는다. Manager가 time/entropy를
DB 밖에서 준비하고, 공통 typed `LoginRecorder[M]`가 같은 scope의 현재 domain·계정·active·stamp·권한과 Admit를 다시
확인한다. 이 관찰은 세션 변경보다 먼저 수행해 비활성화가 기존 세션을 폐기한 경우에도 현재 credential의 거부를 확인한다.
유효한 credential의 세션 누락은 저장 실패로 유지한다. 세션 생성 collision에는 LastLogin을 쓰지 않고 확정된 변경만 시각과
함께 게시한다. LastLogin은 IR에서 선택 항목이며 비어 있으면 같은 이름의 기존 열도 암묵적으로 갱신하지 않는다.
지정했을 때만 nullable DateTime 역할을 검사하고 강제 update하며 계정 revision·password·role·grant·audit는 바꾸지 않는다.
Anonymous/동일 credential의 session 회전, 다른 credential의 fresh replacement, LastLogin은 함께 확정하거나 rollback한다.
기존 record의 만료/없음도 실패한 로그인과 별개로 삭제를 commit하지 않는다. 일반 Authenticate/Resolve/Access/logout은
이 관측 필드를 갱신하지 않는다.

한 번의 callback과 확정 commit 뒤에만 credential/record를 게시한다. 원인 없는 확정 collision만 entropy 재시도가 가능하고,
확정된 동일 admission sentinel만 일반 인증 거부로 돌려준다. Cleanup·취소·callback 소유권 위반·unknown 결과에 포함된
거부는 storage failure와 원인 연결을 유지한다. `Manager.Load`도 AccessPolicy의 오류를 그대로 반환받은 경우에만 일반
policy 오류로 분류하며 wrapped/joined cleanup 실패를 정상 policy 거부로 축소하지 않는다.

이 로그인 구성만으로 계정의 다른 쓰기를 수행하지 않는다. 자기 비밀번호 변경은 아래의 별도 owner를 명시적으로
연결한다. Reset은 아래의 선택 계정 owner를 사용한다. 계정/host 삭제·첫 계정 명령·설치/startup·역사 mapping/retirement와 공개 Admin/HTTP 제품 조립,
양 DB의 독립 HTTP/process 재시작 수용은 I1에 남는다.

## 선택 계정의 자기 비밀번호 변경

`SessionStore.Authenticator(ctx, hasher)`는 해당 세션 owner의 정확한 credential reader 위에 저장 인증기를 만든다.
로그인과 자기 비밀번호 변경에 이 인증기를 재사용한다. `PasswordChangePersistence(manager, authenticator, config)`는
정확히 같은 Store를 사용하는 manager와 같은 credential reader의 인증기만 받는다. 같은 모델명이나 역할 fingerprint를
가진 별도 reader를 허용하지 않는다. 이 구성 검사는 I/O·해싱·callback을 실행하지 않으며 새 hasher를 따로 받지도 않는다.
인증기 구성에 필요한 bounded dummy hash만 실제 인증기 생성 시 DB scope 밖에서 준비한다.

공통 `PasswordValidator[M]`는 생성과 변경에서 실제 detached typed profile을 받는다. Password 역할 필드는 빈 문자열이며
기본 User DTO나 동적 입력 map으로 대체하지 않는다. 순수하고 동시 호출에 안전한 정책이며 비밀번호·profile을 보관하거나
외부 부작용을 일으키지 않는다. 진단은 값 없는 `password`/NonField만 허용한다. Callback 오류·취소·감싼 rejection은
입력 오류로 노출하지 않는다. 정책이 없어도 필수 문자열·byte 한도·hasher 검사는 유지한다. 한도는 기본 1,024 bytes,
구성 범위는 1~4,096 bytes이며 실제 생성 IR의 encoded field 한도도 별도로 검사한다.

`CheckPasswordChange`는 session `Peek`와 종료된 현재 계정 snapshot을 확인하고 stable principal ID로 기존 비밀번호를
검증한다. 읽기 종료 실패를 일반 인증 거부로 축소하지 않는다. Form cleaning에 실패한 nil 필드는 건너뛰고, 정상적으로
cleaning된 필드의 이전 비밀번호 오류와 새 정책 오류는 함께 반환한다. 해싱·세션 touch·만료 정리·쓰기를 하지 않으며
후속 변경에 사용할 권한 객체도 반환하지 않는다.

`ChangePassword`는 이 사전 검사 뒤 DB 밖에서 새 비밀번호를 한 번 해싱한다. Encoded 결과가 unusable이거나 기존
stamp와 같으면 거부한다. 같은 원문도 새 salt와 hash로 실제 변경할 수 있다. 비밀번호를 담은 준비 상태는 함수 내부에만
있으며 종료 시 참조를 버린다. Go 문자열의 메모리 소거를 보장한다는 뜻은 아니다.

Manager가 시각과 entropy를 DB 밖에서 준비한 뒤 하나의 native coordinated transaction에서 현재 namespace·저장 세션의
principal/stamp·선택 계정의 key/active/credential/권한과 현재 typed 정책을 다시 검사한다. 별도 callback의 오래된 승인이나
서버 밖에서 받은 대상 ID를 신뢰하지 않는다. 계정의 username·profile·권한이 바뀌어도 credential이 같으면 현재 값을 쓰고,
이전 snapshot으로 덮지 않는다. 실제 Password/Revision 두 필드만 강제 update하고, 현재 저장 세션의 payload·생성/절대 수명을
유지한 새 stamp/ID로 회전하며, 같은 namespace의 그 principal에 속한 다른 세션을 모두 폐기한다. 익명·다른 계정·역사
namespace는 보존한다. LastLogin은 변경하지 않는다. 현재 계정 자신을 actor로 값 없는 domain audit를 함께 추가한다.

만료된 세션의 삭제도 실패한 변경과 따로 commit하지 않는다. Session insert·password update·뒤 batch의 폐기·audit 실패는
앞선 변경과 함께 rollback한다. 확정 rollback의 직접 인증/정책 거부만 일반 거부로 돌려주며 cleanup·callback 소유권 위반·
unknown 결과는 공개 credential/record 없이 원인을 보존한다. 확정 collision만 manager의 한정 entropy 재시도를 허용하고
해싱은 반복하지 않는다. Unknown 결과는 자동 재시도하지 않으며 확정 commit 뒤 취소는 성공을 되돌리지 않는다.

기존 `web/sessionauth`와 계정 Application의 password Form/JSON은 이 owner를 명시적으로 받아 그대로 사용한다.
CSRF·confirmation·입력 제한은 HTTP가 소유하고, 확정 commit에만 현재 cookie를 교체한다. 독립 CSRF cookie는 유지한다.
선택 모델의 공통 로그인 Form은 같은 인증기의 명시적 입력 metadata를 사용한다. Scaffold/runtime 전체 전환,
통합 reset 조립과 실제 process restart는 별도 남은 I1 요구다. 작성/compile만으로 해당 요구의 완료를 주장하지 않는다.

## 선택 계정의 관리자 비밀번호 변경

생성 `NewAccountPasswordEditor`는 정확한 `SessionStore[M]`와 그 Store에서 만든 인증기, 같은 역할과 전체 host source를
검사한다. 새 hasher나 기본 User reader를 따로 받지 않는다. `PasswordEditConfig[M]`가 관리 권한·추가 필수 권한·raw
byte 한도·같은 typed `PasswordValidator[M]`·순수 clock을 선언하며 생성은 I/O·해싱·entropy·callback을 실행하지 않는다.
공개 권한 metadata는 사본이며 Form/Admin 조립에 사용해도 실제 쓰기 인가를 대신하지 않는다.

`SetPassword(ctx, actor, id, revision, raw)`는 현재 domain·actor active/grants·deny overlay와 대상의 존재·revision·credential을
한 native snapshot에서 읽는다. 권한 거부·대상 없음·revision 충돌도 읽기 종료 성공 뒤에만 게시한다. 해시는 DB scope 밖에서
한 번 준비하고, 그 사이 domain/actor/target이 바뀔 수 있으므로 최종 native write fence에서 다시 관측한다. Revision과
기존 credential stamp가 일치해야 하며 현재 typed profile로 정책을 다시 적용한다. 이전 profile·역할·소속·LastLogin은 덮지
않는다. 실제 Password/Revision만 강제 update하고 현재 namespace의 대상 세션 전부를 폐기하며 현재 actor의 값 없는 audit를
함께 저장한다. 반환 값은 확인된 ID/revision/폐기 수뿐이다. 대상 key의 0/음수도 이미 저장된 유효 key로 취급한다.

관리 작업은 이전 비밀번호의 확인을 요구하지 않으며 inactive/unusable 대상에도 새 비밀번호를 설정할 수 있다. 이를 통해
active가 되거나 로그인한 것으로 처리하지 않는다. `SetUnusablePassword`는 별도 명시적 작업으로 strength policy·hasher 없이
새 unusable 표식을 만들고 같은 최종 권한/revision/credential 검사와 쓰기 원자성을 적용한다. 반복 disable도 표식·revision을
바꾸며, 자기 대상을 관리할 때도 그 브라우저를 포함해 모두 폐기한다. 현재 세션 회전과 cookie 교체는 자기 변경 owner의 책임이다.

Account update·세션 삭제의 뒤 batch·audit insert 후 실패는 함께 rollback한다. 확정 rollback의 직접 거부만 일반 오류로
유지하고, cleanup·callback 소유권 위반·unknown 결과는 성공 metadata나 renderable validation을 게시하지 않는다. 해싱/쓰기는
자동 재시도하지 않으며 확인된 commit 뒤 취소는 성공을 지우지 않는다. 익명·다른 principal·역사 namespace 세션은 보존한다.

`identityadmin.PasswordCommands(editor)`는 이 owner를 공통 Admin의 object command 두 개로 조립한다. 호스트는 선택 계정의
모델에 등록한다. 비밀번호/확인 입력의 공백·byte 한도·비공개 widget과 명시적 disable 확인, 원인이 없는 정책 진단의 confirmation
field 매핑을 유지한다. `CommandConfig.AdditionalPermissions`와 descriptor의 전체 `Permissions`는 복사된 불변 선언이다.
화면의 command link, GET/POST, direct command 실행은 모델 조회와 모든 command 권한을 요구하고 owner는 현재 DB 권한을
다시 확인한다. Admin의 active staff/CSRF 규칙과 최종 revision 검사를 우회하지 않는다. 성공 뒤 재조회나 자동 로그인은 없다.
이 명령 조립은 account/catalog 전체 자동 등록, profile 관계/파일 및 scaffold runtime 전환을
대신하지 않는다. 공통 Admin의 객체 URL key 경계와 모델 owner의 저장 key 지원도 별도 수용 대상으로 둔다.

선택 계정의 profile/role owner도 공통 Admin CRUD에 확인된 ID/revision/변경 필드만 전달한다.
[공통 mutation 결과 계약](0092-model-input-and-crud-assembly.md)에 따라 자기 active 변경으로 현재 actor와 모든
브라우저 세션을 폐기해도 성공 뒤 ProfileReader를 다시 호출하지 않는다. Redirect 뒤의 새 요청은 새 credential과
세션 상태를 적용한다. 이 계약은 account/catalog 자동 등록이나 삭제 정책의 구현을 의미하지 않는다.

## 선택 Catalog의 공통 Admin 조립

`identityadmin.RegisterCatalog(builder, catalog, presentation)`은 선택된 실제 Group/Permission 타입과 전체 host에 묶인
Catalog owner를 공통 Admin에 등록한다. 모델 identity·역할 field·read/write 선택·operation 권한은 owner에서 가져오고,
호스트는 slug와 읽을 수 있는 scalar 목록 열만 정한다. Label과 Form field가 명시적 ReadFields에 없으면 등록을 거부한다.
ID/revision은 이미 공개된 operation metadata로 투영하며 숨은 모델 행이나 기본 User 복사본을 만들지 않는다. 등록은 I/O를
하지 않는다. Builder는 모든 등록이 성공한 뒤에만 게시한다. 중간 등록 실패는 앞서 추가된 모델을 자동으로 되돌리지 않는다.

`ModelConfig.AdditionalPermissions`는 모델의 모든 조회·생성·수정·삭제·작업·명령·선택지 admission에 적용한다.
View 또는 Change라는 읽기 대안은 이 공통 필수 권한을 우회하지 않는다. 생성 전용 AdditionalAddPermissions와 각 command의
별도 권한도 유지한다. 선언과 descriptor는 복사되며 현재 overlay 오류를 일반 거부나 다른 permission의 성공으로 바꾸지 않는다.
UI/registry의 검사는 선행 admission이고 최종 권한·도메인·대상 revision은 Catalog owner가 자기 DB scope에서 다시 읽는다.

그룹 권한 선택지는 실제 project relation과 typed Permission manager의 `ModelChoices`를 사용한다. Add와 Change를 별개로
구성하고 origin의 source/operation을 확인한다. 검색과 제출 key 해석마다 한 native read snapshot 안에서 현재 actor와
전체 operation/추가 권한을 확인한다. 부모의 할당 권한으로 선택하며 별도의 permission 목록 조회 권한을 강요하지 않는다.
표시 page는 입력 허가 목록이 아니며 현재 선택과 제출 key는 별도로 bounded Resolve한다. 최종 저장도 key를 다시 해석한다.

`CatalogResource.CheckCreateForm`/`CheckChangeForm`은 정확한 모델 source·선택 입력·대상/key/revision에 묶인 읽기 전용
검사다. 생성 key의 NULL과 저장된 0/음수 key를 구분한다. 생성 revision은 Catalog 저장 owner가 1로 정하며 schema default에
의존하지 않는다. 다른 필드가 실패한 Form도 유효한 field unique를 검사하고, 그 진단으로 제외를 다시 계산해 복합 unique를
검사한다. 현재 선택 key도 같은 snapshot에서 확인한다. 거부·부재·conflict·validation 진단은 scope/cleanup 성공 뒤에만 게시한다.
이 검사는 값을 예약하거나 최종 typed validator·권한·유효 소속/한도·삭제 graph·write fence를 대신하지 않는다.

Admin은 Create/Update/Delete를 같은 owner에 위임하고 확정된 ID/revision/변경 field만 반환한다. 성공 뒤 row/권한을 다시
읽지 않는다. History는 현재 domain의 durable event를 값 없는 Admin event로 투영한다. Account 자체는 아래의 등록을 사용한다.
관리 JSON과 공개 CLI/scaffold/runtime은 각각의 공통 조립을 사용한다. 현재 공통 Admin 객체 URL의 양수 key 경계를 native
Catalog/Form owner의 signed key 지원으로 완료했다고 해석하지 않는다.

## 선택 계정의 관리 owner와 공통 Admin 등록

생성 `NewAccountManagement`는 정확한 SessionStore·그 Store의 인증기·전체 host source와 실제 collection/삭제 기능을 확인한다.
하나의 `AccountManagementConfig[M]`에서 읽기/생성/수정 노출, view/add/change/delete·공통 추가 권한, Group/Permission의
직접 동반 효과 권한, typed 생성/편집/비밀번호 정책과 서버 principal ID source·clock을 정한다. Creator·ProfileReader·
AccountEditor·AccountDeleter·PasswordEditor는 같은 credential/session owner에 묶인다. 내부 API 이름이나 기본 User 사본을
위한 호환 계층을 만들지 않는다. 구성은 DB·hasher·entropy·사용자 callback을 실행하지 않는다.

읽기는 view 또는 change와 모든 추가 권한, 생성은 add와 change 및 모든 추가 권한을 요구한다. 수정/비밀번호/삭제도 각각의
operation과 모든 추가 권한을 유지한다. 각 owner는 매번 현재 저장 actor·domain·대상 상태를 다시 검사한다. 반환 accessor는
이미 source에 묶인 owner이며 source나 backend를 다시 지정하는 기능이 아니다. 설명·필드·선택지 slice는 복사된 선언이다.

`FormDefinition`은 실제 노출 필드와 쓰기 login/email 정규화를 공통 ModelForm에 연결한다. `CheckCreateForm`과
`CheckChangeForm`은 전체 model source와 선택 입력, 생성 NULL key 또는 기존 key/revision을 확인한다. `BoundForm.FieldNames`
덕분에 cleaning에 실패한 보호 필드도 선행 admission에서 거부한다. Command 전용 입력은 모델 DB 검사에서 제외한다.
현재 권한과 대상 무결성·유효 소속, 각 선택 key와 effective grant 한도, 정규화된 unique field/복합 unique를 한 native snapshot에서
검사한다. 다른 필드가 invalid여도 독립 검사는 계속하고, 앞선 진단으로 제외를 다시 계산한다. Cleanup 실패에는 진단·부재·conflict도
게시하지 않는다. 이 단계는 principal ID·시각·비밀번호를 만들거나 typed 최종 정책을 실행하지 않으며, 성공도 값을 예약하지 않는다.

Add와 Change의 그룹/직접 권한 선택지는 각각의 노출과 전체 account operation 정책에 묶인다. 실제 typed catalog의 label/code,
IR field·관계 source를 사용하고 현재 읽기 scope에서 bounded 검색/Resolve한다. 별도 catalog view 권한을 강요하지 않는다.
Scope의 source/operation과 현재 권한을 확인하며, 최종 쓰기는 동일 key와 최종 상태를 다시 확인한다.

`identityadmin.RegisterAccount`는 login label과 모든 edit field가 읽기 노출에 있어야 한다. Create-only 필드를 읽기로 넓히지
않으며 host는 slug와 읽을 수 있는 scalar 목록 열을 선택한다. 비밀번호/확인 입력은 공백을 보존하는 private widget과 byte 한도를
사용한다. 명시적 unusable 선택은 별도이며 빈 비밀번호를 그 선택으로 추론하지 않는다. Command 이름이 실제 profile 이름과 겹치면
결정적으로 별도 이름을 배정한다. 실제 stored Password/PrincipalID·revision·서버 timestamp는 쓰기 입력에 포함하지 않는다.

등록된 Create는 typed 입력을 준비한 뒤 Creator의 Prepare/Commit을 한 번 호출한다. Update/Delete와 password commands도
기존 수명 owner에 위임하며 확인된 ID/revision/변경 field만 전달한다. 자기 비활성화나 비밀번호 변경으로 읽기 권한·현재 session이
사라져도 commit 뒤 재조회로 성공을 잃지 않는다. ProfileReader의 선택 ModelView와 현재 domain의 값 없는 durable audit만
화면에 투영한다. 공통 Admin의 active staff·CSRF·최종 current authority 경계와 기존 host graph/rollback/unknown 의미를 유지한다.

공개 첫 계정 명령/scaffold/runtime과 FK/OneToOne·파일 편집/Form 생성·CLI 전송은 각 현행 절의 계약을 따른다.
추가 M2M은 위 명시적 정책을 사용하며, 일반 collection의 선택 모델-as-through identity 생성,
채운 DB의 명시적 mapping/retirement와 통합 restart는 남아 있다.
Native signed key를 지원한다는 사실만으로 공통 Admin 양수 객체 URL의
경계를 완료했다고 계산하지 않는다. 구현·최소 compile·환경별 본문 실행은 TEST_EVIDENCE에서 구분한다.

## 선택 계정의 관리 JSON 조립

`identityapi.NewManagement`는 실제 `AccountManagement[M]`·`Catalog[G, P]`와 하나의 `api.PolicyAuthentication`을 받는다.
기본 namespace는 선택 모델의 설치 app label, prefix는 `/api/identity/`다. 구성은 owner의 전체 source·명시적 필드와 권한으로
22개 route·입출력·OpenAPI를 완성하며 I/O·해싱·사용자 callback을 실행하지 않는다. Schema URL과 접근 권한은 host가 정한다.
별도 CRUD callback·SQL adapter·기본 User 사본을 요구하지 않고, 각 owner가 요청마다 현재 actor·domain·대상·revision을 재검사한다.

AccountManagement의 `InputSpec`은 add/change의 노출 안에서 전체 모델 source와 역할 정규화·원문 한도를 보존한다.
모델 입력은 `fields` 객체이며 생성과 password 명령의 `credential.password`는 별도 typed 객체다. Password는 trim·정규화하지
않고 명시적 null만 사용 불가 요청이다. 실제 프로필 `password`는 `fields.password`로 유지한다. 명령의 확정된 직접 입력 거부는
`PasswordInputDiagnostics`가 확인하는 비공개 `validation.RejectionOrigin`으로 식별한다. 출처는 진단 metadata이며 error cause가
아니다. Creator와 관리자 password owner가 명령 단계에서만 표시하고, 공통 scope는 기존의 원인 없는 정확한 callback 거부만
확정 snapshot/rollback 뒤 그대로 반환한다. 출처별 예외를 transaction 판정에 추가하지 않는다. 공유 자기 변경/reset의 진단 의미를
바꾸거나 wrapped/joined·private callback·cleanup 실패를 입력으로 낮추지 않는다.

인증·CSRF·복합 권한은 경로/query·If-Revision·body보다 먼저 적용한다. 조회는 view 또는 change 및 전체 추가 권한,
계정 생성은 add+change 및 전체 추가 권한이다. Catalog 생성과 다른 쓰기도 owner의 operation/추가 권한을 사용하며
Admin staff 조건을 임의로 더하지 않는다. Canonical signed int64 key는 0·음수를 포함하고, If-Revision은 단일 canonical
positive decimal 1..MaxInt64-1이다. 누락 428·잘못된 입력 400·stale 412를 구별한다. 목록/이력 외 query와 미지·반복 query는 거부한다.

JSON 전체는 64 KiB·각 문자열 4,096 bytes 한도와 같은 중첩 parser 예산을 사용한다. Scalar의 blank/nullable·빈 문자열·
full default와 partial 생략은 Schema IR을 따른다. PUT에서도 생략한 optional collection은 보존하고 명시적 빈 배열은 해제한다.
목록은 scalar만 100개 이하로 읽으며 count와 rows가 같은 snapshot에 있다. 상세/목록은 source·key·선택 값 로드를 확인한
ModelView만 표현한다. Collection-only 읽기의 scalar 목록 객체는 비어 있다. `Profile.PasswordUsable`은 같은 credential
snapshot의 boolean만 보관하며 hash·principal identity·session material을 노출하지 않는다. History는 현재 domain의 sequence·
action·changed field names·time만 반환한다.

생성 201과 나머지 쓰기 200은 확정된 `id`·`revision` 두 정수와 Revision header만 반환한다. Delete는 마지막 관측 revision이며
no-op은 revision/audit를 늘리지 않는다. Commit 뒤 DB·model getter·인가·context 작업을 더하지 않아 자기 비활성화나 credential
교체로 현재 session이 사라져도 확인된 성공을 유지한다. Unknown은 503 `outcome_unknown`, Retry-After 없이 durable 확인이 필요하다.
입력 오류는 `/fields/...` 또는 `/credential/...`의 JSON Pointer이며 부분 JSON·잘린 진단을 성공으로 게시하지 않는다. 모든 응답은 no-store다.
공개 경로와 사용법은 [identity/api](../../identity/api/README.md), 생성 소비자와 환경별 검증은 TEST_EVIDENCE를 따른다.

## 선택 계정의 비밀번호 재설정

생성 `NewAccountPasswordResetter`는 기존 `SessionStore[M]`·그 Store의 인증기·정확한 manager와 전체 host source/역할을
검사한다. `PasswordResetConfig[M]`는 공통 불변 key ring, whole-second timeout, 순수 clock과 생성/변경에서 공유하는 실제
typed password 정책·raw byte 한도를 갖는다. 구성 자체는 I/O·clock·entropy·hasher·callback을 실행하지 않는다. 메일 source와
Web persistence는 같은 owner의 불투명 authority를 공유한다. 같은 key라도 따로 구성한 owner를 startup에서 섞지 않는다.

계정의 PrincipalID로 native snapshot을 읽으며 현재 durable domain·실제 key·credential·권한·Email·선택 LastLogin을 함께
관측한다. 서명은 공통 `auth.PasswordResetKeyRing`이 소유하고, 이후의 신규 모델/역할/소유권 세대가 같은 key·principal·hash를
가져도 승계되지 않는다. 별도 연결/owner 재구성 뒤에도 현재 세대와 허용 key가 같으면 유효한 proof는 유지된다. LastLogin은
UTC의 전체 저장 정밀도를 사용하고 부재는 명시적이다. Username·일반 profile·revision·grants는 서명 입력이 아니며 최종 현재
상태를 사용한다. Inactive/unusable/없는 계정·만료/미래/잘못된 서명은 일반 invalid token/proof로 묶는다. 읽기 실패는 별도다.

`PasswordResetCandidates`는 명시적 Email 역할이 있어야 한다. 실제 해당 필드의 DB `iexact`와 field/constant Active 역할을
사용하며 key 순서로 최대 257행을 읽는다. 256개를 넘으면 usable/Unicode 후처리 전에 전부 거부한다. 저장 Email도 UTF-8·NUL·
IR 문자 한도와 4,096-byte 상한을 먼저 확인한 뒤 Unicode 16 NFKC/casefold를 적용한다. 메일러에 줄 수신자·username·token의
완전한 집합은 같은 snapshot에서 분리한 관측으로 만든다. Scope 종료/cleanup 실패 뒤에는 prefix도 게시하지 않는다. 렌더링과
발송은 DB scope 밖에서 이루어지고 별도의 현재 계정 조회로 수신자를 바꾸지 않는다. Email 부재에서도 privileged token 발급은
가능하지만 메일 source 사용은 I/O 전에 명시적으로 거부한다.

`StartPasswordReset`은 사전 토큰 확인 뒤 유효한 기존 브라우저를 현재 저장 payload/수명으로 회전하거나, 없거나 이미 만료된
브라우저에는 익명 proof를 만든다. 쓰기를 기다리는 사이 기존 세션이 사라졌으면 새 익명 세션으로 대체하지 않는다. 입력과 현재
계정/namespace를 native fence에서 다시 확인한다. 실패한 입장에 따로 만료 정리를 commit하지 않는다. `CheckPasswordReset`은
hash·touch·cleanup·쓰기 없이 현재 proof/계정을 읽고, nil password이면 strength 검사도 생략한다. 결과는 후속 쓰기 권한이 아니다.

완료는 사전 typed 정책 뒤 DB scope 밖에서 한 번 hash하고, 이전 stamp와 같거나 unusable인 encoded 결과를 거부한다. 같은
원문도 새 salt로 실제 변경할 수 있다. 내부 준비 값은 한 호출의 bounded collision 재시도에만 쓰이며 외부로 반환하지 않는다.
최종 transaction에서 현재 proof의 target/token·인증 principal/stamp 존재와 값, 계정 key/domain/서명·typed 정책을 재검사한다.
현재 profile·grants·revision은 그대로 이어받아 Password/Revision만 갱신한다. 로그인 시각은 변경하지 않는다.

Session 만료는 token clock과 독립된 manager의 serialized clock으로 검사한다. `CheckActiveRecord`는 I/O·entropy 없이
manager 한도와 현재 clock을 적용하며, final policy 뒤의 현재 세션과 gate에서 기다린 신규 후보도 다시 확인한다. 회전 후보는
현재 생성/절대 수명과 단조로운 access/idle 시각·최신 payload를 유지한다. Proof를 지우고 회전하거나, 현재 브라우저가 대상
자신으로 로그인했다면 새 세션 ID 없이 제거한다. 같은 namespace의 모든 대상 로그인 세션 폐기와 대상 자신을 actor로 한 실제
Password 필드의 domain audit를 같은 transaction에 포함한다. 익명·다른 계정·역사 namespace 세션은 보존한다.

회전 delete/insert·password update·뒤 batch 폐기·audit 뒤의 오류는 앞선 효과와 함께 rollback한다. 확정 rollback의 직접
proof/원인 없는 policy 거부만 일반 입력으로 유지한다. Cleanup·callback scope 위반·unknown 결과는 결과 record/clear cookie
없이 원인을 보존하며 자동 재시도하지 않는다. 확정 ID collision만 manager의 최대 네 번 entropy 시도를 허용하며 hash는 반복하지
않는다. 확정 commit 뒤의 취소는 성공을 되돌리지 않는다. 실제 Form/JSON·메일·DB 재접속 소비자의 작성, 실행, 별도 process
restart와 scaffold/runtime 전체 조립은 각각 독립된 수용 범위이며 TEST_EVIDENCE에서 구분한다.

## 도메인에 묶인 계정 감사 기록

독립 `godj_account_audit` 앱은 domain·actor ID·model·object ID·action·변경 필드 이름·서버 시각을 저장한다.
기본 User나 domain/system migration에 의존하는 FK가 없으며 modeldef·generated model·초기 migration을 자체 root에서
소유한다. 호스트가 이 앱의 migration과 설치를 명시한다. 기존 적용 migration의 bytes를 고쳐 의존성을 끼워 넣지 않는다.
원본 필드 값·display label·credential/session 비밀값은 기록하지 않는다. 계정의 명시적으로 존재하는 0/음수 integer
key도 지원하며, 감사 event 자체의 순번은 양수다. 공통 Admin event도 객체 key의 부호를 존재 여부로 사용하지 않는다.

`Prepare`는 owner가 현재 권한을 확인한 domain-bound actor와 서버 시각으로 event를 준비한다. 준비된 event 자체는
권한 증표가 아니다. `AppendIn`은 live borrowed write scope에서 생성된 ORM으로 저장하며 commit·중첩 transaction·
자동 재시도·정리를 하지 않는다. 반환 Entry는 외부 owner의 commit 확인 전까지 잠정 값이다. 시각은 UTC microsecond로
정규화한다. profile 수정·revision·감사 기록 중 어느 단계가 실패해도 owner가 함께 rollback한다.

`HistoryIn`은 명시한 domain/model/object에 대해 최대 100개의 최신 기록을 선택하고 event 순서대로 반환한다.
역사 조회 인가는 호출자가 같은 scope에서 소유한다. 현재 domain을 자동 선택하거나 서로 다른 세대를 섞지 않으므로,
이전 세대의 같은 model/ID 기록이 새 계정 이력으로 이어지지 않는다. 잘못된 row·canonical JSON·시각·순서는 거부하고
종료 실패를 성공으로 바꾸지 않는다. 이 저장소는 임의 SQL writer의 역사 삭제·변조까지 막는 증명이 아니다.

## 선택 계정의 읽기 전용 기동 관측

선택 계정의 `SessionStore.InspectStartup`은 같은 credential/source·실제 typed account/catalog와 독립 domain·audit·session
저장소를 검사한다. 생성자는 I/O를 하지 않는다. 호스트는 먼저 프로젝트 migration graph를 명시적으로 적용/검사하고 기동
관측을 호출한다. 관측은 migration·계정 초기화·legacy adoption·자동 repair·session 만료 정리를 수행하지 않는다.

첫 단계는 `AppliedMigrationReader`로 세 framework-owned 앱의 현행 migration identity 전체를 읽는다. 누락·중복·알 수 없는
owned migration은 `schema_unavailable`이다. 기존 `godj_system` 이력은 데이터 조회 전에 `account_transition_required`로 거부한다.
기존 operator→고정 User adoption만으로 선택 계정 domain의 mapping/retirement를 대신할 수 없다. 그 외 앱의 이력은 해당 앱이 소유한다.
이 조회는 아래 데이터 snapshot과 별도다.
동시 schema migration을 잠그는 보증이나 project 전체 migration/constraint/index 감사로 확대하지 않는다.

데이터는 정확히 한 native snapshot에서 읽는다. Domain 검사는 전체 sequence/직전 ID chain과 모든 operation/generation의
고유성을 확인한다. 256행 keyset page와 정렬 projection을 사용해 비인접 중복도 확인하며 전체 이력을 메모리에 쌓지 않는다.
첫 page에 양수 하한을 두지 않는다. 과거 행 손상이나 scope/query/scan/iteration/close 실패도 최신 receipt를 게시하지 못한다.

선택 account/catalog는 실제 typed manager로 각 table의 scalar column을 조회하고, 선택한 세 collection의 key/FK column도
확인한다. 이것은 schema column 접근성 검사이며 전체 사용자 업무 validation이나 DB index/constraint 모양의 증명은 아니다.
각 요청은 계속 현재 domain·credential·권한·대상 무결성을 해당 요청의 snapshot/transaction에서 확인한다.

전체 account audit의 canonical encoding·시간·identity와 모든 session namespace의 global capacity·digest·payload를 검사한다.
역사 domain, 삭제된 actor/target의 감사, 익명/삭제된 계정/이전 domain의 session은 정리하지 않는다. 계정 전환이나 session 인증의
유효성은 각 owner의 현재 domain/credential 검사에 속하며 저장 encoding의 유효성과 구분한다.

이력이 완전히 비어 있을 때 선택 계정 행과 account audit도 없어야 `Fresh`다. 0·음수 계정 key는 기존 행으로 취급한다.
유효한 외부 session namespace는 빈 계정 domain과 공존할 수 있다. 소유권이 존재하면 선택 역할과 일치해야 `Ready`이며,
마지막 계정 삭제 후에도 초기화된 domain은 Fresh가 되지 않는다. 역사 receipt를 현재 권한 cache나 재초기화 허가로 쓰지 않는다.

`StartupInspection`은 snapshot 종료와 cleanup까지 성공한 뒤에만 반환하는 불변 관측이다. 실패·취소·누락/중복 callback은
빈 결과와 비밀값 없는 오류를 반환한다. Borrowed `domain.InspectIn`, `audit.InspectIn`, `database.InspectIn`의 결과는 외부 scope
성공 전까지 잠정 값이다. 공개 명령·Article/scaffold 서버는 이 경로와 선택 계정 Admin/HTTP를 함께 조립한다.
이 구현은 채운 DB의 명시적 전환·환경별 실행 및 I1 전체 수용을 대신하지 않는다.

Helpdesk의 선택 계정 조립도 같은 현재 인가 경계를 사용한다. API의 View와 Admin의 View 또는 Change,
Admin staff, form/choice/inline/collection/editor의 추가 권한을 각 업무 snapshot/fence에서 확인한다.
읽기는 생성 `UsingReadScope`로 native reader만 빌리고, 기존 감사 작업은 전체 current Principal로 domain을
선택하여 업무 transaction 안에서 기록한다. 표시명/업무 필드 값을 감사에 복사하지 않는다.
별도 base 업무 adapter와 역사 adoption 검사는 기존 계약을 검증하는 소비자로 유지하며 새 선택 조립의
인가·DB session·이력 증거로 대신하지 않는다.

## 두 선택 모델 사이의 명시적 계정 전환

`identity/model.NewTransition[S,T]`는 같은 실제 backend·전체 host source를 사용하는 서로 다른
계정 table을 묶는다. `Prepare`는 호스트의 명시적 data migration이 먼저 준비한 대상 행을 받는다. `AccountMapping`은 양쪽
전체 inventory를 1:1로 대응시키며, 같은 row key·principal·hash만으로 대응이나 소유권 이전을 추론하지 않는다.
`PrepareCreation`은 빈 대상 모델에 새 행을 생성하는 명시적 입력을 받는다. 일반 host FK/OneToOne의 명시적 재매핑은 아래
staged reference 계약을 따른다. Catalog는 같거나 아래의 명시적 전체 대응표로 연결한다.
기존 `godj_system` adoption과 선택 identity의 through 생성 수명은 I1의 잔여 작업이다.

`Prepare`는 framework migration identity를 확인한 뒤 한 native snapshot에서 현재 source actor의 명시적 전환 권한과
추가 권한·deny overlay, 전체 domain 이력·감사·session inventory, 두 typed 모델의 전체 행을 검사한다.
그룹/직접/유효 권한은 shared catalog 또는 명시적 catalog 대응표에서 정확히 같아야 하고, principal·encoded password·active/staff/superuser도
보존되어야 한다. 로그인/email은 target의 정규화 정책에 이미 맞아야 하며 조용히 수정하지 않는다. Scalar 표현/한도·
전체 unique와 추가 M2M의 존재/집합을 확인하고, 선택적 typed mapping validator에 password를 지운 사본을 제공한다.
새 프로필의 업무 정책·필드 선택 의미는 이 validator와 앞선 data migration이 소유한다.

기본 inventory 한도는 4,096개이며 명시적으로 최대 65,536개까지 정한다. 전체 행을 256개 이하 page로 읽으며
signed key를 건너뛰지 않는다. 검토할 scalar/collection 상태의 framing 포함 한도는 64 MiB다. 한도를 넘는 행이나
값을 버리고 계속하지 않는다. 반환 preparation은 원래 owner·actor·operation과 전체 source/target 상태에 묶인다.
호스트는 commit 전에 값·hash·session 원문이 없는 `TransitionManifest`를 보관한다. Manifest의 operation별 상태 digest와
정규화한 mapping의 digest는 증거 식별자이며 권한이나 비밀값이 아니다.

`Commit`은 사본 사이에서도 한 번만 시도하며 같은 native fence에서 현재 인가·전체 mapping·profile/권한과 policy를
다시 검사한다. Source의 password를 계정/operation별 unusable 표식으로 바꾸고 저장된 boolean role을 false로 만들며
revision을 증가시킨다. 기존 host의 source FK와 소속은 퇴역한 source 행에 남고, 선택한 target FK만 아래 계약으로 채운다. 현재 source domain의 인증·익명·reset proof
session 전체를 제거하되 다른 namespace는 보존한다. Source domain의 퇴역 감사와 새 generation/receipt를 같은 transaction에
확정하며 과거 감사나 session namespace를 target에 복사하지 않는다. Receipt 전후 전체 typed 행/관계 readback을 확인한다.
같은 이전 모델로 돌아가도 새 generation을 사용한다. Schema migration 동시성 잠금은 별도의 프로젝트 migration owner 책임이다.

Commit·cleanup·unknown 실패는 성공 receipt를 게시하지 않고 자동 재시도하지 않는다. `Reconcile`은 보관한 manifest를
정확한 operation·evidence·실제 predecessor 링크와 한 snapshot에서 대조하므로 restart·후속 전환 뒤에도 조회할 수 있다.
미관측 결과는 rollback 확정이 아니다. Unknown fence는 호스트가 별도로 조정한다. `domain.LookupIn`의 부재는 외부 snapshot
종료 전에는 데이터이며, `Lookup`도 cleanup 성공 뒤에만 정확한 `ErrUninitialized`를 반환한다. Scope 위반·중복 operation·
cleanup 오류는 정상 부재가 아니며 `Receipt.Follows`는 sequence 값만으로 predecessor를 인정하지 않는다.

대응표는 receipt에 `mapping_count`·`mapping_sha256`를 묶고 `godj_account_domain_mapping`의 단일 payload에 함께 저장한다.
`TransitionMappedIn`은 실제 source/target key와 검토한 revision을 source key 순으로 정규화하며 65,536쌍·16 MiB를
넘거나 source/target이 중복인 입력을 거부한다. 0/음수 key를 보존하고 principal·password·프로필 원문은 기록하지 않는다.
저장 직후 receipt와 payload를 다시 읽어 정규 형식·checksum·개수·1:1 대응을 대조한다. 실패는 외부 native owner에
반환해 receipt·대응표·계정 변경을 함께 rollback하며 잠정 receipt를 게시하지 않는다.

`LookupMappingsIn`은 같은 borrowed snapshot/transaction에서 정확한 receipt metadata와 대응표를 대조하고 사본을 반환한다.
Startup은 mapping을 선언한 모든 receipt와 실제 payload 목록을 독립 대조한다. Key만 256개씩 읽고 payload는 하나씩
검증해 최대 크기의 여러 payload를 보관하지 않는다. JSON decode에도 행 수 한도를 적용하며, checksum이 일치해도
중복 member·미지정 member·비정규 순서/공백·중복 key·누락/고아/중복 parent를 허용하지 않는다. 후속 전환으로 예전
대응표가 지워지지 않으며 mapping 없는 역사 receipt는 빈 대응표로 읽는다. 기존 `0001_initial`은 보존하고 `0002`가
mapping 저장소와 기본값을 가진 receipt 필드를 추가한다. 보관 `TransitionManifest` v4의 restart 대조는 실제 저장 대응표까지
확인한다. 사전 준비 mapping과 생성 source 목록 중 하나만 선언한다.

### 준비된 권한 catalog의 명시적 대응

`Prepare`와 `PrepareCreation`은 선택적으로 하나의 `CatalogMappings`를 받는다. Catalog 역할이 다르면 생략할 수 없다.
`Groups`·`Permissions` 각각은 source/target 전체 행의 실제 key와 검토한 revision을 1:1로 대응한다. 같은 모델을 양쪽에서
공유하면 그 모델의 row ID는 그대로 대응해야 하며, 같은 권한을 가진 다른 group 행으로 identity를 재지정할 수 없다.
Group·Permission 간 모델 교차와 두 계정 모델을 catalog 행으로 겸용하는 조합은 이 전환 owner가 처리하지 않는다.
부분 공유 catalog도 각 역할의 실제 관계·필드로 검증한다. 표시명·추가 프로필은 명시적으로 준비한 target 값을 사용한다.

각 permission 쌍의 canonical 권한 코드는 같아야 하고, 각 group의 전체 권한 key 집합은 permission 대응표로 옮긴
집합과 정확히 같아야 한다. 현재 어느 계정에도 배정되지 않은 group·permission도 포함한다. 각 계정의 직접 권한과
group 소속을 따로 재매핑한 뒤 유효 권한도 대조한다. 같은 합집합이라는 이유로 직접 권한을 group 권한으로 대체하지 않는다.
새 target 계정 생성도 이 대응표의 key로 소속을 생성하며, 실제 저장 key·현재 권한·최종 선택과 기존 원자성을 유지한다.

`MaxCatalogRows`는 모델별 기본 4,096개·최대 65,536개이며 256개 이하 page를 사용한다. 실제 typed descriptor로 두 catalog의
전체 scalar·revision·unique를 읽고, 선언된 through의 key/FK 전체를 별도로 읽는다. NULL·중복 physical pair의 의미는 유지하고,
존재하지 않는 owner/target·비정규 permission code·초과한 group 권한 집합은 거부한다. Catalog 상태와 실제 through endpoint의
framing은 account/host와 같은 64 MiB 관측 예산에 들어간다. 준비 뒤 미사용 행의 변경·행 추가/삭제·revision 없는 변경도
최종 fence에서 다시 관측한다. 계정 소속은 raw link와 typed join 결과를 독립 대조해 dangling endpoint를 숨기지 않는다.

Host 재매핑, target 생성, source 퇴역·감사와 receipt 게시 전후에도 catalog 전체를 다시 읽는다. 관측이 달라지면 모든
계정/host 변경과 session 폐기·감사를 rollback한다. 이 경로는 미리 준비된 catalog 행을 변경하거나 퇴역시키지 않는다.
공유한 catalog의 다른 소비자를 대신해 생성·삭제·revision·감사 권한을 얻는 경로로 사용하지 않는다.

Manifest v5는 값 원문 없이 정규화한 catalog 대응표를 포함한다. 저장 payload v2는 `accounts`와 `catalog.groups`·
`catalog.permissions`를 한 envelope로 묶으며, 기존 receipt의 account count와 전체 payload checksum으로 같은 transaction에
보관한다. 각 배열 65,536개 및 전체 16 MiB 한도를 적용한다. Streaming decode·정규 순서·필수/중복/미지정 member와 checksum을
검사하며 `LookupMappingsIn`도 catalog가 손상되면 account 부분만 게시하지 않는다. `LookupCatalogMappingsIn`은 명시적인 빈
대응표와 catalog 기록이 없는 이력을 구분한다. 저장 직후 전체 payload를 다시 대조하고 startup은 모든 역사 payload를 검사한다.
기존 mapping v1·manifest v4의 불변 이력은 계속 읽는다. `Reconcile`은 보관한 catalog 대응표까지 실제 receipt와 대조하며,
후속 정상 catalog 편집을 과거 전환의 실패로 바꾸지 않는다. Unknown 결과는 실제 key나 잠정 catalog 결과를 게시하지 않는다.

Catalog target 행의 자동 생성과 역사 `systemstate` 가져오기 및 전체 환경의 통합 수용은 남아 있다.
일반 host의 catalog 참조는 아래의 단계적 전환에 포함하며, 이 대응표 경로만으로 I1 전체 수용을 완료하지 않는다.

### 전환 대상 계정의 생성

생성 경로의 `TransitionTarget[T]`는 전체 source와 실제 typed collection capability, 로그인/프로필 allowlist와 FK/M2M 선택
정책을 묶는다. 입력은 일반 typed Creator와 같이 먼저 정리된 `ModelInput[T]`이며 저장 target key를 가질 수 없다.
현재 source 전체를 빠짐없이 key/revision으로 지정하고, principal·password encoding·boolean role·그룹/직접 권한은 source에서
보존한다. Target revision은 1로 시작하며 CreatedAt/LastLogin은 source에 해당 역할이 있으면 보존한다. 로그인과 추가
프로필은 명시적 입력 및 target의 IR default·정규화·validation을 사용한다. Password를 입력받거나 다시 해싱하지 않는다.
이미 게시된 파일 참조·이미지 크기도 명시적으로 고를 수 있지만 이 작업은 upload·storage 삭제를 수행하지 않는다.

준비 snapshot과 최종 relation fence는 대상의 부재, 모든 column의 실제 조회 가능성과 전체 host source의 incoming
non-NULL FK 부재를 검사한다. 빈 table에 남은 orphan reference가 새 key를 우연히 승계할 수 없게 한다. 전체 source의
현재 인가·프로필·소속과 입력 후보·선택 정책을 대조한 뒤 target들을 INSERT하고, 얻은 key로 소속/프로필 collection을
한 batch에서 저장한다. 기존 링크 삭제가 필요하면 거부한다. Source/target 전체의 독립 readback·현재 선택·unique를
확인하고, source domain의 대상 생성 감사와 기존 퇴역·session 폐기·receipt/실제 대응표를 하나의 transaction에 묶는다.
퇴역과 receipt 저장 뒤에도 target 상태와 선택을 다시 검사한다. 생성된 계정이 주는 권한으로 전환 인가를 확장하지 않는다.

대상 계정 자체가 어떤 M2M의 through로도 쓰이면 이 생성 경로는 구성 시 `unsupported`로 거부한다. 계정 INSERT가
다른 owner의 관계를 변경하는 경우의 revision·인가·감사는 별도의 수명 통합이 필요하며 현재 전환 코드가 대신하지 않는다.
정상적인 별도 through를 가진 target 소속/프로필 collection은 지원한다.

생성 preparation의 manifest에는 예상 target key 없이 source key/revision과 검토한 상태 digest를 보관한다. 실제 target key는
commit 안에서 대응표에 저장하며 확정 결과의 `Mappings`로 반환한다. Unknown이나 cleanup 실패에는 잠정 key를 게시하지 않는다.
재시작 대조는 보관 source 목록을 저장 대응표의 source/revision·초기 target revision 및 receipt의 count/checksum과 확인한다.
새 `Reconcile` 조회는 생성 capability 없이도 같은 source·역할·backend로 수행할 수 있으며, 미관측을 재시도 허가로 사용하지 않는다.

### 일반 host 참조의 단계적 전환

`NewTransitionReference[S,T,H]`는 두 선택 계정 binding과 같은 전체 source의 typed host descriptor를 묶는다.
호스트는 schema migration으로 source 모델을 가리키는 기존 FK와 target 모델을 가리키는 새 FK를 함께 준비한다.
`Fields`의 명시적 쌍은 실제 FK/OneToOne이어야 하며 기존 열의 target 의미를 암묵적으로 바꾸지 않는다.
각 쌍의 `Kind`는 `TransitionReferenceAccount`(생략/빈 값), `TransitionReferenceGroup`,
`TransitionReferencePermission` 중 하나다. 두 role의 실제 account/group/permission 모델에 모두 맞는 쌍만 허용하며
같은 숫자 key라도 세 대응표를 공유하지 않는다. 대상 열은 NULL이거나 해당 종류의 명시적 대응표와 같아야 한다.
Source NULL에는 target NULL만 허용하며 다른 target을 덮어쓰지 않는다. Catalog가 전체 또는 부분 공유된 경우에도
관측한 전체 inventory의 실제 key로 self mapping을 만들며, 대응표 생략을 임의 key 허용으로 해석하지 않는다.
준비된 target과 새로 생성한 계정 target 모두 실제 저장 key를 사용한다. 생성 준비 중 아직 없는 account key를 예상하지
않으며, host 쓰기 전에 전체 source key 집합·이미 준비한 catalog 대응과 실제 할당한 계정 대응을 다시 대조한다.

모델마다 전체 행을 256개 이하 page로 읽고 기본 4,096개·최대 65,536개를 명시한다. 최대 64개 host model과 전체 account/host
관측의 64 MiB 한도를 공유하며 초과를 생략하지 않는다. Source key·전체 scalar 값·추가/삭제된 행과 선택적 revision을 준비
snapshot과 최종 fence에서 대조한다. Revision이 없는 모델도 전체 행 관측으로 변경을 탐지한다. 모델별 필수/추가 권한은
계정 전환 인가에 합치고 현재 source actor로 다시 검사한다. Typed validator는 실제 target key가 있는 원본/최종 행의 사본을
받고 쓰기 전 및 최종 readback에서 실행한다. 빌린 reader의 재진입·보관·효과 게시는 허용하지 않는다.

같은 행의 account/group/permission 여러 쌍은 한 번의 optional revision 증가와 값 없는 한 감사로 저장한다.
No-op은 revision·감사를 만들지 않는다.
Host 쓰기 뒤 계정/catalog 권한·소속과 전체 source/target 상태를 확인하며 source 퇴역·session 폐기·receipt/대응표 전후에도
전체 host 행을 다시 읽는다. 감사나 최종 대조 실패는 대상 생성과 모든 host 쓰기까지 함께 rollback한다. Known/unknown 결과
경계를 유지하고 확정 결과만 `RemappedRows`를 반환한다. Manifest는 모델/필드 쌍·revision field·전체 행 수와 operation별
salted 상태 digest를 보관한다. Catalog 참조를 포함하는 v6는 각 쌍의 종류도 기록한다. Account-only 참조의 기존 v4/v5
wire에서는 종류를 생략하여 불변 receipt의 원래 evidence를 보존한다. Catalog 참조를 예전 버전으로 저장하거나 종류를
바꾼 manifest는 거부한다. 원문 값은 포함하지 않는다. 보관한 원래 binding/source와 manifest로 이후 schema 정리 뒤에도
역사 receipt를 대조할 수 있으며 `Reconcile`은 현재 host 상태가 영구히 같다는 주장이 아니다.

업무 M2M의 명시적 through 행은 이 일반 host로 처리할 수 있다. 선택 계정·catalog 행 자체와 선택 계정 소속/추가 프로필·
catalog 권한 M2M endpoint를 이 경로로 바꾸는 것은 해당 수명 owner가 필요해 거부한다. Schema expansion → data transition →
source FK/M2M contraction은 별도의 명시적 migration 단계다. Contracted 생성 모델로 새 계정 runtime을 다시 조립하며,
역사 source를 퇴역 행으로 남기는 현재 의미와 실제 row 삭제/역사 systemstate 이행은 구분한다.
