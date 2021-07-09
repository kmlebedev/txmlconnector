package main

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	log "github.com/sirupsen/logrus"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

var (
	csvPath = "csv"
)

func init() {
	if dir := os.Getenv("FINANCIAL_CSV_DIR"); dir != "" {
		csvPath = dir
	}
}

func loadCSV(conn *sql.DB) error {
	files, err := os.ReadDir(csvPath)
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range files {
		fileName := file.Name()
		if !strings.HasSuffix(fileName, ".csv") {
			continue
		}
		csvFile, err := os.Open(path.Join(csvPath, fileName))
		if err != nil {
			return err
		}
		defer csvFile.Close()

		csvLines, err := csv.NewReader(csvFile).ReadAll()
		if err != nil {
			return err
		}
		var tx, _ = conn.Begin()
		var stmt, _ = tx.Prepare("INSERT INTO csv_closing_prices (*) VALUES (?, ?, ?)")
		for _, line := range csvLines {
			log.Debugf("'%s', '%s'", line[0], line[1])
			var f float64
			var date time.Time
			if f, err = strconv.ParseFloat(line[1], 32); err != nil {
				continue
			}
			d := strings.Split(line[0][len(line[0])-10:], ".")
			if date, err = time.Parse("2006-01-02", fmt.Sprintf("%s-%s-%s", d[2], d[1], d[0])); err != nil {
				return err
			}
			if _, err := stmt.Exec(fileName[0:len(fileName)-4], date, f); err != nil {
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
