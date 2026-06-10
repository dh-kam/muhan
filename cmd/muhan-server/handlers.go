package main

import (
	"fmt"
	"strings"
	"time"

	enginecmd "muhan/internal/engine/command"
	"muhan/internal/engine/game"
	"muhan/internal/world/model"
	"muhan/internal/world/state"
)

func serverDispatcher(inputs runtimeInputs) enginecmd.Dispatcher {
	groupMemory := game.NewGroupMemory()
	handlers := commandHandlers(inputs, groupMemory)
	wrappedHandlers := make(map[string]enginecmd.Handler, len(handlers))
	for k, h := range handlers {
		hCopy := h
		isDMHandler := strings.HasPrefix(k, "dm_") // C1: DM 핸들러 식별
		wrappedHandlers[k] = func(ctx *enginecmd.Context, resolved enginecmd.ResolvedCommand) (enginecmd.Status, error) {
			if ctx != nil && ctx.Values != nil {
				ctx.Values["game.groupMemory"] = groupMemory
			}
			// C1: DM 핸들러 또는 '*' 접두 명령은 DM class(13)만 허용
			if (isDMHandler || resolved.Privileged()) && !serverIsDM(inputs.world, ctx) {
				return enginecmd.StatusDefault, enginecmd.ErrUnknownCommand
			}
			return hCopy(ctx, resolved)
		}
	}
	return enginecmd.Dispatcher{
		Registry:   inputs.registry,
		Handlers:   wrappedHandlers,
		Special:    enginecmd.NewSpecialHandler(inputs.world, inputs.summary.Root),
		AliasStore: enginecmd.NewFileAliasStore(inputs.summary.Root, inputs.world),
	}
}

func serverIsDM(world *state.World, ctx *enginecmd.Context) bool {
	if ctx == nil || ctx.ActorID == "" || world == nil {
		return false
	}
	player, ok := world.Player(model.PlayerID(ctx.ActorID))
	if !ok || player.CreatureID.IsZero() {
		return false
	}
	creature, ok := world.Creature(player.CreatureID)
	if !ok {
		return false
	}
	class, ok := serverCreatureInt(creature, "class")
	return ok && class == legacyLoginDMClass
}

