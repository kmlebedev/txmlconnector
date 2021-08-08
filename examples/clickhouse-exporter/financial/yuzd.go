package main

import (
	"database/sql"
	"fmt"
	"github.com/gocolly/colly/v2"
	log "github.com/sirupsen/logrus"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	//rzdNewsUrl = "https://%s.rzd.ru/ru/%s/page/13307?f810_pagesize=100&&date_publication_0=&date_publication_1=&rubricator_id=&text_search=%s&f810_pagenumber=1"
	rzdNewsUrl = "https://%s.rzd.ru/ru/%s?f810_pagesize=100&&date_publication_0=&date_publication_1=&rubricator_id=&text_search=%s&f810_pagenumber=1"
)

type TimeSlice []time.Time

// Forward request for length
func (p TimeSlice) Len() int {
	return len(p)
}

// Define compare
func (p TimeSlice) Less(i, j int) bool {
	return p[i].Before(p[j])
}

// Define swap over an array
func (p TimeSlice) Swap(i, j int) {
	p[i], p[j] = p[j], p[i]
}

func craw(conn *sql.DB, name string, id string, search string) error {
	loading := make(map[time.Time]float32)
	c := colly.NewCollector()
	c.OnHTML(".news-card", func(e *colly.HTMLElement) {
		t, err := time.Parse("02-01-2006", strings.ReplaceAll(e.ChildText(".news-card-datetime .text-red"), ".", "-"))
		if err != nil {
			log.Error(err)
			return
		}
		text := e.ChildText(".news-card-text")
		rm := regexp.MustCompile("черных металлов . ([\\d,]+) млн")
		rk := regexp.MustCompile("черных металлов . ([\\d,]+) тыс")
		rmFind := rm.FindStringSubmatch(text)
		rkFind := rk.FindStringSubmatch(text)
		isInmln := true
		var value string
		if len(rmFind) > 0 {
			value = rmFind[1]
		} else if len(rkFind) > 0 && len(rmFind) == 0 {
			value = rkFind[1]
			isInmln = false
		} else {
			log.Warn("черные металлы не найдены")
			return
		}
		if s, err := strconv.ParseFloat(strings.Replace(value, ",", ".", 1), 32); err == nil {
			if isInmln {
				loading[t] = float32(s)
			} else {
				loading[t] = float32(s) / 1000
			}
		}
	})
	c.OnHTML(".search-results__heading", func(e *colly.HTMLElement) {
		link := e.Attr("href")
		log.Debugf("Link found: %q -> %s\n", e.Text, link)
		e.Request.Visit(link)
	})
	c.OnRequest(func(r *colly.Request) {
		log.Debug("Visiting ", r.URL.String())
	})
	if err := c.Visit(fmt.Sprintf(rzdNewsUrl, name, id, url.QueryEscape(search))); err != nil {
		log.Error(err)
	}
	keys := make([]time.Time, 0, len(loading))
	for k := range loading {
		keys = append(keys, k)
	}
	sort.Sort(TimeSlice(keys))
	var tx, _ = conn.Begin()
	var stmt, _ = tx.Prepare("INSERT INTO loading_rzd (*) VALUES (?, ?, ?)")
	for i, k := range keys {
		t := time.Date(k.Year(), k.Month(), 1, 0, 0, 0, 0, time.UTC)
		loadingDiff := float32(0)
		if i == 0 {
			continue
		} else if loading[k] > loading[keys[i-1]] {
			loadingDiff = loading[k] - loading[keys[i-1]]
		} else {
			log.Debugf("Погрузка на начало года %d", loading[k])
			loadingDiff = loading[k]
		}
		if _, err := stmt.Exec(name, t, loadingDiff); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
