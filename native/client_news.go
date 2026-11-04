package native

import (
	"context"
	"fmt"

	"github.com/kmlebedev/txmlconnector/native/model"
)

// Task 24 interfaces (plan): RQ_GETNEWS payload, R_NEWS/R_NEWS_UPLOAD bodies and
// the <news_header>/<news_body> XML forms are not recovered yet (I).
var errNewsLayout = fmt.Errorf("layout not recovered (I)")

// OldNews is get_old_news; the headers arrive as model.NewsHeader events.
func (c *Client) OldNews(ctx context.Context, count int) error {
	return fmt.Errorf("transaq: get_old_news %w", errNewsLayout)
}

// NewsBody is get_news_body.
func (c *Client) NewsBody(ctx context.Context, id int64) (model.NewsBody, error) {
	return model.NewsBody{}, fmt.Errorf("transaq: get_news_body %w", errNewsLayout)
}
