package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	enginecmd "github.com/0xc0de1ab/muhan/internal/engine/command"
	"github.com/0xc0de1ab/muhan/internal/engine/game"
	"github.com/0xc0de1ab/muhan/internal/krtext"
	"github.com/0xc0de1ab/muhan/internal/persist/legacycrypt"
	"github.com/0xc0de1ab/muhan/internal/persist/legacykr"
	"github.com/0xc0de1ab/muhan/internal/session"
	"github.com/0xc0de1ab/muhan/internal/world/model"
	"github.com/0xc0de1ab/muhan/internal/world/state"
)

const defaultListenAddr = ":4000"

const loginNamePrompt = "\n당신의 이름은 무엇입니까? "

const loginNewsWaitPrompt = "\n[엔터]를 누르십시요."

const legacyCreateNewFamilyBroadcast = "\n### 새로운 무한 가족입니다. 많이 지켜봐 주세요."

const legacyLoginDMClass = 13

const legacyLoginDialinHost = "128.200.142.2"

const legacyLoginGoldCap = 300000000

const legacyLoginGoldCapMessage = "\n\n너무 많은 돈을 가지고 있습니다.\n신이 자기보다 더 많은 돈을 가지고 있다고 하여, \n가지고 있는 돈중에 3억만 남겨놓고, 나머지 부분을\n신이 그냥 가져갑니다. (신 : 재수~~~~ )\n\n"

type serverLoginStep int

type serverLoginState struct {
	step         serverLoginStep
	playerID     model.PlayerID
	failures     int
	sitePassword string
	remoteHost   string
	create       serverLoginCreateState
}

type serverLoginCreateState struct {
	name         string
	male         bool
	class        int
	strength     int
	dexterity    int
	constitution int
	intelligence int
	piety        int
	weapon       int
	chaos        bool
	race         int
}

type loginIPRecord struct {
	failures     int
	lastFailure  time.Time
	blockedUntil time.Time
}

type serverLoginManager struct {
	mu         sync.Mutex
	world      *state.World
	root       string
	sessions   map[session.ID]serverLoginState
	ipFailures map[string]*loginIPRecord // C5: IP별 로그인 실패 추적
	getLoop    func() *game.Loop         // S3: 중복 로그인 확인용
}

func newServerLoginManager(world *state.World, root ...string) *serverLoginManager {
	dbRoot := ""
	if len(root) > 0 {
		dbRoot = root[0]
	}
	return &serverLoginManager{
		world:      world,
		root:       dbRoot,
		sessions:   map[session.ID]serverLoginState{},
		ipFailures: map[string]*loginIPRecord{},
	}
}

func (m *serverLoginManager) Start(id session.ID, remoteHost ...string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[id] = serverLoginState{step: serverLoginName, remoteHost: firstLoginRemoteHost(remoteHost)}
}

func (m *serverLoginManager) StartWithSitePassword(id session.ID, password string, remoteHost ...string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[id] = serverLoginState{step: serverLoginSitePassword, sitePassword: password, remoteHost: firstLoginRemoteHost(remoteHost)}
}

