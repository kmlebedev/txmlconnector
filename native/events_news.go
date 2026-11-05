package native

import (
	"time"

	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
)

// News feed: R_NEWS 0x35 and R_NEWS_UPLOAD 0x36 are unsized records with
// variable C-strings (news.md, D/O). The 0x35 headers are reported as
// <news_header>; the 0x36 header form is reported too, while the packed body is
// only stored: the O run shows no <news_body> callback (I until task-56).
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
		Source: parsed.Source,
		Title:  parsed.Title,
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
			Source: parsed.Header.Source,
			Title:  parsed.Header.Title,
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
		if _, ok := c.news[id]; ok {
			if c.newsBodies == nil {
				c.newsBodies = map[int64]string{}
			}
			c.newsBodies[id] = string(text)
		}
		// S (production): the body arrives as RQ_MAIN 0x36 flags=2 and is
		// rendered as <news_body> (the reply RQ_GETNEWS 73 is empty).
		body := model.NewsBody{ID: id, Text: records.DecodeCP1251(string(text))}
		events = append(events, &body)
	}
	return events, nil
}
