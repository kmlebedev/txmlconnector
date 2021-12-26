package main

import (
	"database/sql"
	"time"
)

type DataBook interface {
	Import(conn *sql.DB, sheetUrl string, date time.Time, standard string, double bool) error
}
