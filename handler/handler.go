package handler

import (
	"fmt"
	"math"
	"time"
	"bufio"
	"errors"
	"strings"
	"context"
	"math/rand"
	"runtime/debug"
	"encoding/json"
	"docker.io/go-docker"
	"github.com/op/go-logging"
	"github.com/dgraph-io/badger"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/mount"
	"github.com/docker/go-connections/nat"
	"docker.io/go-docker/api/types/network"
	"docker.io/go-docker/api/types/container"
	"github.com/openbaton/go-openbaton/catalogue"
)

var (
	opt = badger.DefaultOptions
	kv  *badger.KV
	ctx = context.Background()
)

type VnfmImpl struct {
	Logger     *logging.Logger
	Tsl        bool
	CertFolder string
}

func (h *VnfmImpl) ActionForResume(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) catalogue.Action {
	return catalogue.NoActionSpecified
}

func (h *VnfmImpl) CheckInstantiationFeasibility() error {

	return nil
}

func (h *VnfmImpl) Configure(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmImpl) HandleError(vnfr *catalogue.VirtualNetworkFunctionRecord) error {
	h.Logger.Errorf("Recevied Error for vnfr: %v", vnfr.Name)
	return nil
}

func (h *VnfmImpl) Heal(vnfr *catalogue.VirtualNetworkFunctionRecord, component *catalogue.VNFCInstance, cause string) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmImpl) Instantiate(vnfr *catalogue.VirtualNetworkFunctionRecord, scripts interface{}, vimInstances map[string][]interface{}) (*catalogue.VirtualNetworkFunctionRecord, error) {
	if vnfr.VDUs == nil {
		return nil, errors.New("no VDU provided")
	}
	config := NewVnfrConfig(vnfr)
	FillConfig(vnfr, &config)

	pubPorts := make([]string, 0)
	for _, ps := range config.ExpPort {
		h.Logger.Debugf("Ports: %v", ps)
		pubPorts = append(pubPorts, ps)
	}

	for _, vdu := range vnfr.VDUs {
		vdu.VNFCInstances = make([]*catalogue.VNFCInstance, 0)
		vimInstanceChosen := vimInstances[vdu.ParentVDU][rand.Intn(len(vimInstances[vdu.ParentVDU]))]
		dockerVimInstance := vimInstanceChosen.(*catalogue.DockerVimInstance)
		config.VimInstance[vdu.ID] = dockerVimInstance

		h.Logger.Debugf("%v VNF has %v VNFC(s)", vnfr.Name, len(vdu.VNFCs))

		cl, err := getClient(dockerVimInstance, h.CertFolder, h.Tsl)
		if err != nil {
			h.Logger.Errorf("Error while getting client: %v", err)
			return nil, err
		}
		ips, cps, _, err := GetCPsAndIpsFromFixedIps(cl, vdu, h.Logger, vnfr, config)

		if err != nil {
			h.Logger.Errorf("Error while getting CP: %v", err)
			return nil, err
		}
		imageChosen, err := chooseImage(vdu, dockerVimInstance)
		hostname := fmt.Sprintf("%s", vnfr.Name)
		if err != nil {
			debug.PrintStack()
			return nil, err
		}
		config.ImageName = imageChosen

		SetupVNFCInstance(vdu, dockerVimInstance, hostname, cps, nil, ips)

		config.Name = vnfr.Name
	}

	err := SaveConfig(vnfr.ID, config, h.Logger)
	if err != nil {
		h.Logger.Errorf("Error: %v", err)
		return nil, err
	}
	return vnfr, err
}

func getNetworkIdsFromNames(cli *docker.Client, netNames []string) ([]string, error) {
	nets, err := cli.NetworkList(ctx, types.NetworkListOptions{})
	if err != nil {
		return nil, err
	}
	res := make([]string, 0)
	for _, net := range nets {
		for _, netname := range netNames {
			if net.Name == netname {
				res = append(res, net.ID)
				break
			}
		}
	}
	return res, nil
}

func (h *VnfmImpl) Modify(vnfr *catalogue.VirtualNetworkFunctionRecord, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	js, _ := json.Marshal(dependency)
	h.Logger.Debugf("DepencencyRecord is: %s", string(js))
	config := VnfrConfig{}
	err := getConfig(vnfr.ID, &config, h.Logger)
	if err != nil {
		h.Logger.Errorf("Error while getting config: %v", err)
		return nil, err
	}

	for foreignName, vnfcDepParam := range dependency.VNFCParameters {
		if config.Foreign == nil {
			config.Foreign = make(map[string][]map[string]string)
		}
		config.Foreign[foreignName] = make([]map[string]string, len(vnfcDepParam.Parameters))
		x := 0
		for _, depParam := range vnfcDepParam.Parameters {
			h.Logger.Debugf("Adding to config.foreign: %s", depParam.Parameters)
			config.Foreign[foreignName][x] = depParam.Parameters
			x++
		}
	}

	for foreignName, depParam := range dependency.Parameters {
		tmpMap := make(map[string]string)
		for key, val := range depParam.Parameters {
			if val != "" {
				tmpMap[key] = val
			}
		}
		config.Foreign[foreignName] = append(config.Foreign[foreignName], tmpMap)
	}
	//h.Logger.Debugf("%s: Foreign Config is: %v", config.Name, config.Foreign)
	SaveConfig(vnfr.ID, config, h.Logger)
	return vnfr, nil
}

