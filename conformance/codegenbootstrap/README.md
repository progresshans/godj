# Codegen bootstrap spike

이 디렉터리는 Q-001의 선언 package와 generated package 분리안을 검증하는
실행 가능한 M0 실험입니다. GoDj의 production Schema DSL이나 공개 codegen API가
아닙니다.

fixture의 import 경계는 다음과 같습니다.

```text
cmd/spikegen -> modeldef -> schema
             -> spikecodegen -> schema

models/zz_godj_generated.go <- generated output
models/model_methods.go     <- user code
```

`cmd/spikegen`의 build graph에는 `models`가 들어가지 않습니다. 따라서 schema와
사용자 메서드가 새 type 이름을 사용해 현재 `models` package가 compile되지 않는
상태에서도 generator 자체는 실행될 수 있습니다.

generator는 candidate를 숨김 임시 파일에 쓰고 `go test -c -overlay`로 기존
generated 파일 대신 candidate를 사용한 target package를 compile합니다. 사용자
테스트나 package initialization은 실행하지 않습니다. 검증이 성공할 때만 package당
하나인 generated 파일을 `os.Rename`으로 교체합니다. schema validation, format,
candidate compile 중 하나라도 실패하면 마지막 정상 generated 파일을 보존합니다.

## 검증 시나리오

1. `Post.Title`에서 `Article.Headline`으로 rename하고 사용자 메서드도 새 이름으로
   바꿉니다. stale output 때문에 target package는 깨지지만 generation은 성공해야
   합니다.
2. schema에서 `Headline`을 삭제하고 사용자 메서드는 그대로 둡니다. candidate
   compile이 실패하고 이전 output의 bytes/hash가 유지되어야 합니다.
3. 사용자 메서드를 수정한 뒤 재시도하면 성공하며 같은 입력을 두 번 생성한 결과가
   byte-identical해야 합니다.
4. 잘못된 schema와 format할 수 없는 output도 이전 output을 보존해야 합니다.
5. `go list -deps` 결과에 generated target package가 없어야 합니다.

실행 명령:

```bash
go test ./conformance/codegenbootstrap -count=1 -v
```

## 의도적인 제한

- fixture 전용 DSL과 generator이며 public API가 아닙니다.
- package당 기존 generated Go 파일 하나가 있는 rename/delete 복구만 검증합니다.
  최초 생성, 다중 generated 파일 transaction, crash 중 durability는 다루지 않습니다.
- candidate 검증은 fixture의 `./models` package만 실행합니다. 실제 구현은 package
  discovery, build tags, platform별 파일, 외부 consumer compile 범위를 별도로 정해야
  합니다.
- schema 선언 package의 discovery, 전역 CLI와 프로젝트 generator version protocol,
  cross-app relation은 결정하지 않습니다.
- raw Go type 문자열은 format 실패를 재현하기 위한 spike 장치일 뿐 실제 DSL 설계가
  아닙니다.
- 파일 교체의 atomicity는 현재 Unix 계열 host의 같은 filesystem 안에서만 검증하며,
  Windows 교체 의미와 crash 후 directory durability는 다루지 않습니다.
