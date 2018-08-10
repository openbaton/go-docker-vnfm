package handler

import (
	"context"
	"fmt"
	"github.com/op/go-logging"
	"github.com/openbaton/go-openbaton/catalogue"
	"github.com/openbaton/go-openbaton/sdk"
	"testing"

	client "docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"errors"
	"github.com/stretchr/testify/assert"
	"strings"
	//"time"
	"encoding/json"
)

var log *logging.Logger = sdk.GetLogger("docker_vnfm_test", "DEBUG")

func TestDockerListImages(t *testing.T) {

	cli, err := client.NewEnvClient()

	if err != nil {
		panic(err)
	}
	background := context.Background()
	fmt.Println(cli.ClientVersion())
	fmt.Println(cli.DaemonHost())
	fmt.Println(cli.Info(background))
	images, err := cli.ImageList(background, types.ImageListOptions{})
	if err != nil {
		panic(err)
	}

	for _, image := range images {
		fmt.Println(image.RepoTags)
	}
}

func TestDockerListImagesByName(t *testing.T) {

	cli, err := client.NewEnvClient()

	if err != nil {
		panic(err)
	}
	images, err := getImagesByName(cli, "ubuntu")
	if err != nil {
		panic(err)
	}

	fmt.Println(len(images))
	for _, image := range images {
		fmt.Println(image.RepoTags)
	}
}

func getImagesByName(cl *client.Client, imageName string) ([]types.ImageSummary, error) {
	//var args filters.Args
	//args = filters.NewArgs(filters.KeyValuePair{
	//	Key:   "repotag",
	//	Value: imageName,
	//})
	imgs, err := cl.ImageList(ctx, types.ImageListOptions{})
	res := make([]types.ImageSummary, 0)
	if err != nil {
		return nil, err
	}
	for _, img := range imgs {
		if stringInSlice(imageName, img.RepoTags) {
			res = append(res, img)
		}
	}
	if len(res) == 0 {
		return nil, errors.New(fmt.Sprintf("Image with name %s not found", imageName))
	}
	return res, nil
}

func stringInSlice(a string, list []string) bool {
	for _, b := range list {
		if strings.Contains(b, a) {
			return true
		}
	}
	return false
}

func TestDockerListContainers(t *testing.T) {

	cli, err := client.NewEnvClient()

	if err != nil {
		panic(err)
	}
	background := context.Background()
	containers, err := cli.ContainerList(background, types.ContainerListOptions{})
	if err != nil {
		panic(err)
	}

	for _, container := range containers {
		fmt.Printf("%v\n", container.ID)
		fmt.Printf("%v\n", container.Names)
		fmt.Printf("%v\n", container.HostConfig)
		fmt.Printf("%v\n", container.Labels)
		for _, net := range container.NetworkSettings.Networks {
			fmt.Printf("\t%v\n", net.IPAddress)
			fmt.Printf("\t%v\n", net.NetworkID)
			fmt.Printf("\t%v\n", net.EndpointID)
			fmt.Printf("\t%v\n", net.Gateway)
			fmt.Printf("\t%v\n", net.MacAddress)
			fmt.Printf("\t%v\n", net.Links)
			fmt.Printf("\t%v\n", net.Aliases)
			fmt.Printf("\t%v\n", net.DriverOpts)
		}
	}
}

func TestCreateService(t *testing.T) {
	cli, err := getClient(getVimInstance(), "", false)
	assert.NoError(t, err)
	imagename := "ubuntu"
	hostname := "ubuntu"
	netName := "ingress"
	netIds, err := getNetworkIdsFromNames(cli, []string{netName})
	assert.NoError(t, err)
	res, err := createServiceWait(log, cli, ctx, 0, imagename, hostname, []string{"while true; echo 'openbaton'"}, netIds, []string{}, []string{}, make(map[string][]string), false)
	if !assert.NoError(t, err) {
		assert.FailNow(t, err.Error())
	}
	log.Debugf("Response is %v", res)
	service, _, err := cli.ServiceInspectWithRaw(ctx, res.ID, types.ServiceInspectOptions{})
	if !assert.NoError(t, err) {
		assert.FailNow(t, err.Error())
	}
	err = updateService(log, cli, ctx, &service, 5, []string{}, []string{}, []string{}, "")
	if !assert.NoError(t, err) {
		assert.FailNow(t, err.Error())
	}
	val, err := json.MarshalIndent(service, "", "  ")
	fmt.Println(fmt.Sprintf("%v", string(val)))
}

func getVimInstance() *catalogue.DockerVimInstance {
	return &catalogue.DockerVimInstance{
		BaseVimInstance: catalogue.BaseVimInstance{
			AuthURL: "unix:///var/run/docker.sock",
			Name:    "test-vim-instance",
			Type:    "docker",
		},
	}
}
