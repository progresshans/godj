# 파일 입력과 요청 수명

`web.Request.Multipart(uploads.DefaultConfig())`는 multipart 본문을 한 번 읽고 문자열 값과 파일을 분리한다. 같은 요청에서
같은 설정으로 다시 호출하면 같은 결과를 반환한다. 파싱 실패도 기억하며 설정을 바꿔 본문을 다시 읽거나 한도를 높이지 않는다.
직접 사용하는 `uploads.Parse(ctx, reader, contentType, config)`의 결과는 caller가 `Close()`해야 한다. Request 경로는
정상 반환·handler 오류·panic 후 모두 업로드 자원을 정리한다.

```go
parsed, err := request.Multipart(uploads.DefaultConfig())
if err != nil {
    return web.Response{}, err // 실제 handler는 입력 오류와 실행 오류의 응답을 구분한다.
}
bound, err := spec.Bind(request.Context(), forms.NewDataWithFiles(parsed.Values(), parsed.Files()), initial)
if err != nil {
    return web.Response{}, err
}
if !bound.Valid() {
    return web.NewResponse(400, nil, []byte("invalid form"))
}
// 이 route의 인가와 CSRF를 확인한 scope에서 처리한다.
value, ok := bound.Cleaned().File("document")
if ok {
    if file, uploaded := value.Upload(); uploaded {
        reader, err := file.Open(request.Context())
        if err != nil {
            return web.Response{}, err
        }
        // processUpload는 application이 제공하는 context/error 기반 처리다.
        processErr := processUpload(request.Context(), reader)
        closeErr := reader.Close()
        if err := errors.Join(processErr, closeErr); err != nil {
            return web.Response{}, err
        }
    }
}
```

기본 한도는 wire body 32 MiB, 파일당 16 MiB, 메모리에 남기는 파일 payload 합계 2.5 MiB, 문자열 값당 64 KiB/합계 1 MiB,
전체 part 1,000개·파일 part 100개다. `MemoryBytes: 0`은 비어 있지 않은 파일을 바로 임시 파일에 둔다. 이 메모리 한도는
payload 합계이며 parser/metadata/일시 버퍼까지 포함한 전체 heap 한도가 아니다. 서버 설정으로 각 한도를 명시적으로 바꾼다.
파일이 메모리 예산 안에 있으면 디스크를 쓰지 않으며 초과하면 서버가 만든 private 디렉터리와 임의 파일명을 사용한다.
TempDir의 운영체제 접근권한은 서버 배포 설정이 소유한다. POSIX mode와 Windows ACL을 같은 권한 표식으로 해석하지 않는다.
클라이언트 filename은 경로가 아닌 표시용 basename이다. `ContentType()`도 클라이언트의 선언이며 내용 검증 결과가 아니다.

값/파일 container는 복사본이고 파일마다 독립 reader를 열 수 있다. Reader 값을 복사하면 같은 cursor/수명을 공유하며 lock을
복제하지 않는다. Form/Reader/Error의 pointer와 값 formatting 모두 payload를 숨긴다. 파일 metadata는 불변이며 `File.Valid()`는 생성 여부만
나타낸다. 실제 읽기는 context와 소유 수명을 검사한다. Form을 닫으면 이미 열린 reader도 닫히고 임시 파일이 제거되며 이후
`Open`/`Read`는 실패한다. Handler 밖으로 capability를 보관해 수명을 늘릴 수 없다. 일반 formatting은 이름·내용·임시 경로를
공개하지 않는다. 수신 파일을 영구히 사용할 때는 요청이 살아 있을 때 명시적인 저장을 완료해야 한다.

크기·개수 초과, malformed/incomplete body, 취소와 I/O 오류에서 부분 Form을 반환하지 않는다. MIME 종료 경계 뒤 epilogue도
wire body 예산에 포함한다. 누락된 part header의 EOF를 빈 성공으로 처리하지 않는다. 이름 없는 payload, 잘못된 part와
Content-Transfer-Encoding은 명시적으로 거부한다. 브라우저의 빈 파일 선택(`filename=""`, 본문 없음)은 파일로 만들지 않는다.
메타데이터를 파싱해도 실행 파일/이미지 안전성·파일 내용·인가·저장소 경계가 검증된 것은 아니다.

정리 실패는 `Form.Close()`에서 반환한다. Web runtime은 이미 결정된 handler 결과를 재시도 가능한 실패로 바꾸지 않고 별도
정리 오류를 기록한다. 저장된 DB/파일의 결과와 임시 자원 정리를 같은 성공 표식으로 합치지 않는다.

## Form과 Formset