func firstLoginRemoteHost(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func (m *serverLoginManager) cleanupIPFailuresLocked() {
	now := time.Now()
	for ip, rec := range m.ipFailures {
		if now.Sub(rec.lastFailure) > 10*time.Minute && now.After(rec.blockedUntil) {
			delete(m.ipFailures, ip)
		}
	}
}

func (m *serverLoginManager) HandleLine(ctx context.Context, id session.ID, line string) (game.UnauthenticatedLineResult, error) {
	if m == nil {
		return game.UnauthenticatedLineResult{}, errors.New("login manager is nil")
	}
	m.mu.Lock()
	m.cleanupIPFailuresLocked()
	login := m.sessions[id]

	// C5: IP 차단 여부 확인 (비밀번호 단계에서만)
	if login.step == serverLoginPassword && login.remoteHost != "" {
		if rec, ok := m.ipFailures[login.remoteHost]; ok {
			if time.Now().Before(rec.blockedUntil) {
				delete(m.sessions, id)
				m.mu.Unlock()
				return game.UnauthenticatedLineResult{
					Command: session.Command{
						Write: "\r\n로그인 시도가 너무 많습니다. 잠시 후 다시 시도해 주세요.\r\n",
						Close: true,
					},
				}, nil
			}
		}
	}

	switch login.step {
	case serverLoginPassword:
		player, ok := m.world.Player(login.playerID)
		if !ok {
			delete(m.sessions, id)
			m.mu.Unlock()
			return loginCommand("플레이어 정보를 다시 찾을 수 없습니다.\n" + loginNamePrompt), nil
		}
		storedHash := legacyPasswordHash(m.world, player)
		m.mu.Unlock()

		// Perform CPU-intensive password verify outside the lock (W-5)
		verified := legacycrypt.Verify(line, storedHash)
		var newHash string
		var rehashed bool
		if verified && !legacycrypt.IsBcryptHash(storedHash) {
			if nh, err := legacycrypt.HashBcrypt(line); err == nil {
				newHash = nh
				rehashed = true
			}
		}

		m.mu.Lock()
		login, exists := m.sessions[id]
		if !exists || login.step != serverLoginPassword {
			m.mu.Unlock()
			return game.UnauthenticatedLineResult{}, fmt.Errorf("login session expired during verification")
		}
		return m.handlePasswordAfterVerify(id, login, line, verified, rehashed, newHash)

	case serverLoginCreatePassword:
		passwordLen := legacyCreatePasswordByteLen(line)
		if passwordLen > 14 {
			m.mu.Unlock()
			return loginCommand("입력된 암호가 너무 깁니다.\n암호를 다시 넣으십시요(3자이상 14자이하): "), nil
		}
		if passwordLen < 3 {
			m.mu.Unlock()
			return loginCommand("입력된 암호가 너무 짧습니다.\n암호를 다시 넣으십시요(3자이상 14자이하): "), nil
		}
		m.mu.Unlock()

		// Perform CPU-intensive bcrypt generation outside the lock (W-5)
		hash, err := legacycrypt.HashBcrypt(line)
		if err != nil {
			return game.UnauthenticatedLineResult{}, err
		}

		m.mu.Lock()
		login, exists := m.sessions[id]
		if !exists || login.step != serverLoginCreatePassword {
			m.mu.Unlock()
			return game.UnauthenticatedLineResult{}, fmt.Errorf("login session expired during hashing")
		}
		return m.handleCreatePasswordAfterHash(id, login, line, hash)

	default:
		defer m.mu.Unlock()
		switch login.step {
		case serverLoginSitePassword:
			return m.handleSitePassword(id, login, line)
		case serverLoginCreateConfirm:
			return m.handleCreateConfirm(id, login, line)
		case serverLoginCreateEnter:
			return m.handleCreateEnter(id, login, line)
		case serverLoginCreateGender:
			return m.handleCreateGender(id, login, line)
		case serverLoginCreateClass:
			return m.handleCreateClass(id, login, line)
		case serverLoginCreateStats:
			return m.handleCreateStats(id, login, line)
		case serverLoginCreateWeapon:
			return m.handleCreateWeapon(id, login, line)
		case serverLoginCreateAlignment:
			return m.handleCreateAlignment(id, login, line)
		case serverLoginCreateRace:
			return m.handleCreateRace(id, login, line)
		default:
			return m.handleName(id, login, line)
		}
	}
}

func (m *serverLoginManager) handleSitePassword(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	if line != login.sitePassword {
		delete(m.sessions, id)
		return game.UnauthenticatedLineResult{
			Command: session.Command{Write: "\r\nYour site is locked out.\r\n", Close: true},
		}, nil
	}
	m.sessions[id] = serverLoginState{step: serverLoginName, remoteHost: login.remoteHost}
	return loginCommand(loginNamePrompt), nil
}

func (m *serverLoginManager) handleName(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	name := strings.TrimSpace(line)
	if name == "" {
		return loginCommand("이름은 한 자 이상이어야 합니다.\n" + loginNamePrompt), nil
	}

	player, ok := m.world.Player(model.PlayerID(name))
	if !ok {
		if msg := legacyCreateNameRejection(name); msg != "" {
			return loginCommand(msg + loginNamePrompt), nil
		}
		login.create = serverLoginCreateState{name: name}
		login.step = serverLoginCreateConfirm
		m.sessions[id] = login
		return loginCommand(fmt.Sprintf("\n%s%s 하시겠습니까(예/아니오)? ", name, krtext.Particle(name, '4'))), nil
	}
	if legacyPasswordHash(m.world, player) == "" {
		delete(m.sessions, id)
		return loginCommand("저장된 암호 정보를 찾을 수 없습니다.\n" + loginNamePrompt), nil
	}

	m.sessions[id] = serverLoginState{
		step:       serverLoginPassword,
		playerID:   player.ID,
		remoteHost: login.remoteHost,
	}
	return loginCommand("암호를 넣어 주십시요: "), nil
}

func (m *serverLoginManager) handlePasswordAfterVerify(id session.ID, login serverLoginState, line string, verified bool, rehashed bool, newHash string) (game.UnauthenticatedLineResult, error) {
	defer m.mu.Unlock()

	player, ok := m.world.Player(login.playerID)
	if !ok {
		delete(m.sessions, id)
		return loginCommand("플레이어 정보를 다시 찾을 수 없습니다.\n" + loginNamePrompt), nil
	}

	if verified {
		delete(m.sessions, id)
		// C5: 로그인 성공 시 IP 실패 카운터 리셋
		if login.remoteHost != "" {
			delete(m.ipFailures, login.remoteHost)
		}

		// C2: bcrypt가 아니면(DES이면) re-hash 후 저장
		if rehashed {
			_, _ = m.world.SetCreatureProperty(player.CreatureID, "legacyPasswordHash", newHash)
			_ = m.world.SavePlayer(player.ID)
		}

		// S3: 중복 로그인 확인 - 기존 세션 종료
		m.disconnectDuplicateSession(player.ID)
		return m.loginSuccessResult(player, login.remoteHost)
	}

	// C5: 로그인 실패 추적
	if login.remoteHost != "" {
		rec, exists := m.ipFailures[login.remoteHost]
		if !exists {
			rec = &loginIPRecord{}
			m.ipFailures[login.remoteHost] = rec
		}
		rec.failures++
		rec.lastFailure = time.Now()
		if rec.failures >= 5 {
			rec.blockedUntil = time.Now().Add(5 * time.Minute)
			rec.failures = 0 // 차단 후 카운터 리셋
		}
	}

	login.failures++
	if strings.TrimSpace(line) == "" || login.failures >= 3 {
		delete(m.sessions, id)
		return game.UnauthenticatedLineResult{
			Command: session.Command{Write: "\n암호가 틀립니다. 접속을 끊습니다.\n\n", Close: true},
		}, nil
	}
	m.sessions[id] = login
	return loginCommand("\n암호가 틀립니다. 다시 입력하십시요.\n암호를 다시 입력하세요: "), nil
}

func (m *serverLoginManager) disconnectDuplicateSession(playerID model.PlayerID) {
	if m.getLoop == nil {
		return
	}
	loop := m.getLoop()
	if loop == nil {
		return
	}
	for _, active := range loop.ActiveSessions() {
		if strings.EqualFold(active.ActorID, string(playerID)) {
			_ = loop.WriteToSession(active.ID, "\r\n다른 곳에서 접속하여 연결이 끊어집니다.\r\n", false)
			_ = loop.DisconnectSession(active.ID)
		}
	}
}

func (m *serverLoginManager) handleCreateConfirm(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	if !legacyYes(line) {
		login.step = serverLoginName
		login.create = serverLoginCreateState{}
		m.sessions[id] = login
		return loginCommand("당신의 이름은 무엇입니까? "), nil
	}
	login.step = serverLoginCreateEnter
	m.sessions[id] = login
	return loginCommand("\n[엔터]를 누르십시요."), nil
}

func (m *serverLoginManager) handleCreateEnter(id session.ID, login serverLoginState, _ string) (game.UnauthenticatedLineResult, error) {
	login.step = serverLoginCreateGender
	m.sessions[id] = login
	return loginCommand("\n\n당신은 남자입니까, 여자입니까(남자/여자)? "), nil
}

func (m *serverLoginManager) handleCreateGender(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "남"):
		login.create.male = true
	case strings.HasPrefix(line, "여"):
		login.create.male = false
	default:
		m.sessions[id] = login
		return loginCommand("입력이 잘못되었습니다.\n\n당신은 남자입니까, 여자입니까(남자/여자)? "), nil
	}
	login.step = serverLoginCreateClass
	m.sessions[id] = login
	return loginCommand("\n다음과 같은 직업이 있습니다.\n" +
		"1.자  객  2.권법가  3.불제자  4.검  사\n" +
		"5.도술사  6.무  사  7.포  졸  8.도  둑\n" +
		"직업을 고르세요: "), nil
}

