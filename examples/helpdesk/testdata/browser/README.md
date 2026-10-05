# Helpdesk 여러 티켓의 실제 브라우저 검증

저장소 root에서 `go run ./examples/helpdesk/testdata/browser`를 실행한다. 출력한 loopback URL은 새 임시 SQLite의
실제 Helpdesk Admin/API·티켓 편집기, identity·session·audit 저장소를 사용한다. Fixture 로그인은
`operator` / `demo-password`다. 배포용 서비스나 운영 자격 증명의 예제가 아니다.

Node.js와 Playwright 브라우저가 준비된 환경에서 새 세션을 열고 세 probe를 순서대로 실행한다.

```sh
npx --yes --package @playwright/cli@0.1.22 playwright-cli -s=godj-bulk open '출력한 URL' --headed
npx --yes --package @playwright/cli@0.1.22 playwright-cli -s=godj-bulk run-code --filename examples/helpdesk/testdata/browser/bulk_probe.js
npx --yes --package @playwright/cli@0.1.22 playwright-cli -s=godj-bulk run-code --filename examples/helpdesk/testdata/browser/editor_probe.js
npx --yes --package @playwright/cli@0.1.22 playwright-cli -s=godj-bulk run-code --filename examples/helpdesk/testdata/browser/bulk_update_probe.js
npx --yes --package @playwright/cli@0.1.22 playwright-cli -s=godj-bulk close
```

첫 probe의 실제 Result는 `passed: 22`, 두 번째와 세 번째는 각각 `passed: 9`여야 한다. CLI exit code와 함께 `### Error`가 없는지도
확인한다. 재실행은 새 서버/DB와 새 브라우저 세션에서 한다. 첫 probe는 행 추가·제거·재번호·최소/최대·입력/선택 보존,
잘못된 뒤쪽 행의 전체 거부와 수정 후 두 티켓 저장을 확인한다. 두 번째는 기존 행 변경과 두 새 행의 bulk 생성,
UUID 별칭 중복 거부와 전체 rollback, 수정 후 함께 commit된 scalar·라벨을 실제 HTTP로 읽는다.
세 번째는 Admin의 선택 close·반복 시 0건·reopen, 선택하지 않은 행/필드/라벨 보존과 편집기의 기존 두 행 bulk 수정을 확인한다.

서버를 Ctrl-C로 종료하면 listener를 닫고 committed Ticket·TicketLabel과 티켓별 durable audit를 stdout에 출력한 뒤
자신의 DB 디렉터리를 정리한다. 세 probe 뒤 최종 티켓은 5개, 링크는 4개이며 durable audit는 10개다.
기존 티켓은 change 2개, 나머지 네 티켓은 각각 add 1개/change 1개가
`browser-operator` actor로 남아야 한다. 최종 audit 조회는 브라우저 결과와 독립적이다. 이 SQLite 브라우저 검사는
양 DB의 취소·rollback/commit unknown·native batch 실패·동시 요청 검사를 대신하지 않는다.
