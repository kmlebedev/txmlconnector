package tcClient

import (
	"context"
	"errors"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// ClientFactory creates a connected TRANSAQ client for a supervised session.
type ClientFactory func() (*TCClient, error)

// SessionFunc runs application work for one client session. Returning an error
// recreates the client; returning nil stops the supervisor.
type SessionFunc func(context.Context, *TCClient) error

// ReconnectConfig controls client recreation after a session or transport
// failure. Zero duration values use the defaults returned by
// DefaultReconnectConfig. A negative DisconnectTimeout skips the graceful
// disconnect command and closes the client immediately.
type ReconnectConfig struct {
	RetryMin           time.Duration
	RetryMax           time.Duration
	SessionStableAfter time.Duration
	DisconnectTimeout  time.Duration
}

// DefaultReconnectConfig returns conservative reconnect settings suitable for
// long-running clients.
func DefaultReconnectConfig() ReconnectConfig {
	return ReconnectConfig{
		RetryMin:           time.Second,
		RetryMax:           30 * time.Second,
		SessionStableAfter: time.Minute,
		DisconnectTimeout:  2 * time.Second,
	}
}

// RunWithReconnect recreates a TCClient whenever runSession returns an error.
// Retry delays grow exponentially up to RetryMax and reset after a stable
// session. Context cancellation is treated as a graceful shutdown.
func RunWithReconnect(
	ctx context.Context,
	newClient ClientFactory,
	runSession SessionFunc,
	config ReconnectConfig,
) error {
	if ctx == nil {
		return errors.New("reconnect context is required")
	}
	if newClient == nil {
		return errors.New("TRANSAQ client factory is required")
	}
	if runSession == nil {
		return errors.New("TRANSAQ session callback is required")
	}
	config = normalizeReconnectConfig(config)

	retryDelay := config.RetryMin
	for {
		if ctx.Err() != nil {
			return nil
		}

		client, err := newClient()
		if err != nil {
			if client != nil {
				client.Close()
			}
			log.Warnf("Create TRANSAQ client: %v; retry in %s", err, retryDelay)
			if waitForContext(ctx, retryDelay) != nil {
				return nil
			}
			retryDelay = nextRetryDelay(retryDelay, config.RetryMax)
			continue
		}
		if client == nil {
			log.Warnf("Create TRANSAQ client: factory returned nil; retry in %s", retryDelay)
			if waitForContext(ctx, retryDelay) != nil {
				return nil
			}
			retryDelay = nextRetryDelay(retryDelay, config.RetryMax)
			continue
		}

		sessionStarted := time.Now()
		err = runSession(ctx, client)
		closeReconnectClient(client, config.DisconnectTimeout)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			return nil
		}
		if time.Since(sessionStarted) >= config.SessionStableAfter {
			retryDelay = config.RetryMin
		}

		log.Warnf("TRANSAQ session stopped: %v; recreate client in %s", err, retryDelay)
		if waitForContext(ctx, retryDelay) != nil {
			return nil
		}
		retryDelay = nextRetryDelay(retryDelay, config.RetryMax)
	}
}

func normalizeReconnectConfig(config ReconnectConfig) ReconnectConfig {
	defaults := DefaultReconnectConfig()
	if config.RetryMin <= 0 {
		config.RetryMin = defaults.RetryMin
	}
	if config.RetryMax <= 0 {
		config.RetryMax = defaults.RetryMax
	}
	if config.RetryMax < config.RetryMin {
		config.RetryMax = config.RetryMin
	}
	if config.SessionStableAfter <= 0 {
		config.SessionStableAfter = defaults.SessionStableAfter
	}
	if config.DisconnectTimeout == 0 {
		config.DisconnectTimeout = defaults.DisconnectTimeout
	}
	return config
}

func closeReconnectClient(client *TCClient, timeout time.Duration) {
	if client == nil {
		return
	}
	if timeout < 0 {
		client.Close()
		return
	}

	disconnected := make(chan error, 1)
	go func() {
		disconnected <- client.Disconnect()
	}()
	select {
	case err := <-disconnected:
		if err != nil {
			log.Warnf("Disconnect TRANSAQ client: %v", err)
		}
	case <-time.After(timeout):
		log.Warnf("Disconnect TRANSAQ client timed out after %s", timeout)
	}
	client.Close()
}

func nextRetryDelay(current, maximum time.Duration) time.Duration {
	if current >= maximum/2 {
		return maximum
	}
	return current * 2
}

func waitForContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("wait interrupted: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
