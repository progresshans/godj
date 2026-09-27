# ADR-0078 — 메일 값 소유권과 SMTP 접수 결과

- 상태: accepted design; 구현·환경별 검증은 [Evidence](../status/TEST_EVIDENCE.md) 참조
- 날짜: 2026-09-28
- 관련 작업: [GDJ-0100](../../work/0100-multi-user-credential-and-session-lifecycle.md), [Contrib 카탈로그](../CAPABILITY_CATALOG.md)
- 독립 기준: [Django mail 관찰](../../conformance/runners/django/mail_reference.py), [고정 fixture](../../mail/testdata/django61.json)

## 결정과 이유

Password reset의 수신자 선택·token 발급과 공통 메일 전송을 분리한다. `mail.Sender.Send(ctx, Message) error`는
backend의 접수 결과만 소유하며 사용자 계정 조회·메일 화면·재설정 완료나 inbox 도착을 판정하지 않는다.
설정과 message를 전역 변수로 두지 않고 host가 명시적으로 생성해 소비자에 전달한다.

`Address`와 `Message`는 생성 후 불변이다. 수신자 slice·header map·첨부 byte는 생성 때 소유 복사하고,
호출자가 명시적으로 요청하는 수신자와 MIME byte는 별도 slice로 반환한다. 값 복사끼리는 불변 데이터를 공유한다.
fmt/JSON은 주소·본문·첨부·인증 설정을 숨긴다. `mail.Error`는 안정 code/stage만 출력하고 실제 I/O 원인은
명시적 `errors.Is/As/Unwrap` 경로로 보존한다. 서버 reply를 기본 로그에 포함하지 않는다.

메일은 From, To/Cc/Bcc, Reply-To, Subject, Text/HTML alternative와 binary attachment를 지원한다.
Bcc는 envelope에만 넣고 message header에는 넣지 않는다. CR/LF·제어문자·잘못된 UTF-8을 header 입력에서 거부하고
MIME·envelope·Date·Message-ID 같은 필드는 개별 typed 입력과 renderer만 소유한다. Extra header는 unstructured 값이다.
본문은 quoted-printable, 첨부는 base64, 제목·표시 이름은 UTF-8 encoded word로 출력한다. Wire encoding 자체의
Django byte 일치는 목표가 아니며 독립 decoder가 관찰하는 내용·수신자·MIME 구조를 비교한다.

메시지의 합산 입력 상한은 16 MiB, envelope 수신자는 최대 256개다. UTF-8 body의 전송 인코딩 팽창을 허용하되
wire는 64 MiB와 한 줄 998 byte 안에 둔다. 메시지마다 새 random Message-ID와 UTC 시각을 부여한다.
명시적 시각/ID를 받는 `Bytes`는 같은 입력에 같은 결과를 반환한다. MIME 생성에는 DB transaction을 사용하지 않는다.

## 전송 경계

`SMTP`는 메시지마다 별도 연결을 열고 context와 기본 30초 timeout으로 전체 연결·협상·전송·정리를 제한한다.
STARTTLS 필수, 처음부터 TLS, 명시적 plaintext 중 하나를 구성해야 하며 자동 downgrade는 없다.
TLS는 최소 1.2·인증서·서버 이름을 검사하며 CA pool을 소유 복사한다. 인증은 검증된 TLS 위의 AUTH PLAIN만
지원하고 미지원 mechanism은 오류다. Plaintext 설정에 인증 정보를 넣는 것은 구성 오류다.
EHLO의 IP는 address literal로 표시하고, 서버 응답은 TLS record를 포함해 4 MiB로 제한한다.

IDN domain은 고정 `x/net/idna` lookup profile의 transitional mapping을 사용한다. UTF-8 local part는 변경하지 않고
서버의 SMTPUTF8 capability를 요구한다. SMTP transport의 mailbox 문법과 identity의 EmailField 검증은 별개다.
`x/net` 도입으로 선택되는 `x/text`와 `x/sync`의 module graph·기존 소비자 영향도 검증한다.
이 변경은 고정 `internal/unicode16`의 identity/form 의미를 대체하지 않는다.

모든 RCPT가 수락되기 전에는 DATA를 보내지 않는다. 앞 수신자 일부만 수락된 뒤 거절되면 그 연결을 닫고
전체 메시지를 제출하지 않는다. 본문 쓰기 실패 시 DATA writer를 닫아 잘린 메시지를 제출하지 않는다.
마지막 terminator의 쓰기 오류와 최종 응답 읽기 오류도 각각 검사한다.

- `not_sent`: 최종 DATA terminator 전 실패. 완전한 메시지를 제출하지 않았다.
- `rejected`: 서버가 명시적으로 4xx/5xx로 거절했다.
- `outcome_unknown`: 최종 DATA 쓰기/응답을 확정하지 못했다. 접수됐을 수 있으므로 자동 재시도하지 않는다.
- nil: 최종 DATA의 250을 확인했다. 이후 QUIT·연결 정리 실패나 늦은 취소로 결과를 되돌리지 않는다.

이는 [RFC 5321 4.2.5](https://www.rfc-editor.org/rfc/rfc5321.html#section-4.2.5)의 접수 책임 경계다.
전송 backend는 queue·retry·partial delivery 복구를 추측하지 않으며 수신자에게 실제 도착했다고 주장하지 않는다.
새 provider가 추가되면 같은 결과 경계에 맞추거나 지원 불가능함을 명시해야 한다.

## 메모리 backend와 남은 소비자

`Memory`는 instance별 bounded 저장소다. 기본 100 messages/64 MiB wire 상한에서 전체 메시지만 추가하고,
가득 차면 기존 접수를 보존하면서 `capacity`로 거부한다. 동시 Send/Snapshot/Drain은 같은 mutex로 직렬화하며
등록 이후의 취소는 접수를 되돌리지 않는다. Snapshot은 불변 delivery 값들을 복사하고 Drain은 소유권을 이전한다.
이 backend는 process 종료 시 사라지는 개발·시험용 저장소이고 durable outbox를 뜻하지 않는다.

고정 Django 관찰의 MIME·Bcc·IDN/quoted address·locmem 소유 복사·header injection을 비교한다.
빈 수신자의 no-op, arbitrary raw header override, Python mutable object 모양은 채택하지 않는다.
현재 Go의 명시적 한도·강제 TLS·SMTPUTF8와 오류 결과의 차이는 [DEV-0019](../DEVIATIONS.md#dev-0019--메일-소유권과-명시적-smtp-접수-결과)에 기록한다.

Reset request의 active/usable 수신자 선택·메일 template·공개 응답은 다음 소비자가 소유한다.
없는 계정과 전송 실패의 공개 응답을 같게 유지하면서 내부 오류를 operator가 확인하는 정책이 필요하다.
메일 기반의 로컬 검증을 reset Form/API·독립 client 또는 실제 운영 SMTP provider 검증으로 전이하지 않는다.
