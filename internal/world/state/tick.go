package state

import (
	"fmt"
	"os"
)

var exitFunc = os.Exit

func SetExitFunc(fn func(code int)) {
	exitFunc = fn
}

func (w *World) TickWorld(t int64) error {
	if w == nil {
		return fmt.Errorf("tick world: world state is nil")
	}

	// 1. Every 1 second: call UpdateActiveMonsters(t)
	var lastActive int64
	w.rLockDomains(true, true, true, true, true, true, true)
	lastActive = w.lastActiveUpdate
	w.rUnlockDomains(true, true, true, true, true, true, true)

	if t != lastActive {
		w.lockDomains(true, true, true, true, true, true, true)
		w.lastActiveUpdate = t
		w.unlockDomains(true, true, true, true, true, true, true)
		if err := w.UpdateActiveMonsters(t); err != nil {
			// handle/log error if needed
		}
	}

	// 2. Every 20 seconds: call UpdatePlayerStatuses(t)
	var lastPlayer int64
	w.rLockDomains(true, true, true, true, true, true, true)
	lastPlayer = w.lastPlayerUpdate
	w.rUnlockDomains(true, true, true, true, true, true, true)

	if t-lastPlayer >= 20 {
		w.lockDomains(true, true, true, true, true, true, true)
		w.lastPlayerUpdate = t
		w.unlockDomains(true, true, true, true, true, true, true)
		if err := w.UpdatePlayerStatuses(t); err != nil {
			// handle/log error if needed
		}
	}

	// 3. Every Random_update_interval: call UpdateRandomSpawns(t)
	var lastRandom, randomInt int64
	w.rLockDomains(true, true, true, true, true, true, true)
	lastRandom = w.lastRandomUpdate
	randomInt = w.randomUpdateInterval
	if randomInt == 0 {
		randomInt = 20
	}
	w.rUnlockDomains(true, true, true, true, true, true, true)

	if t-lastRandom >= randomInt {
		w.lockDomains(true, true, true, true, true, true, true)
		w.lastRandomUpdate = t
		w.unlockDomains(true, true, true, true, true, true, true)
		if err := w.UpdateRandomSpawns(t); err != nil {
			// handle/log error if needed
		}
	}

	// 4. Every 150 seconds: call UpdateTimeClock(t)
	var lastTime int64
	w.rLockDomains(true, true, true, true, true, true, true)
	lastTime = w.lastTimeUpdate
	w.rUnlockDomains(true, true, true, true, true, true, true)

	if t-lastTime >= 150 {
		w.lockDomains(true, true, true, true, true, true, true)
		w.lastTimeUpdate = t
		w.unlockDomains(true, true, true, true, true, true, true)
		if err := w.UpdateTimeClock(t); err != nil {
			// handle/log error if needed
		}
	}

	// 5. Every TX_interval: call UpdateTimedExits(t)
	var lastExit, txInt int64
	w.rLockDomains(true, true, true, true, true, true, true)
	lastExit = w.lastExitUpdate
	txInt = w.txInterval
	if txInt == 0 {
		txInt = 3600
	}
	w.rUnlockDomains(true, true, true, true, true, true, true)

	if t-lastExit >= txInt {
		w.lockDomains(true, true, true, true, true, true, true)
		w.lastExitUpdate = t
		w.unlockDomains(true, true, true, true, true, true, true)
		if err := w.UpdateTimedExits(t); err != nil {
			// handle/log error if needed
		}
	}

	// 6. Every 30 seconds: call UpdateShutdown(t) if shutdown is scheduled
	var lastShutdown, shutdownLTime int64
	w.rLockDomains(true, true, true, true, true, true, true)
	lastShutdown = w.lastShutdownUpdate
	shutdownLTime = w.shutdownLTime
	w.rUnlockDomains(true, true, true, true, true, true, true)

	if shutdownLTime != 0 && t-lastShutdown >= 30 {
		w.lockDomains(true, true, true, true, true, true, true)
		w.lastShutdownUpdate = t
		w.unlockDomains(true, true, true, true, true, true, true)
		if err := w.UpdateShutdown(t); err != nil {
			// handle/log error if needed
		}
	}

	return nil
}

func (w *World) UpdateActiveMonsters(t int64) error {
	w.rLockDomains(true, true, true, true, true, true, true)
	fn := w.UpdateActiveMonstersFunc
	w.rUnlockDomains(true, true, true, true, true, true, true)
	if fn != nil {
		return fn(t)
	}
	return nil
}