`forms.FileField`는 기본적으로 required이며 `ClearableFileInput`을 사용한다. `WithRequired(false)`,
`WithAllowEmptyFile(true)`, filename의 Unicode 문자 수를 제한하는 `WithMaxLength`, 일반 `FileInput`을 선택할 수 있다.
`Spec.IsMultipart()`와 `SetSpec.IsMultipart()`로 HTML의 `enctype="multipart/form-data"` 필요 여부를 확인한다.
파일 input에는 value를 렌더링하지 않는다. 파일 재선택 없이 이전 업로드를 브라우저에 채워 넣지 않는다.

서버의 기존 저장 이름은 `forms.ExistingFile(name)`으로 initial에 넣는다. 새 파일이 없으면 그 참조를 보존하고 검증 callback을
재실행하지 않는다. optional clearable 필드의 `<name>-clear`는 삭제 의도이며 `FileValue.Clear()`로 구분한다. 업로드와 clear를
함께 제출하면 `contradiction`, 단일 file에 파일을 반복 제출하면 `multiple`이다. 문자열 POST는 파일 capability나 기존 참조를
만들지 못한다. 고정 Django가 마지막 파일을 선택하는 반복 입력과 임의 문자열 clear를 허용하는 동작은 GoDj가 강화한 차이다.
삭제 의도는 실제 저장 파일 삭제 권한이 아니며 파일 이름도 저장 경로나 URL로 직접 사용하지 않는다.

Formset은 각 행의 파일/clear prefix를 분리하고 파일을 받은 추가 행을 활성화한다. readonly initial은 위조된 업로드를 채택하지
않으며 삭제 행/전체 진단의 기존 의미를 유지한다. Pure field/cross validator는 metadata만 검사하며 `Open` 등의 I/O를 실행하지
않는다. Typed ModelForm의 ExtraFields로 파일 명령을 선언하면 준비 결과의 Input에 남고 stored scalar에는 들어가지 않는다.

## 이미지 내용 검증

`forms.ImageField`는 파일 입력의 유지·교체·clear 의미를 그대로 사용하고 새 업로드를 검증한다. `Spec.Bind(ctx, data, initial)`과
`SetSpec.Bind/BindWith(ctx, ...)`, 모델 폼의 Bind 계열은 명시적인 context를 받는다. 빈/기존 이름과 일반 FileField를 위해
파일을 열지 않는다. ImageField는 자체 독립 reader로 내용을 읽고 닫으며 원래 업로드 bytes·다른 reader의 cursor를 바꾸지 않는다.
Field/Cross/Model clean callback은 계속 pure이고 DB·storage 쓰기를 하지 않는다. nil/취소한 context와 실제 읽기 실패는 error다.

```go
photo, err := forms.ImageField("photo", forms.WithImageLimits(uploads.ImageLimits{
    MaxBytes: 8 << 20, MaxPixels: 4 << 20, MaxFrames: 32, MaxTotalPixels: 8 << 20,
}))
if err != nil { return err }
spec, err := forms.NewSpec([]forms.Field{photo})
if err != nil { return err }
bound, err := spec.Bind(ctx, submitted, initial)
if err != nil { return err }
if !bound.Valid() { return validation.Reject(bound.Errors(), nil) }
file, _ := bound.Cleaned().File("photo")
info, verified := file.Image()
if verified {
    // Width/Height는 EXIF 회전 전 실제 raster 크기다. ContentType은 검증 결과다.
    width, height, mediaType := info.Width(), info.Height(), info.ContentType()
    _ = width; _ = height; _ = mediaType
}
```

PNG/APNG·GIF·WebP의 모든 프레임, 정적 JPEG·Windows BMP/DIB와 classic TIFF/BigTIFF의 주 IFD 목록에 있는 모든 페이지를 디코딩한다.
PNG/WebP의 container와 애니메이션 표식을 검사하며 TIFF SubIFD와 지원하지 않는 codec
특성은 명시적으로 거부한다. 표준 decoder의 전역 등록 목록에 따라 허용 형식이 바뀌지 않는다. 잘린 내용·손상은 `invalid_image`,
파일/픽셀/프레임 한도는 `image_bytes`/`image_pixels`/`image_frames` 진단이다. 내용 검증 뒤 확장자를 검사하며 허용 목록은
`.png`, `.apng`, `.jpg`, `.jpeg`, `.jpe`, `.gif`, `.webp`, `.bmp`, `.dib`, `.tif`, `.tiff`(대소문자 무관)다. 검증된 PNG의
이름이 `.jpg`인 경우처럼 허용 확장자와 실제 format이 달라도 허용하지만 `ImageInfo.ContentType()`은 실제 내용에서 얻는다.
클라이언트가 보낸 `uploads.File.ContentType()`은 계속 신뢰하지 않은 원래 선언이다.

