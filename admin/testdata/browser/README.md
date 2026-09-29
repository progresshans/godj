# Admin inline 브라우저 검증

이 명령은 공개 GoDj API로 Category에 Label·Ticket inline을 연결하는 로컬 소비자다. 임시 SQLite에 migration과 초기
데이터를 만들고 loopback 임의 포트에서 실행한다. 운영 인증/감사 저장소의 예제가 아니다. 종료 시 자신의 DB를 정리한다.

저장소 root에서 실행한다.

```sh
go run ./admin/testdata/browser
```

출력한 URL을 따옴표로 감싸 새 Playwright CLI 세션에서 연다. 검증한 CLI 버전은 `@playwright/cli@0.1.22`다.
Node.js와 Playwright가 사용할 브라우저가 필요하다. 아래 명령도 저장소 root에서 실행한다.

```sh
npx --yes --package @playwright/cli@0.1.22 playwright-cli -s=godj-inline open '출력한 URL' --headed
npx --yes --package @playwright/cli@0.1.22 playwright-cli -s=godj-inline run-code --filename admin/testdata/browser/probe.js
npx --yes --package @playwright/cli@0.1.22 playwright-cli -s=godj-inline close
```

서버는 Ctrl-C로 종료한다. `editor`, `noadd`, `readonly`, `viewer`는 fixture 전용 계정이고 비밀번호는 모두
`demo-password`다. Probe는 실제 로그인·폼 제출을 통해 33개 조건을 검사하고 마지막에 `passed: 33`을 반환한다.
CLI의 exit code만으로 성공을 판단하지 말고 `### Error`가 없고 Result에 실제 33개가 있는지 확인한다.

Probe는 초기 DB를 전제로 데이터를 저장하므로 반복할 때 서버를 다시 시작한다. Script 응답을 바꾸는 부정 대조 후에도
브라우저 세션을 새로 열어 immutable asset의 변조된 응답/실행 상태가 정상 검증에 남지 않게 한다. 실제 저장·rollback·audit의
SQLite/PostgreSQL 검증은 [Helpdesk HTTP 검사](../../../examples/helpdesk/admin_inlines_test.go)와 별도다. 정확한 실행 환경과
성공/실패 기록은 [TEST_EVIDENCE](../../../docs/status/TEST_EVIDENCE.md)에 있다.

파일 입력 검증은 새 fixture와 새 브라우저 세션에서 `uploads_probe.js`를 같은 run-code 방식으로 실행한다. 17개 조건을
검사하며 label의 선택 파일 내용이 입력한 이름과 같은지 transaction 안에서 확인한다. 이름이 다르면 field 오류와 rollback,
재선택 후에는 SQLite label 저장을 확인한다. 이는 비저장 파일 명령의 소비자이며 영구 파일 storage의 예제가 아니다.
파일 선택 유지/재번호, 오류 재표시, readonly의 disabled/successful control 제외도 검사한다. 기본 `probe.js`의 33개 조건은
파일이 없는 multipart 제출에서도 그대로 적용한다. 두 probe는 각각 새 DB를 사용한다.
