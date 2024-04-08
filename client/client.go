package main

import (
	"context"
	"etcd-demo/discovery"
	"etcd-demo/proto"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const ServerName = "greeter"

func main() {
	go discovery.WatchServiceName(ServerName)
	for {
		sayHello()
		time.Sleep(2 * time.Second)
	}

}

func getServerAddr(serverName string) string {
	s := discovery.ServiceDiscovery(serverName)
	if s == nil {
		return ""
	}
	if s.IP == "" || s.Port == "" {
		return ""
	}
	return s.IP + ":" + s.Port
}

func sayHello() {
	addr := getServerAddr(ServerName)
	if addr == "" {
		fmt.Println("Service not found", addr)
		return
	}
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	in := &proto.HelloRequest{Msg: "Hello Service"}
	c := proto.NewGreeterClient(conn)
	r, err := c.SayHello(context.Background(), in)
	if err != nil {
		panic(err)
	}
	println(r.Msg)
}
