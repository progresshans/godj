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
Result에는 실제 실행한 Chromium 버전도 포함된다.

Probe는 초기 DB를 전제로 데이터를 저장하므로 반복할 때 서버를 다시 시작한다. Script 응답을 바꾸는 부정 대조 후에도
브라우저 세션을 새로 열어 immutable asset의 변조된 응답/실행 상태가 정상 검증에 남지 않게 한다. 실제 저장·rollback·audit의
SQLite/PostgreSQL 검증은 [Helpdesk HTTP 검사](../../../examples/helpdesk/admin_inlines_test.go)와 별도다. 정확한 실행 환경과
성공/실패 기록은 [TEST_EVIDENCE](../../../docs/status/TEST_EVIDENCE.md)에 있다.

파일 입력 검증은 새 fixture와 새 브라우저 세션에서 `uploads_probe.js`를 같은 run-code 방식으로 실행한다. 20개 조건을
검사하며 label의 선택 파일 내용이 입력한 이름과 같은지 transaction 안에서 확인한다. 일부러 잘못된 파일을 제출할 때
부모 이름도 바꿔 파일 검증 전에 부모를 저장하게 한다. 오류 화면의 원래 부모 입력 보존과 별도 인증된 조회의 부모 저장값
복원·자식 cohort 보존을 확인한다. 부모 입력을 원래 값으로 되돌리고 파일을 재선택한 뒤에는 SQLite label 저장과 부모값
보존을 확인한다. 이는 비저장 파일 명령의 소비자이며 영구 파일 storage의 예제가 아니다.
파일 선택 유지/재번호, 오류 재표시, readonly의 disabled/successful control 제외도 검사한다. 기본 `probe.js`의 33개 조건은
파일이 없는 multipart 제출에서도 그대로 적용한다. 두 probe는 각각 새 DB를 사용한다.

원격 CI에서는 Portable normal integration이 [Admin browser runner](../../../scripts/ci/admin_browser.py)로 세 흐름을
각각 새 fixture·별도 브라우저 세션에서 실행한다. 기존 Helpdesk browser owner와 동일한 고정 CLI/Chromium을 쓰며
추가 npm 잠금이나 전역 도구 설치는 필요하지 않다. 각 흐름의 검사 목록·실제 버전, native 저장 행/부모 연결,
기존 행의 모든 scalar 보존, browser/server 종료와 임시 DB 삭제를 확인한다. 원본 로그·snapshot·screenshot·source와
receipt는 `admin-inline-browser` artifact에 보존한다. 이 브라우저의 파일은 여전히 비저장 검증 명령이며
모델 파일 storage 게시와 실제 PostgreSQL의 검증을 대신하지 않는다.

관계 선택 UI는 `GODJ_ADMIN_BROWSER_MODE=choices go run ./admin/testdata/browser`로 별도 fixture를 시작하고
`choices_probe.js`를 실행한다. 34개 조건을 검사하며 고정된 35개 허용 label 후보와 별도 category의 후보를 사용한다. 일반 Ticket의 M2M,
TicketLabel의 FK, Category→Ticket typed inline M2M은 모두 공개 `NewRelationChoices` / `NewRelatedChoices`를
소비하며 writer는 같은 transaction에서 선택 키를 다시 Resolve하거나 `BindCurrent`로 재검증한다. Label의 이름을
다른 인증된 Admin 탭에서 바꾸면 현재 scope에서 제외되므로 오래된 검색 결과의 제출 거부도 실제 저장 상태로 확인한다.

이 흐름은 선택값을 제외한 검색 페이지 상한, 이전/다음, 페이지 밖 선택 유지, 0·음수·2^53 초과 문자열 ID,
label escaping, 다중 선택 유지·해제·저장, 지연 응답 무시, 오류 후 재시도, JavaScript 없는 초기 선택,
새 inline·재번호·현재 저장 cohort·readonly, 잘못된 선택·대상 권한 거부·오래된 대상 거부를 검사한다.
응답 지연과 일시 오류만 Playwright route로 제어하며 후보 내용·검색·저장은 실제 fixture와 SQLite를 사용한다.
마지막 native receipt는 전체 category/label/ticket/link cohort를 문자열 ID로 기록하고, 의도한 대상 이름 변경을 제외한
초기 모델의 모든 scalar 보존과 최종 관계를 확인한다. 브라우저 검사는 final writer 도중 별도 DB transaction이
동시에 정책을 바꾸는 경쟁, PostgreSQL, 실제 보조공학 사용자 검증의 증거가 아니다.

CI runner는 기존 `inline` 33개·`uploads` 20개 검사와 `choices` 34개 검사를 각각 새 SQLite와 Chromium context에서
실행한다. 새 fixture의 Go 소스 hash도 receipt에 기록하고 같은 `admin-inline-browser` artifact 안에 각 흐름의
원본 로그·snapshot·screenshot·완전한 검사 목록과 native receipt를 남긴다. 검사 작성·syntax/compile 통과와
Hosted 실행 성공은 별개이며, 실제 실행 결과는 해당 source commit의 CI receipt로 판단한다.