func (m *serverLoginManager) handleCreateClass(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	class, ok := legacyCreateClassChoice(line)
	if !ok {
		m.sessions[id] = login
		return loginCommand("직업을 고르세요: "), nil
	}
	login.create.class = class
	login.step = serverLoginCreateStats
	m.sessions[id] = login
	return loginCommand("\n당신은 54점으로 다음 5가지 능력치를 구성할수 있습니다.\n" +
		"5이상 18이하의 수치로 ## ## ## ## ##의 형식으로 5가지 능력치를 적어주십시요.\n" +
		"능력: 힘 민첩 맷집 지식 신앙심\n예: 12 10 12 10 10\n\n" +
		": "), nil
}

func (m *serverLoginManager) handleCreateStats(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	values, err := legacyCreateStatChoices(line)
	if err != nil {
		m.sessions[id] = login
		return loginCommand(err.Error() + "\n: "), nil
	}
	login.create.strength = values[0]
	login.create.dexterity = values[1]
	login.create.constitution = values[2]
	login.create.intelligence = values[3]
	login.create.piety = values[4]
	login.step = serverLoginCreateWeapon
	m.sessions[id] = login
	return loginCommand("\n당신에게 익숙한 무기를 고르십시요.\n" +
		"1.도   2.검   3.봉   4.창   5.궁.\n" +
		": "), nil
}

