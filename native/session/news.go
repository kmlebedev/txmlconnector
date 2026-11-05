package session

// RqGetNews is RQ_GETNEWS (73), used by get_old_news (mode 1) and get_news_body
// (mode 2); the request payloads are O-pinned in native/client_news.go.
const RqGetNews byte = 73
