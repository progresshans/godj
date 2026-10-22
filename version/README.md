# 설치된 GoDj 식별

`godj version`은 실행 중인 전역 도구에 링크된 GoDj를 표시한다. `godj version --json`은
`format_version: 1`과 `framework` 객체를 출력한다. 프로젝트 탐색·build·DB 연결·네트워크 없이
실행 파일의 Go build metadata만 읽으며, 현재 디렉터리나 `godj.toml`에 의존하지 않는다.

프로젝트 runtime에서는 공개 `version.Current()`로 그 실행 파일에 링크된 GoDj를 조회한다.
별도로 설치한 전역 도구와 다른 결과일 수 있다. 반환값은 caller 소유의 값이며 전역 설정이나 cache가 없다.

| `origin` | 의미 | `version` |
|---|---|---|
| `module` | 고정 module version으로 링크됨 | 실제 링크된 version |
| `development` | framework main module 또는 Go workspace 개발 source | 없음 |
| `local_replacement` | 로컬 source를 가리키는 `replace` | 없음 |
| `module_replacement` | version이 있는 다른 module 또는 version으로 `replace` | 대체 module의 version |
| `unknown` | metadata가 없거나 식별 불가능 | 없음 |

Replacement의 `requested_version`은 원래 require한 version이며 실제 코드의 version을 뜻하지 않는다.
Versioned replacement만 `replacement_module`을 표시하고 로컬 경로는 출력하지 않는다.
`revision`과 `modified`는 GoDj 자체가 main module일 때만 포함한다. 외부 프로젝트의 Git revision은
GoDj dependency나 workspace source의 revision으로 사용하지 않는다. `go_version`은 실행 파일을 만든
컴파일러의 정보이며 GoDj의 최소 Go version을 뜻하지 않는다.

Build flags·다른 dependency·프로젝트 module 이름·환경·credential·로컬 replacement 경로는 결과에 포함하지 않는다.
텍스트 token은 길이와 문자 범위를 제한한다. 정보를 찾지 못하면 `unknown`을 유지하며 `latest`나 가짜 version을 만들지 않는다.
이 조회는 compiler metadata의 설명이다. 배포 source의 무결성 증명이나 도구/runtime/generated ABI의 호환성 검사는 아니다.

성공은 exit 0, 잘못된 인자는 2, 이미 취소/중단된 호출은 130, 출력 실패는 1이다.
오류는 stderr의 `version_error/<code>`로 표시하며 원래 인자나 writer의 오류 원문을 포함하지 않는다.
JSON 성공 결과를 전부 쓴 뒤에는 늦은 취소로 성공을 덮어쓰지 않는다.

의미의 기준은 Go의 [BuildInfo](https://pkg.go.dev/runtime/debug#BuildInfo)와
[module replacement](https://go.dev/ref/mod#go-mod-file-replace)다.
실제 CLI process, 고정 version의 로컬 proxy fixture, 로컬 replacement, workspace 소비자는 원격 CI에서 검증한다.