APNG는 IEND까지의 chunk CRC·공유 sequence·선언/실제 frame 수·canvas 안의 frame 영역과 disposal/blend 값을 먼저 검사한다.
별도 기본 이미지와 모든 animation frame의 pixels를 디코딩한다. `Width()`/`Height()`는 canvas 치수이며 `Frames()`는
기본 이미지가 애니메이션에 포함되지 않아도 그 이미지 한 개를 합산한다. 단일 프레임 APNG도 허용한다. 고정 Django ImageField와
같이 `FormatName()`은 `png`, `ContentType()`은 `image/png`다. `.apng` 확장자가 별도 MIME이나 추가 쓰기 권한을 만들지 않는다.

각 frame은 PNG의 bit depth·color type·Adam7·palette/transparency를 상속한다. 고정 header와 데이터 chunk view만 구성하고
원문이나 전체 canvas를 매번 복사하지 않는다. 입력에 있는 빈/분할 data chunk와 frame data 사이의 ancillary chunk도 처리한다.
알 수 없는 critical chunk는 `unsupported_image`이며 손상된 후속 frame을 기본 이미지의 성공으로 대체하지 않는다. 검사는 표시
시간을 기다리거나 반복 재생·화면용 합성·재인코딩을 하지 않고, 반환값에 pixels를 보관하지 않는다.

WebP는 RIFF의 전체 길이·chunk 범위/zero padding, VP8X canvas·ANIM·모든 ANMF 영역과 프레임별 ALPH/VP8/VP8L을
검사한다. 각 프레임의 실제 bitstream header를 별도로 읽어 선언된 크기와 대조하고 모든 크기/합산 예산을 pixel 할당 전에
확인한다. 작은 ANMF가 큰 pixel header를 숨길 수 없다. Lossy/lossless·raw/compressed alpha·프레임별 혼합을 처리하며,
30-byte header와 원본 chunk view를 사용한다. 프레임마다 파일이나 canvas를 복사하거나 표시용 합성을 하지 않는다.
`Width()`/`Height()`는 canvas, `Frames()`는 ANMF 수(정적 파일은 1), format/MIME은 `webp`/`image/webp`다.

BMP/DIB는 40·108·124-byte Windows header와 고정 Go decoder가 지원하는 palette/RGB/alpha 표현을 사용한다. BMP 16-bit,
RLE·다른 header/mask 등 지원하지 않는 특성은 `unsupported_image`다. DIB는 검사 중에만 14-byte BMP header를 앞에 붙여
읽고 원본을 바꾸지 않는다. `FormatName()`은 `dib`, `ContentType()`은 `image/bmp`다.

TIFF는 little/big endian의 주 페이지 목록을 먼저 읽어 IFD 순환·겹침·범위 밖 offset/값, 모든 페이지 크기와 합산 예산을 검사한다.
Strip/tile 개수와 각 데이터 범위를 확인하고 파일 header(classic 8-byte, BigTIFF 16-byte)를 pixel 데이터로 참조하는 입력은 거부한다. 모든 페이지가 참조하는
압축/비압축 블록의 byte 합에도 `MaxBytes`를 적용한다. 같은 블록을 여러 번 사용하면 매번 합산하므로 작은 파일의 반복 참조로
디코딩 작업을 증폭할 수 없다. 공유 블록 자체는 이 예산 안에서 허용한다.
그 뒤 같은 immutable bytes 위에 페이지별 header view를 만들고 전체 pixel을 디코딩한다. 페이지마다 파일 전체를 복사하지 않는다.
표시 크기는 첫 페이지이며 `Frames()`는 페이지 수다. Uncompressed·LZW·Deflate·PackBits·Group 3/4와 gray/palette/RGB/alpha의
지원 범위는 고정 decoder를 따른다. TIFF JPEG/CMYK·분리 plane·volume·SubIFD는 현재 명시적 미지원이다. 원본 방향의 치수를
사용하며 EXIF 회전이나 재인코딩은 하지 않는다.

BigTIFF의 64-bit IFD·값 개수·offset과 LONG8 strip/tile 배열은 곱셈이나 정수 축소 전에 범위를 검사한다. 검사할 때만 작은
classic IFD와 header를 제공하며 LONG8 배열은 읽는 부분만 변환한다. 픽셀 bytes와 저장 파일은 원문 그대로다. `FormatName()`은
`tiff`, MIME은 `image/tiff`, 확장자는 `.tif`/`.tiff`다. 64-bit 컨테이너도 아래 파일/픽셀 한도를 따른다.
[고정 LibTIFF 관찰](testdata/bigtiff-libtiff471.json)은 모든 페이지의 디코딩된 픽셀과 Django/Pillow의 폼 승인을 구분한다.
LibTIFF가 읽는 big-endian BigTIFF를 Pillow 12.3.0 폼이 거부하는 차이를 그대로 재현하지 않는다.

