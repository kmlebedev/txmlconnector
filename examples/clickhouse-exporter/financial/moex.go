package main

import (
	"database/sql"
	"time"
)

var (
	finResultsIFRSUrl = "https://fs.moex.com/files/16239" // ifrs-databook-3q-2021.xlsx https://www.moex.com/s1347
	tradingVolumesUrl = "https://fs.moex.com/files/4243/" // trading-volumes-2021-nov.xlsx https://www.moex.com/s868
)

// Todo interface
func mLoadFinanceResults(conn *sql.DB, sheetUrl string, date time.Time, standard string, double bool) error {
	return nil
}
