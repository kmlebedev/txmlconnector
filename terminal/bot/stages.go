package bot

import (
	"fmt"
	"strings"

	"github.com/kmlebedev/txmlconnector/terminal/backtest"
)

// normalizeStage canonicalizes a caller-supplied stage string.
func normalizeStage(stage Stage) Stage {
	return Stage(strings.ToLower(strings.TrimSpace(string(stage))))
}

// validStage reports whether s is one of the four known stages.
func validStage(s Stage) bool {
	switch s {
	case StageCheck, StageDryRun, StagePaper, StageStopped:
		return true
	default:
		return false
	}
}

// runnableStage reports whether a bot in this stage consumes closed bars.
func runnableStage(s Stage) bool {
	return s == StageDryRun || s == StagePaper
}

// signalOf maps a strategy action to the journal signal name.
func signalOf(action backtest.Action) string {
	switch action {
	case backtest.Buy:
		return SignalEntry
	case backtest.Sell:
		return SignalExit
	default:
		return SignalNone
	}
}

// accountFor returns the paper sub-account of one bot.
func accountFor(id string) string {
	return "bot:" + id
}

// transitionAllowed reports whether stage may be entered from the current one.
// check -> dry_run/paper and dry_run <-> paper are allowed; stopped restarts are
// allowed too. Paper is executed by the bot_start tool only after the human
// approved the proposal (Promote), which the manager can not verify itself.
func transitionAllowed(from, to Stage) bool {
	if from == to {
		return true
	}
	switch to {
	case StageDryRun:
		return from == StageCheck || from == StageStopped
	case StagePaper:
		return from == StageDryRun || from == StageCheck || from == StageStopped
	case StageStopped:
		return runnableStage(from)
	default:
		return false
	}
}

// stageError describes an invalid transition.
func stageError(from, to Stage) error {
	return fmt.Errorf("%w: %s -> %s", ErrInvalidStage, from, to)
}
