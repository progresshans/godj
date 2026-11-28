# Django 관계 표현식 runtime

독립 [observer와 입력](../../runners/django/related_expression_reference/)을 실행하기 위한 고정 환경이다.
`pyproject.toml`과 `uv.lock`을 함께 관리하며 업데이트한 source에서 실제 관찰을 다시 수행한다.
설치 성공이나 과거 출력만으로 현재 Go 구현의 결과를 PASS로 처리하지 않는다.
