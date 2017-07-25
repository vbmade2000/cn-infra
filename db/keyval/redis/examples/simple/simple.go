package main

import (
	"os"
	"time"

	"fmt"
	"github.com/ligato/cn-infra/db"
	"github.com/ligato/cn-infra/db/keyval"
	"github.com/ligato/cn-infra/db/keyval/redis"
	"github.com/ligato/cn-infra/logging"
	"github.com/ligato/cn-infra/logging/logroot"
	"github.com/ligato/cn-infra/utils/config"
)

var usage = `usage: %s -n|-c|-s <client.yaml>
	where
	-n		Specifies the use of a node client
	-c		Specifies the use of a cluster client
	-s		Specifies the use of a sentinel client
`

var log = logroot.Logger()

var redisConn *redis.BytesConnectionRedis
var broker keyval.BytesBroker
var watcher keyval.BytesWatcher

var useRedigo = false

func main() {
	log.SetLevel(logging.DebugLevel)

	//generateSampleConfigs()

	cfg := loadConfig()
	if cfg == nil {
		return
	}

	if useRedigo {
		redisConn = createConnectionRedigo(cfg)
	} else {
		redisConn = createConnection(cfg)
	}

	broker = redisConn.NewBroker("")
	watcher = redisConn.NewWatcher("")

	runSimpleExmple()
}

func loadConfig() interface{} {
	numArgs := len(os.Args)
	defer func() {
		if numArgs > 3 && os.Args[len(os.Args)-1] == "redigo" {
			useRedigo = true
			fmt.Println("Using redigo")
		}
	}()

	if numArgs < 3 {
		fmt.Printf(usage, os.Args[0])
		return nil
	}
	var err error
	t := os.Args[1]
	f := os.Args[2]
	defer func() {
		if err != nil {
			log.Panicf("ParseConfigFromYamlFile(%s) failed: %s", f, err)
		}
	}()

	switch t {
	case "-n":
		var cfg redis.NodeConfig
		err = config.ParseConfigFromYamlFile(f, &cfg)
		return cfg
	case "-c":
		var cfg redis.ClusterConfig
		err = config.ParseConfigFromYamlFile(f, &cfg)
		return cfg
	case "-s":
		var cfg redis.SentinelConfig
		err = config.ParseConfigFromYamlFile(f, &cfg)
		return cfg
	}
	return nil
}

func createConnection(cfg interface{}) *redis.BytesConnectionRedis {
	client, err := redis.CreateClient(cfg)
	if err != nil {
		log.Panicf("CreateNodeClient() failed: %s", err)
	}
	conn, err := redis.NewBytesConnection(client, log)
	if err != nil {
		client.Close()
		log.Panicf("NewBytesConnection() failed: %s", err)
	}
	return conn
}

func createConnectionRedigo(cfg interface{}) *redis.BytesConnectionRedis {
	pool, err := redis.CreateNodeClientConnPool(cfg.(redis.NodeConfig))
	if err != nil {
		log.Panicf("CreateNodeClientConnPool() failed: %s", err)
	}
	conn, err := redis.NewBytesConnectionRedis(pool, log)
	if err != nil {
		pool.Close()
		log.Panicf("NewBytesConnectionRedigo() failed: %s", err)
	}
	return conn
}

func runSimpleExmple() {
	var err error

	var key1, key2, key3 = "key1", "key2", "key3"
	keyPrefix := key1[:3]

	respChan := make(chan keyval.BytesWatchResp, 10)
	err = watcher.Watch(respChan, keyPrefix)
	if err != nil {
		log.Errorf(err.Error())
	}
	go func() {
		for {
			select {
			case r, ok := <-respChan:
				if ok {
					switch r.GetChangeType() {
					case db.Put:
						log.Infof("Watcher received %v: %s=%s", r.GetChangeType(), r.GetKey(), string(r.GetValue()))
					case db.Delete:
						log.Infof("Watcher received %v: %s", r.GetChangeType(), r.GetKey())
					}
				} else {
					log.Error("Something wrong with respChan... bail out")
					return
				}
			default:
				break
			}
		}
	}()
	time.Sleep(2 * time.Second)
	put(key1, "val 1")
	put(key2, "val 2")
	put(key3, "val 3", keyval.WithTTL(time.Second))

	time.Sleep(2 * time.Second)
	get(key1)
	get(key2)
	fmt.Printf("==> NOTE: %s should have expired\n", key3)
	get(key3) // key3 should've expired
	fmt.Printf("==> NOTE: get(%s) should return nil\n", keyPrefix)
	get(keyPrefix) // keyPrefix shouldn't find anything
	listKeys(keyPrefix)
	listVal(keyPrefix)

	del(keyPrefix)

	fmt.Println("==> NOTE: All keys should have been deleted")
	get(key1)
	get(key2)
	listKeys(keyPrefix)
	listVal(keyPrefix)

	txn()

	listVal(keyPrefix)

	log.Info("Sleep for 5 seconds")
	time.Sleep(5 * time.Second)

	// Done watching.  Close the channel.
	log.Infof("Closing connection")
	//close(respChan)
	redisConn.Close()

	fmt.Println("==> NOTE: Call on a closed connection should fail.")
	del(keyPrefix)

	log.Info("Sleep for 10 seconds")
	time.Sleep(30 * time.Second)
}

