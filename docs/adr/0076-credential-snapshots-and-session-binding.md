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
현재 권한은 매 요청의 Principal에서 읽는다. 사용자·그룹·권한의 durable 저장은 이 경계를 사용하는 후속 구현이다.
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
