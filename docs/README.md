# docs/ — 레거시 문서

이 디렉터리는 원본 레거시 C MUD(Mordor 기반) 서버의 문서 파일들을 보존하고 있습니다.  
Go 포팅 시 참조 자료로 활용되며, DM(Dungeon Master) 커맨드, 엔티티 플래그, 빌드 가이드 등을 포함합니다.

## 문서 목록

### DM (운영자) 가이드
| 파일 | 설명 |
|------|------|
| `dm.doc` | DM 커맨드 전체 레퍼런스 (25KB) |
| `dm_intro` | DM 시스템 소개 |
| `dm_cmnd` | DM 커맨드 요약 목록 |

### 엔티티 플래그 정의
| 파일 | 설명 |
|------|------|
| `crt_flag` | 크리처(몬스터) 플래그 비트 정의 (MAGGRE, MPERMT, MGUARD 등) |
| `obj_flag` | 오브젝트 플래그 비트 정의 (ORENCH, OCONTN 등) |
| `rom_flag` | 방 플래그 비트 정의 (RDARK, RSHOP, RTRAP 등) |
| `rom_xflg` | 방 확장 플래그 정의 |

### 빌드 가이드
| 파일 | 설명 |
|------|------|
| `crt_make` | 크리처(몬스터) 생성 가이드 |
| `obj_make` | 오브젝트 생성 가이드 |
| `rom_make` | 방 생성 가이드 |

### 기타 참조
| 파일 | 설명 |
|------|------|
| `crt_talk` | NPC 대화 시스템 설명 (talk/ 디렉터리 구조) |
| `crt_expg` | 크리처 경험치 테이블 |
| `board_make` | 게시판 생성 가이드 |
| `rom_comb` | 방 조합(combination) 시스템 |
| `rom_stor` | 방 저장(storage) 시스템 |
| `cmd_list` | 커맨드 목록 |
| `cmd_ital` | 이탈리아어 커맨드 참조 |
| `cmd_tdel` | 커맨드 삭제/제거 참조 |
| `README` | DM 도움말 시스템 개요 |

### 포팅 관련 문서
| 파일 | 설명 |
|------|------|
| `porting-smoke-checklist.md` | C → Go 포팅 스모크 테스트 체크리스트 (530줄) |
| `utf8-migration-plan.md` | EUC-KR → UTF-8 마이그레이션 전략 |
