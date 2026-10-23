# 저장된 이미지의 크기 갱신

`storagemodel.RefreshImageDimensions(ctx, manager, current, field, backend, limits)`는 선택한 모델 ImageField의 현재 저장
이름을 읽고, IR이 선언한 폭/높이 필드만 바꾼 분리된 모델과 검사 결과를 반환한다. `storage/model`은 ORM metadata와
[storage 검사](../README.md#저장된-이미지-검사)를 연결하며 DB를 읽거나 쓰지 않는다.

```go
import (
    "github.com/progresshans/godj/orm"
    storagemodel "github.com/progresshans/godj/storage/model"
    "github.com/progresshans/godj/uploads"
)

// current와 files는 application이 현재 객체/저장소 권한을 확인해 선택한다.
refreshed, inspection, err := storagemodel.RefreshImageDimensions(
    ctx, models.PhotographObjects, current, "photo", files, uploads.ImageLimits{},
)
if err != nil { return err }
// current와 DB는 아직 바뀌지 않았다. Application은 모델 검증과 최신 revision,
// 파일 참조가 여전히 같은지 확인하고 자신의 transaction 안에서 저장한다.
err = models.PhotographObjects.Save(ctx, tx, &refreshed,
    orm.UpdateFields[models.Photograph](models.PhotographFields.Width, models.PhotographFields.Height),
)
if err != nil { return err }
_ = inspection
```

`manager`의 canonical snapshot에서 ImageField와 크기 참조를 검사한다. 알려지지 않은 필드, 다른 kind, 비정수/PK/공유
크기 참조와 잘못된 모델 표현은 파일을 열기 전에 거부한다. PK의 존재 상태와 다른 scalar 값은 보존하고, 새 모델의 nullable
pointer도 원본과 공유하지 않는다. 이 연산은 Form clean이나 ORM의 자동 저장 hook을 실행하지 않는다.

빈 문자열/NULL 이미지 참조는 파일을 열지 않고 소유한 크기를 NULL로 바꾼다. 크기 필드가 NULL을 허용하지 않으면
`query.Error`의 `invalid_value`로 실패하며 0으로 대체하지 않는다. 가로 또는 세로 하나만 선언할 수 있다. 둘 다 없으면
복사한 모델을 그대로 반환하고 파일을 열지 않는다. 내용 metadata만 필요하면 `storage.InspectImage`를 직접 사용한다.
파일을 읽지 않은 성공은 zero `ImageInspection`을 반환한다.

정상 이미지도 읽기/Close/취소가 실패하면 zero 모델과 zero 검사 결과를 반환한다. 실패 중 원본 모델과 파일을 변경하지 않으며
DB 저장/rollback·파일 삭제는 caller가 각각 소유한다. 검사는 열린 reader의 내용만 확인한다. 이후 파일 이름 재사용이나 외부
writer의 변경을 막거나 DB와 파일의 원자 commit을 보장하지 않는다. 장기간 보관한 결과로 현재 인가·revision을 대신하지 않는다.

고정 Django 6.1은 크기 cache를 재사용하고 손상된 이미지의 크기를 NULL로 만들거나 header만으로 치수를 얻을 수 있다.
GoDj의 명시적 갱신은 매번 독립 reader를 열고 공통 decoder로 전체 내용을 검증한다. 손상/미지원 내용은 오류이며 기존 모델을
보존한다. [독립 관찰](../../conformance/runners/django/stored_image_reference.py),
[파일 결정](../../docs/adr/0082-file-storage-publication-and-reference.md), [실행 증거](../../docs/status/TEST_EVIDENCE.md)를 따른다.