| 한도 | 기본값 | 설정 상한 |
| --- | --- | --- |
| 입력 bytes와 TIFF 블록 참조 bytes 각각 | 16 MiB | 64 MiB |
| 폭·높이 각각 | 16,384 | 65,535 |
| 단일 raster/frame/canvas 픽셀 | 8,388,608 | 33,554,432 |
| GIF/APNG/WebP frame / TIFF page 수 | 128 | 1,024 |
| 전체 frame/page 픽셀 합 | 33,554,432 | 67,108,864 |

0인 항목은 기본값을 적용하고 음수/설정 상한 초과는 시작 시 거부한다. Header 크기와 GIF/APNG/WebP의 모든 frame descriptor·TIFF의
모든 페이지를 실제 pixel 할당 전에 제한한다. APNG 합계는 기본 이미지와 각 후속 frame 영역의 pixel 수다. 예를 들어
3×2 기본 이미지와 2×1 후속 frame은 합계 8픽셀이다. WebP는 모든 ANMF 영역을 합산하며 canvas도 개별/합산 한도 안에 있어야 한다.
TIFF의 개별/합산 픽셀 예산에는 tile padding도 포함한다. 표시 영역이 3×2여도
16×16 tile이면 256픽셀로 센다. 한도는 검사 하나의 입력·raster 예산이며 전체 heap의 정확한 byte 한도가 아니다. 동시 요청 수와
Formset의 행 수·HTTP 업로드 한도는 application의 admission이 함께 제한한다. 취소는 읽기/단계 경계에서 전달하며 decoder의
한 pixel 계산 도중 강제 중단하는 goroutine을 만들지 않는다. 검사 결과에는 pixels나 복사한 원문 buffer를 보관하지 않는다.

