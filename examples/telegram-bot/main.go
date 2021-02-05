package main

import (
	"fmt"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/kmlebedev/txmlconnector/client"
	"github.com/kmlebedev/txmlconnector/client/commands"
	log "github.com/sirupsen/logrus"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvKeyTgbotToken          = "BOT_API_TOKEN"
	EnvKeyTgbotAccessUsername = "BOT_ACCESS_USERNAME"
	EnvKeyLogLevel            = "LOG_LEVEL"
	TgbotTimeout              = 60
)

type GetHistoryData struct {
	period int
	secid  int
}

func main() {
	if lvl, err := log.ParseLevel(os.Getenv(EnvKeyLogLevel)); err == nil {
		log.SetLevel(lvl)
	}
	tc, err := tcClient.NewTCClient()
	if err != nil {
		log.Panic(err)
	}
	defer tc.Disconnect()
	bot, err := tgbotapi.NewBotAPI(os.Getenv(EnvKeyTgbotToken))
	if err != nil {
		log.Panic(err)
	}
	if log.GetLevel() == log.DebugLevel {
		bot.Debug = true
	}
	chatId := int64(0)
	positions := commands.Positions{}
	postmarket := make(map[string]commands.Candle)
	secs := make(map[string]commands.Security)
	total := make(map[string]float64)
	go func() {
		for {
			select {
			case status := <-tc.ServerStatusChan:
				{
					if status.Connected != "true" {
						// Todo try reconect
						log.Fatalf("txmlconnector not connected %+v", status)
					}
				}
			case resp := <-tc.ResponseChannel:
				msg := tgbotapi.NewMessage(chatId, "")
				msg.ParseMode = "markdown"
				log.Infoln("receive ", resp)
				switch resp {
				case "united_portfolio":
					msg.Text = fmt.Sprintf("UnitedPortfolio: ```\n%+v\n```", tc.Data.UnitedPortfolio)
				case "united_equity":
					msg.Text = fmt.Sprintf("UnitedEquity: ```\n%+v\n```", tc.Data.UnitedEquity)
				case "positions":
					// Todo avoid overwrite if only change field
					if tc.Data.Positions.UnitedLimits != nil && len(tc.Data.Positions.UnitedLimits) > 0 {
						positions.UnitedLimits = tc.Data.Positions.UnitedLimits
					}
					if tc.Data.Positions.SecPositions != nil && len(tc.Data.Positions.SecPositions) > 0 {
						positions.SecPositions = tc.Data.Positions.SecPositions
					}
					if tc.Data.Positions.FortsMoney != nil && len(tc.Data.Positions.FortsMoney) > 0 {
						positions.FortsMoney = tc.Data.Positions.FortsMoney
					}
					if tc.Data.Positions.MoneyPosition != nil && len(tc.Data.Positions.MoneyPosition) > 0 {
						positions.MoneyPosition = tc.Data.Positions.MoneyPosition
					}
					if tc.Data.Positions.FortsPosition != nil && len(tc.Data.Positions.FortsPosition) > 0 {
						positions.FortsPosition = tc.Data.Positions.FortsPosition
					}
					if tc.Data.Positions.FortsCollaterals != nil && len(tc.Data.Positions.FortsCollaterals) > 0 {
						positions.FortsCollaterals = tc.Data.Positions.FortsCollaterals
					}
					if tc.Data.Positions.SpotLimit != nil && len(tc.Data.Positions.SpotLimit) > 0 {
						positions.SpotLimit = tc.Data.Positions.SpotLimit
					}
					log.Debugf(fmt.Sprintf("Positions: \n%+v\n", tc.Data.Positions))
				case "candles":
					for _, candle := range tc.Data.Candles.Items {
						if candle.Date == "29.01.2021 18:45:00" {
							log.Infof(fmt.Sprintf("candle: \n%s %+v\n", tc.Data.Candles.SecCode, candle))
							postmarket[tc.Data.Candles.SecCode] = candle
							total[tc.Data.Candles.SecCode] = candle.Open * float64(candle.Volume*int64(secs[tc.Data.Candles.SecCode].LotSize))
							break
						}
					}
				default:
					msg.Text = fmt.Sprintf("receive %s", resp)
				}
				if chatId != 0 && msg.Text != "" {
					bot.Send(msg)
				}
			}
		}
	}()
	u := tgbotapi.NewUpdate(0)
	u.Timeout = TgbotTimeout
	botUpdates, err := bot.GetUpdatesChan(u)
	getHistoryDataDict := make(map[int]*GetHistoryData)

	for update := range botUpdates {
		fromUserName := ""
		if update.Message != nil {
			fromUserName = update.Message.From.UserName
			chatId = update.Message.Chat.ID
		}
		if update.CallbackQuery != nil {
			fromUserName = update.CallbackQuery.From.UserName
			chatId = update.CallbackQuery.Message.Chat.ID
		}
		// ignore any message not from you
		if fromUserName != os.Getenv(EnvKeyTgbotAccessUsername) {
			log.Warnf("ignore message from: %s", fromUserName)
			continue
		}

		msg := tgbotapi.NewMessage(chatId, "")
		msg.ParseMode = "markdown"

		if update.Message != nil && update.Message.IsCommand() {
			log.Debugf("cmd: %s", update.Message.Command())
			switch update.Message.Command() {
			case "start":
				/*
					btns := []tgbotapi.KeyboardButton{
						tgbotapi.NewKeyboardButton("/Портфель"),
						tgbotapi.NewKeyboardButton("/Заявки"),
						tgbotapi.NewKeyboardButton("/Сделки"),
					}
					msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(btns)
				*/
				msg.Text = "Привет!, Я бот от transaq терминала. Я могу Вам помочь автоматизировать торговлю и вести торговый дневник."
			case "positions":
				msg.Text = fmt.Sprintf("```\n%+v\n```", positions)
			case "get_united_portfolio":
				tc.SendCommand(commands.Command{Id: "get_united_portfolio", Union: tc.Data.Unions[0].Id})
			case "get_united_equity":
				tc.SendCommand(commands.Command{Id: "get_united_equity", Union: tc.Data.Unions[0].Id})
			case "gethistorydata":
				btnsPeriod := tgbotapi.NewInlineKeyboardRow()
				msg.Text = "задайте код инструмента и период свечей"
				for _, candleKind := range tc.Data.CandleKinds.Items {
					log.Debugln(candleKind.Name)
					btnsPeriod = append(btnsPeriod, tgbotapi.NewInlineKeyboardButtonData(candleKind.Name,
						fmt.Sprintf("gethistorydata:period:%d", candleKind.ID)))
				}
				btnsSec := tgbotapi.NewInlineKeyboardRow()
				for _, sec := range positions.SecPositions {
					log.Debugln(sec.SecInfo)
					btnsSec = append(btnsSec, tgbotapi.NewInlineKeyboardButtonData(sec.Shortname,
						fmt.Sprintf("gethistorydata:secid:%d", sec.SecId)))
				}
				getHistoryDataDict[update.Message.MessageID] = &GetHistoryData{}
				msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(btnsSec, btnsPeriod)
				msg.ReplyToMessageID = update.Message.MessageID
			case "postmarket":
				for _, sec := range tc.Data.Securities.Items {
					if sec.Board != "TQBR" {
						continue
					}
					if sec.SecId == 0 {
						continue
					}
					secs[sec.SecCode] = sec
					tc.SendCommand(commands.Command{
						Id:     "gethistorydata",
						Period: 1,
						SecId:  sec.SecId,
						Count:  400,
						Reset:  true,
					})
				}
				time.Sleep(5 * time.Second)
				o := ""
				for k, t := range total {
					o = o + fmt.Sprintf("%s %f\n", k, t)
				}
				msg.Text = fmt.Sprintf("postmarket %+v", o)
			case "orders":
				msg.Text = "orders"
			case "deals":
				msg.Text = "deals"
			}
		}

		// Buttons
		if update.CallbackQuery != nil && update.CallbackQuery.Data != "" {
			log.Debugf("CallbackQuery: %s", update.CallbackQuery.Data)
			data := strings.Split(update.CallbackQuery.Data, ":")
			switch data[0] {
			case "gethistorydata":
				msgId := update.CallbackQuery.Message.ReplyToMessage.MessageID
				_, ok := getHistoryDataDict[msgId]
				log.Debugf("getHistoryData: %+v", getHistoryDataDict[msgId])
				if !ok {
					log.Warnf("getHistoryDataDict not found message id %d",
						update.CallbackQuery.Message.ReplyToMessage.MessageID)
					break
				}
				if len(data) != 3 {
					log.Warnf("data size not eq 3,  %d", len(data))
					break
				}
				switch data[1] {
				case "period":
					period, err := strconv.Atoi(data[2])
					if err != nil {
						break
					}
					getHistoryDataDict[msgId].period = period
					log.Debugf("get period %d", period)
				case "secid":
					secid, err := strconv.Atoi(data[2])
					if err != nil {
						break
					}
					getHistoryDataDict[msgId].secid = secid
					log.Debugf("get seccode %d", secid)
				}
				if getHistoryDataDict[msgId].secid != 0 && getHistoryDataDict[msgId].period != 0 {
					tc.SendCommand(commands.Command{
						Id:     "gethistorydata",
						Period: getHistoryDataDict[msgId].period,
						SecId:  getHistoryDataDict[msgId].secid,
						Count:  1000,
						Reset:  true,
					})
					log.Debugf("SendCommand gethistorydata")
				}
			}
		}
		bot.Send(msg)
	}
	// <- tc.ShutdownChannel
}
