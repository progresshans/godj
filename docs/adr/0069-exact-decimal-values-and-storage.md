# ADR-0069: Exact Decimal 값과 저장·입력 경계

- 상태: Accepted
- 날짜: 2026-09-20
- 관련 작업: [GDJ-0091](../../work/0091-decimal-cost-models.md)

설계 채택과 제품 구현·환경별 검증의 완료는 구분한다. 현재 진행과 실제 실행은 CURRENT·TEST_EVIDENCE가 소유한다.

## 모델 값과 precision

Decimal은 finite coefficient × 10^exponent를 나타내는 별도 Go 값이다. Coefficient 문자열과 int32 Exponent는 복사 가능한 값이며
big.Int 포인터를 모델/cache에 보관하지 않는다. New·Parse·Canonical은 leading/trailing coefficient zero를 정규화한다. Go zero value는
양수 zero다. 음수 zero는 모델·Form·JSON 입력에서 구분할 수 있지만 두 DB의 numeric zero 저장은 양수 zero로 돌아온다.
일반 오류·잘못된 literal·범위 초과는 error 또는 invalid value로 처리하고 I/O 전 검증에서 거부한다.

모델 값의 significant coefficient는 최대 1000자리, adjusted exponent는 -1000..1000으로 제한한다. 입력 길이·자릿수·exponent를
검사한 뒤에만 확장된 문자열을 만든다. Field는 명시적 max_digits 1..1000, decimal_places 0..max_digits를 선언한다.
현재 DecimalField는 precision 없는 NUMERIC과 negative scale을 지원하지 않으며 이후의 모델/DB 확장 범위로 남긴다.

Field precision은 Schema IR의 Decimal 전용 arm이 소유하고 raw wire·clone·equality·digest·resource accounting에 포함한다.
Default와 Query AST는 canonical Decimal 문자열을 보존한다. Generated model은 Decimal, nullable은 *Decimal이다.
Typed/dynamic query와 mutation은 Decimal 값만 받으며 float64·int64·string을 묵시적으로 model value로 바꾸지 않는다.
비교 literal은 field의 저장 범위 밖일 수 있다. Write는 선언 precision/scale에 정확히 들어가는 값만 받고 초과 자릿수를 반올림하지 않는다.
Numeric equality와 canonical AST identity를 구분하며 ±0의 sign만 바뀐 Form/Helpdesk 입력은 UPDATE를 만들지 않는다.

## 양 DB에서 정확한 저장과 비교

고정 Django SQLite의 NUMERIC affinity는 30자리 Decimal을 다른 정수로 저장하고 최대 경계값의 조회에서 실패했다.
GoDj의 새 Decimal SQLite column은 BLOB이며 field scale과 무관한 canonical numeric order key를 사용한다.
부호 prefix, fixed-width biased adjusted exponent, 정규화 coefficient digits와 terminator 순서로 값을 인코딩한다.
음수는 magnitude bytes를 반전하고 zero는 단일 표현을 사용한다. SQLite의 기본 BLOB 비교로 exact·ordered·IN·F·Min/Max가
같은 숫자 순서를 사용한다. 비교를 위해 float CAST나 process-global collation/function을 등록하지 않는다.
Decoder는 byte 한도·부호·exponent·digits·terminator·canonical 재인코딩을 검사하고 잘못된 physical 값을 거부한다.

PostgreSQL은 native NUMERIC(max_digits, decimal_places)와 pgx numeric binary parameter를 사용한다. Numeric coefficient를 float로
변환하지 않는다. 양 backend는 field write precision을 I/O 전에 검사한다. PostgreSQL의 native rounding에 의존해 초과 scale을 저장하지 않는다.
Raw NUMERIC NaN·Infinity, SQLite의 다른 storage class/잘못된 key와 선언 범위 밖 외부 값을 scanner에서 명시적으로 거부한다.

SQLite의 물리 표현은 Django NUMERIC table과 호환된다고 주장하지 않는다. Physical preflight는 GoDj의 BLOB shape를 확인한다.
기존 Django Decimal table의 채택에는 별도의 명시적 변환이 필요하며, 이미 소실된 값은 복원했다고 주장할 수 없다.
Historical migration은 precision과 physical column 의미를 함께 유지한다. 넓은 precision AlterField/backfill은 별도 작업이다.

## Form·JSON·실제 비용 소비자

Form과 Decimal serializer는 원문의 digits/exponent를 기준으로 precision을 검증한 뒤 canonical 모델 값으로 변환한다.
Trailing zero를 먼저 버려 max_decimal_places 오류를 숨기지 않는다. Unicode decimal digits·underscore·whitespace와 오류 code는
고정 public API 관찰을 따른다. Non-finite는 invalid이고, sNaN의 changed 계산에서 관찰한 Python 예외를 Go panic으로 재현하지 않는다.
Form의 changed는 precision validator를 적용하기 전의 숫자 equality를 사용한다. 따라서 초과 trailing zero는 검증 오류이면서 변경 없음일 수 있다.
Form은 NumberInput과 scale에 맞는 step, 초기값은 선언 scale의 문자열을 사용한다. Invalid bound form은 제출한 문자열을 escape해서 보존한다.

JSON 기본 표현은 선언 scale의 문자열이다. 숫자 입력은 공통 parser가 보존한 lexical token을 직접 Decimal로 해석한다.
기본 Python json.loads의 float 변환 대신 명시적 parse_float=Decimal reference profile과 대조하며 원래 결과도 보존한다.
이 차이는 [DEV-0016](../DEVIATIONS.md#dev-0016--decimal의-정확한-입력저장과-초과-scale-거부)에서 제한된 selector로 관리한다.
Boolean/list/object·non-finite 입력, precision/scale·문자열/문서 자원 한도는 persistence 전에 거부한다.
DRF 입력 문자열은 trim 뒤 1000 rune까지, lexical JSON number는 Python Decimal의 문자열 길이 기준으로 같은 guard를 적용한다.
모델의 1000자리 precision과 이 입력 transport 한도는 별개다. 큰 fixed-scale 출력이 모든 입력 경로에서 왕복한다고 주장하지 않는다.
명시적 Go Decimal 입력은 canonical 모델 값이며 Python Decimal 객체의 원문 scale을 보존하는 값이 아니다.

Helpdesk nullable 예상 비용은 생성·생략 보존·null 비우기·정확한 변경 감지와 rollback/reopen을 실제 앱에서 검증한다.
OpenAPI는 fixed-scale decimal string의 pattern·nullable·default를 같은 field metadata에서 만들고, 별도 고정 client는 문자열로 전달한다.
실제 HTTP·최종 DB와 권한/CSRF·category 격리·실패-before-transaction을 함께 확인한다.

## 출처와 검증

고정 Django 6.1 model/form DecimalField·DecimalValidator·SQLite adapter와 DRF 3.18.0 DecimalField의 public API를 참조한다(BSD-3-Clause).
[독립 runner](../../conformance/runners/django/decimal_reference.py)는 기본 JSON profile과 lexical Decimal profile, 실제 저장 결과를 별도로 기록한다.
PostgreSQL native probe와 SQLite order-key prototype은 설계 판단 자료이며 제품의 전체 실행 증거가 아니다.
공통 값·IR·generator·DB·실제 소비자를 연결한 뒤 영향을 받는 package·DB·race checkpoint를 수행한다.
