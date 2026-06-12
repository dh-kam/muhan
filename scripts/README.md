# scripts/ — 운영 스크립트

서버 운영, 데이터 마이그레이션, 테스트에 사용하는 스크립트 모음입니다.

## 스크립트 목록

### 서버 운영

| 스크립트 | 설명 |
|----------|------|
| `backup.sh` | 게임 데이터 자동 백업 스크립트. Docker Compose의 `backup` 프로필에서 크론으로 실행됩니다. `player/`, `board/`, `room/` 등을 tar.gz로 압축하여 보관하며, `BACKUP_KEEP` 일수만큼 유지합니다. |
| `db-smoke-postgres.sh` | PostgreSQL DB 스모크 테스트. Docker Compose로 PostgreSQL을 시작하고, `muhan-dbimport`로 데이터를 임포트한 후, 기본 쿼리로 검증합니다. |

### UTF-8 마이그레이션

| 스크립트 | 설명 |
|----------|------|
| `utf8-audit.sh` | 레거시 EUC-KR 인코딩 파일 감사. 파일명과 내용의 인코딩을 분석합니다. |
| `utf8-convert-text.sh` | 텍스트 파일 내용을 EUC-KR에서 UTF-8로 일괄 변환합니다. |
| `utf8-rename-paths.sh` | EUC-KR로 인코딩된 파일명을 UTF-8로 일괄 변경합니다. |

### 테스트 및 검증

| 스크립트 | 설명 |
|----------|------|
| `porting-smoke.sh` | C → Go 포팅 스모크 테스트 스크립트 (93KB). 포팅된 기능의 동작을 포괄적으로 검증합니다. |

### 기타

| 스크립트 | 설명 |
|----------|------|
| `replace_log.py` | `log.Printf` → `slog` 구조화 로깅으로의 자동 변환 스크립트. |
