# ADR-0010: M2 migration은 state, operation, executor, recorder 경계를 먼저 검증한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0003, MIG-001..MIG-004, Q-012
- 대체하는 ADR: 없음

## 맥락

Migration은 현재 Go struct만 변경하거나 DDL 문자열만 순서대로 실행하는 기능이 아닙니다.
과거 project state, forward/backward operation, 적용 기록과 실패 시 transaction 의미가
함께 맞아야 합니다. MIG-001..MIG-004의 Django oracle은 CreateModel, nullable AddField,
reverse, recorder 상태와 atomic failure 후 connection 회복을 외부 DB 상태로 고정합니다.

반면 public migration file 형식, Go callback ABI, process 간 lock까지 한 번에 정하면 아직
검증하지 않은 배포 형식이 core state/executor 설계를 굳힐 위험이 있습니다.

## 결정 기준

- Schema IR이 migration state의 의미 원본이어야 함
- historical state가 현재 generated model에 의존하지 않아야 함
- operation forward/backward와 recorder 갱신이 한 transaction 의미를 가져야 함
- backend별 DDL 차이가 schema editor/capability 경계 밖으로 새지 않아야 함
- 실패가 조용히 무시되거나 적용 기록만 남는 false success가 없어야 함

## 고려한 선택지

### public migration file과 CLI부터 확정

사용자 흐름은 빨리 보이지만 serialization version, callback import, library/CLI version과
lock 정책을 증거 없이 freeze합니다.

### third-party migration engine을 실행 core로 감싸기

초기 구현량은 줄지만 GoDj Schema IR, historical state와 Django 호환 오류/rollback 의미가
외부 도구의 모델에 종속됩니다.

### 내부 state/operation/executor 수직 단면부터 구현

작은 in-memory plan과 SQLite recorder로 MIG 계약을 통과한 뒤 serialization과 CLI를
추가합니다. 사용자 명령은 늦어지지만 핵심 transaction과 recovery 경계를 먼저 검증할 수
있습니다.

## 결정

M2 첫 product 단면은 다음 경계를 갖습니다.

```text
versioned ProjectState derived from Schema IR
→ typed Migration Operations
→ Migration Executor
→ backend Schema Editor + capability
→ Migration Recorder
```

- 첫 operation은 `CreateModel`, nullable `AddField`와 그 역방향으로 제한합니다.
- executor는 operation과 recorder 갱신을 backend가 지원하는 atomic DDL 경계 안에서
  처리하고, 실패 시 구조화된 error와 회복 가능한 connection을 반환합니다.
- recorder의 계약 key는 app/name이며 비결정적인 적용 timestamp는 differential 비교에서
  제외합니다.
- historical state는 현재 generated model package를 import하지 않습니다.
- unsupported reverse/capability는 명시적 오류이며 silent no-op이 아닙니다.
- public migration file 없이 test/in-memory plan으로 먼저 구현합니다.
- 모든 state transition은 DB I/O 전에 계산하고 검증합니다. State 오류는 transaction을
  시작하지 않으며 commit 성공 뒤에만 새 `ProjectState`를 반환합니다.
- forward operation은 선언 순서, backward operation은 역순으로 실행합니다.
- `migrations/backend`은 `SchemaEditor`, `Recorder`, migration `Transaction`과
  `AtomicBackend` 경계를 소유합니다. `migrations` core는 이 interface와 Schema IR만
  import하고 `db/sqlite`가 같은 `sql.Tx`에 묶인 editor/recorder를 구현합니다.
- operation 실패와 recorder 실패는 원인과 rollback 오류를 보존하는 서로 다른
  구조화된 error code를 사용합니다.
- SQLite native `DROP COLUMN`은 nullable·비인덱스·비참조 field에 한해 첫 단면에서
  지원합니다. Index/trigger/view/FK 의존성이 있으면 silent fallback이나 과장된 capability
  대신 구조화된 capability error를 반환합니다. Table rebuild는 후속 범위입니다.

## 결과

MIG-001..MIG-004를 최소 end-to-end 단면으로 구현하면서 file/CLI ABI를 성급하게 고정하지
않습니다. 이후 serialization은 이미 검증된 state/operation 의미를 보존해야 합니다.

Go 1.26.5와 `modernc.org/sqlite v1.56.0` 별도 module spike에서 CreateModel/AddField,
reverse, operation/recorder failure rollback, connection recovery, race와 `CGO_ENABLED=0`을
실행했습니다. Nullable unindexed field의 native DROP COLUMN은 성공했지만 indexed field는
`no such column` schema error로 실패해 위 capability 제한을 결정했습니다.

## 의도적으로 결정하지 않은 것

- public migration file encoding과 generator version upgrade 규칙
- data migration callback ABI와 historical model mutation API
- migration dependency graph merge/squash/optimizer
- multi-process/database lock와 crash recovery protocol
- PostgreSQL/MySQL/Oracle DDL transaction 차이

이 항목들은 Q-012에 남으며 public `godj makemigrations/migrate` 전에 별도 Accepted ADR이
필요합니다.

## 검증

- state preflight가 실패할 때 transaction I/O가 0인지 확인
- DDL과 recorder가 동일 transaction object만 사용하는지 확인
- operation/recorder/reverse-recorder failure에서 schema와 record가 함께 복원되는지 확인
- state/operation forward-backward unit/property test
- recorder와 DDL failure fault-injection Go-native test
- SQLite integration에서 MIG-001..MIG-004 differential comparison
- failure 뒤 connection query/새 transaction과 relevant race test
- migration package가 generated model package를 import하지 않는 dependency gate
