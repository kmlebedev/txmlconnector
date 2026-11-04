package model

// TransactionID is the transaction number of RQ_TRANSACT (PDF 3.8). The DLL
// assigns its own number to every transaction; the reply carries the server
// number for a known outcome.
type TransactionID uint64