func (m *serverLoginManager) handleCreateWeapon(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	weapon, ok := legacyCreateDigitChoice(line, 1, 5)
	if !ok {
		m.sessions[id] = login
		return loginCommand("다시 고르세요.\n: "), nil
	}
	login.create.weapon = weapon - 1
	login.step = serverLoginCreateAlignment
	m.sessions[id] = login
	return loginCommand("\n선한 구성원은 다른사람을 공격하지 못하고 공격 받을수도 없으며" +
		"\n그 구성원에게서 물건을 훔칠수도 없습니다." +
		"\n그러나 악한 구성원은 공격할수도 있고 물건을 훔칠수도 있으며" +
		"\n다른 악한 구성원들에게 공격을 받을 수도 있습니다.\n" +
		"\n성향을 고르십시요(선함/악함): "), nil
}

func (m *serverLoginManager) handleCreateAlignment(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "악"):
		login.create.chaos = true
	case strings.HasPrefix(line, "선"):
		login.create.chaos = false
	default:
		m.sessions[id] = login
		return loginCommand("성향을 고르십시요(선함/악함): "), nil
	}
	login.step = serverLoginCreateRace
	m.sessions[id] = login
	return loginCommand("\n다음과 같은 종족들이 있습니다." +
		"\n1.난장이족  2.용 신 족  3.땅귀신족 4.요 괴 족" +
		"\n5.거 인 족  6.토 신 족  7.인 간 족 8.도깨비족" +
		"\n종족을 고르십시요: "), nil
}

func (m *serverLoginManager) handleCreateRace(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	race, ok := legacyCreateRaceChoice(line)
	if !ok {
		m.sessions[id] = login
		return loginCommand("\n종족을 고르십시요: "), nil
	}
	login.create.race = race
	legacyApplyCreateRaceBonus(&login.create)
	login.step = serverLoginCreatePassword
	m.sessions[id] = login
	return loginCommand("\n새 암호를 넣으십시요(3자이상 14자이하): "), nil
}

func (m *serverLoginManager) handleCreatePassword(id session.ID, login serverLoginState, line string) (game.UnauthenticatedLineResult, error) {
	passwordLen := legacyCreatePasswordByteLen(line)
	if passwordLen > 14 {
		m.sessions[id] = login
		return loginCommand("입력된 암호가 너무 깁니다.\n암호를 다시 넣으십시요(3자이상 14자이하): "), nil
	}
	if passwordLen < 3 {
		m.sessions[id] = login
		return loginCommand("입력된 암호가 너무 짧습니다.\n암호를 다시 넣으십시요(3자이상 14자이하): "), nil
	}
	hash, err := legacycrypt.HashBcrypt(line)
	if err != nil {
		return game.UnauthenticatedLineResult{}, err
	}

	m.mu.Lock()
	return m.handleCreatePasswordAfterHash(id, login, line, hash)
}

func (m *serverLoginManager) handleCreatePasswordAfterHash(id session.ID, login serverLoginState, line string, hash string) (game.UnauthenticatedLineResult, error) {
	defer m.mu.Unlock()

	player, creature, err := legacyCreatedPlayerCharacter(login.create, hash)
	if err != nil {
		return game.UnauthenticatedLineResult{}, err
	}
	if err := m.world.CreatePlayerCharacter(player, creature); err != nil {
		return game.UnauthenticatedLineResult{}, err
	}
	if err := m.world.SavePlayer(player.ID); err != nil {
		return game.UnauthenticatedLineResult{}, err
	}
	_ = m.world.BroadcastAll(legacyCreateNewFamilyBroadcast)

	delete(m.sessions, id)
	return m.loginSuccessResultWithExtra(player, login.remoteHost,
		"[환영]이라고 치시면 초보자 분들에게 도움이 되는 많은 정보를 얻을수 있습니다.\n"+
			"레벨이 6이상 되지 않으면 아이디가 삭제됩니다.\n"+
			legacyCreateNewFamilyBroadcast)
}

func loginCommand(write string) game.UnauthenticatedLineResult {
	return game.UnauthenticatedLineResult{Command: session.Command{Write: write}}
}

func legacyCreatePasswordByteLen(password string) int {
	encoded, err := legacykr.EncodeEUCKR(password)
	if err != nil {
		return len([]byte(password))
	}
	return len(encoded)
}

func (m *serverLoginManager) loginSuccessResult(player model.Player, remoteHost string) (game.UnauthenticatedLineResult, error) {
	return m.loginSuccessResultWithExtra(player, remoteHost, "")
}

