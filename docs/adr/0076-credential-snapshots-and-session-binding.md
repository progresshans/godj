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

Credential·Account·인증기·Directory·전환 receipt는 내부 상태를 불투명한 pointer 뒤에 둔다. Formatter가 일반 진단 형식을 가리고,
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
self-service와 reset service는 아래 별도 계약을 사용한다. Reset의 메일 요청 service는 아래 별도 계약을 사용하며 Form/API 소비자는 후속 구현이다. 관리 Form/Admin/API는 이 정책을 사용한다.

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
인가 실행 오류는 다른 권한으로 우회하지 않는다. OpenAPI의 `x-godj-any-permissions`도 같은 대안을 명시하고
conjunction과 혼합한 선언은 거부한다. API의 모델 권한과 Admin의 active staff admission은 별개다.
Manager는 HTTP에서 받은 principal의 과거 grant를 쓰기 권한으로 신뢰하지 않고 현재 저장 상태를 다시 확인한다.

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

ModelConfig의 공통 Form은 편집을 소유하고 CreateForm은 생성의 model field 선택·override·비저장 입력·cross validator를
별도로 소유한다. 비저장 입력은 선택하지 않은 저장 필드를 포함해 Schema IR의 어떤 field도 가리지 못한다.
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

User 생성은 username·password1·password2만 받는다. Profile/role·그룹·직접 권한은 편집 Form이 소유하고,
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
한도를 넓히지 않고 입력 한도를 좁힐 수 있다. Username 생성은 Python whitespace strip·NFKC·150자·문자 문법을 적용한다.
편집은 IR의 256자 한도를 사용해 API/CLI에서 정상 생성한 긴 이름도 Form initial과 저장을 왕복할 수 있다.
각 Form은 raw 글자 수가 해당 한도를 넘으면 NFKC를 생략하는 UsernameField의 비용 경계를 유지한다.
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

전체 UserCreationForm 호환은 self-service/reset 등 남은 lifecycle 구현과 구분한다.
[독립 observer](../../conformance/runners/django/identity_admin_reference.py), 실행 source/환경/범위는 TEST_EVIDENCE를 따른다.

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
[Password validation](../../identity/PASSWORD_VALIDATION.md)에 기록한다. 임의 Python user 속성 반사나 전체 UserCreationForm·self-service/reset 완료를 뜻하지 않는다.

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
전체 UserCreationForm 동작과 self-service/reset·다른 인증 provider는 후속 범위다.

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
rollback/unknown 검증을 구분한다. Reset, 전체 UserCreationForm, token issuer/JWT/OAuth/OIDC는 남은 구현이다.
실행 범위와 환경별 완료는 TEST_EVIDENCE에서 별도로 확인한다.

## 비밀번호 재설정 token과 원자 저장

`identity.PasswordResetter`는 `ManagementBackend`와 명시적 hasher·`PasswordResetConfig`에 결합한다.
서버 내부의 `IssueToken(ctx, principalID)`는 현재 active/usable 계정을 읽어 수신자에게만 전달할 값을 만든다.
이 함수를 identifier 기반 공개 조회 API로 노출하지 않는다. 수신자 선택·email 정규화·메일 전달은
아래 `PasswordResetMailer`가 소유하며 실제 HTTP 소비자는 후속 연결이다. `CheckPassword`는 token과 선택적인 새 password를 읽기 전용으로 검사하며,
새 hash·write admission·session을 만들지 않는다. Raw token은 명시적인 `Encoded()`로만 꺼내고 fmt/JSON에서는 감춘다.

`PasswordResetKeyRing`은 active 1개와 validation 전용 최대 7개의 서로 다른 32-byte 키를 소유 복사한다.
키의 생성·보관·배포는 host가 맡고 CSRF/session 키와 공유하는 API를 제공하지 않는다. 검증은 모든 등록 키를 확인한다.
Token은 domain-separated HMAC-SHA256의 전체 MAC과 canonical base-36 Unix 초를 raw URL-base64와 함께 인코딩한다.
HMAC 입력은 길이 framing한 User ID·stable principal ID·credential stamp·email 원문·UTC last_login·발급 시각이다.
URL에는 password·stamp·email을 넣지 않으며 wire 형식은 Django와 호환되지 않는다.
기본 만료는 Django와 같은 72시간이고 host는 양의 정수 초 duration을 선택한다. 경계 초는 유효하며 미래 발급은 거부한다.
Key ring과 validator 목록은 생성 후 불변이고 clock/validator callback은 pure·concurrency-safe여야 한다.

`ResetPassword`는 preflight read scope를 닫은 뒤 password policy·새 hash를 처리하고, native write fence 안에서
현재 행과 token의 credential/email/last_login·active/usable·만료를 다시 검사한다. 현재 profile로 policy도 다시 적용한다.
변경되지 않은 encoded password를 돌려주는 hasher는 거부한다. Password와 **현재 revision + 1**만 patch하며,
대상의 모든 session 폐기와 본인을 actor로 한 값 없는 password audit를 같은 transaction에 저장한다.
사용자를 로그인시키거나 현재 브라우저 session을 보존하지 않는다. Username/profile/role/revision만 바뀌면
token을 불필요하게 거부하지 않되 최신 값을 덮어쓰지 않는다. 두 연결에서 같은 token을 제출하면 하나만 성공한다.

없는/비활성/사용 불가/변경된 계정, malformed/만료/다른 서명은 원인 구분 없는 `ErrInvalidResetToken`이다.
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

`identity.PasswordResetMailer`는 하나의 `PasswordResetter`, 명시적 `mail.Sender`와 mail 설정에 결합한다.
`CleanPasswordResetEmail`은 고정 PasswordResetForm의 strip·required·EmailValidator·254자·NUL 진단을 공유한다.
Raw UTF-8와 4,096 byte 한도는 Unicode 작업 전에 검사한다. EmailValidator 문법은 공통 `validation.ValidEmail`이 소유하며
기존 Identity Admin도 같은 함수를 사용한다. Model 의미는 Schema IR에 남고 254자 입력 제한은 native reset form의 계약이다.

수신자 선택은 한 native read snapshot에서 `active=true`와 DB의 `Email.IExact`로 후보를 찾은 뒤,
고정 Unicode 16의 NFKC + full casefold 및 usable password를 검사한다. CPU 정규화로 전체 사용자를 검색하지 않는다.
Case folding은 공식 Unicode 16 CaseFolding C/F data에서 생성하고 모든 scalar의 독립 CPython 결과와 비교한다.
SQL 단계에서 다른 이메일인 값은 Unicode 정규화 결과가 같더라도 선택하지 않는다.
후보는 ID 순서이며 active/DB-iexact 후보 256개까지 전부 읽는다. 초과·읽기 실패·callback 위반·cleanup 오류이면 아무 메일도 보내지 않는다.

각 수신 주소와 token은 **같은 Account snapshot**에서 만든다. 나중에 계정을 다시 읽어 새 token만 발급하면
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
실제 proof session persistence·Web runtime·Form/API·독립 client 연결은 후속 구현이며 native fixture 확보나
transaction composition의 검증을 공개 HTTP 흐름의 완료로 표시하지 않는다.
