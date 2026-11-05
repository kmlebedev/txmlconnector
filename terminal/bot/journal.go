package bot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// journalStore is the bounded in-memory journal of one bot with an optional
// JSONL mirror under <dir>/bots/<id>.jsonl (plan Task 22).
type journalStore struct {
	mu      sync.Mutex
	entries []JournalEntry
	nextSeq uint64
	file    *os.File
}

func newJournalStore() *journalStore { return &journalStore{} }

// append adds one entry with the next monotonic seq, keeps the ring bounded to
// journalLimit and mirrors the entry to the JSONL file when configured.
func (j *journalStore) append(entry JournalEntry) JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.nextSeq++
	entry.Seq = j.nextSeq
	j.entries = append(j.entries, entry)
	if len(j.entries) > journalLimit {
		j.entries = append([]JournalEntry(nil), j.entries[len(j.entries)-journalLimit:]...)
	}
	if j.file != nil {
		if raw, err := json.Marshal(entry); err == nil {
			_, _ = j.file.Write(append(raw, '\n'))
		}
	}
	return entry
}

// after returns the entries with Seq > afterSeq (newest last), at most limit
// records (limit <= 0 means all).
func (j *journalStore) after(afterSeq uint64, limit int) []JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]JournalEntry, 0, len(j.entries))
	for _, entry := range j.entries {
		if entry.Seq <= afterSeq {
			continue
		}
		out = append(out, entry)
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// counts returns how many journal entries of every kind are kept.
func (j *journalStore) counts() map[string]int {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make(map[string]int, 8)
	for _, entry := range j.entries {
		out[entry.Kind]++
	}
	return out
}

// openFile enables the JSONL mirror at path, creating the parent directory.
func (j *journalStore) openFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	j.mu.Lock()
	old := j.file
	j.file = file
	j.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func (j *journalStore) close() error {
	j.mu.Lock()
	file := j.file
	j.file = nil
	j.mu.Unlock()
	if file == nil {
		return nil
	}
	return file.Close()
}