func (h *VnfmImpl) Query() error {
	return nil
}

func (h *VnfmImpl) Resume(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmImpl) Scale(scaleInOrOut catalogue.Action, vnfr *catalogue.VirtualNetworkFunctionRecord, component catalogue.Component, scripts interface{}, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	switch scaleInOrOut {
	case catalogue.ActionScaleOut:
		cfg := VnfrConfig{}
		getConfig(vnfr.ID, &cfg, h.Logger)
		//TODO handle the case with multiple VDU!
		switch vnfci := component.(type) {
		case *catalogue.VNFCInstance:
			id, ips, err := h.startContainer(cfg, vnfr.VDUs[0].ID, firstNet(vnfci))
			if err != nil {
				return nil, err
			}
			i := 0
			for k, v := range ips {
				vnfci.IPs[i] = &catalogue.IP{
					NetName: k,
					IP:      v,
				}
				i++
			}
			vnfci.VCID = id
		default:
			return nil, errors.New(fmt.Sprintf("Received type %T but VNFCInstance required", vnfci))
		}
	case catalogue.ActionScaleIn:
	}
	return vnfr, nil
}

func (h *VnfmImpl) Start(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	cfg := VnfrConfig{}
	err := getConfig(vnfr.ID, &cfg, h.Logger)
	if err != nil {
		h.Logger.Errorf("Error while getting config: %v", err)
		return nil, err
	}
	for _, vdu := range vnfr.VDUs {
		for _, vnfc := range vdu.VNFCInstances {
			id, ips, err := h.startContainer(cfg, vdu.ID, firstNet(vnfc))
			if err != nil {
				return nil, err
			}
			vnfc.VCID = id
			vnfc.IPs = make([]*catalogue.IP, len(ips))
			i := 0
			for k, v := range ips {
				vnfc.IPs[i] = &catalogue.IP{
					NetName: k,
					IP:      v,
				}
				i++
			}
		}
	}
	SaveConfig(vnfr.ID, cfg, h.Logger)
	return vnfr, nil
}

