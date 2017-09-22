package main

import (
	"github.com/openbaton/go-openbaton/vnfmsdk"
	"github.com/openbaton/go-openbaton/sdk"
)

func main() {

	h := &HandlerVnfmImpl{
		logger: sdk.GetLogger("docker-vnfm","DEBUG"),
	}
	InitDB(true,"badger")
	vnfmsdk.Start("config.toml", h, "docker")
}
