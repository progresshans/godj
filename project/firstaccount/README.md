# 선택 모델의 초기 계정 명령

`New(definition, open, config)`는 생성된 `FirstAccountDefinition[M]`과 실제 backend opener를 조립한다.
선언은 Schema IR·선택 역할·정규화·필수/default·파일 검사 정책을 유지하고 DB나 파일 저장소를 열지 않는다.
Scalar/FK/OneToOne과 파일 choices만 받는 명령은 `Config{}`를 사용한다. 업로드 필드가 있으면 한도와 보고 함수를 명시한다.

```go
command, err := firstaccount.New(definition, open, firstaccount.Config{
    MaxFileBytes:  16 << 20,
    MaxFilesBytes: 32 << 20,
    ReportFiles:  reportFilePublications,
})
```

`ReportFiles`는 `func(context.Context, firstaccount.FileReport) error`이며 애플리케이션이 구현한다.
선택 파일의 storage·이름 생성·이미지 정책은 선언의 `CreationPolicy.Files`에 있다. 이 명령 설정이 저장 권한을 만들지는 않는다.
호스트의 `EvidenceSHA256`은 callback 정책을 식별한다. 파일당/합계 한도도 Description fingerprint에 묶인다.
두 한도는 양수이고 파일당 ≤ 합계 ≤ 64 MiB여야 한다. Scalar는 기존 필드당 4,096 bytes·합계 65,536 bytes 한도를 유지한다.

공개 `createsuperuser`는 실제 runtime의 선언과 프로젝트 source를 확인한 뒤 TTY에서 값을 받는다. 업로드 필드는 local
file path를 묻고 선택 필드의 생략을 별도로 확인한다. 상대 경로는 명령의 작업 디렉터리를 기준으로 하며 shell 확장은 하지 않는다.
한 번 연 regular file의 handle에서 크기와 bytes를 읽고 닫는다. FIFO·device·directory, 한도 초과, 읽는 동안 관측한 크기/시각
변경과 읽기/닫기 오류는 입력 실패다. 읽기 사이에 context/interrupt를 확인한다. 자식에게 local path를 전달하지 않는다.

Private v3 frame은 scalar의 존재 여부·정확한 bytes, 파일의 basename·길이·bytes와 원문 비밀번호를 구분한다.
빈 파일과 생략은 다르다. 전체 길이 prefix를 먼저 제한하고 실제 크기만큼 할당하며 EOF·중복·종류·개별/합계 한도를 검사한다.
문자열 값·파일 bytes·password는 argv, 환경 또는 JSON으로 전달하지 않는다. 정적인 파일 choices는 선언된 문자열 선택이다.

직접 호출하는 코드는 `Value{Upload: &Upload{Name: basename, Content: bytes}, Present: true}`를 사용한다.
`Prepare`는 정확한 source/order를 검사하고 파일을 독립적인 `uploads.Buffer`로 복사한 뒤 실제 ModelForm으로 검증한다.
이미지 내용/크기 검사는 DB 연결 전에 수행한다. `Value.Clear()`는 호출자 입력을 비우며 준비된 사본을 바꾸지 않는다.
`Prepared.Clear()`는 시작하지 않은 준비를 포기한다. 복사본도 한 번의 시도와 같은 수명을 공유한다.

`Provision`에는 호출자가 게시 전에 보관한 operation ID와 실제 backend를 전달한다. 최초 계정 owner가 현재 DB의 부재를
확인하고, scope를 닫은 뒤 게시하고, 실제 이름/정책을 다시 검사한 후 해싱·최종 transaction을 수행한다. 모든 upload와
열린 reader는 작업 종료 또는 포기 때 만료된다. Backend의 획득/닫기는 process owner가 한 번씩 수행한다.

반환 `Result.FirstAccountCreation`은 확정된 계정/소유권 metadata이고 `Files()`는 모든 게시 시도다. 반환 error는 생성
실패를 나타내며 `Result.ReportErr()`는 별도의 보고 실패다. 일반 진단은 closed code만 표시하고 원인 오류는 명시적인
`errors.Is`/`Unwrap`으로 보존한다. 게시 시도가 있으면 `ReportFiles`를 한 번 호출한다.
`FileReport`는 operation ID·확정 Creation·각 `FilePublication`·생성 오류를 분리해 제공한다. 보고 함수는 이 결과를 보관하고
동시 호출에 안전해야 한다. 같은 context를 전달하고 panic/Goexit를 포함한 callback 종료를 기다리며 자동 재시도하지 않는다.
보고 실패가 확정 commit을 취소하지 않으며 DB 실패/unknown도 이미 게시한 파일을 지우지 않는다.

공개 JSON은 operation ID·DB 상태·오류 코드와 `cleanup_failed`/`file_report_failed`만 출력한다. 실제 파일 이름/결과는
보고 owner가 보관한다. 확정 생성 뒤 응답이 유실돼도 private exit 86/87은 생성/정리 결과를, 88/89는 추가 보고 실패를
보존한다. 부모는 완전한 한 번의 전달과 child 종료를 확인한 경우에만 이 exit를 사용한다. `unknown`의 false flag가 보고 성공을
증명하지는 않는다. 보관한 operation과 `domain.Lookup`·게시 보고를 대조하며 자동 생성 재시도나 파일 삭제를 하지 않는다.

일반 M2M profile 입력과 채운 DB의 계정 모델 전환은 [활성 I1 작업](../../work/0138-custom-account-lifecycle.md)에 남는다.
구현·컴파일·실제 환경별 수용은 [TEST_EVIDENCE](../../docs/status/TEST_EVIDENCE.md)에서 구분한다.
