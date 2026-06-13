# Muhan Server (무한 서버)

[![CI/CD](https://github.com/0xc0de1ab/muhan/actions/workflows/ci.yml/badge.svg)](https://github.com/0xc0de1ab/muhan/actions/workflows/ci.yml)

Muhan(무한)은 레거시 C 기반 MUD(Multi-User Dungeon) 게임 서버를 Go 언어로 완전히 재작성한 프로젝트입니다.  
기존의 방대한 바이너리 맵, 플레이어 데이터, 오브젝트/몬스터 데이터를 안정적으로 파싱하고, 최신 동시성 제어, 보안, 모니터링을 탑재한 프로덕션 수준의 게임 서버입니다.

## 📋 목차

- [시작하기](#-시작하기)
- [Docker 빌드 및 실행](#-docker-빌드-및-실행)
- [Docker Compose](#-docker-compose)
- [Persistent Volume 마운트](#-persistent-volume-마운트-데이터-보존)
- [CLI 명령어](#-cli-명령어-muhan-server)
- [CLI 유틸리티 도구](#-cli-유틸리티-도구)
- [프로젝트 구조](#-프로젝트-구조)
- [데이터 디렉터리 구조](#-데이터-디렉터리-구조)
- [보안 및 모니터링](#-보안-및-모니터링)
- [CI/CD 파이프라인](#-cicd-파이프라인)
- [기술 스택](#-기술-스택)

---

## 🚀 시작하기

### 요구 사항

- **Go 1.25+** (서버 빌드)
- **Node.js 22+** (Web UI 빌드, 선택적)
- **Docker / Docker Compose** (컨테이너 실행, 선택적)

### 로컬 빌드 및 실행

```bash
# 전체 패키지 빌드
go build ./...

# 전체 테스트 실행
go test ./...

# 서버 빌드 및 실행
go build -o muhan-server ./cmd/muhan-server/
./muhan-server -root . -listen :4000 -ws-listen 0.0.0.0:4041 -ansi
```

### run.sh 스크립트 (개발용)

Go 게임 서버와 Vite Web UI 개발 서버를 동시에 실행합니다.

```bash
# 기본 실행 (TCP :4000, WS :4041, WebUI :5173)
./run.sh

# 포트 커스터마이즈
TCP_PORT=5000 WS_PORT=5041 WEBUI_PORT=3000 ./run.sh

# 다른 데이터 경로 지정
DATA_ROOT=/path/to/gamedata ./run.sh
```

접속 정보:
- **게임 서버**: `telnet localhost:4000`
- **웹소켓**: `ws://localhost:4041`
- **웹 클라이언트**: `http://localhost:5173`

---

## 🐳 Docker 빌드 및 실행

### Dockerfile (멀티스테이지)

3단계 빌드: Go 서버 → Web UI (Vite/Vue) → 최종 Alpine 런타임 이미지

```bash
# Docker 이미지 빌드
docker build -t muhan-server:latest .

# 컨테이너 실행
docker run -d --name muhan \
  -p 4000:4000 \
  -p 4041:4041 \
  -v /srv/muhan-data:/data \
  muhan-server:latest
```

### 빌드 인자 (ARG)

| 인자 | 기본값 | 설명 |
|------|--------|------|
| `UID` | `1000` | 컨테이너 내 사용자 UID |
| `GID` | `1000` | 컨테이너 내 그룹 GID |

```bash
# 호스트 UID/GID에 맞추어 빌드
docker build --build-arg UID=$(id -u) --build-arg GID=$(id -g) -t muhan-server:latest .
```

### 컨테이너 포트

| 포트 | 용도 |
|------|------|
| `4000` | TCP 게임 서버 (텔넷 접속) |
| `4041` | WebSocket 프록시 (웹 클라이언트) |

---

## 🐙 Docker Compose

`compose.yaml`로 전체 서비스를 한 번에 관리합니다.

```bash
# 기본 실행 (게임 서버 + 웹 UI)
docker compose up -d muhan webui

# 전체 실행 (게임 서버 + 웹 UI + PostgreSQL)
docker compose --profile db up -d

# 백업 크론 포함 실행
docker compose --profile backup up -d

# 다른 데이터 경로 지정
MUHAN_DATA_DIR=/path/to/gamedata docker compose up -d
```

### 서비스 구성

| 서비스 | 이미지 | 포트 | 설명 |
|--------|--------|------|------|
| `muhan` | 자체 빌드 | `4000`, `4041` | Go 게임 서버 (TCP + WebSocket) |
| `webui` | `nginx:alpine` | `8080` | Web UI 정적 파일 서빙 |
| `postgres` | `postgres:16-alpine` | `55432` | DB Import 용 (프로필: `db`) |
| `backup` | `alpine:3.21` | - | 자동 백업 크론 (프로필: `backup`) |

### 환경 변수 (.env)

```bash
# .env.example을 복사하여 설정
cp .env.example .env
```

| 변수 | 기본값 | 설명 |
|------|--------|------|
| `MUHAN_DATA_DIR` | `.` | 게임 데이터 루트 경로 |
| `MUHAN_UID` / `MUHAN_GID` | `1000` | 컨테이너 사용자 UID/GID |
| `MUHAN_TCP_PORT` | `4000` | TCP 게임 서버 포트 |
| `MUHAN_WS_PORT` | `4041` | WebSocket 프록시 포트 |
| `MUHAN_WEBUI_PORT` | `8080` | Web UI (Nginx) 포트 |
| `TZ` | `Asia/Seoul` | 타임존 |
| `MUHAN_POSTGRES_DB` | `muhan` | PostgreSQL DB명 |
| `MUHAN_POSTGRES_USER` | `muhan` | PostgreSQL 사용자명 |
| `MUHAN_POSTGRES_PASSWORD` | `muhan_dev_password` | PostgreSQL 비밀번호 |
| `MUHAN_POSTGRES_PORT` | `55432` | PostgreSQL 호스트 포트 |

---

## 💾 Persistent Volume 마운트 (데이터 보존)

게임 데이터는 반드시 Persistent Volume으로 마운트하여 보존해야 합니다.  
`-v [호스트경로]:/data` 형태로 마운트합니다.

```bash
# 호스트의 /srv/muhan-data를 컨테이너의 /data에 마운트
docker run -d --name muhan \
  -p 4000:4000 \
  -p 4041:4041 \
  -v /srv/muhan-data:/data:rw \
  muhan-server:latest

# 백업 볼륨 추가
docker run -d --name muhan \
  -p 4000:4000 \
  -v /srv/muhan-data:/data:rw \
  -v /srv/muhan-backups:/data/backups:rw \
  muhan-server:latest
```

---

## ⚙️ CLI 명령어 (muhan-server)

서버 실행 시 다양한 플래그로 동작을 제어할 수 있습니다.

| 플래그 | 기본값 | 설명 |
|--------|--------|------|
| `-root` | `.` | 레거시 Muhan 소스/데이터가 위치한 루트 경로 |
| `-source-root` | (없음) | `-root`를 오버라이드하는 소스/데이터 루트 경로 |
| `-listen` | `:4000` | 텔넷 클라이언트가 접속할 TCP 포트 |
| `-ws-listen` | `127.0.0.1:4041` | 웹소켓(WebUI) 클라이언트가 접속할 포트 |
| `-metrics-listen` | `:2112` | Prometheus 메트릭 수집을 위한 HTTP 포트 |
| `-actor` | (없음) | 임시 actor 플레이어 ID |
| `-ansi` | `true` | 클라이언트에 ANSI 색상 코드 전송 여부 |
| `-validate` | `false` | 서버를 실행하지 않고 런타임 입력값과 맵 유효성만 검증 후 종료 |
| `-dry-run` | `false` | 유효성 검증 후 리스닝 없이 종료 |
| `-migrate-sidecars` | `false` | 시작 전 기존 JSON 사이드카 스키마를 최신으로 마이그레이션 |

### 실행 예시

```bash
# 기본 실행
go run ./cmd/muhan-server/

# 프로덕션 실행 (외부 데이터, 모든 포트 지정)
./muhan-server -root=/data/muhan -listen=:4000 -ws-listen=0.0.0.0:4041 -metrics-listen=:2112

# 유효성 검증만 수행
./muhan-server -root=/data/muhan -validate

# Dry-run (데이터 로드 + 검증, 서버 미시작)
./muhan-server -root=/data/muhan -dry-run

# 사이드카 마이그레이션 후 실행
./muhan-server -root=/data/muhan -migrate-sidecars
```

---

## 🔧 CLI 유틸리티 도구

`cmd/` 하위에 다양한 데이터 분석/마이그레이션/디버깅 유틸리티가 포함되어 있습니다.

### 데이터 분석 도구

| 도구 | 용도 | 주요 플래그 |
|------|------|-------------|
| `muhan-worldload` | 전체 월드 데이터를 로드하고 요약 | `-root`, `-json`, `-max-findings` |
| `muhan-inspect` | 데이터 루트의 파일 현황을 조사 | `-root`, `-json`, `-max-errors` |
| `muhan-dataissues` | 데이터 무결성 문제점을 스캔 | `-root`, `-json` |
| `muhan-repairplan` | 데이터 복구 계획을 생성 | `-root`, `-json` |
| `muhan-protoaudit` | 오브젝트/몬스터 프로토타입 감사 | `-root`, `-outdir`, `-json` |

### 마이그레이션 도구

| 도구 | 용도 | 주요 플래그 |
|------|------|-------------|
| `muhan-migrate` | 마이그레이션 매니페스트 생성 | `-root`, `-out` |
| `muhan-sidecarmigrate` | JSON 사이드카 스키마 마이그레이션 | (내장 플래그) |
| `muhan-dbimport` | 월드 데이터를 PostgreSQL에 임포트 | (내장 플래그) |
| `muhan-dbschema` | DB 스키마 DDL 생성 | `-dialect`, `-outdir`, `-json`, `-sql`, `-target-schema` |
| `muhan-snapshot` | 전체 월드를 JSONL로 스냅샷 | `-root`, `-out` |
| `muhan-bundle` | 번들 매니페스트 생성 | `-root`, `-out` |

### 매핑/파싱 도구

| 도구 | 용도 | 주요 플래그 |
|------|------|-------------|
| `muhan-roommap` | 방(room) 바이너리 파일 매핑/요약 | `-root`, `-json` |
| `muhan-playermap` | 플레이어 바이너리 파일 매핑/요약 | `-root`, `-json`, `-max-findings` |
| `muhan-objectmap` | 오브젝트 트리 바이너리 파싱 | `-file`, `-root`, `-json`, `-strict` |
| `muhan-protomap` | 오브젝트/몬스터 프로토타입 매핑 | `-root`, `-json` |
| `muhan-bankmap` | 은행 데이터 매핑/분석 | `-root`, `-json`, `-max-findings` |
| `muhan-boardmap` | 게시판 데이터 매핑/분석 | `-root`, `-json`, `-max-findings` |
| `muhan-cmdlist` | 레거시 C 소스에서 명령어 목록 추출 | `-root`, `-json` |
| `muhan-resolve` | 명령어 입력을 파싱하여 핸들러 매칭 | `-root`, `-deny-privileged` |

### 기타

| 도구 | 용도 |
|------|------|
| `muhan-client` | 간단한 텔넷 게임 클라이언트 |
| `muhan-jsonstore` | JSON 스토어 내보내기 유틸리티 |

### 공통 사용법

대부분의 도구는 `-root`로 데이터 경로를, `-json`으로 JSON 출력을 지정합니다.

```bash
# 월드 데이터 로드 및 요약 (텍스트)
go run ./cmd/muhan-worldload/ -root /path/to/data

# 방 매핑 결과를 JSON으로 출력
go run ./cmd/muhan-roommap/ -root /path/to/data -json

# 오브젝트 트리 파싱
go run ./cmd/muhan-objectmap/ -file player/홍길동 -root /path/to/data -json

# DB 스키마를 PostgreSQL DDL로 출력
go run ./cmd/muhan-dbschema/ -sql -target-schema muhan_import
```

---

## 🗂 프로젝트 구조

```
muhan/
├── cmd/                        # 실행 가능한 CLI 도구 (22개)
│   ├── muhan-server/           #   주 게임 서버 (TCP + WebSocket)
│   ├── muhan-client/           #   텔넷 클라이언트
│   ├── muhan-worldload/        #   전체 월드 데이터 로더/검증
│   ├── muhan-dbimport/         #   PostgreSQL 임포트
│   ├── muhan-migrate/          #   마이그레이션 매니페스트
│   └── ...                     #   기타 분석/매핑/디버깅 도구
│
├── internal/                   # 비공개 내부 패키지
│   ├── engine/                 #   게임 엔진
│   │   ├── command/            #     커맨드 파싱 및 실행 (이동, 전투, 아이템 등)
│   │   ├── command/table/      #     레거시 C 명령어 테이블 로더
│   │   ├── game/               #     게임 루프, 대화, 가족, 전투, NPC AI
│   │   └── legacy/             #     레거시 바이너리 호환 레이어
│   ├── world/                  #   가상 세계
│   │   ├── model/              #     데이터 모델 (Room, Creature, Player, Object 등)
│   │   ├── load/               #     월드 데이터 파서/로더
│   │   └── state/              #     런타임 월드 상태 관리 (CRUD, 저장 큐)
│   ├── persist/                #   데이터 영속성
│   │   ├── cbin/               #     C 바이너리 구조체 파서
│   │   ├── jsonstore/          #     JSON 사이드카 스토어
│   │   ├── legacycrypt/        #     레거시 DES 암호화 호환
│   │   ├── legacykr/           #     EUC-KR ↔ UTF-8 인코딩
│   │   └── store/              #     일반 파일 스토어
│   ├── session/                #   TCP/WebSocket 세션 관리
│   ├── protocol/               #   ANSI 색상 및 텔넷 프로토콜
│   ├── commandparse/           #   한국어 커맨드 파서
│   ├── commandspec/            #   커맨드 스펙 레지스트리
│   ├── metrics/                #   Prometheus 메트릭 정의
│   ├── migrate/                #   데이터 마이그레이션 도구 모음
│   │   ├── bankmap/            #     은행 데이터 매핑
│   │   ├── boardmap/           #     게시판 데이터 매핑
│   │   ├── bundle/             #     번들 매니페스트
│   │   ├── creaturemap/        #     크리처 매핑
│   │   ├── dbimport/           #     PostgreSQL 임포트 로직
│   │   ├── dbschema/           #     DB 스키마 생성
│   │   ├── objectmap/          #     오브젝트 트리 매핑
│   │   ├── playermap/          #     플레이어 매핑
│   │   ├── protoaudit/         #     프로토타입 감사
│   │   ├── protomap/           #     프로토타입 매핑
│   │   ├── protoresolve/       #     프로토타입 해석
│   │   ├── roommap/            #     방 매핑
│   │   ├── snapshot/           #     월드 스냅샷
│   │   └── invitemap/          #     초대 매핑
│   ├── report/                 #   데이터 이슈 리포트
│   ├── krtext/                 #   한국어 텍스트 처리
│   └── textfmt/                #   텍스트 포매팅
│
├── webui/                      # Vue.js 웹 클라이언트 (Vite)
├── docker/                     # Nginx 설정 등
├── scripts/                    # 백업 등 운영 스크립트
├── docs/                       # 레거시 문서 (DM 커맨드, 방/오브젝트/몬스터 플래그 등)
├── objmon/                     # 오브젝트/몬스터 정의 데이터
├── help/                       # 인게임 도움말 파일
├── Dockerfile                  # 멀티스테이지 Docker 빌드
├── compose.yaml                # Docker Compose 구성
├── run.sh                      # 개발용 병렬 실행 스크립트
├── .env.example                # 환경 변수 템플릿
└── .github/workflows/ci.yml    # CI/CD 파이프라인
```

---

## 📁 데이터 디렉터리 구조

서버가 `-root`로 참조하는 데이터 디렉터리의 구성:

```
/data (또는 프로젝트 루트)
├── player/                # 플레이어 캐릭터 (레거시 바이너리)
│   ├── json/              #   플레이어 JSON 사이드카 (런타임 저장)
│   ├── bank/              #   은행 데이터
│   └── alias/             #   플레이어 명령어 별칭
├── rooms/                 # 방 정의 데이터 (r00~r10, 레거시 바이너리)
├── room/                  # 방 런타임 상태
│   └── json/              #   바닥 아이템, 시체 등 (JSON 사이드카)
├── objmon/                # 오브젝트/몬스터 정의
│   ├── m00~m10/           #   몬스터 프로토타입 (바이너리)
│   ├── o00~o10/           #   오브젝트 프로토타입 (바이너리)
│   ├── talk/              #   NPC 대화 스크립트
│   ├── ddesc/             #   상세 설명 텍스트
│   └── rndtalk/           #   랜덤 대화 텍스트
├── board/                 # 게시판 데이터
├── post/                  # 게시글 원본 데이터
├── help/                  # 인게임 도움말 파일
├── log/                   # 서버 로그
└── src/                   # 레거시 C 소스 (포팅 참조용, git 미포함)
    └── global.c           #   명령어 테이블 원본
```

---

## 🔒 보안 및 모니터링

### 보안 기능

- **동시 접속 제한**: 글로벌 256명, 단일 IP당 최대 5개 커넥션으로 DDoS 방지
- **Rate Limiting**: 5회 이상 로그인 실패 시 IP 5분 차단
- **최신 암호화**: 레거시 DES 암호를 `bcrypt`로 자동 마이그레이션 (`golang.org/x/crypto/bcrypt`)
- **Graceful Shutdown**: SIGINT/SIGTERM 수신 시 활성 세션 안전 종료

### 모니터링

- **구조화 로깅**: `log/slog` JSON 핸들러를 통한 구조화 로깅
- **Prometheus 메트릭**: `:2112/metrics` 엔드포인트
  - `muhan_active_sessions_total` (Gauge): 현재 활성 세션 수
  - `muhan_commands_processed_total` (Counter): 처리된 커맨드 수
  - `muhan_login_failures_total` (Counter): 로그인 실패 횟수

---

## 🔄 CI/CD 파이프라인

GitHub Actions를 통해 모든 push/PR에서 자동 실행됩니다.  
수동 트리거도 가능합니다 (Actions 탭 → Run workflow).

| 단계 | 내용 |
|------|------|
| **Lint** | `go vet ./...` 정적 분석 |
| **Test** | `go test ./... -timeout 300s` 유닛 테스트 |
| **Build** | `go build ./...` 바이너리 빌드 검증 |
| **Docker Build** | Docker 이미지 빌드 및 GHCR(GitHub Container Registry) 푸시 |

---

## 🛠 기술 스택

| 분류 | 기술 |
|------|------|
| **서버** | Go 1.25, `net` (TCP), `golang.org/x/net/websocket` |
| **웹 UI** | Vue.js 3, Vite, Nginx |
| **데이터** | 레거시 C 바이너리 파싱, JSON 사이드카, PostgreSQL (선택적) |
| **보안** | `golang.org/x/crypto/bcrypt`, Rate Limiting |
| **모니터링** | `log/slog` (JSON), Prometheus (`client_golang`) |
| **CI/CD** | GitHub Actions, Docker (멀티스테이지), GHCR |
| **인코딩** | EUC-KR ↔ UTF-8 (`golang.org/x/text`) |
| **DB** | PostgreSQL 16 (`pgx/v5`) |