func (m *serverLoginManager) loginSuccessResultWithExtra(player model.Player, remoteHost string, extra string) (game.UnauthenticatedLineResult, error) {
	steps := m.loginPostLoadSteps(player, remoteHost)
	if extra != "" {
		steps = append(steps, serverLoginPostLoadStep{kind: serverLoginPostLoadStepMessage, content: extra})
	}
	sequence := &serverLoginPostLoadSequence{steps: steps}
	var pending enginecmd.PendingLineHandler
	ctx := &enginecmd.Context{
		ActorID: string(player.ID),
		Values: map[string]any{
			enginecmd.ContextPendingLineKey: func(handler enginecmd.PendingLineHandler) {
				pending = handler
			},
		},
	}
	ctx.WriteString("\n무한에 접속했습니다.\n")
	status, err := sequence.render(ctx)
	if err != nil {
		return game.UnauthenticatedLineResult{}, err
	}
	result := game.UnauthenticatedLineResult{
		ActorID: string(player.ID),
		Command: session.Command{Write: ctx.OutputString()},
	}
	if status == enginecmd.StatusDoPrompt && pending != nil {
		result.Pending = pending
		return result, nil
	}
	result.Command.Prompt = "> "
	return result, nil
}

func (m *serverLoginManager) loginPostLoadSteps(player model.Player, remoteHost string) []serverLoginPostLoadStep {
	root := strings.TrimSpace(m.root)

	creature, hasCreature := model.Creature{}, false
	if m.world != nil && !player.CreatureID.IsZero() {
		creature, hasCreature = m.world.Creature(player.CreatureID)
	}

	steps := []serverLoginPostLoadStep{}
	if root != "" {
		newsName := "news"
		if hasCreature {
			if class, ok := serverCreatureInt(creature, "class"); ok && class == legacyLoginDMClass {
				newsName = "DM_news"
			}
		}
		if strings.TrimSpace(remoteHost) == legacyLoginDialinHost {
			steps = append(steps, m.loginViewFileStep(filepath.Join(root, "help", "dialin"), true, nil))
		}
		steps = append(steps,
			m.loginViewFileStep(filepath.Join(root, "log", newsName), true, nil),
			serverLoginPostLoadStep{kind: serverLoginPostLoadStepWait, prompt: loginNewsWaitPrompt},
		)

		if hasCreature && serverCreatureFlag(creature, "familyFlag", "PFAMIL") {
			if familyID, ok := serverCreatureInt(creature, "familyID", "dailyExpndMax", "legacyDailyExpndMax"); ok && familyID > 0 {
				path := filepath.Join(root, "player", "family", fmt.Sprintf("family_news_%d", familyID))
				if loginRegularFileExists(path) {
					steps = append(steps, m.loginViewFileStep(path, true, nil))
				}
			}
		}

		if name := loginPlayerFileName(player); name != "" {
			path := filepath.Join(root, "player", "fal", name)
			if loginRegularFileExists(path) {
				steps = append(steps, m.loginViewFileStep(path, true, func() error {
					return os.Remove(path)
				}))
			}
		}
		if notice := m.loginPostNotice(player); notice != "" {
			steps = append(steps, serverLoginPostLoadStep{kind: serverLoginPostLoadStepMessage, content: notice})
		}
	}
	if hasCreature {
		if step, ok := m.loginGoldCapStep(creature); ok {
			steps = append(steps, step)
		}
	}
	return steps
}

