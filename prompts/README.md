# Codex 실행 프롬프트

이 디렉터리는 새 대화에서 시작 비용을 줄이는 편의 프롬프트입니다. 설계나 상태의 정본이 아니며, 내용을 길게 복사해 유지하지 않습니다.

Codex는 루트 `AGENTS.md`를 읽도록 구성되어 있으므로 보통 다음 한 문장으로 충분합니다.

> `AGENTS.md`와 `docs/status/CURRENT.md`를 따라 현재 활성 작업을 이어서 수행해줘.

더 엄격한 인수인계가 필요하면 [CONTINUE_WORK.md](CONTINUE_WORK.md)를 사용합니다.
현재 다음 작업은 [START_GDJ_0002.md](START_GDJ_0002.md), 완료된 M0의 역사적
시작 프롬프트는 [START_GDJ_0001.md](START_GDJ_0001.md)입니다.

프롬프트가 가리키는 문서와 실제 checkout이 충돌하면 checkout을 먼저 조사하고 CURRENT/work 문서를 고칩니다. 프롬프트 내용을 사실로 가정하지 않습니다.
