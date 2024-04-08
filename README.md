## 安装

1. 创建网络：
   ```bash
   docker network create network1 --driver bridge
   ```
2. 启动 `etcd` 服务端实例
   ```bash
   docker run -d --name etcd-server --network network1 --publish 2379:2379 --publish 2380:2380 --env ALLOW_NONE_AUTHENTICATION=yes --env ETCD_ADVERTISE_CLIENT_URLS=http://etcd-server:2379 bitnami/etcd:latest
   ```
3. 启动 `etcd` 客户端实例
   ```bash
   docker run -it --rm --network network1 --env ALLOW_NONE_AUTHENTICATION=yes bitnami/etcd:latest etcdctl --endpoints http://etcd-server:2379 set /message Hello
   ```

打开浏览器，访问 `http://localhost:2379/version`，就可以看到 `etcd` 的版本信息

## etcd 基本操作

进入 `etcd` 容器：

```bash
docker exec -it <etcd_container_id> bash
```

设置和读取值：

```bash
etcdctl put foo bar # ok
etcdctl get foo     # bar
```

初始化一个客户端，使用的是 `go.etcd.io/etcd/client/v3` 包：

```go
import (
  "time"
  clientv3 "go.etcd.io/etcd/client/v3"
)
func main (){
  cli, err := clientv3.New(clientv3.Config{
    Endpoints:   []string{"http://etcd-server:2379"},
    DialTimeout: time.Second * 5,
  })
  if err != nil {
    log.Fatalln(err)
  }
  defer cli.Close()
}
```

### 客户端的读取

这里我们先讲两个 `api`：

- `clientv3.WithPrevKV()`，这个 `api` 是用来获取之前的值的，我们可以在 `Put` 方法中使用这个 `api`，获取之前的值，方便做一些后续的处理
  - 在 `etcd` 中，可以通过 `etcdctl put -h` 查看 `--prev-kv` 的使用
- `clientv3.WithPrefix()`，这个 `api` 是用来获取某一个开头的 `key` 所有属性值
  - 在 `etcd` 中，可以通过 `etcdctl get -h` 查看 `--prefix` 的使用

```go
putRes, err := cli.Put(context.Background(), "key1", "value1", clientv3.WithPrevKV())
if putRes.PrevKv!=nil{
  fmt.Println(putRes.PrevKv)
}
```

```go
getRes, err := cli.Get(context.Background(), "key", clientv3.WithPrefix())
fmt.Println(getRes.Count)
```

```go
delRes, err := cli.Delete(context.Background(), "key1", clientv3.WithPrefix())
fmt.Println(delRes.Deleted)
```

## 实现两个服务

我们先通过 `grpc` 两个服务：`server` 和 `client`

文件目录：

- `client/client.go`
- `server/server.go`
- `proto/hello.proto`

我们在 `proto/hello.proto` 文件中编写 `proto` 文件

```go
syntax = "proto3";
option go_package = "../proto";
package hello;
service Greeter{
  rpc sayHello(HelloRequest)returns(HelloResponse);
}
message HelloRequest {
  string msg = 1;
}
message HelloResponse{
  string msg = 1;
}
```

运行下面命令，就会在当前 `proto` 文件夹下生成两个文件：`hello_grpc.pb.go` 和 `hello.pb.go`

```bash
protoc --go-grpc_out=require_unimplemented_servers=false:. --go_out=. ./hello.proto
```

### server

1. 实现 `SayHello` 方法
2. 启动服务
   1. 监听 `8080` 端口
   2. 注册 `Greeter` 服务
   3. 启动服务

```go
type server struct{}

// 实现 SayHello 方法
func (s *server) SayHello(ctx context.Context, req *proto.HelloRequest) (*proto.HelloResponse, error) {
  fmt.Println(req.Msg) // Hello Service
  return &proto.HelloResponse{Msg: "Hello Client"}, nil
}

func main() {
  // 监听端口
  lis, err := net.Listen("tcp", ":8080")
  if err != nil {
    panic(err)
  }
  fmt.Println("Server started at :8080")
  // 创建 grpc 服务
  s := grpc.NewServer()
  // 注册 server 服务
  proto.RegisterGreeterServer(s, &server{})
  // 启动服务
  if err := s.Serve(lis); err != nil {
    panic(err)
  }
}
```

### client

1. 连接服务端
2. 创建客户端
3. 调用服务端方法

```go
func main() {
  // 连接服务端
  conn, err := grpc.Dial("127.0.0.1:8080", grpc.WithTransportCredentials(insecure.NewCredentials()))
  if err != nil {
    panic(err)
  }
  defer conn.Close()
  in := &proto.HelloRequest{Msg: "Hello Service"}
  // 创建客户端
  c := proto.NewGreeterClient(conn)
  // 调用服务端方法
  r, err := c.SayHello(context.Background(), in)
  if err != nil {
    panic(err)
  }
  fmt.Println(r.Msg) // Hello Client
}
```

## 服务注册和发现

上面我们已经准备好两个服务了，现在我们需要通过 `etcd` 来实现服务的注册和发现