func (m *serverLoginManager) loginPostNotice(player model.Player) string {
	if m == nil || strings.TrimSpace(m.root) == "" {
		return ""
	}
	name := loginPlayerFileName(player)
	if name == "" {
		return ""
	}
	postRoot := filepath.Clean(filepath.Join(m.root, "post"))
	path := filepath.Clean(filepath.Join(postRoot, name))
	rel, err := filepath.Rel(postRoot, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ""
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	return "\n*** 우체국에 편지가 와있습니다.\n"
}

func (m *serverLoginManager) loginViewFileStep(path string, reportMissing bool, onDone func() error) serverLoginPostLoadStep {
	text, ok := loginReadLegacyText(path)
	if !ok {
		if reportMissing {
			return serverLoginPostLoadStep{kind: serverLoginPostLoadStepMessage, content: "화일을 읽을 수 없습니다.\n", onDone: onDone}
		}
		return serverLoginPostLoadStep{kind: serverLoginPostLoadStepMessage, onDone: onDone}
	}
	return serverLoginPostLoadStep{kind: serverLoginPostLoadStepFile, content: text, onDone: onDone}
}

func (m *serverLoginManager) loginGoldCapStep(creature model.Creature) (serverLoginPostLoadStep, bool) {
	if m == nil || m.world == nil || creature.ID.IsZero() {
		return serverLoginPostLoadStep{}, false
	}
	gold, ok := serverCreatureInt(creature, "gold")
	if !ok || gold <= legacyLoginGoldCap {
		return serverLoginPostLoadStep{}, false
	}
	return serverLoginPostLoadStep{
		kind:    serverLoginPostLoadStepMessage,
		content: legacyLoginGoldCapMessage,
		onDone: func() error {
			return m.world.SetCreatureStat(creature.ID, "gold", legacyLoginGoldCap)
		},
	}, true
}

type serverLoginPostLoadStepKind int

type serverLoginPostLoadStep struct {
	kind    serverLoginPostLoadStepKind
	content string
	prompt  string
	onDone  func() error
}

type serverLoginPostLoadSequence struct {
	steps []serverLoginPostLoadStep
	index int
	next  int
}

func (s *serverLoginPostLoadSequence) handleLine(ctx *enginecmd.Context, line string) (enginecmd.Status, error) {
	if s == nil || s.index >= len(s.steps) {
		enginecmd.ClearPendingLineHandler(ctx)
		return enginecmd.StatusDefault, nil
	}
	step := s.steps[s.index]
	if step.kind == serverLoginPostLoadStepWait {
		s.index++
		s.next = 0
		return s.render(ctx)
	}
	if step.kind == serverLoginPostLoadStepFile && strings.HasPrefix(line, ".") {
		ctx.WriteString("중단합니다.\n")
		if step.onDone != nil {
			if err := step.onDone(); err != nil {
				return enginecmd.StatusDefault, err
			}
		}
		s.index++
		s.next = 0
		return s.render(ctx)
	}
	return s.render(ctx)
}

func (s *serverLoginPostLoadSequence) render(ctx *enginecmd.Context) (enginecmd.Status, error) {
	if s == nil {
		return enginecmd.StatusDefault, nil
	}
	for s.index < len(s.steps) {
		step := s.steps[s.index]
		switch step.kind {
		case serverLoginPostLoadStepMessage:
			ctx.WriteString(step.content)
			if step.onDone != nil {
				if err := step.onDone(); err != nil {
					return enginecmd.StatusDefault, err
				}
			}
			s.index++
			s.next = 0
		case serverLoginPostLoadStepFile:
			if s.next >= len(step.content) {
				if step.onDone != nil {
					if err := step.onDone(); err != nil {
						return enginecmd.StatusDefault, err
					}
				}
				s.index++
				s.next = 0
				continue
			}
			page, next := enginecmd.LegacyViewFilePage(step.content, s.next)
			s.next = next
			ctx.WriteString(page)
			if s.next < len(step.content) {
				ctx.WriteString(enginecmd.LegacyViewFileContinuePrompt)
				if !enginecmd.SetPendingLineHandler(ctx, s.handleLine) {
					return enginecmd.StatusDefault, errors.New("로그인 파일 읽기 상태를 시작할 수 없습니다")
				}
				return enginecmd.StatusDoPrompt, nil
			}
			if step.onDone != nil {
				if err := step.onDone(); err != nil {
					return enginecmd.StatusDefault, err
				}
			}
			s.index++
			s.next = 0
		case serverLoginPostLoadStepWait:
			ctx.WriteString(step.prompt)
			if !enginecmd.SetPendingLineHandler(ctx, s.handleLine) {
				return enginecmd.StatusDefault, errors.New("로그인 확인 상태를 시작할 수 없습니다")
			}
			return enginecmd.StatusDoPrompt, nil
		default:
			s.index++
			s.next = 0
		}
	}
	enginecmd.ClearPendingLineHandler(ctx)
	return enginecmd.StatusDefault, nil
}

func loginReadLegacyText(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	text, err := legacykr.ValidUTF8OrDecodeContext(legacykr.Context{Path: path, Field: "login"}, data)
	if err != nil {
		return "", false
	}
	return text, true
}

func loginRegularFileExists(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0
}

func loginPlayerFileName(player model.Player) string {
	name := strings.TrimSpace(player.DisplayName)
	if name == "" {
		name = strings.TrimSpace(string(player.ID))
	}
	if !loginSafePostFileName(name) {
		return ""
	}
	return name
}

func loginSafePostFileName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	if name == "." || name == ".." || filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		return false
	}
	for _, r := range name {
		if r == 0 || r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

type legacyCreateClassStats struct {
	hpStart int
	mpStart int
	hp      int
	mp      int
	nDice   int
	sDice   int
	pDice   int
}

var legacyCreateClassStatTable = map[int]legacyCreateClassStats{
	model.ClassAssassin:  {hpStart: 55, mpStart: 40, hp: 5, mp: 2, nDice: 1, sDice: 6, pDice: 0},
	model.ClassBarbarian: {hpStart: 57, mpStart: 40, hp: 7, mp: 1, nDice: 2, sDice: 3, pDice: 1},
	model.ClassCleric:    {hpStart: 54, mpStart: 50, hp: 4, mp: 3, nDice: 1, sDice: 4, pDice: 0},
	model.ClassFighter:   {hpStart: 56, mpStart: 50, hp: 6, mp: 1, nDice: 1, sDice: 5, pDice: 0},
	model.ClassMage:      {hpStart: 54, mpStart: 50, hp: 4, mp: 3, nDice: 1, sDice: 3, pDice: 0},
	model.ClassPaladin:   {hpStart: 55, mpStart: 50, hp: 5, mp: 2, nDice: 1, sDice: 4, pDice: 0},
	model.ClassRanger:    {hpStart: 56, mpStart: 40, hp: 6, mp: 2, nDice: 2, sDice: 2, pDice: 0},
	model.ClassThief:     {hpStart: 55, mpStart: 50, hp: 5, mp: 2, nDice: 2, sDice: 2, pDice: 1},
}

var legacyCreateProficiencyKeys = []string{
	"proficiencySharp",
	"proficiencyThrust",
	"proficiencyBlunt",
	"proficiencyPole",
	"proficiencyMissile",
}

func legacyCreateNameRejection(name string) string {
	if name == "" {
		return "이름은 한 자 이상이어야 합니다.\n"
	}
	runes := []rune(name)
	if krtext.IsAllHangulSyllables(name) {
		if len(runes) > krtext.LegacyNameMaxSyllables {
			return "이름이 너무 깁니다.\n\n"
		}
		return ""
	}
	if len(name) > 12 {
		return "이름이 너무 깁니다.\n\n"
	}
	return "이름은 한글로 적으셔야 합니다.\n\n"
}

func legacyYes(line string) bool {
	line = strings.TrimSpace(line)
	return line == "예" || strings.HasPrefix(line, "y") || strings.HasPrefix(line, "Y")
}

func legacyCreateClassChoice(line string) (int, bool) {
	value, ok := legacyCreateDigitChoice(line, 1, 8)
	if !ok {
		return 0, false
	}
	switch value {
	case 1:
		return model.ClassAssassin, true
	case 2:
		return model.ClassBarbarian, true
	case 3:
		return model.ClassCleric, true
	case 4:
		return model.ClassFighter, true
	case 5:
		return model.ClassMage, true
	case 6:
		return model.ClassPaladin, true
	case 7:
		return model.ClassRanger, true
	case 8:
		return model.ClassThief, true
	default:
		return 0, false
	}
}

func legacyCreateRaceChoice(line string) (int, bool) {
	value, ok := legacyCreateDigitChoice(line, 1, 8)
	if !ok {
		return 0, false
	}
	switch value {
	case 1:
		return legacyCreateRaceDwarf, true
	case 2:
		return legacyCreateRaceElf, true
	case 3:
		return legacyCreateRaceGnome, true
	case 4:
		return legacyCreateRaceHalfElf, true
	case 5:
		return legacyCreateRaceHalfGiant, true
	case 6:
		return legacyCreateRaceHobbit, true
	case 7:
		return legacyCreateRaceHuman, true
	case 8:
		return legacyCreateRaceOrc, true
	default:
		return 0, false
	}
}

func legacyCreateDigitChoice(line string, min int, max int) (int, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, false
	}
	r := rune(line[0])
	if r < '0' || r > '9' {
		return 0, false
	}
	value := int(r - '0')
	return value, value >= min && value <= max
}

