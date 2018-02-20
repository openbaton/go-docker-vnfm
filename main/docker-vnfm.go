package main

import (
	"github.com/openbaton/go-openbaton/vnfmsdk"
	"github.com/openbaton/go-openbaton/sdk"
	"github.com/openbaton/go-docker-vnfm/handler"
	"flag"
	"os"
	"fmt"
)

func main() {

	var configFile = flag.String("conf", "", "The config file of the Docker Vim Driver")
	var level = flag.String("level", "INFO", "The Log Level of the Docker Vim Driver")
	var persist = flag.Bool("persist", true, "to persist the local database using badger")
	var swarm = flag.Bool("swarm", false, "Use Handler for docker swarm services")
	var certFolder = flag.String("cert", "/Users/usr/.docker/machine/machines/myvm1/", "Use Handler for docker swarm services")
	var tsl = flag.Bool("tsl", false, "Use docker client with tsl")
	var dirPath = flag.String("dir", "badger", "The directory where to persist the local db")

	var typ = flag.String("type", "docker", "The type of the Docker Vim Driver")
	var name = flag.String("name", "docker", "The docker vnfm name")
	var description = flag.String("desc", "The docker vnfm", "The description of the Docker Vim Driver")
	var username = flag.String("username", "openbaton-manager-user", "The registering user")
	var password = flag.String("password", "openbaton", "The registering password")
	var brokerIp = flag.String("ip", "localhost", "The Broker Ip")
	var brokerPort = flag.Int("port", 5672, "The Broker Port")
	var workers = flag.Int("workers", 5, "The number of workers")
	var allocate = flag.Bool("allocate", true, "if the docker vnfm must allocate resources (must be true)")
	var timeout = flag.Int("timeout", 2, "Timeout of the Dial function")

	flag.Parse()
	pathExists, err := exists(*dirPath)
	if err != nil {
		fmt.Errorf("%v", err)
		os.Exit(12)
	}
	if !pathExists {
		err = os.MkdirAll(*dirPath, os.ModePerm);
		if err != nil {
			fmt.Errorf("%v", err)
			os.Exit(13)
		}
	}
	var h vnfmsdk.HandlerVnfm
	logger := sdk.GetLogger("docker-vnfm", *level)
	if *swarm {
		h = &handler.VnfmSwarmHandler{
			Logger:     logger,
			Tsl:        *tsl,
			CertFolder: *certFolder,
		}
	} else {
		h = &handler.VnfmImpl{
			Logger:     logger,
			Tsl:        *tsl,
			CertFolder: *certFolder,
		}
	}

	handler.InitDB(*persist, *dirPath)
	if *configFile != "" {
		vnfmsdk.Start(*configFile, h, "docker")
	} else {
		vnfmsdk.StartWithConfig(*typ, *description, *username, *password, *level, *brokerIp, *brokerPort, *workers, *timeout, *allocate, h, *name)
	}
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}