### 初始化 etcd 客户端

首先声明一个 `clientv3` 客户端

这里要注意一点：**不能在这里关闭服务**，也就是说不能使用 `defer cli.Close`，不然 `initClientv3` 函数运行结束之后，`cli` 就会被关闭

```go
func initClientv3() *clientv3.Client {
	cli, err := clientv3.New(clientv3.Config{
    // etcd 服务
		Endpoints:   []string{"http://etcd-server:2379"},
    // 超时时间
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	return cli
}
```

### 服务注册

1. 我们先从 `etcd` 中读取一个服务注册的名字
   ```go
   getRes, err := cli.Get(ctx, s.Name, clientv3.WithPrefix())
   ```
2. 如果没有这个服务，我们就设置 `grantLease`，等待后续注册服务
   ```go
   if getRes.Count == 0 {
     grantLease = true
   }
   ```
3. 设置租约，租约 `10` 秒过期
   ```go
   if grantLease {
      // 设置租约，租约 10 秒过期
     leaseRes, err := cli.Grant(ctx, 10)
     if err != nil {
       panic(err)
     }
     // 拿到租约的 ID
     leaseID = leaseRes.ID
   }
   ```
4. 创建 `key-value` 客户端
   ```go
   // 创建 key-value 客户端
   kv := clientv3.NewKV(cli)
   // 创建事务
   txn := kv.Txn(ctx)
   ```
5. 使用 `txn` 事务，进行服务注册
   - `txn.If` 指定事务的条件
     - `clientv3.Compare` 比较 `key` 的 创建版本是否等于 `0`，如果等于 `0`，说明这个服务还没有注册
   - `.Then` 方法定义了满足条件时需要执行的操作
     - 这里一系列的 `clientv3.OpPut()` 操作是将数据写入 `etcd` 中，每个`OpPut` 都指定了 `key`、`value` 和租约 `ID`，其中租约`ID` 是通过`WithLease` 写入
   - `.Else` 方法定义了不满足条件时需要执行的操作，这里使用 `withIgnoreLease` 忽略租约
   - `.Commit` 提交事务
   ```go
   _, err = txn.If(clientv3.Compare(clientv3.CreateRevision(s.Name), "=", 0)).Then(
     clientv3.OpPut(s.Name, s.Name, clientv3.WithLease(leaseID)),
     clientv3.OpPut(s.Name+".ip", s.IP, clientv3.WithLease(leaseID)),
     clientv3.OpPut(s.Name+".port", s.Port, clientv3.WithLease(leaseID)),
     clientv3.OpPut(s.Name+".protocol", s.Protocol, clientv3.WithLease(leaseID)),
   ).Else(
     clientv3.OpPut(s.Name, s.Name, clientv3.WithIgnoreLease()),
     clientv3.OpPut(s.Name+".ip", s.IP, clientv3.WithIgnoreLease()),
     clientv3.OpPut(s.Name+".port", s.Port, clientv3.WithIgnoreLease()),
     clientv3.OpPut(s.Name+".protocol", s.Protocol, clientv3.WithIgnoreLease()),
   ).Commit()
   if err != nil {
     panic(err)
   }
   ```

完整的代码：

```go
func RegisterService(s *Service) {
  cli := initClientv3()
  defer cli.Close()

  var grantLease bool
  var leaseID clientv3.LeaseID
  ctx := context.Background()
  getRes, err := cli.Get(ctx, s.Name, clientv3.WithPrefix())
  if err != nil {
    fmt.Printf("%v not found\n", s.Name)
    return
  }

  if getRes.Count == 0 {
    grantLease = true
  }

  if grantLease {
    leaseRes, err := cli.Grant(ctx, 10)
    if err != nil {
      panic(err)
    }
    leaseID = leaseRes.ID
  }

  kv := clientv3.NewKV(cli)
  txn := kv.Txn(ctx)
  _, err = txn.If(clientv3.Compare(clientv3.CreateRevision(s.Name), "=", 0)).Then(
    clientv3.OpPut(s.Name, s.Name, clientv3.WithLease(leaseID)),
    clientv3.OpPut(s.Name+".ip", s.IP, clientv3.WithLease(leaseID)),
    clientv3.OpPut(s.Name+".port", s.Port, clientv3.WithLease(leaseID)),
    clientv3.OpPut(s.Name+".protocol", s.Protocol, clientv3.WithLease(leaseID)),
  ).Else(
    clientv3.OpPut(s.Name, s.Name, clientv3.WithIgnoreLease()),
    clientv3.OpPut(s.Name+".ip", s.IP, clientv3.WithIgnoreLease()),
    clientv3.OpPut(s.Name+".port", s.Port, clientv3.WithIgnoreLease()),
    clientv3.OpPut(s.Name+".protocol", s.Protocol, clientv3.WithIgnoreLease()),
  ).Commit()
  if err != nil {
    panic(err)
  }

  if grantLease {
    leaseKeepAlive, err := cli.KeepAlive(ctx, leaseID)
    if err != nil {
      panic(err)
    }
    for lease := range leaseKeepAlive {
      fmt.Println("Lease is alive", lease.ID)
    }
  }
}
```

