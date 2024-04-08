package discovery

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

type Service struct {
	Name     string
	IP       string
	Port     string
	Protocol string
}

type Services struct {
	services map[string]*Service
	sync.RWMutex
}

var myServices = &Services{services: map[string]*Service{}}

func initClientv3() *clientv3.Client {
	cli, err := clientv3.New(clientv3.Config{
		Endpoints:   []string{"http://etcd-server:2379"},
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	return cli
}

func RegisterService(s *Service) {
	cli := initClientv3()
	defer cli.Close()

	var grantLease bool
	var leaseID clientv3.LeaseID
	ctx := context.Background()
	getRes, err := cli.Get(ctx, s.Name, clientv3.WithPrefix())
	if err != nil {
		fmt.Printf("Get %v not found\n", s.Name)
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

func ServiceDiscovery(serverName string) *Service {
	var s *Service = nil
	myServices.RLock()
	s, _ = myServices.services[serverName]
	myServices.RUnlock()
	return s
}

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

func sliceToMap(list []*mvccpb.KeyValue) map[string]*mvccpb.KeyValue {
	mp := make(map[string]*mvccpb.KeyValue, 0)
	for _, item := range list {
		mp[string(item.Key)] = item
	}
	return mp
}
