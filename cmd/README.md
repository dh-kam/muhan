# cmd/ — CLI 실행 도구

이 디렉터리는 `go build`로 개별 실행 가능한 바이너리를 생성하는 22개의 CLI 도구를 포함합니다.

## 주 서버

| 도구 | 설명 |
|------|------|
| **muhan-server** | 게임 서버 본체. TCP 텔넷 + WebSocket 접속을 처리하며, 월드 데이터를 로드하고 게임 루프를 실행합니다. |
| **muhan-client** | 간단한 텔넷 게임 클라이언트. 터미널에서 직접 게임 서버에 접속할 수 있습니다. |

## 데이터 분석 도구

| 도구 | 설명 |
|------|------|
| **muhan-worldload** | 전체 월드 데이터(방, 몬스터, 오브젝트, 플레이어)를 로드하고 통계를 요약합니다. |
| **muhan-inspect** | 데이터 루트의 파일 현황(프로토타입 수, 방 수 등)을 조사합니다. |
| **muhan-dataissues** | 데이터 무결성 문제점(깨진 참조, 누락 파일 등)을 스캔합니다. |
| **muhan-repairplan** | 발견된 데이터 문제에 대한 복구 계획을 생성합니다. |
| **muhan-protoaudit** | 오브젝트/몬스터 프로토타입의 상세 감사 리포트를 생성합니다. |

## 매핑/파싱 도구

레거시 C 바이너리 데이터를 파싱하여 Go 구조체로 매핑하고 검증합니다.

| 도구 | 설명 |
|------|------|
| **muhan-roommap** | `rooms/` 디렉터리의 방 바이너리 파일을 파싱하여 매핑합니다. |
| **muhan-playermap** | `player/` 디렉터리의 플레이어 바이너리 파일을 매핑합니다. |
| **muhan-objectmap** | 오브젝트 트리(인벤토리, 컨테이너)를 바이너리에서 파싱합니다. |
| **muhan-protomap** | `objmon/` 디렉터리의 프로토타입을 매핑합니다. |
| **muhan-bankmap** | 은행 데이터를 파싱하여 매핑합니다. |
| **muhan-boardmap** | 게시판 데이터를 파싱하여 매핑합니다. |
| **muhan-cmdlist** | 레거시 C 소스(`src/global.c`)에서 명령어 목록을 추출합니다. |
| **muhan-resolve** | 한국어 명령어 입력을 파싱하여 대응하는 핸들러를 매칭합니다. |

## 마이그레이션 도구

| 도구 | 설명 |
|------|------|
| **muhan-migrate** | 레거시 데이터에서 마이그레이션 매니페스트를 생성합니다. |
| **muhan-sidecarmigrate** | JSON 사이드카 파일의 스키마를 최신 버전으로 마이그레이션합니다. |
| **muhan-dbimport** | 월드 데이터를 PostgreSQL 데이터베이스에 임포트합니다. |
| **muhan-dbschema** | PostgreSQL DDL 스키마를 생성합니다. |
| **muhan-snapshot** | 전체 월드 상태를 JSONL(JSON Lines) 파일로 스냅샷합니다. |
| **muhan-bundle** | 번들 매니페스트를 생성합니다. |
| **muhan-jsonstore** | JSON 스토어 데이터를 내보냅니다. |

## 빌드 방법

```bash
# 전체 빌드
go build ./cmd/...

# 개별 빌드
go build -o muhan-server ./cmd/muhan-server/
go build -o muhan-worldload ./cmd/muhan-worldload/

# 개별 실행 (빌드 없이)
go run ./cmd/muhan-roommap/ -root /path/to/data -json
```

## 공통 플래그

대부분의 도구는 다음 플래그를 공유합니다:

- `-root <경로>`: 레거시 Muhan 데이터 루트 디렉터리 (기본값: `.`)
- `-json`: 출력을 JSON 형식으로 변경
- `-max-findings <N>`: 텍스트 모드에서 최대 출력 항목 수 (기본값: `30`)
