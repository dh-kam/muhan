# webui/ — 웹 클라이언트 (Vue.js)

Vue.js 3 + Vite 기반의 웹 클라이언트입니다.  
WebSocket을 통해 게임 서버에 접속하여 브라우저에서 MUD 게임을 플레이할 수 있습니다.

## 기술 스택

- **프레임워크**: Vue 3.5
- **빌드 도구**: Vite 6.4
- **플러그인**: `@vitejs/plugin-vue`

## 개발 서버 실행

```bash
cd webui
npm install
npm run dev    # http://localhost:5173
```

또는 프로젝트 루트에서 `run.sh`를 실행하면 게임 서버와 함께 자동 실행됩니다.

## 프로덕션 빌드

```bash
cd webui
npm run build  # dist/ 디렉터리에 정적 파일 생성
```

빌드된 `dist/` 파일은 Docker 이미지의 Nginx에서 서빙되거나,  
`compose.yaml`의 `webui` 서비스에서 마운트됩니다.

## 디렉터리 구조

```
webui/
├── index.html          # HTML 엔트리포인트
├── package.json        # npm 의존성
├── package-lock.json   # 의존성 잠금 파일
├── vite.config.js      # Vite 설정
├── public/             # 정적 에셋 (favicon, icons)
├── src/                # Vue 소스 코드
│   ├── main.js         # Vue 앱 엔트리포인트
│   ├── App.vue         # 루트 컴포넌트
│   ├── style.css       # 글로벌 스타일
│   ├── assets/         # 이미지 등 에셋
│   └── components/     # Vue 컴포넌트
└── dist/               # 빌드 산출물 (git 미포함)
```