func commandHandlers(inputs runtimeInputs, optionalGroupMemory ...*game.GroupMemory) map[string]enginecmd.Handler {
	world := inputs.world
	var groupMemory *game.GroupMemory
	if len(optionalGroupMemory) > 0 {
		groupMemory = optionalGroupMemory[0]
	} else {
		groupMemory = game.NewGroupMemory()
	}
	move := game.NewGroupMoveHandler(world, groupMemory, enginecmd.NewMoveHandler(world))
	tellMemory := game.NewTellMemory()
	ignoreMemory := game.NewIgnoreMemory()
	familyMembershipRequests := game.NewFamilyMembershipRequests()
	marriageRequests := game.NewMarriageRequests()
	voteMemory := game.NewVoteMemory()
	aliasStore := enginecmd.NewFileAliasStore(inputs.summary.Root, world)
	deathFinalizer := func(ctx *enginecmd.Context, attacker, victim model.Creature) error {
		killerPlayerID := serverDeathFinalizerPlayerID(ctx, attacker)
		if inputs.getLoop != nil {
			if activeLoop := inputs.getLoop(); activeLoop != nil {
				_, _ = game.HandlePermanentCreatureDeath(activeLoop, killerPlayerID, victim.ID, time.Now().Unix())
			}
		}
		var err error
		if ctx == nil || ctx.ActorID == "" {
			_, err = world.FinalizeMonsterDeath(victim.ID)
			return err
		}
		snapshot, ok := groupMemory.Snapshot(ctx.ActorID)
		if !ok {
			_, err = world.FinalizeMonsterDeath(victim.ID)
			serverMarkDeathRewardPlayerDirty(world, killerPlayerID)
			return err
		}
		leaderID, followerIDs, ok := snapshot.CreatureSnapshot(world)
		if !ok {
			_, err = world.FinalizeMonsterDeath(victim.ID)
			serverMarkDeathRewardPlayerDirty(world, killerPlayerID)
			return err
		}
		_, err = world.FinalizeMonsterDeathWithOptions(victim.ID, state.FinalizeMonsterDeathOptions{
			RewardGroup: state.MonsterDeathRewardGroup{
				LeaderID:    leaderID,
				FollowerIDs: followerIDs,
			},
		})
		// B/C: Mark+Queue after combat reward (FinalizeMonsterDeath may have mutated gold/exp)
		serverMarkDeathRewardPlayerDirty(world, killerPlayerID)
		return err
	}
	attack := enginecmd.NewAttackHandlerWithDeathFinalizer(world, deathFinalizer)

	handlers := map[string]enginecmd.Handler{
		"look": enginecmd.NewLookHandler(world),
		"go":   move,
		"move": move,
		"get":  enginecmd.NewGetHandler(world),
		"drop": enginecmd.NewDropHandler(world),
		"quit": enginecmd.NewQuitHandlerWithOptions(world,
			enginecmd.WithQuitLowLevelSink(serverLowLevelQuitSink{
				world:      world,
				root:       inputs.summary.Root,
				aliasStore: aliasStore,
			}),
		),
		"inventory":          enginecmd.NewInventoryHandler(world),
		"wear":               enginecmd.NewWearHandler(world),
		"remove_obj":         enginecmd.NewRemoveObjectHandler(world),
		"equipment":          enginecmd.NewEquipmentHandler(world),
		"hold":               enginecmd.NewHoldHandler(world),
		"ready":              enginecmd.NewReadyHandler(world),
		"savegame":           enginecmd.NewSaveGameHandler(),
		"set_title":          enginecmd.NewSetTitleHandler(inputs.summary.Root, world),
		"clear_title":        enginecmd.NewClearTitleHandler(inputs.summary.Root, world),
		"info_obj":           enginecmd.NewAppraiseHandler(world),
		"obj_compare":        enginecmd.NewObjectCompareHandler(world),
		"health":             enginecmd.NewHealthHandler(world),
		"info":               enginecmd.NewInfoHandler(world),
		"where":              enginecmd.NewWhereHandler(world),
		"effect_flag_list":   enginecmd.NewEffectStatusHandler(world),
		"cast":               enginecmd.NewCastHandler(world, nil),
		"study":              enginecmd.NewStudyHandler(world),
		"train":              enginecmd.NewTrainHandler(world),
		"invince_train":      enginecmd.NewInvinceTrainHandler(world),
		"list":               enginecmd.NewShopListHandler(world),
		"buy":                enginecmd.NewShopBuyHandler(world),
		"sell":               enginecmd.NewShopSellHandler(world),
		"value":              enginecmd.NewShopValueHandler(world),
		"repair":             enginecmd.NewRepairHandler(world, nil),
		"drink":              enginecmd.NewDrinkHandler(world, nil),
		"zap":                enginecmd.NewZapHandler(world, nil),
		"use":                enginecmd.NewUseHandlerWithRoot(world, inputs.summary.Root, nil),
		"bank_inv":           enginecmd.NewBankInventoryHandler(world),
		"bank":               enginecmd.NewBankBalanceHandler(world),
		"deposit":            enginecmd.NewBankDepositHandler(world),
		"withdraw":           enginecmd.NewBankWithdrawHandler(world),
		"output_bank":        enginecmd.NewBankOutputHandler(world),
		"peek":               enginecmd.NewPeekHandler(world, nil),
		"track":              enginecmd.NewTrackHandler(world),
		"attack":             attack,
		"kick":               enginecmd.NewKickHandlerWithDeathFinalizer(world, deathFinalizer),
		"poison_mon":         enginecmd.NewPoisonMonHandlerWithDeathFinalizer(world, deathFinalizer),
		"up_dmg":             enginecmd.NewUpDamageHandler(world, nil),
		"magic_stop":         enginecmd.NewMagicStopHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"power":              enginecmd.NewPowerHandler(world, nil),
		"accurate":           enginecmd.NewAccurateHandler(world, nil),
		"absorb":             enginecmd.NewAbsorbHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"backstab":           enginecmd.NewBackstabHandlerWithDeathFinalizer(world, deathFinalizer),
		"bash":               enginecmd.NewBashHandlerWithDeathFinalizer(world, deathFinalizer),
		"circle":             enginecmd.NewCircleHandler(world),
		"invincible_kick":    enginecmd.NewInvincibleKickHandlerWithDeathFinalizer(world, deathFinalizer),
		"one_kill":           enginecmd.NewOneKillHandlerWithDeathFinalizer(world, deathFinalizer),
		"scratch":            enginecmd.NewScratchHandler(world),
		"eight":              enginecmd.NewEightHandlerWithDeathFinalizer(world, deathFinalizer),
		"nahan":              enginecmd.NewNahanHandlerWithDeathFinalizer(world, deathFinalizer),
		"red_eye":            enginecmd.NewRedEyeHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"thief_stat":         enginecmd.NewThiefStatHandler(world, nil),
		"poback":             enginecmd.NewPobackHandlerWithDeathFinalizer(world, deathFinalizer),
		"bnahan":             enginecmd.NewBnahanHandlerWithDeathFinalizer(world, deathFinalizer),
		"tagu":               enginecmd.NewTaguHandlerWithDeathFinalizer(world, deathFinalizer),
		"reflect":            enginecmd.NewReflectHandler(world, nil),
		"shadow":             enginecmd.NewShadowHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"chang":              enginecmd.NewChangHandlerWithDeathFinalizer(world, deathFinalizer),
		"sasal":              enginecmd.NewSasalHandlerWithDeathFinalizer(world, deathFinalizer),
		"rm_blind2":          enginecmd.NewRmBlind2Handler(world),
		"choi":               enginecmd.NewChoiHandlerWithDeathFinalizer(world, deathFinalizer),
		"turn":               enginecmd.NewTurnHandlerWithDeathFinalizer(world, nil, deathFinalizer),
		"teach":              enginecmd.NewTeachHandler(world),
		"steal":              enginecmd.NewStealHandler(world, nil),
		"search":             enginecmd.NewSearchHandler(world, nil),
		"hide":               enginecmd.NewHideHandler(world, nil),
		"set":                enginecmd.NewSetHandler(world),
		"clear":              enginecmd.NewClearHandler(world),
		"openexit":           enginecmd.NewOpenExitHandler(world),
		"closeexit":          enginecmd.NewCloseExitHandler(world),
		"unlock":             enginecmd.NewUnlockExitHandler(world),
		"lock":               enginecmd.NewLockExitHandler(world),
		"picklock":           enginecmd.NewPicklockHandler(world, nil),
		"flee":               enginecmd.NewFleeHandler(world, nil),
		"prt_time":           enginecmd.NewTimeHandlerWithWorld(world, nil),
		"prepare":            enginecmd.NewPrepareHandler(world),
		"guard":              enginecmd.NewGuardHandler(world),
		"meditate":           enginecmd.NewMeditateHandler(world, nil),
		"lion_scream":        enginecmd.NewLionScreamHandlerWithDeathFinalizer(world, deathFinalizer),
		"haste":              enginecmd.NewHasteHandler(world, nil),
		"pray":               enginecmd.NewPrayHandler(world, nil),
		"angel":              enginecmd.NewAngelHandler(world, nil),
		"return_square":      enginecmd.NewReturnSquareHandler(world),
		"who":                game.NewWhoHandler(world),
		"whois":              game.NewWhoisHandler(world),
		"pfinger":            game.NewPfingerHandler(world, inputs.summary.Root),
		"follow":             game.NewFollowHandler(world, groupMemory),
		"lose":               game.NewLoseHandler(world, groupMemory),
		"group":              game.NewGroupHandler(world, groupMemory),
		"gtalk":              game.NewGroupTalkHandler(world, groupMemory),
		"family_who":         game.NewFamilyWhoHandler(world),
		"family_talk":        game.NewFamilyTalkHandler(world),
		"family_news":        game.NewFamilyNewsHandler(world, inputs.summary.Root),
		"family":             game.NewFamilyJoinHandler(world, familyMembershipRequests),
		"boss_family":        game.NewFamilyJoinApproveHandler(world, inputs.summary.Root, familyMembershipRequests),
		"fm_dis":             game.NewFamilyJoinCancelHandler(world, familyMembershipRequests),
		"out_family":         game.NewFamilyLeaveHandler(world, inputs.summary.Root, familyMembershipRequests),
		"fm_out":             game.NewFamilyKickHandler(world, inputs.summary.Root, familyMembershipRequests),
		"invite":             game.NewInviteHandler(world),
		"marriage":           game.NewMarriageHandler(world, marriageRequests, inputs.summary.Root),
		"family_member":      game.NewFamilyMemberHandler(world),
		"list_family":        game.NewListFamilyHandler(world),
		"family_bank_inv":    game.NewFamilyBankInventoryHandler(world),
		"input_family_bank":  game.NewFamilyBankInventoryHandler(world),
		"output_family_bank": game.NewFamilyBankOutputHandler(world),
		"family_deposit":     game.NewFamilyDepositHandler(world),
		"family_withdraw":    game.NewFamilyWithdrawHandler(world),
		"call_war":           game.NewCallWarHandler(world),
		"give":               game.NewGiveHandler(world),
		"memo":               game.NewMemoHandler(world, inputs.summary.Root),
		"vote":               game.NewVoteHandler(world, voteMemory, inputs.summary.Root),
		"talk":               game.NewTalkHandlerWithRoot(world, inputs.summary.Root),
		"ignore":             game.NewIgnoreHandler(world, ignoreMemory),
		"send":               game.NewTellHandler(world, tellMemory, ignoreMemory),
		"resend":             game.NewReplyHandler(world, tellMemory, ignoreMemory),
		"say":                game.NewSayHandler(world),
		"broadsend":          game.NewBroadcastChatHandler(world),
		"broadsend2":         game.NewCheerHandler(world),
		"action":             game.NewActionHandler(world),
		"emote":              game.NewEmoteHandler(world),
		"yell":               game.NewYellHandler(world),
		"help":               enginecmd.NewHelpHandler(inputs.summary.Root, inputs.registry),
		"welcome":            enginecmd.NewWelcomeHandler(inputs.summary.Root),
		"look_board":         enginecmd.NewBoardLookHandler(world, inputs.summary.Root),
		"readscroll":         enginecmd.NewReadScrollHandler(world, inputs.summary.Root, nil),
		"writeboard":         enginecmd.NewBoardWriteHandler(world, inputs.summary.Root),
		"del_board":          enginecmd.NewBoardDeleteHandler(world, inputs.summary.Root),
		"postsend":           enginecmd.NewPostSendHandler(world, inputs.summary.Root),
		"postread":           enginecmd.NewPostReadHandler(world, inputs.summary.Root),
		"postdelete":         enginecmd.NewPostDeleteHandler(world, inputs.summary.Root),
		"trade":              enginecmd.NewTradeHandler(world),
		"trans_exp":          enginecmd.NewTransExpHandler(world),
		"m_send":             enginecmd.NewMarriageSendHandler(&marriageSendWorldWrapper{World: world, getLoop: inputs.getLoop}, inputs.summary.Root),
	}

	// 16차 포팅 추가 핸들러 등록
	handlers["description"] = enginecmd.NewDescriptionHandler(world)
	handlers["passwd"] = enginecmd.NewPasswdHandler(serverPasswordWorld{World: world}, enginecmd.WithPasswordSink(serverPasswordSink{world: world}))
	handlers["ply_alias"] = enginecmd.NewPlyAliasesHandler(aliasStore)
	handlers["suicide"] = enginecmd.NewPlySuicideHandler(world,
		enginecmd.WithSuicideSink(serverSuicideSink{world: world, root: inputs.summary.Root, aliasStore: aliasStore}),
		enginecmd.WithSuicideAliasStore(aliasStore),
	)
	handlers["burn"] = enginecmd.NewBurnHandlerWithRoot(world, inputs.summary.Root)
	handlers["purchase"] = enginecmd.NewMonsterPurchaseHandler(world)
	handlers["selection"] = enginecmd.NewMonsterSelectionHandler(world)
	handlers["forge"] = enginecmd.NewForgeHandler(world, nil)
	handlers["newforge"] = enginecmd.NewNewForgeHandler(world, nil)
	handlers["change_class"] = enginecmd.NewChangeClassHandler(world)

	// 17차 포팅 추가 핸들러 등록
	handlers["buy_states"] = enginecmd.NewBuyStatesHandler(world, nil)
	handlers["chg_name"] = enginecmd.NewChangeNameHandler(world)
	handlers["pledge"] = enginecmd.NewPledgeHandler(world)
	handlers["rescind"] = enginecmd.NewRescindHandler(world)
	handlers["sneak"] = enginecmd.NewSneakHandler(world, nil)
	handlers["dm_reload_rom"] = enginecmd.NewDMReloadRomHandler(world)
	handlers["dm_loadlockout"] = enginecmd.NewDMLoadLockoutHandler(world)
	handlers["dm_resave"] = enginecmd.NewDMResaveHandler(world)
	handlers["dm_rmstat"] = enginecmd.NewDMRmstatHandler(world)
	handlers["dm_teleport"] = enginecmd.NewDMTeleportHandler(world)
	handlers["dm_stat"] = enginecmd.NewDMStatHandler(world)
	handlers["dm_create_obj"] = enginecmd.NewDMCreateObjHandler(world)
	handlers["dm_create_crt"] = enginecmd.NewDMCreateCrtHandler(world)
	handlers["dm_perm"] = enginecmd.NewDMPermHandler(world)
	handlers["dm_invis"] = enginecmd.NewDMInvisHandler(world)
	handlers["dm_ac"] = enginecmd.NewDMAcHandler(world)
	handlers["dm_send"] = enginecmd.NewDMSendHandler(world)
	handlers["dm_echo"] = enginecmd.NewDMEchoHandler(world)
	handlers["dm_broadecho"] = enginecmd.NewDMBroadechoHandler(world)
	handlers["dm_nameroom"] = enginecmd.NewDMNameroomHandler(world)
	handlers["dm_spy"] = enginecmd.NewDMSpyHandler(world)
	handlers["dm_purge"] = enginecmd.NewDMPurgeHandler(world)
	handlers["dm_users"] = enginecmd.NewDMUsersHandler(world)
	handlers["dm_flush_crtobj"] = enginecmd.NewDMFlushCrtObjHandler(world)
	handlers["dm_flushsave"] = enginecmd.NewDMFlushsaveHandler(world)
	handlers["dm_shutdown"] = enginecmd.NewDMShutdownHandler(world)
	handlers["dm_force"] = enginecmd.NewDMForceHandler(&forceWorldWrapper{World: world, getLoop: inputs.getLoop})
	handlers["dm_log"] = enginecmd.NewDMLogHandler(world)
	handlers["dm_help"] = enginecmd.NewDMHelpHandler(inputs.summary.Root, world)
	handlers["dm_add_rom"] = enginecmd.NewDMAddRomHandler(world)
	handlers["dm_set"] = enginecmd.NewDMSetHandler(world)
	handlers["dm_finger"] = enginecmd.NewDMFingerHandler(world)
	handlers["dm_replace"] = enginecmd.NewDMReplaceHandler(world)
	handlers["dm_list"] = enginecmd.NewDMListHandler(world)
	handlers["dm_info"] = enginecmd.NewDMInfoHandler(world)
	handlers["dm_param"] = enginecmd.NewDMParamHandler(world)
	handlers["dm_silence"] = enginecmd.NewDMSilenceHandler(world)
	handlers["dm_group"] = enginecmd.NewDMGroupHandler(world)
	handlers["list_act"] = enginecmd.NewListActHandler(world)
	handlers["dm_save_all_ply"] = enginecmd.NewDMSaveAllPlyHandler(world)
	handlers["dm_append"] = enginecmd.NewDMAppendHandler(world)
	handlers["dm_prepend"] = enginecmd.NewDMPrependHandler(world)
	handlers["dm_cast"] = enginecmd.NewDMCastHandler(world)
	handlers["notepad"] = enginecmd.NewNotepadHandler(world, inputs.summary.Root)
	handlers["dm_delete"] = enginecmd.NewDMDeleteHandler(world)
	handlers["dm_obj_name"] = enginecmd.NewDMObjNameHandler(world)
	handlers["dm_crt_name"] = enginecmd.NewDMCrtNameHandler(world)
	handlers["dm_dust"] = enginecmd.NewDMDustHandler(world)
	handlers["dm_follow"] = enginecmd.NewDMFollowHandler(world)
	handlers["dm_attack"] = enginecmd.NewDMAttackHandler(world)
	handlers["list_enm"] = enginecmd.NewListEnmHandler(world)
	handlers["list_charm"] = enginecmd.NewListCharmHandler(world)

	// C 레거시 매핑 호환성 수정
	handlers["ply_aliases"] = handlers["ply_alias"]
	handlers["ply_suicide"] = handlers["suicide"]

	// DM Placeholder 핸들러 등록
	for k, h := range enginecmd.NewDMPlaceholderHandlers(world) {
		if _, exists := handlers[k]; !exists {
			handlers[k] = h
		}
	}

	return handlers
}

