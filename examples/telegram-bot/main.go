package main

import (
	"fmt"
	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/kmlebedev/txmlconnector/client"
	"github.com/kmlebedev/txmlconnector/client/commands"
	log "github.com/sirupsen/logrus"
	"os"
)

const (
	EnvKeyTgbotToken          = "BOT_API_TOKEN"
	EnvKeyTgbotAccessUsername = "BOT_ACCESS_USERNAME"
	EnvKeyLogLevel            = "LOG_LEVEL"
	TgbotTimeout              = 60
)

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
	select {
	case status := <-tc.ServerStatusChan:
		{
			if status.Connected != "true" {
				log.Fatalf("txmlconnector not connected %+v", status)
			}
		}
	}
	u := tgbotapi.NewUpdate(0)
	u.Timeout = TgbotTimeout
	botUpdates, err := bot.GetUpdatesChan(u)
	var chatId int64
	go func() {
		for {
			select {
			case resp := <-tc.ResponseChannel:
				msg := tgbotapi.NewMessage(chatId, "")
				msg.ParseMode = "markdown"
				if resp == "united_portfolio" {
					msg.Text = fmt.Sprintf("UnitedPortfolio: ```\n%+v\n```", tc.Data.UnitedPortfolio)
				} else if resp == "united_equity" {
					msg.Text = fmt.Sprintf("UnitedEquity: ```\n%+v\n```", tc.Data.UnitedEquity)
				} else {
					log.Infoln("received message", resp)
				}
				bot.Send(msg)
			}
		}
	}()
	for update := range botUpdates {
		if update.Message == nil { // ignore any non-Message Updates
			continue
		}
		// ignore any message not from you
		if update.Message.From.UserName != os.Getenv(EnvKeyTgbotAccessUsername) {
			continue
		}
		chatId = update.Message.Chat.ID
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")
		msg.ParseMode = "markdown"
		log.Debugf("cmd: %s", update.Message.Command())
		switch {
		case update.Message.Command() == "start":
			/*
				btns := []tgbotapi.KeyboardButton{
					tgbotapi.NewKeyboardButton("/Портфель"),
					tgbotapi.NewKeyboardButton("/Заявки"),
					tgbotapi.NewKeyboardButton("/Сделки"),
				}
				msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(btns)
			*/
			msg.Text = "Привет!, Я бот от transaq терминала. Я могу Вам помочь автоматизировать торговлю и вести торговый дневник."
		case update.Message.Command() == "positions":
			msg.Text = fmt.Sprintf("```\n%+v\n```", tc.Data.Positions)
			log.Infof("%+v", tc.Data.Positions)
		case update.Message.Command() == "get_united_portfolio":
			tc.SendCommand(commands.Command{Id: "get_united_portfolio", Union: tc.Data.Unions[0].Id})
		case update.Message.Command() == "get_united_equity":
			tc.SendCommand(commands.Command{Id: "get_united_equity", Union: tc.Data.Unions[0].Id})
		case update.Message.Command() == "orders":
			msg.Text = "orders"
		case update.Message.Command() == "deals":
			msg.Text = "deals"
		}
		bot.Send(msg)
	}
	// <- tc.ShutdownChannel
}
