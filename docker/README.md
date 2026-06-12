# docker/ — Docker 관련 설정

Docker 컨테이너 실행에 필요한 설정 파일을 포함합니다.

## 파일 목록

| 파일 | 설명 |
|------|------|
| `nginx.conf` | Nginx 리버스 프록시 설정. Web UI 정적 파일을 서빙하고, WebSocket 요청을 게임 서버(`muhan:4041`)로 프록시합니다. |

## Nginx 구성

`compose.yaml`의 `webui` 서비스에서 이 설정을 마운트하여 사용합니다:

```yaml
volumes:
  - ./docker/nginx.conf:/etc/nginx/conf.d/default.conf:ro
```

주요 기능:
- `/` → Web UI 정적 파일 서빙 (`/usr/share/nginx/html`)
- `/ws` → WebSocket 프록시 (`muhan:4041`)
- Gzip 압축, 캐시 헤더 설정
