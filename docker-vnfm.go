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

	var configFile = flag.String("conf", "config.toml", "The config file of the Docker Vim Driver")
	var level = flag.String("level", "INFO", "The Log Level of the Docker Vim Driver")
	var persist = flag.Bool("persist", true, "to persist the local database using badger")
	var swarm = flag.Bool("swarm", true, "Use Handler for docker swarm services")
	var certFolder = flag.String("swarm", "/Users/usr/.docker/machine/machines/myvm1/", "Use Handler for docker swarm services")
	var tsl = flag.Bool("tsl", false, "Use docker client with tsl")
	var dirPath = flag.String("dir", "badger", "The directory where to persist the local db")

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
		h = &handler.HandlerVnfmSwarm{
			Logger:     logger,
			Tsl:        *tsl,
			CertFolder: *certFolder,
		}
	} else {
		h = &handler.HandlerVnfmImpl{
			Logger:     logger,
			Tsl:        *tsl,
			CertFolder: *certFolder,
		}
	}

	handler.InitDB(*persist, *dirPath)
	vnfmsdk.Start(*configFile, h, "docker")
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
