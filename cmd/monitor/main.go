package main

import (
	"encoding/json"
	"fmt"
	"github.com/golang/protobuf/proto"
	zmq "github.com/pebbe/zmq4"
	log "github.com/sirupsen/logrus"
	"os"
	"time"
	mt "github.com/saurav-c/aftsi/proto/monitor"
)

const (
	MONITOR_SERVER_PORT = 10000
	PullTemplate = "tcp://*:%d"
)

func createSocket(tp zmq.Type, context *zmq.Context, address string, bind bool) *zmq.Socket {
	sckt, err := context.NewSocket(tp)
	if err != nil {
		fmt.Println("Unexpected error while creating new socket:\n", err)
		os.Exit(1)
	}

	if bind {
		err = sckt.Bind(address)
	} else {
		err = sckt.Connect(address)
	}

	if err != nil {
		fmt.Println("Unexpected error while binding/connecting socket:\n", err)
		os.Exit(1)
	}

	return sckt
}

func main() {
	zctx, _ := zmq.NewContext()
	puller := createSocket(zmq.PULL, zctx, fmt.Sprintf(PullTemplate, MONITOR_SERVER_PORT), true)
	poller := zmq.NewPoller()
	poller.Add(puller, zmq.POLLIN)

	if _, err := os.Stat("logs"); os.IsNotExist(err) {
		os.Mkdir("logs", os.ModePerm)
	}
	file, err := os.OpenFile("logs/monitor", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	log.SetOutput(file)

	log.Info("Started monitoring node")

	for true {
		sockets, _ := poller.Poll(10 * time.Millisecond)

		for _, socket := range sockets {
			switch s := socket.Socket; s {
			case puller:
				{
					data, _ := puller.RecvBytes(zmq.DONTWAIT)
					logStatistics(data)
				}
			}
		}
	}
}

func logStatistics(data []byte) {
	log.Debug("Received statistics message")
	statResp := &mt.Statistics{}
	proto.Unmarshal(data, statResp)
	statsMap := statResp.GetStats()

	jdoc, err := json.Marshal(statsMap)
	if err != nil {
		log.Fatal(err)
	}

	jstring := string(jdoc)

	f, err := os.OpenFile("stats.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	f.WriteString(jstring)
	f.Close()
}