func (h *VnfmImpl) startContainer(cfg VnfrConfig, vduID string, firstNetName string) (string, map[string]string, error) {

	cl, err := getClient(cfg.VimInstance[vduID], h.CertFolder, h.Tsl)
	if err != nil {
		h.Logger.Errorf("Error while getting client: %v", err)
		return "", nil, err
	}
	mounts := make([]mount.Mount, len(cfg.Mnts))

	for i, mnt := range cfg.Mnts {
		split := strings.Split(mnt, ":")
		readOnly := false
		if len(split) > 2 {
			readOnly = split[2] == "ro"
		}
		h.Logger.Debugf("%s: Mount  %s --> %s", cfg.Name, split[0], split[1])
		mounts[i] = mount.Mount{
			Source:   split[0],
			Target:   split[1],
			ReadOnly: readOnly,
			Type:     mount.TypeBind,
		}
	}

	endCfg := make(map[string]*network.EndpointSettings)
	for netId, values := range cfg.NetworkCfg {
		net, err := cl.NetworkInspect(ctx, netId, types.NetworkInspectOptions{})
		if err != nil {
			h.Logger.Errorf("Network with id [%s] not found", netId)
			return "", nil, err
		}
		aliases := []string{fmt.Sprintf("%s.%s", cfg.Name, strings.Split(net.Name, "_")[0])}
		h.Logger.Debugf("%s: Aliases: %v", cfg.Name, aliases)
		endCfg[net.Name] = &network.EndpointSettings{
			IPAddress: values.IpV4Address,
			Aliases:   aliases,
			IPAMConfig: &network.EndpointIPAMConfig{
				IPv4Address: values.IpV4Address,
			},
			NetworkID: netId,
		}
	}

	firstCfg := make(map[string]*network.EndpointSettings)
	h.Logger.Debugf("%s: First network is: %s", cfg.Name, firstNetName)
	firstCfg[firstNetName] = endCfg[firstNetName]

	delete(endCfg, firstNetName)
	networkingConfig := network.NetworkingConfig{
		EndpointsConfig: firstCfg,
	}

	portBindings := make(nat.PortMap)

	expPorts := make(nat.PortSet)
	var pubAllPort = false
	for _, v := range cfg.PubPort {
		pubAllPort = true
		port, err := nat.NewPort("tcp", v)
		if err != nil {
			debug.PrintStack()
			return "", nil, err
		}
		expPorts[port] = struct{}{}
		portBindings[port] = []nat.PortBinding{{
			HostIP:   "0.0.0.0",
			HostPort: port.Port(),
		},
		}
	}
	hostCfg := container.HostConfig{
		DNS:          cfg.DNSs,
		CapAdd:       []string{"NET_ADMIN", "SYS_ADMIN"},
		Mounts:       mounts,
		PortBindings: portBindings,

		PublishAllPorts: pubAllPort,
	}
	envList := GetEnv(h.Logger, cfg)

	h.Logger.Noticef("%s: Image: %v", cfg.Name, cfg.ImageName)

	config := &container.Config{
		Image:        cfg.ImageName,
		Env:          envList,
		ExposedPorts: expPorts,
		Hostname:     cfg.Name,
		Cmd:          cfg.Cmd,
	}

	h.Logger.Debugf("NetworkConfig is %+v", networkingConfig)

	resp, err := cl.ContainerCreate(ctx, config, &hostCfg, &networkingConfig, fmt.Sprintf("%s-%d", cfg.Name, randInt(1000, 9999)))
	if err != nil {
		return "", nil, err
	}

	options := types.ContainerStartOptions{
	}
	if err := cl.ContainerStart(ctx, resp.ID, options); err != nil {
		return "", nil, err
	}

	for netName, endpointSettings := range endCfg {
		h.Logger.Debugf("%v: Adding network %v", cfg.Name, netName)
		netIds, _ := getNetworkIdsFromNames(cl, []string{netName})
		err := cl.NetworkConnect(ctx, netIds[0], resp.ID, endpointSettings)
		if err != nil {
			h.Logger.Errorf("Error connecting to network: ", err)
		}
	}

	go h.readLogsFromContainer(cl, resp.ID, cfg)
	cfg.ContainerIDs[vduID] = append(cfg.ContainerIDs[vduID], resp.ID)
	c, err := cl.ContainerInspect(ctx, resp.ID)
	if err != nil {
		return "", nil, err
	}
	ips := make(map[string]string)
	for netName, cfg := range c.NetworkSettings.Networks {
		ips[netName] = cfg.IPAddress
	}
	return resp.ID, ips, nil
}

func firstNet(vnfc *catalogue.VNFCInstance) string {
	currId := math.MaxInt64
	var firstNetName string
	for _, cp := range vnfc.ConnectionPoints {
		if cp.InterfaceID < currId {
			firstNetName = cp.VirtualLinkReference
			currId = cp.InterfaceID
		}
	}
	return firstNetName
}

func randInt(min int, max int) int {
	rand.Seed(time.Now().UTC().UnixNano())
	return min + rand.Intn(max-min)
}

func (h *VnfmImpl) readLogsFromContainer(cl *docker.Client, contID string, cfg VnfrConfig) {
	time.Sleep(5 * time.Second)
	logs, _ := cl.ContainerLogs(ctx, contID, types.ContainerLogsOptions{
		Details:    false,
		Follow:     false,
		Timestamps: true,
	})
	if logs != nil {
		for {
			rd := bufio.NewReader(logs)
			line, _, err := rd.ReadLine()
			h.Logger.Debugf("%s: Logs: %v", cfg.Name, string(line))
			if err != nil {
				break
			}
		}
	}
}

func (h *VnfmImpl) StartVNFCInstance(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmImpl) Stop(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmImpl) StopVNFCInstance(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmImpl) Terminate(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	h.Logger.Noticef("Remove container for vnfr: %v", vnfr.Name)
	cfg := &VnfrConfig{}
	err := getConfig(vnfr.ID, cfg, h.Logger)
	if err != nil {
		h.Logger.Errorf("Error while getting config: %v", err)
		h.Logger.Errorf("Probably not found")
		return vnfr, nil
	}
	for _, vdu := range vnfr.VDUs {
		cl, err := getClient(cfg.VimInstance[vdu.ID], h.CertFolder, h.Tsl)
		if err != nil {
			h.Logger.Errorf("Error while getting client: %v", err)
			return nil, err
		}
		var timeout = 10 * time.Second
		for _, ids := range cfg.ContainerIDs {
			for _, id := range ids {
				cl.ContainerStop(ctx, id, &timeout)
				go cl.ContainerRemove(ctx, id, types.ContainerRemoveOptions{
					Force: true,
				})
			}
		}
	}
	deleteConfig(vnfr.ID)

	return vnfr, nil
}

func (h *VnfmImpl) UpdateSoftware(script *catalogue.Script, vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmImpl) UpgradeSoftware() error {
	return nil
}

func (h *VnfmImpl) UserData() string {
	return ""
}
