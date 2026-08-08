# 라이선스와 upstream provenance 정책

- 상태: Accepted for conformance artifacts
- 마지막 검토: 2026-08-08

## 현재 저장소 라이선스

GoDj 자체의 배포 라이선스는 아직 선택되지 않았습니다. 루트 `LICENSE`가 없는 현재
상태를 open-source license가 부여된 것으로 해석하지 않습니다. 공개 배포나 외부
기여 수용 전에 프로젝트 소유자가 별도 결정을 내려야 합니다.

`LICENSE.django`는 Django의 라이선스 사본이며 GoDj 자체에 BSD 3-Clause를
적용한다는 뜻이 아닙니다.

M1/M2 SQLite backend가 사용하는 `modernc.org/sqlite v1.56.0`과 locked dependency
`modernc.org/libc v1.74.4`의 BSD 3-Clause 전문은 각각
`LICENSE.modernc-sqlite`, `LICENSE.modernc-libc`에 보존합니다. 이 두 고지는 향후
binary에 들어가는 모든 transitive dependency의 배포 검토를 대신하지 않습니다.

## Conformance artifact 분류

각 contract provenance는 다음 두 경우를 구분합니다.

### 독립 동작 시나리오

- 공개 문서나 실행 결과로 동작을 파악합니다.
- GoDj 고유 모델명, fixture, 설명, 코드 구조로 새로 작성합니다.
- manifest의 `derived`를 `false`로 기록합니다.
- Django version/commit과 참조 문서 또는 테스트 경로를 기록합니다.
- 경로를 참조했다는 이유만으로 upstream 코드를 복사했다고 표현하지 않습니다.

### 복사·번역·변형한 upstream material

- manifest의 `derived`를 `true`로 기록하고 `license`를 필수로 둡니다.
- upstream commit, 파일 경로, class/function/test 이름과 변경 내용을 파일 가까이에
  남깁니다.
- 원본 저작권 고지, 조건, disclaimer와 파일별 추가 라이선스를 보존합니다.
- 파생물 전용 검토 없이 독립 시나리오 디렉터리에 섞지 않습니다.

M0/M1 query 시나리오, GDJ-0003 write/migration 시나리오·static migration fixture,
GDJ-0005 Save lifecycle, GDJ-0007 QuerySet evaluation/cache, GDJ-0009 migration
planning과 GDJ-0011 migration plan execution 시나리오는 모두 첫 번째
분류입니다. GDJ-0013 recorder-backed restart planning 시나리오도 같은 독립 작성
분류입니다. Upstream test code나 fixture를 복사하지 않고 GoDj 고유 app/table/value로
작성했으며 manifest reference는 동작 근거 추적용입니다. QuerySet cache와 migration
planning/execution/restart provenance entry도 모두 `derived=false`이고 pinned source/doc/test
symbol은 의미와 버전 추적만 합니다. Migration planning graph/module/fixture와 execution
failure sentinel/assertion과 recorder/restart fixture 구조도 upstream에서 복사·번역하지 않고 최소 GoDj 고유 정의로
작성했습니다.
GDJ-0014는 이 locked recorder/restart 시나리오에 GoDj 제품 adapter를 연결하고 manifest
status만 전환했으므로 기존 독립 작성 분류를 바꾸지 않습니다. 다음 GDJ-0015의
MIG-037..046 historical-state artifact는 아직 이 문단의 독립 시나리오 목록에 포함하지
않습니다. 실제 scenario/source가 작성될 때 파일별 provenance와 `derived` 값을 검토합니다.
Django의 고지 전문은 향후 경계가 흐려지는 것을 막기 위한 보수적 정책으로 저장소에
포함합니다.

## 배포와 CI

- CI에서 PyPI dependency로 Django를 설치하는 것과 Django source를 GoDj 산출물에
  재배포하는 것을 구분합니다.
- Django wheel, source, container layer를 배포 artifact에 포함하면 별도 third-party
  license 검사를 수행합니다.
- Go binary를 배포하기 전 `go.mod` 전체 dependency graph의 license와 notice 의무를
  다시 수집하고, root project license 결정과 함께 release gate로 검토합니다.
- 공식 재단의 보증으로 오해될 표현이나 Django/contributor 이름을 홍보에 사용하지
  않습니다.

관련 third-party 고지는 [`NOTICE.md`](../NOTICE.md), Django 전문은
[`LICENSE.django`](../LICENSE.django)에 있습니다.
