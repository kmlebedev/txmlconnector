//+build windows,amd64

// https://github.com/ivanantipin/transaqgrpc/blob/master/tqgrpcserver/XmlConnector.cs
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
	"txmlconnector/proto"
	"unsafe"
)

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

func init() {
	log.Println("Initialize txmlconnector")
	_, _, err := procInitialize.Call(uintptr(unsafe.Pointer(C.CString("logs"))), uintptr(3))

	if err != syscall.Errno(0) {
		log.Panic("Initialize error: ", err)
	}
	_, _, err = procSetCallback.Call(syscall.NewCallback(receiveData))
	if err != syscall.Errno(0) {
		log.Panic("Set callback fn error: ", err)
	}
}

//export receiveData
func receiveData(cmsg *C.char) (ret uintptr) {
	msg := C.GoString(cmsg)
	fmt.Printf("Go.receiveData(): called with arg = %s\n", msg)
	defer C.free(unsafe.Pointer(cmsg))
	Messages <- msg
	return 0
}

func main() {
	log.Println("Server running ...")

	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalln(err)
	}

	srv := grpc.NewServer()
	// Setup our Ctrl+C handler
	SetupCloseHandler(srv)

	transaqConnector.RegisterConnectServiceServer(srv, &server{})
	log.Println("Press CRTL+C to stop the server...")
	log.Fatalln(srv.Serve(lis))
}

func SetupCloseHandler(srv *grpc.Server) {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("\r- Ctrl+C pressed in Terminal")
		srv.GracefulStop()
		close(Messages)
		Done <- true
		os.Exit(0)
	}()
}

func (s *server) SendCommand(ctx context.Context, request *transaqConnector.SendCommandRequest) (*transaqConnector.SendCommandResponse, error) {
	log.Println("Request: ", request.Message)
	reqData := C.CString(request.Message)
	resp, _, err := procSendCommand.Call(uintptr(unsafe.Pointer(reqData)))
	if err != syscall.Errno(0) {
		return nil, err
	}
	resData := C.GoString((*C.char)(unsafe.Pointer(resp)))
	log.Println("SendCommand response: ", resData)
	return &transaqConnector.SendCommandResponse{Message: resData}, nil
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
			fmt.Println("Done loop")
			return ctx.Err()
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
