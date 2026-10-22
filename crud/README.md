# 모델 CRUD 조립

`crud.Resource[M]`은 생성된 `orm.Manager[M]`과 명시적 쓰기 필드 목록으로 기본 목록·단건·생성·수정·삭제를
조립한다. 새 모델 필드는 자동으로 입력에 노출되지 않는다. `Filter`는 기존 범위에 AND 조건을 추가하며,
수정·삭제는 같은 범위에서 현재 행을 다시 조회한다. SQL과 descriptor를 애플리케이션에서 복제하지 않는다.

```go
resource, err := crud.New(models.ArticleObjects, crud.Config{
    Fields: []string{"title", "published", "summary", "slug"},
})
```

`api/input.NewResource(resource, spec, parserConfig)`는 같은 모델 IR의 JSON Spec이 이 쓰기 목록을 넓히지 않는지
시작 시 확인한다. 반환형은 기존 `Body[orm.ModelInput[M]]`이다. 동일 Body가 parser·full/partial·OpenAPI·배열을
계속 소유한다. `forms/model.PrepareInput`은 검증된 모델 폼을 같은 입력형으로 전달하며 폼의 원래 객체 ID도
보존한다. 폼에서 제외된 현재 값은 쓰기 시점에 다시 읽은 모델에서 가져온다.

`CreateInSession`·`UpdateInSession`·`DeleteInSession`은 transaction을 열거나 확정하지 않는다. 소유자는
`db.AtomicResult` 또는 `db.RelationAtomicResult` 안에서 이들을 호출하고, 출력 준비와 감사까지 끝낸 뒤
오류를 그대로 반환해야 한다. InSession의 반환값은 commit 전까지 잠정 값이다.

```go
return db.AtomicResult(ctx, backend, func(work context.Context, session db.Session) (Response, error) {
    mutation, err := resource.UpdateInSession(work, session, id, input, crud.SaveOptions[models.Article]{
        Before: checkCurrentObjectAndChoices,
    })
    if err != nil { return Response{}, err }
    // 같은 callback 안에서 응답과 감사도 준비한다.
    return prepareResponseAndAudit(work, session, mutation)
})
```

- 요청 권한과 CSRF는 body·객체 I/O 전에 확인한다. Resource는 인증이나 권한을 추측하지 않는다.
  `SaveOptions.Before`는 현재 객체·revision·관계 선택 검사와 명시적 서버 조정을 담당한다.
- `ServerValues`는 입력 검증 뒤 서버 소유 값을 추가한다. 입력에 이미 같은 필드가 있으면 오류다.
  Before가 객체 키를 교체할 수도 없다.
- 기본 고유성 검사는 실제 바뀐 constraint의 전체 현재 값을 사용한다. 폼처럼 전체 candidate 검사가 필요한 경로는
  `UniqueAll`을 명시한다. 두 정책 모두 DB의 최종 uniqueness를 대체하지 않는다.
- 현재 행은 실제 변경 열만 UPDATE한다. no-op은 DML을 생략하고, 사라진 행을 INSERT로 되살리지 않는다.
  생성은 IR의 제외 필드 기본값까지 적용한다. 준비는 `full_clean`이나 쓰기 권한이 아니다.
- Collection은 scalar와 분리된다. 선택된 각 collection에 명시적 saver가 없거나 설정이 중복되면 I/O 전에 실패한다.
  선택 검사와 저장은 같은 session에서 수행하며, saver 실패도 전체 소유 transaction으로 전달한다.
- 삭제에는 host 정책을 반드시 제공한다. 관계가 있는 앱은 생성된 `RelationDeleter.DeleteInSession`을 연결한다.
  단일 모델의 `Manager.Delete`도 애플리케이션이 명시적으로 선택한다.
- 목록의 count와 rows는 두 조회다. 같은 snapshot이 필요하면 소유자가 snapshot session을 제공한다.
  `ListQuery`는 생성된 eager/prefetch query도 그대로 사용하므로 관계 로딩 방식을 바꾸지 않는다.

파일 업로드는 별도 publication outcome을 가진다. Pending upload가 있는 폼은 `PrepareInput`이 거부하며,
기존 `PrepareInstance`와 명시적 파일 게시·collection 저장 경로를 사용한다.

실제 조립은 [Article](../examples/article/articleapp/repository.go)과
[Helpdesk](../examples/helpdesk/resources.go), 입력 의미는 [typed input](../api/input/README.md)을 참고한다.
