# ADR-0036: Project Schema Generated Bundle and Recoverable Publication

- 상태: Accepted
- 날짜: 2026-08-20
- 관련 work/contract: [GDJ-0037](../../work/0037-project-schema-generated-bundle-and-recoverable-publication.md), Q-010, Q-017
- 선행 결정: [ADR-0035](0035-pre-release-current-only-format-and-generated-publication.md)

## 맥락

GDJ-0036은 Schema IR, generated ABI와 migration lifecycle을 하나의 current 형식으로 재기준화했습니다. 그 terminal
baseline의 production publication은 파일별 `WriteFile` 경계였고, app/project renderer는 project 전체 snapshot과
output roster를 공동 소유하지 않았습니다. 여러 generated 파일을 순차 교체하면 중간 실패나 프로세스 중단 뒤 old/new 세대가
섞일 수 있으며, 그 GDJ-0036 baseline의 `generate-check`는 project 전체 manifest와 stale-file ownership을 검증하지 않습니다.

여러 디렉터리에 걸친 rename을 filesystem-atomic transaction으로 표현할 수는 없습니다. 대신 생성 입력을 한 번
normalize한 immutable bundle, target mutation 전 whole-candidate compile, manifest commit marker와 journal recovery,
mixed-generation compile seal을 결합해 제공 가능한 보장을 정확히 정의해야 합니다.

## 결정 기준

- 한 project schema snapshot이 모든 app/project generated output을 소유할 것
- 사용자 파일과 수정된 generated 파일을 자동 덮어쓰거나 삭제하지 않을 것
- candidate 전체가 compile되기 전에 target을 변경하지 않을 것
- 정상 오류는 prior committed bundle을 exact 보존할 것
- crash 뒤 exact old 또는 exact new 상태로 결정적으로 복구할 것
- 혼합 세대가 조용히 compile-success하지 않고 fail-closed할 것
- `generate --check`는 선택한 project tree와 Git 상태를 수정하지 않을 것. 격리 candidate workspace는 project 밖에서 정리할 것
- 개별 renderer는 pure helper로 유지하되 공식 generate/publication entry는 하나일 것

## 결정

1. `codegen.ProjectSpec`은 project package와 ordered app declarations를 소유하며 모든 Schema를 한 번 clone,
   normalize, canonicalize합니다.
2. `codegen.GenerateProject(ProjectSpec)`은 opaque immutable `GeneratedBundle`을 반환합니다. Bundle은 fixed current
   roster, canonical manifest, snapshot digest와 cloned source accessors를 소유합니다.
3. Snapshot digest는 bundle format, generator ABI roster, package/layout identity와 normalized schemas에서
   계산합니다. Rendered output hash는 manifest에 기록하지만 snapshot 입력에는 넣지 않습니다.
4. App main/companions와 project binding/companions는 snapshot marker를 참조합니다. Old/new 파일이 섞인 상태는
   compile failure가 되어야 합니다.
5. `internal/projectgenerate`가 whole-candidate overlay compile, read-only check, publication lock, journal과 recovery를
   소유합니다. 외부 사용자가 arbitrary file bundle이나 publisher를 조립하는 public API는 만들지 않습니다.
6. Publication은 prior manifest/file digest 재검증, same-filesystem staging+fsync, whole-candidate compile, durable
   journal, deterministic rename, manifest-last commit marker, directory fsync 순서로 수행합니다.
7. Manifest commit 전 중단은 exact old bundle로 rollback하고, commit 뒤 publisher cleanup 중단은 exact new bundle을
   검증한 뒤 cleanup을 재개합니다. 어느 상태도 증명할 수 없으면 structured recovery-required error로
   fail-closed합니다. Publisher가 성공한 뒤 outer private workspace 또는 retained root FD cleanup이 실패하면 target에
   새 snapshot이 이미 commit된 채 CLI가 success stdout 없이
   `project_generation_process_error/project_cleanup_failed`와 exit 3을 반환할 수 있습니다. 이때 caller는 재시도 전에
   `godj generate --check`로 상태를 확인합니다.
8. Stale file은 prior manifest가 소유하고 on-disk hash가 exact old value일 때만 이동/삭제합니다. Symlink,
   non-regular file, path traversal, root escape와 user modification은 conflict입니다.
9. `godj generate`만 recovery와 publication을 수행합니다. `godj generate --check`는 missing/extra/drift/interrupted
   상태를 read-only로 진단합니다.
10. 여러 디렉터리에서 external compiler가 publication 내내 성공한다는 보장은 하지 않습니다. 제공하는 보장은
    returned ordinary failure의 prior-bundle 보존, crash recovery와 mixed-generation compile fail-closed입니다.
11. Global orchestration은 선택한 project root의 device/inode identity를 `SealProjectRoot`로 고정해 `CheckRoot`,
    `NewGoCandidateVerifierRoot`, `PublishRoot` 전 과정에 전달합니다. Path rebound는 target mutation 전에 닫힌 오류로
    거부합니다. String 기반 함수는 호출 시 root를 새로 seal하는 internal convenience wrapper입니다.

