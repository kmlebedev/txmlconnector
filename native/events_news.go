package native

import (
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// News feed: R_NEWS 0x35 and R_NEWS_UPLOAD 0x36 are unsized records with
// variable C-strings (news.md, D/O). The 0x35 headers are reported as
// <news_header>; 0x36 flags=1 headers and flags=2 bodies are S-confirmed. A body
// is emitted only for the requested header (pending get_news_body) and known id.
func init() {
	registerDecoder(records.TagNews, decodeNewsHeader)
	registerDecoder(records.TagNewsUpload, decodeNewsUpload)
}

// storeNewsHeaderLocked keeps the header store; decoders run under Client.mu.
func (c *Client) storeNewsHeaderLocked(header model.NewsHeader) {
	if c.news == nil {
		c.news = map[int64]model.NewsHeader{}
	}
	if _, seen := c.news[header.ID]; !seen {
		c.newsOrder = append(c.newsOrder, header.ID)
	}
	c.news[header.ID] = header
}

func decodeNewsHeader(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseNewsHeader(body)
	if err != nil {
		return nil, err
	}
	header := model.NewsHeader{
		ID:     parsed.ID,
		Time:   time.Unix(parsed.Timestamp, 0).UTC(),
		Source: records.DecodeCP1251(parsed.Source),
		Title:  records.DecodeCP1251(parsed.Title),
		Flag:   parsed.Flag,
	}
	c.storeNewsHeaderLocked(header)
	return []model.Event{&header}, nil
}

func decodeNewsUpload(c *Client, body []byte) ([]model.Event, error) {
	parsed, err := records.ParseNewsUpload(body)
	if err != nil {
		return nil, err
	}
	var events []model.Event
	if parsed.Header != nil {
		header := model.NewsHeader{
			ID:     parsed.Header.ID,
			Time:   time.Unix(parsed.Header.Timestamp, 0).UTC(),
			Source: records.DecodeCP1251(parsed.Header.Source),
			Title:  records.DecodeCP1251(parsed.Header.Title),
			Flag:   parsed.Header.Flag,
		}
		c.storeNewsHeaderLocked(header)
		events = append(events, &header)
	}
	if len(parsed.Package) > 0 {
		text, err := records.UnpackNewsBody(parsed.Package)
		if err != nil {
			// DLL logs "Failed to unpack body!" and drops the element; a bad
			// package must not abort the poll/Run stream.
			return events, nil
		}
		id := parsed.NewsID
		if parsed.Header != nil {
			id = parsed.Header.ID
		}
		utf8Text := records.DecodeCP1251(string(text))
		if c.newsBodies == nil {
			c.newsBodies = map[int64]string{}
		}
		// The body is stored only when its header is known (P3: silent store).
		if _, ok := c.news[id]; ok {
			c.newsBodies[id] = utf8Text
		}
		// P2: emit only for the S channel flags=2 AND when a get_news_body
		// request is pending and its header is known; flags=3/unknown header are
		// silently stored (news.md).
		requested := c.newsBodyRequest
		if parsed.Flags&2 != 0 && requested != 0 {
			if _, ok := c.news[requested]; ok {
				c.newsBodyRequest = 0
				body := model.NewsBody{ID: id, Text: utf8Text}
				events = append(events, &body)
			}
		}
	}
	return events, nil
}