func legacyCreateStatChoices(line string) ([5]int, error) {
	var values [5]int
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return values, errors.New("5가지 능력치 모두를 위의 형식대로 적어 주십시요.")
	}
	sum := 0
	for i := 0; i < 5; i++ {
		value, err := strconv.Atoi(fields[i])
		if err != nil {
			value = 0
		}
		if value < 5 || value > 18 {
			return values, errors.New("각 능력치는 5이상 18이하로 설정해야 합니다.")
		}
		values[i] = value
		sum += value
	}
	if sum > 54 {
		return values, errors.New("각 능력치의 합이 54점을 초과할수 없습니다.")
	}
	return values, nil
}

func legacyApplyCreateRaceBonus(create *serverLoginCreateState) {
	if create == nil {
		return
	}
	switch create.race {
	case legacyCreateRaceDwarf:
		create.strength += 5
		create.piety--
		create.dexterity += 2
	case legacyCreateRaceElf:
		create.intelligence += 2
		create.constitution--
		create.strength--
	case legacyCreateRaceGnome:
		create.piety += 5
		create.strength--
	case legacyCreateRaceHalfElf:
		create.intelligence += 5
		create.constitution--
	case legacyCreateRaceHobbit:
		create.dexterity += 5
		create.strength--
		create.piety -= 2
	case legacyCreateRaceHuman:
		create.constitution--
		create.strength -= 3
	case legacyCreateRaceOrc:
		create.strength++
		create.constitution += 5
		create.dexterity += 3
		create.intelligence--
	case legacyCreateRaceHalfGiant:
		create.strength += 5
		create.intelligence -= 3
		create.piety--
	}
}

