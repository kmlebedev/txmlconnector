package main

//+build windows,amd64
//https://github.com/ivanantipin/transaqgrpc/blob/master/tqgrpcserver/XmlConnector.cs

import "C"
import (
	"context"
	"fmt"
	cmds "github.com/kmlebedev/txmlconnector/client/commands"
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

const (
	txml_dll_name     = "txmlconnector64"
	txml_dll_ver_demo = "6.19.2.21.6"
	txml_dll_ver_main = "6.17.2.21.2"
)

var (
	txmlconnector    = &syscall.LazyDLL{}
	procSetCallback  = &syscall.LazyProc{}
	procSendCommand  = &syscall.LazyProc{}
	procFreeMemory   = &syscall.LazyProc{}
	procInitialize   = &syscall.LazyProc{}
	procUnInitialize = &syscall.LazyProc{}
	Messages         = make(chan string)
	Done             = make(chan bool)
)

type server struct {
	transaqConnector.UnimplementedConnectServiceServer
}

//export receiveData
func receiveData(cmsg *C.char) (ret uintptr) {
	msg := C.GoString(cmsg)
	log.Debugf("Go.receiveData(): called with arg = %s\n", msg)
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
	var dllPath string
	if _, ok := os.LookupEnv("TC_DEMO"); ok {
		dllPath = fmt.Sprintf("%s-%s.dll", txml_dll_name, txml_dll_ver_demo)
	} else if ver, ok := os.LookupEnv("TC_DLL_VER"); ok {
		dllPath = fmt.Sprintf("%s-%s.dll", txml_dll_name, ver)
	} else if path, ok := os.LookupEnv("TC_DLL_PATH"); ok {
		dllPath = path
	} else {
		dllPath = fmt.Sprintf("%s-%s.dll", txml_dll_name, txml_dll_ver_main)
	}
	txmlconnector = syscall.NewLazyDLL(dllPath)
	log.Infof("Initialize module: %s", dllPath)
	procSetCallback = txmlconnector.NewProc("SetCallback")
	procSendCommand = txmlconnector.NewProc("SendCommand")
	procFreeMemory = txmlconnector.NewProc("FreeMemory")
	procInitialize = txmlconnector.NewProc("Initialize")
	procUnInitialize = txmlconnector.NewProc("UnInitialize")
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
	log.Info("txmlSendCommand() Call: ", msg)
	reqData := C.CString(msg)
	resp, _, err := procSendCommand.Call(uintptr(unsafe.Pointer(reqData)))
	if err != syscall.Errno(0) {
		log.Error("txmlSendCommand() ", err)
		return nil
	}
	respData := C.GoString((*C.char)(unsafe.Pointer(resp)))
	defer procFreeMemory.Call(resp)
	log.Info("SendCommand Data: ", respData)
	return &respData
}

func (s *server) SendCommand(ctx context.Context, request *transaqConnector.SendCommandRequest) (*transaqConnector.SendCommandResponse, error) {
	return &transaqConnector.SendCommandResponse{
		Message: *txmlSendCommand(request.Message),
	}, nil
}

func (s *server) FetchResponseData(in *transaqConnector.DataRequest, srv transaqConnector.ConnectService_FetchResponseDataServer) error {
	ctx := srv.Context()
	ticker := time.NewTicker(5000 * time.Millisecond)
	for {
		select {
		case msg := <-Messages:
			log.Debug("Received message", msg)
			resp := transaqConnector.DataResponse{Message: msg}
			if err := srv.Send(&resp); err != nil {
				log.Error("Sending error %s", err)
			}
		case <-ctx.Done():
			fmt.Println("Loop done", ctx.Err())
			txmlSendCommand(cmds.EncodeRequest(cmds.Command{Id: "disconnect"}))
			return nil
		case done := <-Done:
			if done {
				fmt.Println("Stop loop")
			}
			return nil
		case t := <-ticker.C:
			//txmlSendCommand(cmds.EncodeRequest(cmds.Command{Id: "server_status"}))
			log.Infoln("no message received, Tick at", t)
		}
	}
}
