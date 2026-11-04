package native

import (
	"context"
	"errors"
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// ServerTimeDifference is the DLL's get_servtime_difference: it sends RQ_TIME
// and returns the server clock minus the local clock at that moment, so that a
// caller can correct the local time by the server one.
func (c *Client) ServerTimeDifference(ctx context.Context) (time.Duration, error) {
	conn := c.connection()
	if conn == nil {
		return 0, errors.New("transaq: client is not connected")
	}
	st, err := conn.Time(ctx)
	if err != nil {
		return 0, err
	}
	return st.Wall.Sub(time.Now().UTC()), nil
}

// ServerID returns the SID from the RQ_PWD reply, the server id the DLL reports
// in server_status (get_server_id answers it from the cache).
func (c *Client) ServerID() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.info == nil {
		return 0
	}
	return int(c.info.SID)
}

// Markets returns a copy of the market dictionary from RQ_STARTUP, the data the
// DLL's get_markets command answers from its cache.
func (c *Client) Markets() []model.Market {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.catalog == nil {
		return nil
	}
	startup := c.catalog.Startup()
	if startup == nil {
		return nil
	}
	return append([]model.Market(nil), startup.Markets...)
}