func legacyCreatedPlayerCharacter(create serverLoginCreateState, passwordHash string) (model.Player, model.Creature, error) {
	name := strings.TrimSpace(create.name)
	if name == "" {
		return model.Player{}, model.Creature{}, errors.New("create player: name is required")
	}
	stats, ok := legacyCreateClassStatTable[create.class]
	if !ok {
		return model.Player{}, model.Creature{}, fmt.Errorf("create player %q: invalid class %d", name, create.class)
	}
	if create.race == 0 {
		return model.Player{}, model.Creature{}, fmt.Errorf("create player %q: race is required", name)
	}
	if create.weapon < 0 || create.weapon >= len(legacyCreateProficiencyKeys) {
		return model.Player{}, model.Creature{}, fmt.Errorf("create player %q: weapon proficiency is required", name)
	}

	playerID := model.PlayerID(name)
	creatureID := model.CreatureID("creature:player:" + name)
	roomID := model.RoomID("room:00001")
	pDice := stats.pDice
	if pDice < 1 {
		pDice = 1
	}

	creatureStats := map[string]int{
		"class":         create.class,
		"race":          create.race,
		"level":         1,
		"strength":      create.strength,
		"dexterity":     create.dexterity,
		"constitution":  create.constitution,
		"intelligence":  create.intelligence,
		"piety":         create.piety,
		"hpMax":         stats.hpStart,
		"hpCurrent":     stats.hpStart,
		"mpMax":         stats.mpStart,
		"mpCurrent":     stats.mpStart,
		"nDice":         stats.nDice,
		"sDice":         stats.sDice,
		"pDice":         pDice,
		"gold":          1000,
		"PLECHO":        1,
		"PPROMP":        1,
		"PANSIC":        1,
		"PBRIGH":        1,
		"PNOEXT":        1,
		"PDSCRP":        1,
		"PNOSUM":        1,
		"experience":    0,
		"alignment":     0,
		"inventoryGold": 0,
	}
	if create.male {
		creatureStats["PMALES"] = 1
	}
	if create.chaos {
		creatureStats["PCHAOS"] = 1
	}
	creatureStats[legacyCreateProficiencyKeys[create.weapon]] = 1024
	creatureStats[fmt.Sprintf("proficiency/%d", create.weapon)] = 1024

	player := model.Player{
		ID:          playerID,
		DisplayName: name,
		CreatureID:  creatureID,
		RoomID:      roomID,
	}
	creature := model.Creature{
		ID:          creatureID,
		Kind:        model.CreatureKindPlayer,
		DisplayName: name,
		Level:       1,
		RoomID:      roomID,
		PlayerID:    playerID,
		Stats:       creatureStats,
		Properties:  map[string]string{"legacyPasswordHash": passwordHash},
		Metadata:    model.Metadata{Tags: []string{"PLECHO", "PPROMP", "PANSIC", "PBRIGH", "PNOEXT", "PDSCRP", "PNOSUM"}},
	}
	if create.male {
		creature.Metadata.Tags = append(creature.Metadata.Tags, "PMALES")
	}
	if create.chaos {
		creature.Metadata.Tags = append(creature.Metadata.Tags, "PCHAOS")
	}
	return player, creature, nil
}

const (
	serverLoginSitePassword serverLoginStep = iota
	serverLoginName
	serverLoginPassword
	serverLoginCreateConfirm
	serverLoginCreateEnter
	serverLoginCreateGender
	serverLoginCreateClass
	serverLoginCreateStats
	serverLoginCreateWeapon
	serverLoginCreateAlignment
	serverLoginCreateRace
	serverLoginCreatePassword
)

const (
	serverLoginPostLoadStepMessage serverLoginPostLoadStepKind = iota
	serverLoginPostLoadStepFile
	serverLoginPostLoadStepWait
)

const (
	legacyCreateRaceDwarf     = 1
	legacyCreateRaceElf       = 2
	legacyCreateRaceHalfElf   = 3
	legacyCreateRaceHobbit    = 4
	legacyCreateRaceHuman     = 5
	legacyCreateRaceOrc       = 6
	legacyCreateRaceHalfGiant = 7
	legacyCreateRaceGnome     = 8
)
