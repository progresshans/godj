# ADR-0004: 전역 `godj` CLI와 프로젝트 바이너리의 역할을 분리

> 결정 이유를 보존한 기록이다. 현재 API·지원 범위는 [현행 아키텍처](../ARCHITECTURE.md)와
> [구현 현황](../status/IMPLEMENTATION_MATRIX.md)를 따른다. 옛 내부 파일 구성·단계별 검증 절차는 현재 호환 요구가 아니다.
> 당시의 전체 기록과 실행 증거는 [고정 원문](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr/0004-cli-and-project-binary.md)에 있다.

- 상태: Accepted
- 날짜: 2026-08-07
- 관련 질문: Q-001, Q-010

## 맥락

Django의 `manage.py`는 project settings, installed apps, models, migration, custom commands를 로드하는 프로젝트 전용 진입점입니다. Go에서 Python 파일 모양을 복제할 이유는 없지만 그 역할은 필요합니다.

## 결정

- 사용자 명령 namespace는 `godj`로 통일합니다.
- 전역 CLI는 `version`, `startproject`, `startapp`, 프로젝트 탐색과 build/orchestration을 담당합니다.
- settings/app/model/custom command가 필요한 동작은 project code가 링크된 프로젝트 바이너리가 실행합니다.
- production에서는 같은 project binary가 `serve`, `migrate`, `createsuperuser` 등을 subcommand로 제공합니다.
- `go generate`는 보조 진입점이고 `godj generate`가 공식 명령입니다.

## 결과

Django의 project-aware command 경험을 Go build/deployment 모델에 맞게 보존합니다. 전역 CLI와 project library/generator version mismatch, build cache, codegen bootstrap을 명시적으로 처리해야 합니다.

## 의도적으로 결정하지 않은 것

project entrypoint API, `godj.toml` schema, temporary runner 구현, binary command protocol은 정하지 않았습니다.

## 현재 startproject 게시

2026-10-23 C1에서 전역 도구의 새 프로젝트 생성을 연결한다. `startproject MODULE DIRECTORY`의 dependency는
고정된 설치 module version 또는 명시한 `--framework-version`/`--framework-source`다. 개발 source는 실제 절대경로로
해석하며 current directory나 실행 파일 위치를 framework source로 추정하지 않는다. `latest`나 자동 upgrade는 사용하지 않는다.

새 프로젝트는 `modeldef`의 순수 Schema DSL 선언, 선언만 import하는 runner, generated binding을 import하는 HTTP runtime을
분리한다. 기존 generator가 canonical bundle/manifest를 소유하며 scaffold는 private ABI bytes를 작성하지 않는다.
Identity 앱과 모델이 없는 site 앱을 기본으로 제공하고 업무 모델·field 노출·권한은 프로젝트의 명시적 정책으로 남긴다.

대상 이름이 없는 parent 아래에 0700 staging 디렉터리를 만들고 declaration dependency 확인→실제 `generate`→module 정리→
readonly runtime build→`generate --check`를 마친다. 기존 command의 private workspace/process 수명과 정리를 재사용한다.
파일과 디렉터리를 sync하고 retained parent/stage identity를 재확인한 뒤 Linux/macOS의 exclusive rename으로 게시한다.
현재 기존 파일·빈/비어 있지 않은 디렉터리·symlink 모두 거부한다. Parent/stage가 바뀌면 다른 경로를 지우거나 채택하지 않는다.

완료 전 취소/준비 실패에는 callback/process를 join한 후 소유한 staging만 정리한다. 이미 rename한 결과와 이후 parent fsync·
cleanup·stdout 실패를 구분한다. `published_...`는 기존 정상 생성물을 보존하며 자동 rollback이나 재시도를 하지 않는다.
출력은 `format_version=1`, `status=created`, `directory`의 JSON 한 문서다. Credential·DB provisioning·server start는 생성 과정에 없다.

