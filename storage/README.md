# 이름이 있는 파일의 저장

`storage.Backend`는 `Save`, `Open`, `Stat`, `Delete`를 context/error와 연결한다. `Save`가 돌려주는 `Info.Name()`은 실제
저장된 상대 이름이다. 이름 충돌과 길이 제한으로 제안한 이름과 달라질 수 있다. 이름이나 `Info.Valid()`는 읽기/삭제/서빙
권한이 아니다. DB에는 후속 모델 FileField가 이 상대 이름을 기록하며 실제 I/O는 명시적인 backend가 소유한다.

## 로컬 파일 저장소

`OpenFilesystem(ctx, FilesystemConfig{Directory: directory})`는 이미 존재하는 application 소유 디렉터리의 `os.Root` handle을
연다. 원본 경로 문자열을 다시 결합해 파일을 열지 않는다. 새 디렉터리는 0700, 새 파일은 0600으로 생성하며 기존 디렉터리의
권한은 바꾸지 않는다. Windows ACL과 root의 운영 권한은 배포 설정이 소유한다. Root는 권한 있는 외부 writer·bind mount·
특수 파일에 대한 sandbox가 아니므로 다른 신뢰 경계의 프로세스가 이 디렉터리를 수정하지 않게 관리한다.

기본 한도는 파일당 32 MiB, 전체 저장 이름 1,024 Unicode 문자, 이름 충돌 시도 64회다. `FilesystemConfig.Limits`의 0 값은
각 기본값을 사용하고 음수/범위 밖 설정은 거부한다. `SaveOptions.MaxLength`는 모델 필드 등의 더 작은 이름 한도를 적용한다.
각 경로 성분은 최대 255 bytes이며 절대 경로, 빈/점 경로 성분, backslash, 제어 문자, Windows 장치 이름/금지 문자와 끝의
점/공백을 거부한다. 이름을 암묵적으로 정규화해서 다른 파일을 선택하지 않는다. `.godj-staging` namespace는 예약되어 있다.

Save는 source를 한 번만 읽어 비공개 staging 파일을 완성하고 File.Sync/Close 후 hard link로 게시한다. 기존 destination은
대체하지 않는다. 충돌하면 compound suffix를 보존하고 `_`와 7개 영숫자를 붙이며 필요한 경우 basename을 문자 단위로 줄인다.
같은 source를 rewind하지 않으며 별도 인스턴스/프로세스도 파일을 덮어쓰지 않는다. 제한 초과, 입력 오류·취소·reader panic에서
완성되지 않은 staging 파일을 정리한다. Reader는 빌린 자원이므로 Save가 닫지 않는다. 임시로 만든 빈 디렉터리는 남을 수 있다.

게시 전 실패는 `Error.Outcome == NotPublished`, 성공한 게시 뒤 정리 실패는 `Published`로 구분한다. Link에서 오류가 나면
결과를 확정할 수 없는 filesystem도 고려해 `Uncertain`과 후보 Info를 반환한다. **error가 있어도 Info가 존재할 수 있다.**
이 경우 다시 저장하거나 파일을 지우지 말고 실제 파일과 DB 상태를 조정한다. 이 분류는 DB commit을 뜻하지 않으며
File.Sync만으로 directory entry의 정전 후 영속성까지 보장하지 않는다. 프로세스 강제 종료 후의 staging 회수도 자동 수행하지 않는다.

Open은 독립 cursor와 context를 가진 `io.ReadCloser`를 반환한다. Caller가 닫아야 하며 이미 반환한 reader는 backend Close 뒤에도
caller가 소유한다. 읽기 취소는 다음 Read에서 검사하고 외부 reader의 이미 막힌 Read를 강제로 끊지는 않는다. Filesystem Close는
진행 중인 연산이 끝날 때까지 기다리고 이후 새 연산을 거부한다. Delete는 없는 파일에 대해 성공하며 디렉터리와 마지막 symlink를
파일로 삭제하지 않는다. Open/Stat의 없는 파일은 `errors.Is(err, fs.ErrNotExist)`로 검사한다.

## HTTP 업로드 소비

```go
// backend는 application 시작 시 열고 종료 시 닫는다.
info, err := storage.SaveUpload(ctx, backend, "attachments/"+upload.Name(), upload,
    storage.SaveOptions{MaxLength: 100})
if err != nil {
    // storage.Error.Outcome과 info를 보존해 실패/불확실한 결과를 처리한다.
    return err
}
name := info.Name()
// 인가된 별도 DB 연산이 name을 기록한다. 그 결과를 확인하기 전에는
// 이전 파일을 삭제하거나 파일/DB가 함께 commit됐다고 판단하지 않는다.
```

`SaveUpload`는 업로드 capability를 요청이 살아 있을 때 열고 새 reader만 닫는다. 저장된 파일은 요청 임시 파일과 독립적이다.
반환 metadata의 구성/크기를 검사하지만 실제 저장의 증명이나 backend 인가를 대신하지 않는다. Form의 순수 validator와
템플릿은 저장 I/O를 실행하지 않는다. 현재 [HTTP 소비자 검사](upload_test.go)는 multipart/Form에서 받은 바이너리가 요청
종료와 backend 재개방 후에도 남는 것을 검증한다. 운영 endpoint의 인증 정책·모델 FileField·DB/파일 원자 commit의 증거는 아니다.

## 비교와 범위

고정 Django 6.1의 FileSystemStorage 기본 no-overwrite, 이름 충돌/compound suffix/Unicode 길이, 내용·빈 파일·없는 파일 삭제를
독립 실행과 대조한다. [reference observer](../conformance/runners/django/storage_reference.py)의 출처는 upstream BSD-3-Clause다.
GoDj는 portable 이름 제한, bounded 시도/입력, 게시 전 완성, 불확실한 결과를 명시한다. Django의 선택적 overwrite·절대 Path/URL
편의 API를 구현했다고 주장하지 않는다. root confinement을 보장하지 못하는 js/plan9는 명시적으로 거부한다.

모델 FileField의 IR/생성/ORM/Form 저장 연결, storage alias 등록, 다른 backend·URL/인증된 serving과 DB/파일 결과 조정은 후속
범위다. 파일 삭제와 DB 삭제가 자동으로 함께 수행되지 않는다. [ADR-0082](../docs/adr/0082-file-storage-publication-and-reference.md),
[실행 증거](../docs/status/TEST_EVIDENCE.md)를 따른다.
