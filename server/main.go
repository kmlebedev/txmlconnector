//go:build windows && amd64
// +build windows,amd64

package tcServer

// https://github.com/ivanantipin/transaqgrpc/blob/master/tqgrpcserver/XmlConnector.cs

import "C"
import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	"unsafe"

	cmds "github.com/nableru/txmlconnector/client/commands"
	"github.com/nableru/txmlconnector/proto"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

/*
#include <stdlib.h>
*/
import "C"

const (
	txml_dll_name     = "txmlconnector64"
	txml_dll_ver_main = "6.19.2.21.14"
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
		dllPath = fmt.Sprintf("%s-%s.dll", txml_dll_name, txml_dll_ver_main)
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
	logPathPtr := uintptr(unsafe.Pointer(C.CString("logs")))
	logLevelPtr := uintptr(2)
	_, _, err := procInitialize.Call(logPathPtr, logLevelPtr)
	if err != syscall.Errno(0) && err != nil {
		log.Panic("Initialize error: ", err.Error())
	}

	_, _, err = procSetCallback.Call(syscall.NewCallback(receiveData))
	if err != syscall.Errno(0) && err != nil {
		log.Panic("Set callback fn error: ", err.Error())
	}
}

func Run() {
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
		log.Info("\r Ctrl+C pressed in Terminal")
		srv.GracefulStop()
		close(Messages)
		Done <- true
		os.Exit(0)
	}()
}

func TxmlSendCommand(msg string) (data *string) {
	log.Info("txmlSendCommand() Call: ", msg)
	reqData := C.CString(msg)
	resp, _, err := procSendCommand.Call(uintptr(unsafe.Pointer(reqData)))
	if err != syscall.Errno(0) && err != nil {
		log.Error("txmlSendCommand() ", err.Error())
		return nil
	}
	respData := C.GoString((*C.char)(unsafe.Pointer(resp)))
	defer procFreeMemory.Call(resp)
	log.Info("SendCommand Data: ", respData)
	return &respData
}

func (s *server) SendCommand(ctx context.Context, request *transaqConnector.SendCommandRequest) (*transaqConnector.SendCommandResponse, error) {
	return &transaqConnector.SendCommandResponse{
		Message: *TxmlSendCommand(request.Message),
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
			TxmlSendCommand(cmds.EncodeRequest(cmds.Command{Id: "disconnect"}))
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
