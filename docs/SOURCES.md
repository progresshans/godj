# 기준 출처와 검증 기록

- 마지막 확인: 2026-08-07 (Asia/Seoul)
- 외부 사실은 가능하면 공식 1차 출처를 사용합니다.

## Django

- [Django download page](https://www.djangoproject.com/download/) — 2026-08-07 확인 시 최신 공식 버전은 6.1입니다.
- [Django 6.1 release notes](https://docs.djangoproject.com/en/6.1/releases/6.1/) — release date 2026-08-05, Python 3.12/3.13/3.14 지원.
- [Django 6.1 documentation](https://docs.djangoproject.com/en/6.1/)
- [QuerySet API](https://docs.djangoproject.com/en/6.1/ref/models/querysets/)
- [Django test suite guide](https://docs.djangoproject.com/en/6.1/internals/contributing/writing-code/unit-tests/)
- [Django 6.1 source tag](https://github.com/django/django/tree/6.1)
- [Django BSD license](https://github.com/django/django/blob/6.1/LICENSE)

로컬 `/Users/hanhyeonjin/Documents/django`에서 tag `6.1`은 commit `fe0a859f537d4238cf49fca39073513206f83122`이며 `VERSION = (6, 1, 0, "final", 0)`임을 확인했습니다. 현재 checkout `main`은 2026-08-07 확인 시 commit `4243ab11dc957fd14a1875e6b715ff5e6114a415`, Django `6.2.0-alpha`이므로 6.1 oracle로 직접 사용하지 않습니다.

## Go

- [Go release history](https://go.dev/doc/devel/release) — Go 1.26.5 release 기록.
- [Go 1.26 announcement](https://go.dev/blog/go1.26)
- [Go language specification](https://go.dev/ref/spec) — generic type/method receiver와 type parameter 규칙.
- [Generics tutorial](https://go.dev/doc/tutorial/generics)
- [`go generate` design](https://go.dev/blog/generate) — build에 자동 포함되지 않는 별도 명령.

로컬 개발 환경은 2026-08-07 확인 시 `go1.26.5 darwin/arm64`입니다. 프로젝트의 언어 기준은 Go 1.26이고 exact CI toolchain pin은 M0에서 확정합니다.

## Codex 작업 지침

- [OpenAI 공식 AGENTS.md 문서](https://learn.chatgpt.com/docs/agent-configuration/agents-md) — Codex가 작업 전 지침 chain을 구성하고 project root에서 현재 directory까지 계층적으로 파일을 읽는 방식.

루트 `AGENTS.md`는 반복 규칙과 읽기 순서만 유지하고, 전체 설계·상태·작업 기록은 별도 문서로 분리했습니다. 하위 `AGENTS.md`는 실제 하위 시스템이 생기고 별도 규칙이 필요할 때 추가합니다.

## 현재 로컬 환경 관찰

| 항목 | 2026-08-07 관찰값 | 호환 약속 여부 |
|---|---|---|
| Go | 1.26.5, darwin/arm64 | 언어 기준만 Accepted |
| Python | 3.13.1 | M0에서 exact profile 재결정 |
| SQLite (`python3`) | 3.51.0 | M0에서 exact profile 재결정 |
| SQLite CLI | 3.51.0 | 개발 환경 관찰만 |
| GoDj Git branch | unborn `main` | 현재 상태 |
| GoDj remote | `https://github.com/progresshans/godj.git` | module path 근거, `go.mod` 미생성 |

## 갱신 규칙

- reference profile 변경 시 날짜, exact version/commit, 이유, 영향을 받는 contract를 함께 기록합니다.
- 웹의 `latest` 문구보다 tag/commit과 lockfile을 우선합니다.
- drift 가능성이 있는 version·tool behavior는 새 milestone 시작 시 재확인합니다.
- upstream source에서 코드를 복사·번역하면 file-level provenance와 license notice를 실제 파생 파일 가까이에 둡니다.