직접 업로드를 검사할 때는 `uploads.InspectImage(ctx, file, limits)`가 독립 reader를 열고 닫는다.
`InspectImageReader(ctx, reader, limits)`는 caller가 소유한 reader의 **현재 cursor부터 EOF까지** 읽으며 seek/Close/보관을
하지 않는다. 읽기는 최대 32 KiB씩, 전체는 byte 한도와 한 개의 초과 확인 byte로 제한한다. Reader는 막힌 Read의 취소에
협조해야 한다. 잘못된 읽기 길이·반복되는 무진행·원래 I/O 오류를 성공으로 바꾸지 않는다. 저장된 파일의 독립 handle·길이
검사·정리는 [storage.InspectImage](../storage/README.md#저장된-이미지-검사)가 소유한다.

`FileValue.Image()`는 **이번 바인딩에서 검증한 새 업로드 또는 명시적으로 검사한 이미지 선택**의 metadata를 반환한다. 단순히 유지한 저장 이름·clear·일반 FileField는
검증 결과를 만들지 않는다. 검증은 재인코딩·metadata 제거·저장·접근 인가를 수행하지 않으며 원본 bytes를 그대로 보존한다.
요청 수명이 끝나기 전에 `storage.SaveUpload` 등 명시적인 저장을 완료해야 한다. 고정 Django/Pillow가 받아들이는 손상된
GIF 후속 frame은 GoDj가 전체 frame을 디코딩해 거부한다. [관찰](../forms/testdata/image-django61.json)의 차이를 별도로 검증한다.
잘린 BMP/DIB·TIFF와 손상된 후속 TIFF 페이지도 내용 오류다. Django 폼의 첫 페이지 검증 성공과 이후 페이지 열거 실패를
[추가 codec 관찰](testdata/image-codecs-django61.json)에 구분한다.
[APNG 관찰](testdata/apng-django61.json)은 폼 검증과 별도의 전체 frame 열기를 구분한다. 폼이 허용하는 순서/개수/CRC와
pixel 손상도 GoDj는 거부한다. 고정 Pillow의 frame player가 일부 Adam7/ancillary 배치에서 실패하는 결과도 별도로 보존한다.
[WebP 관찰](testdata/webp-animation-django61.json)은 libwebp 버전과 폼/전체 frame 결과를 나눈다. GoDj는 고정 폼이 허용하는
누락/중복 control·실제 크기 불일치·후속 pixel/alpha 손상과 잘못된 padding 등을 거부한다. 반대로 공개 형식이 무시하도록
정한 VP8X의 reserved/future field는 허용하며 native의 header 거부와 구분한다. ALPH reserved bits에서 native player만
실패하는 경우도 기록한다. 애니메이션의 alpha는 각 frame의 실제 ALPH로 판단한다. 정적 lossy WebP의 alpha flag와 ALPH가
모순되는 두 native 허용 입력은 고정 Go decoder가 거부한다. 전체 Pillow 재생 동등성이나 원문 정화를 뜻하지 않는다.

모델 폼의 ExtraFields/파일 명령과 `schema.ImageField`는 같은 검사기를 사용한다. 모델 이미지의 IR·크기 참조·생성 descriptor·
migration·typed 저장은 [모델 이미지](../forms/model/README.md#모델-이미지와-크기-필드)를 따른다. 일반 FileField의 입력 종류만
바꿔 이 연결을 우회할 수 없다. 기존 저장 이름의 내용과 크기를 자동으로 다시 검사하지 않는다.
`WithChoices`를 지정한 File/ImageField는 저장 이름을 선택하는 Select다. 이미지 선택은
`WithImageChoiceInspector`로 제공한 읽기 capability와 같은 내용 검사/한도를 사용한다.
모델의 빈 값·default·크기 준비는 [저장 이름 선택](../forms/model/README.md#저장-이름-선택)을 따른다.

## Admin

Admin의 생성·수정·명령 폼은 choices가 없는 FileField/ImageField를 `type="file"`로 표시하고 편집 가능한 inline의 빈 prototype까지 포함해
multipart 전송 여부를 결정한다. `CreateForm.Definition.ExtraFields`, inline의 `Form.Definition.ExtraFields`와
`CommandConfig.Form`으로 비저장 파일 명령을 선언할 수 있다. 기존 저장 이름은 표시 텍스트로만 다루며 URL을 만들어 링크하지
않는다. 기존 값이 있으면 파일의 HTML required를 생략하고 optional clear 선택과 오류를 재표시한다. 업로드는 오류 화면에
보존되지 않으므로 재선택 안내를 표시한다. 일반 FileInput에는 clear 제어를 만들지 않는다.

`SiteConfig.Uploads`는 `uploads.Config`의 snapshot을 받으며 nil이면 위 기본값을 사용한다. Admin은 문자열 값당 4 KiB·합계
64 KiB와 전체 입력 1,024개 상한을 추가로 적용하고 더 작은 설정을 유지한다. URL-encoded 본문은 기존 64 KiB 한도를 따른다.
파일은 `AcceptsUpload()`인 FileField/ImageField에만 전달하며 choices·scalar·PK·management·CSRF 이름의 파일 part는 거부한다. 중복 파일은 Form의 `multiple`
진단으로 전달한다. 생략한 파일과 clear 제어는 원래 제출에서도 생략된 상태를 유지한다.

Multipart 본문을 읽기 전에 현재 principal을 조회해 staff/site/route 권한을 검사한다. 이때 session의 idle expiry를 갱신하거나
stale session을 정리하지 않는다. 본문을 파싱한 뒤에도 CSRF와 현재 권한·관계/inline 범위·revision을 검사하며, callback은
최종 쓰기 권한을 확인한다. Hidden inline의 파일-only 위조도 거부하고 readonly 기존 행은 업로드를 채택하지 않는다.
파일은 동기 callback 안에서만 읽을 수 있다. 입력 한도/형식 오류와 취소·읽기/임시 저장/정리 오류를 구분한다.

Admin ImageField는 `accept="image/*"`와 검증 오류·재선택 안내를 표시한다. 실제 허용 여부는 서버 내용 검증이 판단한다.

현재 구현은 파일/이미지 입력·context 기반 바인딩·수명과 Admin 전송이다. [storage.SaveUpload](../storage/README.md)는 명시적으로 선택한 로컬
저장소로 내용을 옮기며 요청 종료 뒤에도 유지한다. [모델 FileField](../forms/model/README.md#모델-파일의-준비와-저장)는
Schema IR·생성 모델·ORM/Form의 저장 이름과 명시적인 SaveFiles를 연결하며 파일 게시와 DB commit 결과를 구분한다. 모델 ImageField는 검사한 업로드의 크기를 반영하며 임의 auto-save는 제공하지 않는다. 인가된 다운로드는 [파일 응답](../web/streaming.md)을 따른다.
실행 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md), 실제 HTTP 소비자는 [Web 검사](../web/multipart_test.go)와 [Admin 검사](../admin/site_uploads_test.go)에 있다.
