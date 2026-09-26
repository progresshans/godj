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

로그인 검증 뒤 저장소가 바뀌어도 이미 확인한 credential만 새 세션에 담는다. 다음 요청이 변경을 감지한다.
한 번 허용한 요청을 소급 취소하거나 자동 재로그인·재시도하지 않는다. 로그인과 비밀번호 변경을 하나의 DB transaction으로
직렬화한다는 보장은 이 공통 경계에 없다. 실제 durable maintenance의 transaction·revocation 소유권은 별도로 구현한다.

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

모든 협력 writer는 같은 fence와 revision 증가를 따라야 한다. 임의 SQL이나 이미 승인된 요청까지 소급 통제하는 보장은 아니다.
이전 credential로 시작한 동시 로그인이 commit 뒤 오래된 stamp의 새 세션을 만들 수 있어 다음 요청의 resolver 검사도 유지한다.
자기 계정을 관리자 권한으로 교체해도 현재 세션을 특별히 유지하지 않는다. 자기 비밀번호 확인·현재 세션 회전을 제공하는
self-service 흐름과 password reset, 관리 Form/Admin/API는 후속 구현이다.

실패나 unknown outcome에는 Profile을 게시하지 않고 자동 재시도하지 않는다. Unknown rollback/commit 분류는 일반 callback
오류보다 우선하며 `errors.Is/As`로 확인한다. 정상 commit 뒤 늦은 취소는 이미 확인된 성공을 뒤집지 않는다. 현재 revision과
audit 조회만으로 특정 요청의 성공을 증명한다고 주장하지 않으며, 이 서비스는 일반적인 command receipt/idempotency API를 제공하지 않는다.