func (w *World) UpdatePlayerStatuses(t int64) error {
	w.rLockDomains(true, true, true, true, true, true, true)
	fn := w.UpdatePlayerStatusesFunc
	w.rUnlockDomains(true, true, true, true, true, true, true)
	if fn != nil {
		return fn(t)
	}
	return nil
}

func (w *World) UpdateRandomSpawns(t int64) error {
	w.rLockDomains(true, true, true, true, true, true, true)
	fn := w.UpdateRandomSpawnsFunc
	w.rUnlockDomains(true, true, true, true, true, true, true)
	if fn != nil {
		return fn(t)
	}
	return nil
}

func (w *World) UpdateTimeClock(t int64) error {
	w.rLockDomains(true, true, true, true, true, true, true)
	fn := w.UpdateTimeClockFunc
	w.rUnlockDomains(true, true, true, true, true, true, true)
	if fn != nil {
		return fn(t)
	}
	return nil
}

func (w *World) UpdateTimedExits(t int64) error {
	w.rLockDomains(true, true, true, true, true, true, true)
	fn := w.UpdateTimedExitsFunc
	w.rUnlockDomains(true, true, true, true, true, true, true)
	if fn != nil {
		return fn(t)
	}
	return nil
}

func (w *World) UpdateShutdown(t int64) error {
	w.rLockDomains(true, true, true, true, true, true, true)
	fn := w.UpdateShutdownFunc
	w.rUnlockDomains(true, true, true, true, true, true, true)
	if fn != nil {
		return fn(t)
	}

	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)

	if w.shutdownLTime == 0 {
		return nil
	}

	target := w.shutdownLTime + w.shutdownInterval
	if target > t {
		diff := target - t
		if diff > 60 {
			msg := fmt.Sprintf("\n### %d분 %02d초 후에 머드를 종료합니다.", diff/60, diff%60)
			w.unlockDomains(true, true, true, true, true, true, true)
			_ = w.BroadcastAll(msg)
			w.lockDomains(true, true, true, true, true, true, true)
		} else {
			msg := fmt.Sprintf("\n### %d초 후에 머드를 종료합니다. 모두 나가 주십시요.", diff)
			w.unlockDomains(true, true, true, true, true, true, true)
			_ = w.BroadcastAll(msg)
			w.lockDomains(true, true, true, true, true, true, true)
		}
	} else {
		w.unlockDomains(true, true, true, true, true, true, true)
		_ = w.BroadcastAll("\n### 머드를 종료합니다.")
		w.lockDomains(true, true, true, true, true, true, true)
		exitFunc(0)
	}
	return nil
}

// Getters and Setters for scheduling fields

func (w *World) LastActiveUpdate() int64 {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	return w.lastActiveUpdate
}

func (w *World) SetLastActiveUpdate(val int64) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.lastActiveUpdate = val
}

func (w *World) LastPlayerUpdate() int64 {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	return w.lastPlayerUpdate
}

func (w *World) SetLastPlayerUpdate(val int64) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.lastPlayerUpdate = val
}

func (w *World) LastRandomUpdate() int64 {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	return w.lastRandomUpdate
}

func (w *World) SetLastRandomUpdate(val int64) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.lastRandomUpdate = val
}

func (w *World) LastTimeUpdate() int64 {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	return w.lastTimeUpdate
}

func (w *World) SetLastTimeUpdate(val int64) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.lastTimeUpdate = val
}

func (w *World) LegacyTime() int64 {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	return w.legacyTime
}

func (w *World) SetLegacyTime(val int64) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.legacyTime = val
}

func (w *World) IncrementTime() int64 {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.legacyTime++
	return w.legacyTime
}

func (w *World) LastExitUpdate() int64 {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	return w.lastExitUpdate
}

func (w *World) SetLastExitUpdate(val int64) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.lastExitUpdate = val
}

func (w *World) RandomUpdateInterval() int64 {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	if w.randomUpdateInterval == 0 {
		return 20
	}
	return w.randomUpdateInterval
}

func (w *World) SetRandomUpdateInterval(val int64) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.randomUpdateInterval = val
}

func (w *World) TXInterval() int64 {
	w.rLockDomains(true, true, true, true, true, true, true)
	defer w.rUnlockDomains(true, true, true, true, true, true, true)
	if w.txInterval == 0 {
		return 3600
	}
	return w.txInterval
}

func (w *World) SetTXInterval(val int64) {
	w.lockDomains(true, true, true, true, true, true, true)
	defer w.unlockDomains(true, true, true, true, true, true, true)
	w.txInterval = val
}
