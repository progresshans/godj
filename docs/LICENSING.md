# 라이선스와 provenance

## 현재 저장소

GoDj 자체의 배포 라이선스는 아직 선택되지 않았다. 루트 LICENSE가 없는 상태를 외부 사용·배포 라이선스가 부여된 것으로
해석하지 않는다. 프로젝트 소유자의 라이선스 결정과 전체 binary dependency의 고지 검토는 외부 배포 전에 필요하다.

`LICENSE.django`는 Django 고지이며 GoDj 자체에 그 라이선스를 적용하지 않는다.
SQLite와 PostgreSQL dependency의 기존 `LICENSE.modernc-sqlite`, `LICENSE.modernc-libc`, `LICENSE.pgx`와
[NOTICE](../NOTICE.md)를 보존한다. 이 파일들은 모든 transitive dependency의 검토를 대신하지 않는다.

## 독립 시나리오와 파생물

| 분류 | 기록할 것 |
|---|---|
| 공개 동작을 보고 독립 작성한 scenario | `derived=false`, 동작 기준 version/commit/source/test와 GoDj 고유 fixture |
| GoDj 자체의 wire·안전성 정책 | decision/proposal provenance와 해당 ADR/작업; Django 동작으로 표현하지 않음 |
| upstream 코드·fixture·주석·assertion을 복사/번역/변형 | `derived=true`, license, exact source/symbol과 변경 내용, 필요한 원본 고지 |

기준 경로를 참조했다는 이유만으로 코드가 파생물이라고 하거나, 실제 표현을 복사하고도 독립 시나리오라고 표시하지 않는다.
Manifest의 provenance와 파일 가까이의 copyright/license/modification notice가 source authority다.

현재 conformance의 독립 작성 분류는 각 manifest에 기록되어 있다. 역사적 proposal이 나중에 accepted되었다고 당시 artifact의
provenance를 소급 변경하지 않는다. Current-only 형식 reset으로 새로 만든 reference는 자신의 새 decision provenance를 가지며
old artifact는 Git 이력에 남는다. Oracle 디렉터리 이름은 provenance 분류를 대체하지 않는다.

Raw token/password·credential-bearing URL·verifier cause는 source/provenance/observation/audit에 남기지 않는다.
Codegen이나 platform 실행 증거가 어떤 코드의 저작권·라이선스 분류를 바꾸는 것도 아니다.

## 배포

개발 CI에서 Django dependency를 설치하는 것과 source/wheel/container layer를 제품으로 재배포하는 것을 구분한다.
외부 binary에 포함되는 dependency graph와 필요한 고지를 다시 확인하고 source·license를 함께 보존한다.
기존 자세한 scenario별 provenance 판단은
[고정 Git 기록](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/LICENSING.md)에 있다.
이번 정리는 그 고지나 분류를 변경하지 않는다.