## Current bundle roster

App마다 다음 네 파일을 소유합니다.

- `zz_godj_generated.go`
- `zz_godj_relation.go`
- `zz_godj_relation_object.go`
- `zz_godj_relation_projection.go`

Project는 다음 여덟 파일을 소유합니다.

- `zz_godj_bindings.go`
- `zz_godj_relation_query.go`
- `zz_godj_relation_object.go`
- `zz_godj_relation_reverse.go`
- `zz_godj_relation_prefetch.go`
- `zz_godj_relation_select_related.go`
- `zz_godj_relation_delete.go`
- `zz_godj_relation_facade.go`

Project root에는 canonical `.godj/generated-manifest.json` 하나를 둡니다.

Manifest는 map이 아닌 고정 struct 순서의 canonical JSON과 final LF를 사용합니다.

```json
{
  "format_version": 1,
  "snapshot_sha256": "<64 lowercase hex>",
  "generator_abi": [
    {"role": "<fixed role>", "filename": "<fixed filename>", "version": "<renderer ABI>"}
  ],
  "project": {"package_name": "...", "import_path": "...", "directory": "..."},
  "apps": [
    {
      "alias": "...",
      "app_label": "...",
      "package": {"package_name": "...", "import_path": "...", "directory": "..."},
      "schema_sha256": "<64 lowercase hex>"
    }
  ],
  "files": [
    {"path": "...", "owner": "project|app:<label>", "mode": "0644", "sha256": "<64 lowercase hex>"}
  ]
}
```

현재 producer ABI는 bundle/seal 1개, app renderer 4개, project renderer 8개의 고정 role 순서입니다. Apps는
app-label/alias/import/directory canonical 순서, files는 slash path lexical 순서입니다. Full normalized schema는
snapshot digest preimage에만 넣고 manifest에는 진단용 app schema digest만 둡니다. Persisted format-1 decoder는
unknown/duplicate/trailing/noncanonical bytes와 순서·owner·path·mode·hash 위반을 fail-closed하지만, 안전한 prior
ABI version/roster는 소유권·stale-file upgrade 입력으로 읽습니다. 새 bundle publication은 별도 current validator가
exact current ABI와 roster를 요구합니다. 따라서 renderer version/roster 변화는 read-old/write-current로 수렴하며,
prior manifest를 현재 bytes와 같지 않다는 이유만으로 읽지 못하는 dead-end가 없습니다.

## 고려한 선택지

### 파일별 `WriteFile` 반복

각 파일의 last-good은 보존하지만 project 전체 old/new 혼합과 stale-file ownership을 닫지 못합니다. 공식 project
publication 경계로 채택하지 않습니다.

### app/project 디렉터리 swap

Generated 파일과 사용자 source가 같은 package directory에 있으므로 사용자 파일을 안전하게 보존하면서 project 전체를
원자 교체할 수 없습니다. generated-only subtree/import ownership을 다시 설계하지 않는 한 채택하지 않습니다.

### arbitrary `[]GeneratedFile` public publisher

파일이 동일 schema snapshot과 generator ABI에서 왔는지 증명하지 못합니다. Opaque `GeneratedBundle`만 공식 입력으로
사용합니다.

## 결과

- 현재 renderer 전체가 하나의 project snapshot과 manifest 아래 수렴합니다.
- Full candidate compile과 read-only drift check가 per-file write보다 앞섭니다.
- Normal failure와 crash recovery 보장이 구분됩니다.
- Manifest-owned stale deletion과 사용자 파일 보호가 명시됩니다.
- 현재 relation runtime 의미와 contract status는 바뀌지 않습니다.
- 이 ADR의 `Accepted`는 current 방향 채택을 뜻하며 `Verified`와 같지 않습니다. GDJ-0037의 final
  full/386/repository-external source-clean-copy local gate는 통과했지만 exact-head hosted 검증은 pending입니다.

## 비목표

- 일반-purpose filesystem transaction framework
- distributed/network filesystem atomicity
- publication 중 external compiler 무중단 성공
- arbitrary plugin generator/file layout
- user-edited generated source merge
- raw-model embedding/unwrap/sidecar 최종 UX
- 새 Field/Relation, PostgreSQL, Web, Form/Admin/API 구현
- migration writer/autodetector와 first-alpha 이후 upgrader/semver 정책
- 검증하지 않은 Windows publisher 지원

## 검증

- ProjectSpec/Bundle/manifest determinism, deep snapshot과 permutation
- full current renderer union과 external consumer compile
- missing/extra/modified/stale/interrupted read-only check
- verifier/cancellation/CAS 실패에서 target write 0
- filesystem step fault injection과 child-process crash recovery
- exact old 또는 exact new만 accepted, hybrid compile-success 0
- concurrent publisher serialization, symlink/path traversal/user edit rejection
- actual project `generate`/`generate --check`, deterministic regeneration과 clean Git state
- final frozen tree의 full local/386/repository-external source-clean-copy gate와 exact-head hosted matrix 한 번
