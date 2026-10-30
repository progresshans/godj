# Admin 위젯과 검증 확장

`forms.WithWidgetTemplate("asset_code")`와 `formmodel.WithWidgetTemplate("asset_code")`는
필드의 표시 템플릿 이름만 선택한다. `Field.Widget()`의 native 제출 표현, Schema IR의 타입·choices·nullable·길이,
기존 validator와 현재 값에서의 준비/저장 경로는 바꾸지 않는다. 모델의 노출 allowlist와 권한도 따로 유지한다.

```go
form := formmodel.Definition{
    Fields: []string{"code", "attachment"},
    Overrides: []formmodel.Override{
        formmodel.OverrideField("code", formmodel.WithWidgetTemplate("asset_code")),
    },
}
site, err := admin.NewSite(admin.SiteConfig{
    Apps: installed, Namespace: "inventory", Registry: registry, Auth: runtime,
    Widgets: admin.WidgetConfig{
        Templates: embeddedWidgetFiles,
        Entries: []admin.WidgetTemplate{
            {Name: "asset_code", Widget: forms.TextInput, Template: "code.html"},
        },
    },
})
```

`Templates`는 신뢰하는 애플리케이션 소스의 `fs.FS`다. 요청이나 사용자 업로드에서 가져오지 않는다.
시작 시 제한된 template engine이 전체 파일을 읽어 compile하고 snapshot하므로 요청마다 파일을 읽거나 callback을
실행하지 않는다. 최대 64개 이름, 이름당 64 bytes이며 중복·존재하지 않는 템플릿·잘못된 구문/참조·순환을 거부한다.
일반 수정/생성 Form, object/collection command, inline, collection FormSet의 선택된 이름을 모두 확인한다.
등록 누락이나 native widget 종류 불일치가 있으면 Site와 route를 게시하지 않는다.

템플릿은 control markup을 반환한다. 예를 들어 `code.html`은 다음과 같다.

```html
<input name="{{ field.name }}" id="{{ field.control_id }}" value="{{ field.value }}"
  {% if field.required %}required{% endif %}{% if field.readonly %} disabled{% endif %}
  {% if field.invalid %}aria-invalid="true" aria-describedby="{{ field.error_id }}"{% endif %}>
```

템플릿 작성자는 native control의 name·value 표현·disabled·required와 label/키보드 접근성을 보존한다.
Checkbox의 체크 상태는 `field.checked`, select의 후보는 `field.options`의 value/label/selected를 사용한다.
검색 select는 `id="{{ field.name }}-choices"`, `aria-describedby="{{ field.name }}-choice-status"`와
각 option의 `page`에 따른 `data-choice-page`를 유지한다. `Textarea`는 시작 태그 뒤에 newline 하나를 넣어
HTML parser가 원래 입력의 첫 newline을 제거하지 않게 한다. 파일은 `type="file"`을 사용하며 value를 만들지 않는다.
표시 코드를 바꿔도 잘못된 제출이 서버 검증을 통과하거나 새 저장 권한을 얻지 않는다.

Admin은 공통 label·오류 목록·검색 버튼과 상태·파일의 현재 참조/clear·재선택 안내를 계속 소유한다.
Control만 교체하므로 오류/clear/search wrapper를 템플릿에 중복해서 만들지 않는다.
Readonly inline/collection 행은 바깥 disabled fieldset도 유지하며 PK·revision은 별도의 서버 control로 남는다.
Prototype와 재번호도 같은 표시 경로를 사용하고 오류 연결 ID를 함께 바꾼다.

템플릿의 유일한 입력은 표시용 `field` 객체다. `name`, `label`, `control_id`, `error_id`, `invalid`, `readonly`,
`required`, 원래 입력의 안전한 `value`, checked/options, 파일 표시 및 선택 검색 상태를 제공한다.
오류는 code와 안전한 message만 제공하며 전체 요청·principal·모델 후보·cleaned data·파일 capability·CSRF/URL capability는
전달하지 않는다. 값과 label은 engine escaping을 거치며 신뢰하는 템플릿을 렌더링한 결과만 최종 페이지에 HTML로 합친다.
위젯 출력 합계와 전체 페이지 출력 한도를 각각 적용한다. 취소나 렌더 오류에서는 부분 응답을 게시하지 않는다.

PasswordInput·HiddenInput은 사용자 정의 템플릿 선택과 등록을 거부한다. 숨긴 값, 선택하지 않은 stored field 또는
source가 없는 computed 값을 템플릿 등록으로 입력 필드로 승격할 수 없다. `ReadOnlyField`의 순수 display 값은
이 입력 렌더 경로를 지나지 않는다. 원래 Password의 입력/initial과 파일 bytes도 표시 context에 넣지 않는다.

필드의 `WithValidators`, `Definition.Validators`, `PostClean.Clean`/`Validators`, Set validator의 실행 순서와
진단 합성은 [모델 Form](../forms/model/README.md)을 따른다. 위젯은 validator가 아니며 성공한 표시가 저장 승인도 아니다.
현재 DB 범위·권한·revision·unique 검사와 명시적인 writer, 파일 게시 receipt는 그대로 필요하다.
`WithErrors`/확정된 rejection은 원래 입력과 기존 진단을 보존하며 I/O/transaction 실패를 입력 오류로 바꾸지 않는다.

외부 생성 [Directory–Inventory 소비자](../codegen/consumertest/testdata/admincollection/runtime_test.go)는 같은 공개 API로
code/file 템플릿을 등록하고 기존/새 행·readonly·파일 게시와 rollback을 연결한다. 실제 실행 결과와 미검증 범위는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.
