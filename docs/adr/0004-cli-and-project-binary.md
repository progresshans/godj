# ADR-0004: 전역 `godj` CLI와 프로젝트 바이너리의 역할을 분리

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

## 검증

M0/M1에서 empty project discovery, version mismatch, stale generated code, failed build가 기존 정상 파일을 손상시키지 않는지 검증합니다.
