package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	"github.com/gocolly/colly/v2"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// https://www.vtb.ru/akcionery-i-investory/finansovaya-informaciya/raskrytie-finansovyh-rezultatov-po-msfo-na-ezhemesyachnoy-osnove/

var (
	financeResultsIFRSUrl = "https://www.vtb.ru/akcionery-i-investory/finansovaya-informaciya/raskrytie-finansovyh-rezultatov-po-msfo-na-ezhemesyachnoy-osnove/"
	financeResultsRSBUrl  = "https://www.vtb.ru/akcionery-i-investory/finansovaya-informaciya/raskrytie-finansovoj-otchetnosti-po-rsbu-ezhemesyachno/"
	segmentTables         = []string{"Отчет о прибылях и убытках", "Отчет о финансовом положении"}
)

func loadFinanceResults(conn *sql.DB, sheetUrl string, date time.Time, standard string, double bool) error {
	resp, err := http.Get(sheetUrl)
	if err != nil {
		return err
	}
	log.Info(sheetUrl)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	pubDate, err := time.Parse(time.RFC1123, resp.Header.Get("Last-Modified"))
	if err != nil {
		return err
	}
	reader := bytes.NewReader(body)
	xlsxFile, err := excelize.OpenReader(reader)
	if err != nil {
		return err
	}
	var tx_financeResults, _ = conn.Begin()
	var stmt_financeResults, _ = tx_financeResults.Prepare("INSERT INTO finance_results (*) VALUES (?, ?, ?, ?, ?, ?)")

	for _, sheetName := range xlsxFile.GetSheetMap() {
		// log.Info(sheetName)
		rows, err := xlsxFile.GetRows(sheetName)
		if err != nil {
			return err
		}
		tableColumns := make(map[int]string)
		segmentTableCol := 0
		for _, row := range rows {
			if len(row) < 3 {
				continue
			}
			isSegmentTable := false
			if strings.Contains(sheetName, "Сегментный анализ") {
				for i, col := range row {
					for _, table := range segmentTables {
						if strings.Contains(col, table) {
							isSegmentTable = true
							segmentTableCol = i
							tableColumns = make(map[int]string)
						}
					}
					if isSegmentTable && len(col) > 0 {
						tableColumns[i] = col
					}
				}
				if len(tableColumns) > 0 {
					for i, colName := range tableColumns {
						if row[i] == "-" {
							row[i] = "0"
						}
						value, verr := strconv.ParseFloat(row[i], 32)
						if verr != nil {
							continue
						}
						if _, err := stmt_financeResults.Exec(
							"VTBR",
							standard,
							strings.TrimSpace(row[segmentTableCol]),
							strings.TrimSpace(colName),
							pubDate,
							date,
							value); err != nil {
							return err
						}
					}
					continue
				}
			}
			if len(row[0]) == 0 || len(row[1]) == 0 {
				continue
			}
			value, verr := strconv.ParseFloat(row[1], 32)
			if verr != nil {
				continue
			}
			if double {
				value = value / 2
				if _, err := stmt_financeResults.Exec(
					"VTBR",
					standard,
					strings.TrimSpace(sheetName),
					strings.TrimSpace(row[0]),
					pubDate,
					date.AddDate(0, -1, 0),
					value); err != nil {
					return err
				}
			}
			if _, err := stmt_financeResults.Exec(
				"VTBR",
				standard,
				strings.TrimSpace(sheetName),
				strings.TrimSpace(row[0]),
				pubDate,
				date,
				value); err != nil {
				return err
			}
		}
	}
	if err := tx_financeResults.Commit(); err != nil {
		return err
	}
	return nil
}

func crawFinanceResults(conn *sql.DB) error {
	c := colly.NewCollector()
	c.OnHTML(".padding-slim .docs-items .docs-items__doc_xls", func(e *colly.HTMLElement) {
		article := e.ChildText("div")
		filePath := e.Attr("href")
		if !strings.HasSuffix(filePath, ".xlsx") {
			log.Errorf("xlsx not found is path: %v", filePath)
			return
		}
		log.Info(article)
		double := false
		// Неаудированные ключевые показатели группы ВТБ по МСФО за 2 месяца 2020 года
		dater := regexp.MustCompile(".* (РСБУ|МСФО) .* (\\d+) (месяцев|месяца) (\\d+) года.*")
		dateFind := dater.FindStringSubmatch(article)
		if len(dateFind) == 0 {
			log.Error(fmt.Errorf("дата не найдена"))
			return
		}
		m, err := strconv.Atoi(dateFind[2])
		if err != nil {
			log.Error(err)
		}
		y, err := strconv.Atoi(dateFind[4])
		if err != nil {
			log.Error(err)
		}
		t := time.Date(y, time.Month(m+1), -1, 0, 0, 0, 0, time.UTC)
		if m == 2 && dateFind[1] == "МСФО" {
			double = true
		}
		if err := loadFinanceResults(conn, e.Request.URL.Scheme+"://"+e.Request.URL.Host+filePath, t, dateFind[1], double); err != nil {
			log.Error(err)
		}
	})
	if err := c.Visit(financeResultsIFRSUrl); err != nil {
		log.Error(err)
	}
	if err := c.Visit(financeResultsRSBUrl); err != nil {
		log.Error(err)
	}
	return nil
}
