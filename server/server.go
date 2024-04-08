package main

import (
	"context"
	"etcd-demo/discovery"
	"etcd-demo/proto"
	"fmt"
	"net"

	"google.golang.org/grpc"
)

type server struct{}

func (s *server) SayHello(ctx context.Context, req *proto.HelloRequest) (*proto.HelloResponse, error) {
	fmt.Println(req.Msg)
	return &proto.HelloResponse{Msg: "Hello Client"}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)
	}
	fmt.Println("Server started at :8080")
	s := grpc.NewServer()
	s1 := &discovery.Service{
		Name:     "greeter",
		IP:       "localhost",
		Port:     "8080",
		Protocol: "grpc",
	}
	go discovery.RegisterService(s1)
	proto.RegisterGreeterServer(s, &server{})
	if err := s.Serve(lis); err != nil {
		panic(err)
	}
}
