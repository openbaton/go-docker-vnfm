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
	h := &handler.HandlerVnfmImpl{
		Logger: sdk.GetLogger("docker-vnfm", *level),
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
