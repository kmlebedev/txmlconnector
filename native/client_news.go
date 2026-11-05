package native

import (
	"context"
	"errors"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/session"
	"github.com/kmlebedev/txmlconnector/native/wire"
)

// errNewsBodyI says the request was sent but the body reply/emit mechanics are
// not recovered yet (news.md I). The Connector maps it to the DLL command
// success (the O run shows no <news_body> callback).
var errNewsBodyI = errors.New("news body reply is not recovered (I)")

func (c *Client) newsPayload(from, to int64, count, mode byte) []byte {
	var w wire.Writer
	w.Int32(int32(from))
	w.Int32(int32(to))
	w.Byte(count)
	w.Byte(mode)
	return w.Bytes()
}

// newsHeader returns one stored header (news store populated by 0x35/0x36).
func (c *Client) newsHeader(id int64) (model.NewsHeader, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	header, ok := c.news[id]
	return header, ok
}

// newsRange implements the RQ_GETNEWS from/to rule of news.md: the anchor is the
// MINIMUM stored header id (D: std::map first node; O: {1001,1500} -> 999/1001).
// An empty store sends 0..0x7fffffff.
func (c *Client) newsRange(count int) (from, to int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.news) == 0 {
		return 0, 0x7fffffff
	}
	first := true
	for id := range c.news {
		if first || id < to {
			to = id
			first = false
		}
	}
	from = to - int64(count)
	if from < 0 {
		from = 0
	}
	return from, to
}

// OldNews sends get_old_news (RQ_GETNEWS 73, mode 1); the headers arrive
// asynchronously as 0x35/0x36 RQ_MAIN records (news.md, D/O).
func (c *Client) OldNews(ctx context.Context, count int) error {
	if count < 1 || count > 100 {
		return fmt.Errorf("News count must be in interval from 1 to 100. Received value is %d", count)
	}
	conn := c.connection()
	if conn == nil {
		return errors.New("transaq: client is not connected")
	}
	from, to := c.newsRange(count)
	if _, err := conn.Request(ctx, session.RqGetNews, c.newsPayload(from, to, byte(count), 1)); err != nil {
		return fmt.Errorf("RQ_GETNEWS: %w", err)
	}
	return nil
}

// NewsBody sends get_news_body (RQ_GETNEWS 73, mode 2). The request itself is
// D+O, but the reply/emit path is not recovered (news.md I): after sending the
// method returns errNewsBodyI, never a fabricated body.
func (c *Client) NewsBody(ctx context.Context, id int64) (model.NewsBody, error) {
	if _, ok := c.newsHeader(id); !ok {
		return model.NewsBody{}, fmt.Errorf("Header with id = %d not found", id)
	}
	conn := c.connection()
	if conn == nil {
		return model.NewsBody{}, errors.New("transaq: client is not connected")
	}
	if _, err := conn.Request(ctx, session.RqGetNews, c.newsPayload(id, id, 1, 2)); err != nil {
		return model.NewsBody{}, fmt.Errorf("RQ_GETNEWS: %w", err)
	}
	return model.NewsBody{}, errNewsBodyI
}