이 결정의 구현과 환경별 실행은 [C1](../../work/0137-project-core-startup.md) 및
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)를 따른다. 일반 command·호환성/reload 완료를 뜻하지 않는다.

## 앱 생성과 설치 등록

`startapp LABEL [DIRECTORY]`는 같은 exclusive directory 게시 경계로 새 선언과 안내를 만든다.
프로젝트가 선택하는 설치 앱은 `ProjectSpec`의 명시적인 Go 선언이다. 파일 생성과 등록을 구분해 전역 init registry나
파일 목록 탐색으로 실행 중인 앱 집합이 바뀌는 일을 피한다. 생성 결과는 `installed: false`를 명시한다.

Starter의 `Applications()`에서 새 선언 package를 import하고 `Definition()`을 추가한다. 기존 앱 목록·생성물·migration·DB는
앱 생성 명령이 수정하지 않는다. `generate`가 정규화된 IR과 동일 project binding을 만들고 migration은 별도 실행한다.
빈 앱도 source namespace와 imported companion 검사 대상이다. Raw model surface가 없다는 이유만으로 검사를 생략하지 않고,
해당 label의 canonical empty Schema IR hash와 같을 때만 모델이 없음을 인정한다. 첫 모델부터 기존 namespace guard를 적용한다.

기본 scaffold는 SQL label에서 Go alias를 만들 때 고정 `app` prefix와 `_`→`X` 인코딩을 사용한다. Canonical label에 uppercase가
없으므로 서로 다른 underscore 배치가 같은 alias가 되지 않으며 Go keyword도 사용할 수 있다. 앱 identity는 원래 IR label이다.
다른 project layout은 공개 `AppSpec`에 명시적 alias와 위치를 선택할 수 있다.

이 생성/등록 구분은 Django의 [startapp](https://docs.djangoproject.com/en/6.1/ref/django-admin/#startapp)과
[설치 목록 편집](https://docs.djangoproject.com/en/6.1/intro/tutorial02/#activating-models)의 외부 흐름을 참고한다.
Python import 구조를 복제하지 않는다. 고정 source의 독립 실행 비교와 C1 전체 수용은 해당 작업에 남는다.

## 사용자 명령과 호환성 조회

사용자 명령은 `commands_package`로 선택한 별도 executable에 명시적으로 등록한다. Generated model import가
선언 runner의 bootstrap을 막지 않게 하며, 전역 CLI는 업무 package를 직접 import하지 않는다. 이름 중복·reserved 이름·
미설치 앱 소유자는 거부한다. Django의 순서에 의한 command override는 이 명시적 등록 정책으로 대체한다.
Typed 인자/option을 모두 검증한 뒤 순수 check와 resource open을 수행한다. Command resource는 invocation이 소유하고,
handler를 join한 뒤 독립 cleanup context로 닫는다. Callback/출력/cleanup의 실패를 DB rollback으로 해석하지 않는다.

전역 management 경로는 runserver와 같은 선언 snapshot·generated bundle·readonly runtime build·재검사를 사용한다.
별도의 metadata probe는 설정·앱 초기화·DB 없이 framework build identity와 실제 generator/명령 contract를 조회한다.
같은 private executable을 조회 후 실행하며 불일치 시 업무 callback에 진입하지 않는다. 고정 module version은 정확히
비교하고, 명시적 개발 source끼리는 동일 contract를 요구한다. Contract 일치는 source 무결성 증명이 아니다.
현재 적용된 경로와 기존 명령·독립 설치로의 잔여 연결은 [C1](../../work/0137-project-core-startup.md)에 기록한다.

모델 없는 설치 앱의 identity는 Schema IR/ProjectSpec에 유지한다. 과거 table state에 없던 빈 앱은 migration을 만들지 않는다.
첫 모델부터 실제 초기 migration을 만든다. 이 규칙으로 기존 nonempty history의 삭제·외부 app의 모델/이력 불일치나
예상 밖 state를 허용하지 않는다. 생성 snapshot과 storage history는 서로 다른 책임을 갖는다.
