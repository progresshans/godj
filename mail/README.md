# 메일

`mail`은 소유 복사된 message와 `Send(context.Context, Message) error`를 제공한다.
`ParseAddress`로 단일 주소를 만들고 `NewMessage(MessageConfig{From, To, Subject, Text, ...})`로 메시지를 구성한다.
To/Cc/Bcc·ReplyTo, Text/HTML alternative, MIME type을 명시한 attachment를 지원한다.
입력 slice/map/byte를 나중에 수정해도 만들어진 message는 바뀌지 않는다.

실제 전송은 `NewSMTP(SMTPConfig{Address: "smtp.example.test:587", Security: SMTPStartTLS, ...})`로 생성한다.
Address에는 명시적 port를 넣으며 STARTTLS 필수·implicit TLS·plaintext 중 하나를 선택한다.
Username/Password는 검증된 TLS에서만 사용하고 host가 비공개 설정으로 공급한다. 자동 downgrade·retry는 없다.
기본 timeout은 30초이며 호출 context의 더 짧은 deadline과 취소를 따른다.
nil은 서버가 접수했다는 뜻이며 inbox 도착이나 사용자의 확인을 뜻하지 않는다.

오류는 `errors.Is(err, &mail.Error{Code: mail.CodeOutcomeUnknown})`처럼 분류할 수 있다.
`outcome_unknown`이면 접수됐을 수 있어 자동으로 다시 보내면 안 된다. `rejected`는 명시적 서버 거절,
`not_sent`는 완전한 제출 전 실패다. 접수 후 QUIT 실패는 nil 결과를 바꾸지 않는다.
fmt/JSON은 message·SMTP 설정·서버 reply를 숨긴다. `Mailbox`, `Text`, `Bytes`와 오류 원인을 꺼내는 호출은
민감 자료를 명시적으로 읽는 경계이므로 일반 진단에 넣지 않는다.

테스트와 개발에는 `NewMemory(MemoryConfig{})`를 주입한다. 기본 100 messages/64 MiB wire 상한을 사용하고
`Snapshot`으로 확인하거나 `Drain`으로 가져오며 가득 찬 저장소는 새 메시지를 거부한다. 이 backend는 영속 queue가 아니다.
Message 원본 입력 합계는 16 MiB, 수신자는 최대 256명, filename은 255 byte다. SMTPUTF8가 필요한 주소는
서버 지원 없이 전송하지 않는다. Extra header는 unstructured 값이며 CR/LF나 reserved field override를 허용하지 않는다.

메시지·transport와 Identity의 reset 수신자 선택/메일 요청 service를 구현했다. 실제 reset Form/API 연결은 남아 있다.
설계와 차이는 [ADR-0078](../docs/adr/0078-mail-message-ownership-and-delivery.md),
실제로 실행한 환경과 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
