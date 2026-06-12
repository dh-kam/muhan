# =============================================================================
#  Muhan MUD Server - Multi-stage Docker Build
#  Stage 1: Go 게임 서버 빌드
#  Stage 2: Node.js 웹 UI 빌드
#  Stage 3: 최종 런타임 이미지 (최소 크기)
# =============================================================================

# ─── Stage 1: Go 서버 빌드 ──────────────────────────────────────────────────
FROM golang:1.25-alpine AS go-builder

WORKDIR /build

# 캐시 효율을 위해 의존성 파일만 먼저 복사
COPY go.mod go.sum ./
RUN go mod download

# 버전 정보 (빌드 시 --build-arg로 주입 가능)
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

# 소스 전체 복사 및 빌드
COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildDate=${BUILD_DATE}" \
    -o /muhan-server \
    ./cmd/muhan-server/

# ─── Stage 2: Web UI 빌드 ──────────────────────────────────────────────────
FROM node:22-alpine AS webui-builder

WORKDIR /build

COPY webui/package.json webui/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY webui/ ./
RUN npm run build

# ─── Stage 3: 최종 런타임 이미지 ────────────────────────────────────────────
FROM alpine:3.21

# 런타임 필수 패키지만 설치
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    tini

# 비-root 사용자 생성 (호스트 UID/GID와 일치시켜 볼륨 권한 문제 방지)
ARG UID=1000
ARG GID=1000
RUN addgroup -g ${GID} -S muhan && adduser -u ${UID} -G muhan -S muhan

# 애플리케이션 디렉토리
WORKDIR /app

# Go 서버 바이너리
COPY --from=go-builder /muhan-server /app/muhan-server

# Web UI 정적 파일 (nginx 없이 내장 서빙 또는 별도 서비스)
COPY --from=webui-builder /build/dist /app/webui/dist

# 데이터 디렉토리 마운트 포인트 생성
# 실제 데이터는 Docker 볼륨으로 마운트됩니다.
RUN mkdir -p /data && chown -R muhan:muhan /data /app

# 포트 노출
#   4000 - TCP 게임 서버 (telnet 접속)
#   4041 - WebSocket 프록시 (웹 클라이언트 → 게임 서버)
EXPOSE 4000 4041

# tini를 PID 1으로 사용 (좀비 프로세스 방지, 시그널 전달)
ENTRYPOINT ["/sbin/tini", "--"]

USER muhan

# 기본 실행 명령
# -root /data : 게임 데이터 루트 (볼륨 마운트 포인트)
# -listen :4000 : TCP 서버 포트
# -ws-listen 0.0.0.0:4041 : WebSocket 프록시 (모든 인터페이스)
CMD ["/app/muhan-server", \
     "-root", "/data", \
     "-listen", ":4000", \
     "-ws-listen", "0.0.0.0:4041", \
     "-ansi"]
