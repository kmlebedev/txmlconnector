package main

import (
	"bytes"
	"database/sql"
	"github.com/360EntSecGroup-Skylar/excelize/v2"
	"github.com/gocolly/colly/v2"
	log "github.com/sirupsen/logrus"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var (
	exportGoodsUrl = "https://customs.gov.ru/statistic/eksport-rossii-vazhnejshix-tovarov"
	exportSubRFUrl = "https://customs.gov.ru/folder/527"
	monthsToNum    = map[string]time.Month{
		"январь":   time.January,
		"февраль":  time.February,
		"март":     time.March,
		"апрель":   time.April,
		"май":      time.May,
		"июнь":     time.June,
		"июль":     time.July,
		"август":   time.August,
		"сентябрь": time.September,
		"октябрь":  time.October,
		"ноябрь":   time.November,
		"декабрь":  time.December,
	}
)

func loadCustomsGov(conn *sql.DB, sheetUrl string, date time.Time) error {
	resp, err := http.Get(sheetUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	reader := bytes.NewReader(body)
	xlsxFile, err := excelize.OpenReader(reader)
	if err != nil {
		return err
	}
	rows, err := xlsxFile.GetRows(xlsxFile.GetSheetName(0))
	if err != nil {
		return err
	}
	var tx, _ = conn.Begin()
	var stmt, _ = tx.Prepare("INSERT INTO export_goods (*) VALUES (?, ?, ?, ?, ?)")
	for _, row := range rows {
		if row[0] == "" || row[1] == "" || row[6] == "" || row[7] == "" {
			continue
		}
		quantity, err := strconv.ParseFloat(row[6], 32)
		if err != nil {
			continue
		}
		value, err := strconv.ParseFloat(row[7], 32)
		if err != nil {
			continue
		}
		if _, err := stmt.Exec(row[0], row[1], date, quantity, value); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func crawExports(conn *sql.DB) error {
	c := colly.NewCollector()
	c.OnHTML(".file-download__item", func(e *colly.HTMLElement) {
		article := e.ChildText(".file-download__item-article")
		filePath := e.ChildAttr(".file-download__item-link a", "href")
		if !strings.HasSuffix(filePath, ".xlsx") {
			return
		}
		log.Info(article)
		as := strings.Split(article, " ")
		y, _ := strconv.Atoi(as[len(as)-1])
		t := time.Date(y, monthsToNum[as[len(as)-2]], 1, 0, 0, 0, 0, time.UTC)
		if err := loadCustomsGov(conn, e.Request.URL.Scheme+"://"+e.Request.URL.Host+filePath, t); err != nil {
			log.Error(err)
		}
	})
	c.OnHTML(".pagination__link", func(e *colly.HTMLElement) {
		if strings.Contains(e.Attr("class"), "active") {
			return
		}
		link := e.ChildAttr("a", "href")
		log.Debugf("Link found: %q -> %s\n", e.Text, link)
		e.Request.Visit(link)
	})
	if err := c.Visit(exportGoodsUrl); err != nil {
		log.Error(err)
	}
	if err := c.Visit(exportSubRFUrl); err != nil {
		log.Error(err)
	}
	return nil
}
