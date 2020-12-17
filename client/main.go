package main

import (
	"bytes"
	"context"
	"encoding/xml"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"io"
	"os"
	"txmlconnector/client/commands"
	"txmlconnector/proto"
)

// Encodes the request into XML format.
func encodeRequest(request interface{}) string {
	var bytesBuffer bytes.Buffer
	e := xml.NewEncoder(&bytesBuffer)
	e.Encode(request)
	return bytesBuffer.String()
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

	client := transaqConnector.NewConnectServiceClient(conn)
	req := commands.Connect{
		Id:             "connect",
		Login:          os.Getenv("TC_LOGIN"),
		Password:       os.Getenv("TC_PASSWORD"),
		Host:           os.Getenv("TC_HOST"),
		Port:           os.Getenv("TC_PORT"),
		Rqdelay:        100,
		SessionTimeout: 1000,
		RequestTimeout: 1000,
	}
	request := &transaqConnector.SendCommandRequest{Message: encodeRequest(req)}

	//ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	//defer cancel()
	ctx := context.Background()
	response, err := client.SendCommand(ctx, request)
	if err != nil {
		log.Error("SendCommand: ", err)
	}
	log.Println("res ", response.GetMessage())

	stream, err := client.FetchResponseData(ctx, &transaqConnector.DataRequest{})
	if err != nil {
		log.Fatalf("open stream error %v", err)
	}

	done := make(chan bool)
	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				log.Printf("Resp received: %s", resp.Message)
				done <- true //means stream is finished
				return
			}
			if err != nil {
				log.Fatalf("cannot receive %v", err)
			}
			log.Printf("Resp received: %s", resp.Message)
		}
	}()
	<-done //we will wait until all response is received
	log.Printf("finished")
}
