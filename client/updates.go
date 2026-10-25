package tcClient

import (
	"errors"
	"time"

	"github.com/kmlebedev/txmlconnector/client/commands"
)

// errStreamClosed is returned when a typed send is interrupted by the response
// stream ending. The message that could not be delivered is not retried.
var errStreamClosed = errors.New("response stream closed")

// PortfolioUpdateKind identifies a portfolio-related TRANSAQ message.
type PortfolioUpdateKind string

const (
	UpdateClient          PortfolioUpdateKind = "client"
	UpdateSecurities      PortfolioUpdateKind = "securities"
	UpdatePositions       PortfolioUpdateKind = "positions"
	UpdateUnitedPortfolio PortfolioUpdateKind = "united_portfolio"
	UpdateUnitedLimits    PortfolioUpdateKind = "united_limits"
)

// PortfolioUpdate is a race-safe copy of one portfolio message. Exactly one
// payload field is set according to Kind. Raw keeps the original XML and
// ReceivedAt is the UTC time the message was decoded.
type PortfolioUpdate struct {
	Kind            PortfolioUpdateKind
	ReceivedAt      time.Time
	Raw             string
	Client          *commands.Client
	Securities      *commands.Securities
	Positions       *commands.Positions
	UnitedPortfolio *commands.UnitedPortfolio
	UnitedLimits    *commands.UnitedLimits
}

// PortfolioUpdates returns the receive-only portfolio stream. It is nil when
// the client was created without Options.PortfolioBuffer. The channel is
// closed when the response stream ends.
func (tc *TCClient) PortfolioUpdates() <-chan PortfolioUpdate {
	return tc.portfolioUpdates
}

func (tc *TCClient) sendPortfolioUpdate(update PortfolioUpdate) error {
	if tc.portfolioUpdates == nil {
		return nil
	}
	return sendTyped(tc, tc.portfolioUpdates, update)
}

func (tc *TCClient) closePortfolioUpdates() {
	if tc.portfolioUpdates == nil {
		return
	}
	tc.closeUpdatesOnce.Do(func() {
		close(tc.portfolioUpdates)
	})
}

func sendTyped[T any](tc *TCClient, ch chan T, value T) error {
	select {
	case ch <- value:
		return nil
	case <-tc.done():
		return tc.contextErr()
	case <-tc.shutdownChan():
		return errStreamClosed
	}
}

func (tc *TCClient) contextErr() error {
	if tc.ctx == nil {
		return errStreamClosed
	}
	return tc.ctx.Err()
}

func (tc *TCClient) shutdownChan() <-chan struct{} {
	return tc.shutdown
}

func (tc *TCClient) closeShutdown() {
	if tc.shutdown == nil {
		return
	}
	tc.closeShutdownOnce.Do(func() {
		close(tc.shutdown)
	})
}
