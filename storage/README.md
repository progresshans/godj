# 이름이 있는 파일의 저장

저장 이미지 선택 폼에는 `NewImageInspector(opener).Inspect`를 `forms.WithImageChoiceInspector` 또는
`formmodel.WithImageChoiceInspector`로 전달할 수 있다. `Opener`는 Open만 제공하는 빌린 capability이며 Save/Delete나
backend 종료 권한을 요구하지 않는다. 생성 시 I/O를 하지 않고 매 검사마다 `InspectImage`의 독립 reader·전체 내용 검증·Close를
재사용한다. 선택 목록과 실제 파일 접근 권한은 application이 제공한다. [저장 이름 선택](../forms/model/README.md#저장-이름-선택)을 따른다.

`storage.Backend`는 `Save`, `Open`, `Stat`, `Delete`를 context/error와 연결한다. `Save`가 돌려주는 `Info.Name()`은 실제
저장된 상대 이름이다. 이름 충돌과 길이 제한으로 제안한 이름과 달라질 수 있다. 이름이나 `Info.Valid()`는 읽기/삭제/서빙
권한이 아니다. DB에는 모델 FileField가 이 상대 이름을 기록하며 실제 I/O는 명시적인 backend가 소유한다.

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
사용자 제공 Random의 panic도 전파하지만 staging/충돌 이름을 위한 직렬화 잠금은 반드시 해제한다. 상위 요청 경계가 panic을
복구한 뒤 같은 backend의 후속 저장이 멈추지 않으며, 실패한 게시의 staging과 기존 파일 보존 의미는 같다.

게시 전 실패는 `Error.Outcome == NotPublished`, 성공한 게시 뒤 정리 실패는 `Published`로 구분한다. Link에서 오류가 나면
결과를 확정할 수 없는 filesystem도 고려해 `Uncertain`과 후보 Info를 반환한다. **error가 있어도 Info가 존재할 수 있다.**
이 경우 다시 저장하거나 파일을 지우지 말고 실제 파일과 DB 상태를 조정한다. 이 분류는 DB commit을 뜻하지 않으며
File.Sync만으로 directory entry의 정전 후 영속성까지 보장하지 않는다. 프로세스 강제 종료 후의 staging 회수도 자동 수행하지 않는다.

Open은 독립 cursor와 context를 가진 `io.ReadCloser`를 반환한다. Caller가 닫아야 하며 이미 반환한 reader는 backend Close 뒤에도
caller가 소유한다. 읽기 취소는 다음 Read에서 검사하고 외부 reader의 이미 막힌 Read를 강제로 끊지는 않는다. Filesystem Close는
진행 중인 연산이 끝날 때까지 기다리고 이후 새 연산을 거부한다. Delete는 없는 파일에 대해 성공하며 디렉터리와 마지막 symlink를
파일로 삭제하지 않는다. Open/Stat의 없는 파일은 `errors.Is(err, fs.ErrNotExist)`로 검사한다.

로컬 reader는 선택적 `storage.Reader`도 구현한다. `Info()`는 동일하게 열린 handle에서 확인한 이름/크기의 불변 snapshot이며
뒤에 같은 이름의 파일이 교체되거나 backend가 닫혀도 바뀌지 않는다. 임의 backend가 이 인터페이스를 구현하지 않으면 HTTP는
길이 미상으로 전송한다. 별도 Stat 결과를 다음 Open의 metadata로 취급하지 않는다.

기본 filesystem/memory reader는 `SeekableReader`도 구현한다. Read/Seek/Close는 같은 cursor와 잠금을 공유하며 Open의 context를
검사한다. 독립 Open끼리는 cursor를 공유하지 않는다. 여러 goroutine의 개별 연산은 직렬화되지만 Seek 다음 Read의 연속 소유권은
caller가 관리한다. Backend Close·이름 삭제/재사용 뒤에도 이미 연 handle을 seek할 수 있고 reader 자체를 닫으면 거부한다.

`Info.ContentMetadata()`는 선택적인 수정 시각과 공개 content version을 반환한다. 외부 backend는 `WithContentMetadata`로
같은 열린 내용의 metadata를 붙일 수 있다. Version은 bytes가 달라지면 반드시 달라지는 안정적인 token이며 이름/크기/mtime에서
추측하지 않는다. 빈 Version과 zero Modified는 미상이다. Filesystem Open/Stat는 handle/파일에서 얻은 수정 시각만 제공하고
strong version을 발명하지 않는다. Save receipt에 metadata가 없을 수 있으며 HTTP의 기준은 실제 Open의 Info다.
`ModifiedStrong`은 서로 다른 내용이 같은 초 단위 timestamp를 공유하지 않는다는 backend의 추가 보장이다. 기본 backend는
이 보장을 하지 않는다. HTTP는 이 구분을 [conditional/Range](../web/streaming.md#조건부-조회와-부분-다운로드)에 사용한다.

## 메모리 저장소

`NewMemory(MemoryConfig{})`는 프로세스 안에서만 유지되는 독립 저장소를 만든다. 별도 filesystem이나 전역 map을 사용하지 않는다.
같은 facade의 값 복사는 수명과 내용을 공유하고 새 생성자는 빈 별도 저장소다. Backend 인터페이스·portable 이름·collision
rename·SaveOptions와 URL/alias 등록은 로컬 저장소와 같다. Open은 독립 cursor와 같은 열린 내용의 불변 Info를 반환한다.
이 backend를 영구 파일의 내구성 대체물로 취급하지 않는다. 프로세스 종료나 새 인스턴스 생성으로 이전 내용이 사라진다.
Save가 content SHA256을 함께 계산하고 게시 시각을 기록한다. 같은 bytes는 같은 Version이며 같은 이름을 다른 bytes로 재사용하면
Version이 바뀐다. 이미 열린 reader의 Info는 원래 내용의 Version/시각을 계속 보존한다.

기본 `Limits` 외에 `MaxBytes` 64 MiB, `MaxFiles` 4,096개, `MaxConcurrentSaves` 최대 32개의 한도를 적용한다. 0 값은 기본값이며
작은 MaxFiles를 지정하면 기본 동시 저장 수도 그 이하가 된다. MaxBytes는 저장 중 예약한 공간·게시된 내용·삭제됐지만 열린
reader가 보유한 내용을 포함한다. MaxFiles도 저장 중/게시됨/reader가 남은 객체를 모두 센다. 마지막 reader가 닫혀야 삭제된
내용의 quota를 회수한다. 빈 파일도 파일 개수 한도를 소비한다. quota 초과는 `capacity_exceeded`/`NotPublished`다.
동시 저장 한도는 새 source를 읽기 전에 검사한다. 다른 저장의 예약 때문에 순간적으로 여유 공간이 없을 수도 있다.

이 한도는 프로세스 RSS 한도가 아니다. 각 active Save의 32 KiB 전송 버퍼, slice의 여유 capacity·일시적 할당, 이름·map과 caller가
보유하는 reader handle에도 메모리가 필요하다. Source가 준 bytes를 private content로 복사하고 EOF까지 확인한 뒤 한 번의
임계 구역에서 게시한다. Source 오류·길이/용량 초과·취소·panic은 부분 파일을 게시하지 않으며 모든 예약을 반환한다.
0 bytes/nil을 반복하는 reader는 유한하게 거부한다. Random callback도 panic에서 직렬화 잠금을 해제한다.

이름의 디렉터리는 현재 파일들의 가상 prefix다. 파일을 부모 경로로 사용하지 않고, live prefix와 같은 파일 이름은 collision
rename하며 directory prefix의 Open/Stat/Delete는 `not_regular`다. 재귀 삭제를 하지 않고 빈 디렉터리 metadata도 보관하지 않는다.
Close는 진행 중인 연산이 끝날 때까지 기다린 뒤 이름 공간을 지우고 새 연산을 거부한다. 이미 연 reader는 이전 bytes를 계속
소유하며 Delete/동일 이름 재사용/Close로 내용이나 cursor가 바뀌지 않는다. 임의 source의 막힌 Read는 context에 협조해야 한다.

고정 Django의 InMemoryStorage와 정상 bytes/이름/크기·인스턴스 분리를 비교한다. Native의 동일 reader 객체/cursor 공유,
읽기 실패 뒤 부분 내용 게시, 디렉터리 재귀 삭제는 GoDj의 독립 reader·완성 후 게시·파일 단위 삭제 계약과 다르다.
[독립 관찰](../conformance/runners/django/memory_storage_reference.py)과 [실행 증거](../docs/status/TEST_EVIDENCE.md)를 따른다.

## S3 객체 저장소

`NewS3(S3Config{Bucket: bucket, Region: region, Credentials: provider})`는 이미 존재하는 일반 S3 bucket을 선택한다.
`provider`는 application이 명시적으로 전달한 AWS SDK v2 `aws.CredentialsProvider`다. 생성자는 네트워크·credential 조회·
bucket 생성/정책 변경을 하지 않으며 anonymous/빈 credential로 요청하지 않는다. Provider와 선택적 `Transport`는 빌린
동시 사용 capability다. Application이 credential 갱신/cache와 권한을 소유한다.

`Endpoint`가 비어 있으면 고정 SDK의 AWS regional endpoint를 사용한다. 명시한 endpoint는 HTTPS이며 userinfo·경로 prefix·
query·fragment를 받지 않는다. 로컬 서비스는 `AllowLoopbackHTTP`와 literal loopback IP를 함께 지정해야 HTTP를 허용한다.
`UsePathStyle`은 서비스가 요구할 때 선택한다. `Prefix`는 slash로 끝나지 않는 portable 상대 directory 이름이고 모든 key 앞에
고정된다. `ExpectedBucketOwner`는 선택적 12자리 account ID로 모든 저장/읽기/삭제/서명에 결합한다. Bucket ARN/access point·
directory bucket을 일반 bucket으로 해석하지 않는다. 이름의 공통 문자/성분 한도에 더해 prefix를 포함한 S3 key는 1,024 bytes다.
충돌 suffix를 붙일 때 두 한도를 모두 적용하고 UTF-8 문자를 중간에서 자르지 않는다.

Save는 빌린 입력을 EOF까지 한 번 읽어 bounded private buffer를 완성하고 SHA256을 계산한다. 기본 파일 한도는 32 MiB,
전체 진행 중인 content/reservation 한도 `MaxBufferedBytes`는 64 MiB, `MaxConcurrentSaves`는 32다. 이 값은 content 예산이며
Go allocator/SDK overhead를 포함한 RSS 상한이 아니다. 진행 중인 예약은 취소·입력/entropy 실패·panic에서도 반환한다.
동시 Save 슬롯이 가득 차면 새 source를 읽기 전에 거부한다. 현재 단일 PUT profile의 파일 상한은 5 GiB이며 multipart 업로드는
지원하지 않는다. `MaxBufferedBytes`도 실제 파일 크기 이상이어야 한다.

게시 요청은 SigV4·`If-None-Match: *`·명시적 길이·SHA256으로 한 번 전송한다. Endpoint는 conditional PUT과 전체 객체 SHA256을
지원해야 한다. 확인한 `412 PreconditionFailed`만 새 후보 이름으로 이어가며, 같은 source를 다시 읽지 않는다. 명시적인 인증/입력
거부와 `409 ConditionalRequestConflict`는 `NotPublished`다. 전송 이후 응답 유실·서버 오류 등은 후보 이름/크기와 `Uncertain`을
보존한다. SDK retry·redirect·HEAD 조정·자동 보상 삭제는 실행하지 않는다. 2xx여도 전체 객체 checksum이 없거나 다르면
`publication_integrity_failed`와 `Published`를 반환하고 strong metadata를 만들지 않는다. DB 저장 결과는 별도로 처리한다.

Open은 한 GET의 길이·수정 시각·checksum/version과 그 body를 함께 소유한다. 별도 HEAD로 metadata를 조합하지 않는다.
전체 읽기는 정확한 길이·EOF와 제공된 full SHA256을 확인한다. Checksum이 있으면 content hash, 없지만 immutable version ID가
있으면 service/bucket/key/version에 결합한 identity를 strong validator로 사용한다. ETag·크기·수정 시각을 content hash로
간주하지 않는다. `Stat`은 독립 HEAD이므로 다음 Open의 snapshot을 보증하지 않는다.

Immutable version ID가 있는 reader만 `SeekableReader`를 제공한다. 이후 Range GET은 원래 version과 정확한 범위/길이를
확인하므로 같은 이름의 교체·삭제 뒤에도 그 버전을 읽는다. Version이 없거나 `null`이면 독립 GET body만 제공하고 HTTP Range는
기존 전체 전송 규칙을 따른다. Reader는 backend Close 뒤에도 caller 소유이며, reader Close는 진행 중인 HTTP read를 먼저
취소한 뒤 response body를 한 번 닫고 backend Close 이후 다시 생긴 owned idle connection도 회수한다.
기본 `RequestTimeout`은 각 HTTP 요청과 그 body 읽기를 포함한 30초다.
Delete는 현재 이름만 삭제한다. 버전 bucket에서는 delete marker를 만들며 과거 version·prefix를 열거하거나 지우지 않는다.

`resolver, err := files.SignedURL(time.Minute)`를 선택적 `Registration.URL`에 명시적으로 등록할 수 있다. URL 생성 전에 현재
principal과 모델 소유권을 확인해야 한다. SigV4 GET은 정확한 bucket/prefix/key, 1초..7일의 정수 초 만료, attachment·
octet-stream·private/no-store 응답에 결합한다. URL 발급은 존재 확인이나 파일 snapshot이 아니다. 이미 발급한 bearer URL은
signature/credential 만료까지 유효할 수 있고 그 사이 같은 이름에 다시 게시한 내용도 읽을 수 있다. 소유권 변경은 이후 URL
발급의 인가에 반영하며 이미 발급한 URL의 취소와 혼동하지 않는다. URL을 로그/DB의 영구 파일 이름으로 저장하지 않는다.

실제 서비스 검증은 고정 MinIO 프로세스의 새 bucket에서 수행한다. AWS 운영 account나 모든 S3 호환 provider를 실제 실행한
것으로 간주하지 않는다. [검증 profile](../docs/TESTING.md#s3-실제-서비스-profile), [출처](../docs/SOURCES.md),
[실행 증거](../docs/status/TEST_EVIDENCE.md)를 따른다.

## 별칭과 URL

`NewRegistry(Registration{Alias: "documents", Backend: files})`는 이미 연 backend를 빌리는 application별 불변 등록이다.
`Settings.Definition.Storages`에 전달하고 `request.Settings().Storages().Lookup("documents")`로 조회한다. `Default()`는
명시적으로 등록한 `"default"`만 찾으며 전역 fallback·lazy 생성·암묵적인 파일 open은 없다. Registry가 backend를 닫지 않는다.
같은 backend를 여러 alias에 등록할 수 있고 각 alias의 URL capability는 독립이다. Alias는 대소문자를 구분하는 1..64 ASCII
문자(영숫자·`_`·`.`·`-`, 단독 `.`/`..` 제외)이며 입력 등록과 Aliases getter의 slice를 공유하지 않는다.

URL은 선택적 `Registration.URL`로만 제공한다. `NewURLPrefix("https://cdn.example.test/media/")` 또는 root-relative prefix는
portable 저장 이름을 한 번 escape해 붙이며 기존 파일 조회/읽기·route 등록을 하지 않는다. 설정 없는 alias의 `URL`은
`url_unavailable`이다. Scheme-relative·userinfo·fragment·control·backslash와 잘못된 URL은 거부하며 prefix의 query는 허용하지
않는다. 서명 URL 등의 별도 resolver는 유효한 query를 포함할 수 있지만 생성 전 인가와 유효기간·provider 동작은 caller가 소유한다.
URL 자체가 credential일 수 있으므로 사용자에게 주기 전에 admission을 확인하고 로그에 남기지 않는다.

고정 Django StorageHandler의 같은 alias 재사용·없는 alias 오류와 URL의 경로 escaping을 대조한다. GoDj는 이미 초기화한
capability를 등록하므로 Django의 lazy factory/cache를 복제하지 않는다. Django가 받아들이는 빈 이름·상위/절대/역슬래시 이름은
GoDj의 portable 이름 계약에서 거부한다. [독립 관찰](../conformance/runners/django/storage_serving_reference.py)에 이 차이를 남긴다.

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

## 저장된 이미지 검사

`InspectImage(ctx, backend, name, uploads.ImageLimits{})`는 명시적으로 인가한 저장 이름을 한 번 열고 전체 내용을 검사한 뒤
그 reader를 닫는다. 다른 reader의 cursor와 원본 파일은 바꾸지 않는다. `ImageInspection.File()`은 같은 handle의 이름/크기와
선택적 content metadata, `Image()`는 검증한 format·가로·세로·frame 수다. 형식과 자원 한도는 [이미지 입력](../uploads/README.md#이미지-내용-검증)과 같다.

`storage.Reader`의 metadata가 있으면 요청한 이름과 일치하고 실제 읽은 길이가 정확해야 한다. 별도 `Stat`을 호출하지 않는다.
Metadata가 없는 reader는 EOF까지 측정한 크기를 반환하며 content version/수정 시각을 발명하지 않는다. 크기를 먼저 알 수
없어도 인코딩 입력은 `MaxBytes`와 한 개의 초과 확인 byte로 제한한다. 매 호출 독립 reader를 열고 이름별 크기를 cache하지 않는다.

읽기·Close·context 취소 중 하나라도 실패하면 zero 결과를 반환하며 `errors.Is/As`로 원래 원인을 확인할 수 있다. Backend의
callback panic은 전파하지만 이미 얻은 reader는 정리한다. 검사 결과는 권한·파일 게시·DB commit의 증명이나 이후 같은 이름의
내용 보장이 아니다. 모델 크기 갱신과 명시적 저장은 [storage/model](model/README.md)을 사용한다.

## 비교와 범위

고정 Django 6.1의 FileSystemStorage 기본 no-overwrite, 이름 충돌/compound suffix/Unicode 길이, 내용·빈 파일·없는 파일 삭제를
독립 실행과 대조한다. [reference observer](../conformance/runners/django/storage_reference.py)의 출처는 upstream BSD-3-Clause다.
GoDj는 portable 이름 제한, bounded 시도/입력, 게시 전 완성, 불확실한 결과를 명시한다. Django의 선택적 overwrite·절대 Path
편의 API를 구현했다고 주장하지 않는다. root confinement을 보장하지 못하는 js/plan9는 명시적으로 거부한다.

[모델 FileField](../forms/model/README.md#모델-파일의-준비와-저장)는 IR/생성/ORM/Form의 참조와 명시적 `SaveFiles`를 연결한다.
별칭과 URL, [인가된 파일 응답](../web/streaming.md)과 S3 backend/서명 다운로드를 연결했다. 추가 provider·multipart 저장·자동 파일 회수는 후속 범위다.
파일 삭제와 DB 삭제가 자동으로 함께 수행되지 않는다. [ADR-0082](../docs/adr/0082-file-storage-publication-and-reference.md),
[실행 증거](../docs/status/TEST_EVIDENCE.md)를 따른다.
