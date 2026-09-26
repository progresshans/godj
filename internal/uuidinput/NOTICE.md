# UUID 입력 기준

UUID string/integer 입력 의미는 고정 Python 3.14.3 `uuid.UUID`와 Django 6.1·DRF 3.18.0의 public API 관찰을 따른다.
Python은 PSF-2.0, Django와 Django REST Framework는 BSD-3-Clause 참조다.
Python UUID의 문자 전처리와 integer 수용/거부 의미를 독립 Go 구현으로 표현했으며 upstream 소스 파일을 포함하지 않는다.

독립 실행과 원본은 [runner](../../conformance/runners/django/uuid_reference.py),
[raw](../uuidtest/testdata/django61.json), [검증 기록](../../docs/status/TEST_EVIDENCE.md)에 있다.
Unicode decimal digit는 고정 Python 3.14의 Unicode 16.0 범위 76개를 사용한다. 독립 runner가 760개 문자를 model/Form/serializer에
각각 전달해 값을 관찰한다. Go 표준 라이브러리의 Unicode 버전이 달라도 입력 범위를 바꾸지 않는다.
Python 3.12/3.13의 Unicode 15에는 새 범위 8개가 없으며 reference test에서 그 차이를 명시적으로 대조한다.
UUID generation·Python 객체 내부 구조의 호환은 이 패키지 범위가 아니다.
