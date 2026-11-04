package model

// Symbol identifies a security on a board, the stable key across sessions
// (the session-local secid is not).
type Symbol struct {
	Board   string
	SecCode string
}

func (s Symbol) String() string { return s.Board + ":" + s.SecCode }
