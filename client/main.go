package main

import (
	"bytes"
	"context"
	"encoding/xml"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"io"
	"os"

	. "github.com/kmlebedev/txmlconnector/client/commands"
	pb "github.com/kmlebedev/txmlconnector/proto"
)

// Encodes the request into XML format.
func encodeRequest(request interface{}) string {
	var bytesBuffer bytes.Buffer
	e := xml.NewEncoder(&bytesBuffer)
	e.Encode(request)
	return bytesBuffer.String()
}

func init() {
	ll := log.InfoLevel
	if lvl, ok := os.LookupEnv("TC_LOG_LEVEL"); ok {
		if level, err := log.ParseLevel(lvl); err == nil {
			ll = level
		}
	}
	log.SetLevel(ll)
}

func main() {
	log.Println("Client running ...")
	conn, err := grpc.Dial(
		":50051",
		grpc.WithInsecure(),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
	)
	if err != nil {
		log.Fatalln("grpc.Dial()", err)
	}
	defer conn.Close()

	client := pb.NewConnectServiceClient(conn)
	req := Connect{
		Id:             "connect",
		Login:          os.Getenv("TC_LOGIN"),
		Password:       os.Getenv("TC_PASSWORD"),
		Host:           os.Getenv("TC_HOST"),
		Port:           os.Getenv("TC_PORT"),
		Rqdelay:        100,
		SessionTimeout: 10,
		RequestTimeout: 5,
		PushUlimits:    0,
		PushPosEquity:  0,
		Language:       "en",
		Autopos:        true,
	}
	request := &pb.SendCommandRequest{Message: encodeRequest(req)}

	ctx := context.Background()
	response, err := client.SendCommand(ctx, request)
	if err != nil {
		log.Error("SendCommand: ", err)
	}
	log.Info("res ", response.GetMessage())
	defer client.SendCommand(ctx, &pb.SendCommandRequest{Message: encodeRequest(Command{Id: "disconnect"})})

	stream, err := client.FetchResponseData(ctx, &pb.DataRequest{})
	if err != nil {
		log.Fatalf("open stream error %v", err)
	}

	done := make(chan bool)
	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				log.Debug("Resp received: %s", resp.Message)
				done <- true //means stream is finished
				return
			}
			if err != nil {
				log.Panicf("stream.Recv() cannot receive %v", err)
			}
			log.Debugf("Resp received: %s", resp.Message)
		}
	}()
	<-done //we will wait until all response is received
	log.Info("finished")
}