func put(key, value string, opts ...keyval.PutOption) {
	err := broker.Put(key, []byte(value), opts...)
	if err != nil {
		//log.Panicf(err.Error())
		log.Errorf(err.Error())
	}
}

func get(key string) {
	var val []byte
	var found bool
	var revision int64
	var err error

	val, found, revision, err = broker.GetValue(key)
	if err != nil {
		log.Errorf(err.Error())
	} else if found {
		log.Infof("GetValue(%s) = %t ; val = %s ; revision = %d", key, found, val, revision)
	} else {
		log.Infof("GetValue(%s) = %t", key, found)
	}
}

func listKeys(keyPrefix string) {
	var keys keyval.BytesKeyIterator
	var err error

	keys, err = broker.ListKeys(keyPrefix)
	if err != nil {
		log.Errorf(err.Error())
	} else {
		var count int32
		for {
			key, rev, done := keys.GetNext()
			if done {
				break
			}
			log.Infof("ListKeys(%s):  %s (rev %d)", keyPrefix, key, rev)
			count++
		}
		log.Infof("ListKeys(%s): count = %d", keyPrefix, count)
	}
}

func listVal(keyPrefix string) {
	var keyVals keyval.BytesKeyValIterator
	var err error

	keyVals, err = broker.ListValues(keyPrefix)
	if err != nil {
		log.Errorf(err.Error())
	} else {
		var count int32
		for {
			kv, done := keyVals.GetNext()
			if done {
				break
			}
			log.Infof("ListValues(%s):  %s = %s (rev %d)", keyPrefix, kv.GetKey(), kv.GetValue(), kv.GetRevision())
			count++
		}
		log.Infof("ListValues(%s): count = %d", keyPrefix, count)
	}
}

func del(keyPrefix string) {
	var found bool
	var err error

	found, err = broker.Delete(keyPrefix)
	if err != nil {
		log.Errorf(err.Error())
		return
	}
	log.Infof("Delete(%s): found = %t", keyPrefix, found)
}

func txn() {
	var key101, key102, key103, key104 = "key101", "key102", "key103", "key104"
	var txn keyval.BytesTxn

	txn = broker.NewTxn()
	txn.Put(key101, []byte("val 101")).Put(key102, []byte("val 102"))
	txn.Put(key103, []byte("val 103")).Put(key104, []byte("val 104"))
	txn.Delete(key101)
	err := txn.Commit()
	if err != nil {
		log.Errorf("txn: %s", err)
	}
}

func generateSampleConfigs() {
	clientConfig := redis.ClientConfig{
		Password:     "",
		DialTimeout:  0,
		ReadTimeout:  0,
		WriteTimeout: 0,
		Pool: redis.PoolConfig{
			PoolSize:           0,
			PoolTimeout:        0,
			IdleTimeout:        0,
			IdleCheckFrequency: 0,
		},
	}
	redis.GenerateConfig(
		&redis.NodeConfig{
			Endpoint: "localhost:6379",
			DB:       0,
			AllowReadQueryToSlave: false,
			TLS:          redis.TLS{},
			ClientConfig: clientConfig,
		}, "./node-client.yaml")
	redis.GenerateConfig(
		&redis.ClusterConfig{
			Endpoints:             []string{"localhost:7000", "localhost:7001", "localhost:7002", "localhost:7003"},
			AllowReadQueryToSlave: true,
			MaxRedirects:          0,
			RouteByLatency:        true,
			ClientConfig:          clientConfig,
		}, "./cluster-client.yaml")
	redis.GenerateConfig(
		&redis.SentinelConfig{
			Endpoints:    []string{"localhost:26379"},
			MasterName:   "mymaster",
			DB:           0,
			ClientConfig: clientConfig,
		}, "./sentinel-client.yaml")
}