type marriageSendWorldWrapper struct {
	*state.World
	getLoop func() *game.Loop
}

type forceWorldWrapper struct {
	*state.World
	getLoop func() *game.Loop
}

func (w *forceWorldWrapper) ForcePlayerCommand(playerID model.PlayerID, cmd string) error {
	if w == nil || w.getLoop == nil {
		return fmt.Errorf("force player %s: game loop is not available", playerID)
	}
	loop := w.getLoop()
	if loop == nil {
		return fmt.Errorf("force player %s: game loop is not available", playerID)
	}
	for _, active := range loop.ActiveSessions() {
		if strings.EqualFold(active.ActorID, string(playerID)) {
			if loop.HasPendingLineHandler(active.ID) {
				return fmt.Errorf("force player %s: command input unavailable", playerID)
			}
			return loop.DispatchCommand(active.ID, playerID, cmd)
		}
	}
	return fmt.Errorf("force player %s: active session not found", playerID)
}

func (w *forceWorldWrapper) CanForcePlayerCommand(playerID model.PlayerID) bool {
	if w == nil || w.getLoop == nil {
		return false
	}
	loop := w.getLoop()
	if loop == nil {
		return false
	}
	for _, active := range loop.ActiveSessions() {
		if strings.EqualFold(active.ActorID, string(playerID)) {
			return !loop.HasPendingLineHandler(active.ID)
		}
	}
	return false
}

func (w *marriageSendWorldWrapper) ActiveSessions() []string {
	if w.getLoop == nil {
		return nil
	}
	loop := w.getLoop()
	if loop == nil {
		return nil
	}
	active := loop.ActiveSessions()
	ids := make([]string, len(active))
	for i, s := range active {
		ids[i] = string(s.ID)
	}
	return ids
}

func (w *marriageSendWorldWrapper) SessionActor(id string) (string, bool) {
	if w.getLoop == nil {
		return "", false
	}
	loop := w.getLoop()
	if loop == nil {
		return "", false
	}
	for _, s := range loop.ActiveSessions() {
		if string(s.ID) == id {
			return s.ActorID, true
		}
	}
	return "", false
}
