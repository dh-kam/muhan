# internal/ — 내부 패키지

외부 모듈에서 임포트할 수 없는 비공개 패키지입니다.  
Go의 `internal/` 규약에 따라 `muhan` 모듈 내에서만 사용됩니다.

## 패키지 구성

### engine/ — 게임 엔진

게임 플레이의 핵심 로직을 담당합니다.

| 패키지 | 설명 |
|--------|------|
| `engine/command` | 커맨드 파싱 및 실행. 이동(`move`), 전투(`fight`), 아이템 조작(`get`, `drop`, `put`, `take`), 상점(`buy`, `sell`), 대화(`say`, `tell`), DM 커맨드 등 |
| `engine/command/table` | 레거시 C 소스(`global.c`)에서 추출한 명령어 테이블을 로드하여 `commandspec.Registry`를 구성 |
| `engine/game` | 게임 루프(`Loop`), NPC 대화(`talk`), 가족 시스템(`family`), 전투 시스템, 영구 사망(`permanent death`), 주문(`spell`) 등 상위 게임 로직 |
| `engine/legacy` | 레거시 C 바이너리와의 호환 레이어. 비트 플래그 해석, 구조체 크기 상수 등 |

### world/ — 가상 세계

게임 세계의 데이터 모델, 로딩, 런타임 상태를 관리합니다.

| 패키지 | 설명 |
|--------|------|
| `world/model` | 핵심 데이터 모델: `Room`, `Creature`, `Player`, `ObjectPrototype`, `ObjectInstance`, `Family` 등 |
| `world/load` | 레거시 바이너리 데이터를 파싱하여 `model` 구조체로 변환. 방(`rooms/`), 몬스터/오브젝트(`objmon/`), 플레이어(`player/`) 로더 |
| `world/state` | 런타임 월드 상태(`World` 구조체). CRUD 연산, 백그라운드 저장 큐(`backgroundSaver`), 방 점유자 추적, 인벤토리 관리 등 |

### persist/ — 데이터 영속성

파일 I/O, 인코딩, 암호화 등 저수준 데이터 처리를 담당합니다.

| 패키지 | 설명 |
|--------|------|
| `persist/cbin` | C 바이너리 구조체 파서. 레거시 데이터의 고정 크기 필드를 Go 구조체로 읽어들임 |
| `persist/jsonstore` | JSON 사이드카 파일 읽기/쓰기. 플레이어, 방 등의 확장 데이터를 JSON으로 관리 |
| `persist/legacycrypt` | 레거시 DES 암호화 호환. 기존 비밀번호를 검증하고 bcrypt로 자동 마이그레이션 |
| `persist/legacykr` | EUC-KR ↔ UTF-8 인코딩 변환. 레거시 한국어 텍스트 처리 |
| `persist/store` | 일반 파일 기반 스토어 인터페이스 |

### session/ — 세션 관리

TCP 텔넷 및 WebSocket 클라이언트 연결을 관리합니다.

- 동시 접속 제한 (글로벌 256, IP당 5)
- Rate Limiting (로그인 실패 시 IP 차단)
- Graceful Shutdown 지원

### migrate/ — 마이그레이션 도구 모음

레거시 데이터를 분석, 매핑, 변환하는 패키지 모음입니다.

| 패키지 | 설명 |
|--------|------|
| `migrate/bankmap` | 은행 바이너리 데이터 매핑 |
| `migrate/boardmap` | 게시판 데이터 매핑 |
| `migrate/bundle` | 번들 매니페스트 생성 |
| `migrate/creaturemap` | 크리처(몬스터) 데이터 매핑 |
| `migrate/dbimport` | PostgreSQL 데이터 임포트 로직 |
| `migrate/dbschema` | PostgreSQL DDL 스키마 생성 |
| `migrate/invitemap` | 초대 데이터 매핑 |
| `migrate/objectmap` | 오브젝트 트리(인벤토리, 컨테이너) 매핑 |
| `migrate/playermap` | 플레이어 바이너리 데이터 매핑 |
| `migrate/protoaudit` | 프로토타입 감사 리포트 |
| `migrate/protomap` | 프로토타입 매핑 |
| `migrate/protoresolve` | 프로토타입 참조 해석 |
| `migrate/roommap` | 방 바이너리 데이터 매핑 |
| `migrate/snapshot` | 월드 JSONL 스냅샷 |

### 기타 패키지

| 패키지 | 설명 |
|--------|------|
| `commandparse` | 한국어 커맨드 파서. "사과 주워", "북쪽 가" 등의 자연어 명령을 토큰으로 분리 |
| `commandspec` | 커맨드 스펙 레지스트리. 명령어 이름 → 핸들러 매핑, 접두어 매칭 지원 |
| `protocol` | ANSI 색상 코드 및 텔넷 프로토콜 처리 |
| `metrics` | Prometheus 메트릭 정의 (`muhan_active_sessions_total`, `muhan_commands_processed_total`, `muhan_login_failures_total`) |
| `report/dataissues` | 데이터 무결성 이슈 스캐너 |
| `report/repairplan` | 데이터 복구 계획 생성기 |
| `krtext` | 한국어 텍스트 유틸리티 (조사 처리, 문자열 정규화 등) |
| `textfmt` | 텍스트 포매팅 유틸리티 (테이블 출력, 줄바꿈 등) |
