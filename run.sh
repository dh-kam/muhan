#!/bin/bash
# =============================================================================
#  Muhan MUD Server + Web UI 병렬 실행 스크립트
#  - Go 게임 서버 (TCP :4000 + WebSocket :4041)
#  - Vite 개발 서버 (HTTP :5173)
# =============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 기본값
TCP_PORT="${TCP_PORT:-4000}"
WS_PORT="${WS_PORT:-4041}"
WEBUI_PORT="${WEBUI_PORT:-5173}"
DATA_ROOT="${DATA_ROOT:-.}"

# 색상 코드
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

cleanup() {
    echo ""
    echo -e "${YELLOW}[SHUTDOWN]${NC} 서버들을 종료합니다..."
    # 자식 프로세스 그룹 전체 종료
    kill -- -$$ 2>/dev/null || true
    wait 2>/dev/null || true
    echo -e "${GREEN}[DONE]${NC} 모든 서버가 종료되었습니다."
    exit 0
}

trap cleanup SIGINT SIGTERM

echo -e "${CYAN}╔══════════════════════════════════════════════╗${NC}"
echo -e "${CYAN}║     ${GREEN}무한 MUD Server + Web UI Launcher${CYAN}       ║${NC}"
echo -e "${CYAN}╚══════════════════════════════════════════════╝${NC}"
echo ""

# 1) Go 게임 서버 빌드 확인 및 실행
echo -e "${YELLOW}[BUILD]${NC} Go 게임 서버를 빌드합니다..."
go build -o ./muhan-server ./cmd/muhan-server/

echo -e "${GREEN}[START]${NC} Go 게임 서버 시작: TCP=:${TCP_PORT}  WS=:${WS_PORT}"
./muhan-server \
    -root "$DATA_ROOT" \
    -listen ":${TCP_PORT}" \
    -ws-listen "0.0.0.0:${WS_PORT}" \
    -ansi &
GO_PID=$!

# 서버 시작 대기
sleep 1

# 2) Vite 개발 서버 실행
if [ -d "webui" ] && [ -f "webui/package.json" ]; then
    echo -e "${GREEN}[START]${NC} Vite 웹 UI 서버 시작: http://0.0.0.0:${WEBUI_PORT}"
    cd webui
    npx vite --host 0.0.0.0 --port "$WEBUI_PORT" &
    VITE_PID=$!
    cd ..
else
    echo -e "${RED}[WARN]${NC} webui 디렉토리를 찾을 수 없습니다. 웹 UI 없이 실행합니다."
fi

echo ""
echo -e "${CYAN}══════════════════════════════════════════════${NC}"
echo -e "  ${GREEN}게임 서버${NC}:    telnet localhost:${TCP_PORT}"
echo -e "  ${GREEN}웹소켓${NC}:      ws://localhost:${WS_PORT}"
echo -e "  ${GREEN}웹 클라이언트${NC}: http://localhost:${WEBUI_PORT}"
echo -e "${CYAN}══════════════════════════════════════════════${NC}"
echo ""
echo -e "${YELLOW}[INFO]${NC} Ctrl+C로 모든 서버를 종료합니다."
echo ""

# 모든 백그라운드 프로세스 대기
wait
