# 설치된 GoDj 식별

`godj version`은 실행 중인 전역 도구에 링크된 GoDj를 표시한다. `godj version --json`은
`format_version: 1`과 `framework` 객체를 출력한다. 프로젝트 탐색·build·DB 연결·네트워크 없이
실행 파일의 Go build metadata만 읽으며, 현재 디렉터리나 `godj.toml`에 의존하지 않는다.

프로젝트 runtime에서는 공개 `version.Current()`로 그 실행 파일에 링크된 GoDj를 조회한다.
별도로 설치한 전역 도구와 다른 결과일 수 있다. 반환값은 caller 소유의 값이며 전역 설정이나 cache가 없다.

| `origin` | 의미 | `version` |
|---|---|---|
| `module` | VCS source stamp 없이 고정 module version이 기록됨 | 실제 기록된 version |
| `development` | framework checkout, version 없는 main module 또는 Go workspace source | compiler가 추론한 tag/pseudo-version이 있으면 진단용으로 표시 |
| `local_replacement` | 로컬 source를 가리키는 `replace` | 없음 |
| `module_replacement` | version이 있는 다른 module 또는 version으로 `replace` | 대체 module의 version |
| `unknown` | metadata가 없거나 식별 불가능 | 없음 |

Replacement의 `requested_version`은 원래 require한 version이며 실제 코드의 version을 뜻하지 않는다.
Versioned replacement만 `replacement_module`을 표시하고 로컬 경로는 출력하지 않는다.
`revision`과 `modified`는 GoDj 자체가 main module일 때만 포함한다. 외부 프로젝트의 Git revision은
GoDj dependency나 workspace source의 revision으로 사용하지 않는다. `go_version`은 실행 파일을 만든
컴파일러의 정보이며 GoDj의 최소 Go version을 뜻하지 않는다.
Go는 깨끗한 Git 체크아웃에도 tag나 pseudo-version을 기록할 수 있다. Main module의 VCS stamp가 있으면
`development`로 구분하고 기록된 version·revision을 유지한다. 이 version을 고정 dependency로 추정하지 않으며
`startproject`에는 framework source나 version을 명시해야 한다. `-buildvcs=false`인 checkout은 version 없는
`development`로 표시한다. `module`도 배포 출처의 무결성이나 registry의 현재 다운로드 가능성을 증명하지 않는다.

Build flags·다른 dependency·프로젝트 module 이름·환경·credential·로컬 replacement 경로는 결과에 포함하지 않는다.
텍스트 token은 길이와 문자 범위를 제한한다. 정보를 찾지 못하면 `unknown`을 유지하며 `latest`나 가짜 version을 만들지 않는다.
이 조회는 compiler metadata의 설명이다. 배포 source의 무결성 증명이나 도구/runtime/generated ABI의 호환성 검사는 아니다.

성공은 exit 0, 잘못된 인자는 2, 이미 취소/중단된 호출은 130, 출력 실패는 1이다.
오류는 stderr의 `version_error/<code>`로 표시하며 원래 인자나 writer의 오류 원문을 포함하지 않는다.
JSON 성공 결과를 전부 쓴 뒤에는 늦은 취소로 성공을 덮어쓰지 않는다.

의미의 기준은 Go의 [BuildInfo](https://pkg.go.dev/runtime/debug#BuildInfo),
[VCS에서 main version을 기록하는 동작](https://go.dev/doc/go1.24#go-command)과
[module replacement](https://go.dev/ref/mod#go-mod-file-replace)다.
실제 CLI process, 고정 version의 로컬 proxy fixture, 로컬 replacement, workspace 소비자는 원격 CI에서 검증한다.
