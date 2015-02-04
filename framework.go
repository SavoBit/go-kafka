// +build scheduler

package main

import (
	kafka "github.com/stealthly/go_kafka_client"
	mesos "github.com/stealthly/go-kafka/mesos"
	"github.com/mesos/mesos-go/mesosproto"
	"github.com/mesos/mesos-go/scheduler"
	"github.com/golang/protobuf/proto"
	"net/http"
	"flag"
	"fmt"
	"os"
	"strings"
)

var artifactServerPort = flag.Int("artifact.port", 6666, "Binding port for artifact server.")
var master = flag.String("master", "127.0.0.1:5050", "Mesos Master address <ip:port>.")
var cpuPerConsumer = flag.Float64("cpu.per.consumer", 1, "CPUs per consumer instance.")
var memPerConsumer = flag.Float64("mem.per.consumer", 256, "Memory per consumer instance.")
var executorBinaryName = flag.String("executor.name", "executor", "Executor binary name.")

var zookeeper = flag.String("zookeeper", "", "Zookeeper connection string separated by comma.")
var whitelist = flag.String("whitelist", "", "Whitelist of topics to consume.")
var blacklist = flag.String("blacklist", "", "Blacklist of topics to consume.")

func parseAndValidateSchedulerArgs() {
	flag.Parse()

	if *zookeeper == "" {
		fmt.Println("Zookeeper connection string is required.")
		os.Exit(1)
	}

	if *whitelist == "" && *blacklist == "" {
		fmt.Println("Whitelist or blacklist of topics to consume is required.")
		os.Exit(1)
	}
}

func startArtifactServer() {
	http.HandleFunc("/executor", func(w http.ResponseWriter, r *http.Request) {
			http.ServeFile(w, r, *executorBinaryName)
		})
	http.ListenAndServe(fmt.Sprintf(":%d", *artifactServerPort), nil)
}

func main() {
	parseAndValidateSchedulerArgs()
	kafka.Logger = kafka.NewDefaultLogger(kafka.DebugLevel)

	go startArtifactServer()

	frameworkInfo := &mesosproto.FrameworkInfo{
		User: proto.String(""),
		Name: proto.String("Go Kafka Client Framework"),
	}

	var filter kafka.TopicFilter
	if *whitelist != "" {
		filter = kafka.NewWhiteList(*whitelist)
	} else {
		filter = kafka.NewBlackList(*blacklist)
	}

	schedulerConfig := mesos.NewSchedulerConfig()
	schedulerConfig.CpuPerTask = *cpuPerConsumer
	schedulerConfig.MemPerTask = *memPerConsumer
	schedulerConfig.Filter = filter
	schedulerConfig.Zookeeper = strings.Split(*zookeeper, ",")
	schedulerConfig.ExecutorBinaryName = *executorBinaryName
	schedulerConfig.ArtifactServerPort = *artifactServerPort
	consumerScheduler, err := mesos.NewScheduler(schedulerConfig)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	driver, err := scheduler.NewMesosSchedulerDriver(consumerScheduler, frameworkInfo, *master, nil)

	if err != nil {
		fmt.Println("Unable to create a SchedulerDriver ", err.Error())
	}

	if stat, err := driver.Run(); err != nil {
		fmt.Println("Framework stopped with status %s and error: %s\n", stat.String(), err.Error())
	}
}