## 服务发现

1. 从 `etcd` 中读取服务注册的名字
   ```go
   getRes, err := cli.Get(ctx, serverName, clientv3.WithPrefix())
   ```
2. 将从 `etcd` 中读取的服务信息保存到 `myServices` 中
   ```go
   if getRes.Count > 0 {
     mp := sliceToMap(getRes.Kvs)
     s := &Service{}
     fmt.Println(mp[serverName])
     if kv, ok := mp[serverName]; ok {
       s.Name = string(kv.Value)
     }
     if kv, ok := mp[serverName+".ip"]; ok {
       s.IP = string(kv.Value)
     }
     if kv, ok := mp[serverName+".port"]; ok {
       s.Port = string(kv.Value)
     }
     if kv, ok := mp[serverName+".protocol"]; ok {
       s.Protocol = string(kv.Value)
     }
     myServices.Lock()
     myServices.services[serverName] = s
     myServices.Unlock()
   }
   ```
3. 监听指定前缀的服务的变化
   ```go
   rch := cli.Watch(ctx, serverName, clientv3.WithPrefix())
   ```
4. 根据服务的变化拿到最新的服务信息
   - 循环迭代通道中的监测事件
   - 如果是删除事件，就删除 `myServices` 中的服务
   - 如果是新增事件，就更新 `myServices` 中的服务
   ```go
   for wresp := range rch {
     for _, ev := range wresp.Events {
       if ev.Type == clientv3.EventTypeDelete {
         myServices.Lock()
         delete(myServices.services, serverName)
         myServices.Unlock()
       }
       if ev.Type == clientv3.EventTypePut {
         myServices.Lock()
         if _, ok := myServices.services[serverName]; ok {
           myServices.services[serverName] = &Service{}
         }
         switch string(ev.Kv.Key) {
         case serverName:
           myServices.services[serverName].Name = string(ev.Kv.Value)
         case serverName + ".ip":
           myServices.services[serverName].IP = string(ev.Kv.Value)
         case serverName + ".port":
           myServices.services[serverName].Port = string(ev.Kv.Value)
         case serverName + ".protocol":
           myServices.services[serverName].Protocol = string(ev.Kv.Value)
         }
         myServices.Unlock()
       }
     }
   }
   ```

完整代码：

```go
func WatchServiceName(serverName string) {
  cli := initClientv3()
  defer cli.Close()

  ctx := context.Background()
  getRes, err := cli.Get(ctx, serverName, clientv3.WithPrefix())
  if err != nil {
    panic(err)
  }
  if getRes.Count > 0 {
    mp := sliceToMap(getRes.Kvs)
    s := &Service{}
    fmt.Println(mp[serverName])
    if kv, ok := mp[serverName]; ok {
      s.Name = string(kv.Value)
    }
    if kv, ok := mp[serverName+".ip"]; ok {
      s.IP = string(kv.Value)
    }
    if kv, ok := mp[serverName+".port"]; ok {
      s.Port = string(kv.Value)
    }
    if kv, ok := mp[serverName+".protocol"]; ok {
      s.Protocol = string(kv.Value)
    }
    myServices.Lock()
    myServices.services[serverName] = s
    myServices.Unlock()
  }

  rch := cli.Watch(ctx, serverName, clientv3.WithPrefix())
  for wresp := range rch {
    for _, ev := range wresp.Events {
      if ev.Type == clientv3.EventTypeDelete {
        myServices.Lock()
        delete(myServices.services, serverName)
        myServices.Unlock()
      }
      if ev.Type == clientv3.EventTypePut {
        myServices.Lock()
        if _, ok := myServices.services[serverName]; ok {
          myServices.services[serverName] = &Service{}
        }
        switch string(ev.Kv.Key) {
        case serverName:
          myServices.services[serverName].Name = string(ev.Kv.Value)
        case serverName + ".ip":
          myServices.services[serverName].IP = string(ev.Kv.Value)
        case serverName + ".port":
          myServices.services[serverName].Port = string(ev.Kv.Value)
        case serverName + ".protocol":
          myServices.services[serverName].Protocol = string(ev.Kv.Value)
        }
        myServices.Unlock()
      }
    }
  }
}
```

### 修改 client

我们对 `client` 代码进行改动

1. 监听 `etcd` 中的服务：
   ```go
   go discovery.WatchServiceName(ServerName)
   ```
2. 循环调用 `sayHello`
   ```go
    for {
      sayHello()
      time.Sleep(2 * time.Second)
    }
   ```
3. `sayHello` 函数中会拿到 `etcd` 中的服务信息
   ```go
   addr := getServerAddr(ServerName)
   ```
4. 通过 `grpc.Dial` 连接服务
   ```go
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
   ```

### 修改 server

在服务启动之前注册服务

```go
s1 := &discovery.Service{
  Name:     "greeter",
  IP:       "localhost",
  Port:     "8080",
  Protocol: "grpc",
}
go discovery.RegisterService(s1)
proto.RegisterGreeterServer(s, &serve
```