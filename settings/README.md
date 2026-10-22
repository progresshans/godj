# 프로젝트 설정과 환경

`settings.New`는 프로젝트 이름·설치 앱·storage registry의 불변 설정을 만든다. 실제 backend의 열기와 닫기는
이를 만든 애플리케이션이 소유한다. 설정 생성이 DB나 파일 backend를 몰래 열지 않는다.

`settings.NewEnvironment`는 process 진입점에서 한 번 읽은 환경을 별도 snapshot으로 만든다.
Library는 `os.Getenv`, `os.Setenv` 또는 전역 registry를 사용하지 않는다.

```go
environment, err := settings.NewEnvironment(os.Environ())
if err != nil {
    return err
}
databaseURL, configured := environment.Lookup("APP_DATABASE_URL")
// configured는 존재 여부다. 존재하는 빈 값의 허용 여부와 backend 선택은
// 프로젝트의 명시적인 설정 정책에서 검증한다. databaseURL은 로그에 쓰지 않는다.
```

입력은 `NAME=value` 목록이며 첫 `=` 뒤의 공백·추가 `=`·newline을 그대로 보존한다. 존재하지 않는 값과
존재하는 빈 값은 구분한다. 이름 없는 항목·구분자 누락·NUL·중복 이름과 4,096개/전체 1 MiB 초과를 거부하고
오류에는 입력의 위치와 사유만 포함한다. 실패한 입력의 일부를 사용 가능한 snapshot으로 반환하지 않는다.

입력 slice를 수정하거나 process 환경이 바뀌어도 기존 snapshot은 변하지 않는다. 복사한 `Environment`는
같은 불변 값을 안전하게 읽으며 수정 가능한 map/slice를 노출하지 않는다. Zero value는 비어 있는 환경이다.
`fmt` 표시에는 값을 넣지 않고 JSON으로도 값 목록을 직렬화하지 않는다. `Lookup`의 반환값에는 secret이 있을 수
있으므로 진단·로그·HTTP 응답으로 전달하지 않는다. 암호화 저장소나 secret provider를 대신하는 타입은 아니다.

Article의 [project runner](../examples/article/cmd/projectrunner/main.go)와
[서버 진입점](../examples/article/cmd/site/main.go)은 각각 이 snapshot을 만들고 기존의 엄격한 DB 선택 정책에
`environment.Lookup`을 전달한다. 실행·환경별 검증 상태는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
