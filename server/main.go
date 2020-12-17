//+build windows,amd64

// https://github.com/ivanantipin/transaqgrpc/blob/master/tqgrpcserver/XmlConnector.cs
package main

import "C"
import (
	"context"
	"fmt"
	"github.com/kmlebedev/txmlconnector/proto"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"
)

/*
#include <stdlib.h>
*/
import "C"

var (
	txmlconnector    = syscall.NewLazyDLL("txmlconnector64.dll")
	procSetCallback  = txmlconnector.NewProc("SetCallback")
	procSendCommand  = txmlconnector.NewProc("SendCommand")
	procFreeMemory   = txmlconnector.NewProc("FreeMemory")
	procInitialize   = txmlconnector.NewProc("Initialize")
	procUnInitialize = txmlconnector.NewProc("UnInitialize")
	procSetLogLevel  = txmlconnector.NewProc("SetLogLevel")
	Messages         = make(chan string)
	Done             = make(chan bool)
)

type server struct {
	transaqConnector.UnimplementedConnectServiceServer
}

//export receiveData
func receiveData(cmsg *C.char) (ret uintptr) {
	msg := C.GoString(cmsg)
	fmt.Printf("Go.receiveData(): called with arg = %s\n", msg)
	defer procFreeMemory.Call(uintptr(unsafe.Pointer(cmsg)))
	Messages <- msg
	ok := true
	return uintptr(unsafe.Pointer(&ok))
}

func init() {
	ll := log.InfoLevel
	if lvl, ok := os.LookupEnv("TC_LOG_LEVEL"); ok {
		if level, err := log.ParseLevel(lvl); err == nil {
			ll = level
		}
	}
	log.SetLevel(ll)
	log.Infoln("Initialize txmlconnector")
	_, _, err := procInitialize.Call(uintptr(unsafe.Pointer(C.CString("logs"))), uintptr(3))
	if err != syscall.Errno(0) {
		log.Panic("Initialize error: ", err)
	}

	_, _, err = procSetCallback.Call(syscall.NewCallback(receiveData))
	if err != syscall.Errno(0) {
		log.Panic("Set callback fn error: ", err)
	}
}

func main() {
	defer procUnInitialize.Call()
	log.Infoln("Server running ...")

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalln(err)
	}

	srv := grpc.NewServer()
	// Setup our Ctrl+C handler
	SetupCloseHandler(srv)

	transaqConnector.RegisterConnectServiceServer(srv, &server{})
	log.Infoln("Press CRTL+C to stop the server...")
	log.Fatalln(srv.Serve(lis))
}

func SetupCloseHandler(srv *grpc.Server) {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		log.Info("\r- Ctrl+C pressed in Terminal")
		srv.GracefulStop()
		close(Messages)
		Done <- true
		os.Exit(0)
	}()
}

func txmlSendCommand(msg string) (data *string) {
	log.Debug("txmlSendCommand() Call: ", msg)
	reqData := C.CString(msg)
	resp, _, err := procSendCommand.Call(uintptr(unsafe.Pointer(reqData)))
	if err != syscall.Errno(0) {
		log.Error("txmlSendCommand() ", err)
		return nil
	}
	respPointer := unsafe.Pointer(resp)
	respData := C.GoString((*C.char)(respPointer))
	defer procFreeMemory.Call(resp)
	log.Debug("SendCommand Data: ", respData)
	return &respData
}

func (s *server) SendCommand(ctx context.Context, request *transaqConnector.SendCommandRequest) (*transaqConnector.SendCommandResponse, error) {
	resData := txmlSendCommand(request.Message)
	return &transaqConnector.SendCommandResponse{Message: *resData}, nil
}

func (s *server) FetchResponseData(in *transaqConnector.DataRequest, srv transaqConnector.ConnectService_FetchResponseDataServer) error {
	ctx := srv.Context()
	ticker := time.NewTicker(5000 * time.Millisecond)
	for {
		select {
		case msg := <-Messages:
			fmt.Println("received message", msg)
			resp := transaqConnector.DataResponse{Message: msg}
			if err := srv.Send(&resp); err != nil {
				log.Error("send error %v", err)
			}
		case <-ctx.Done():
			fmt.Println("Done loop ", ctx.Err())
			txmlSendCommand("<command id=\"disconnect\"/>")
			return nil
		case done := <-Done:
			if done {
				fmt.Println("Stop loop")
			}
			return nil
		case t := <-ticker.C:
			fmt.Println("no message received, Tick at", t)
		}
	}
}
