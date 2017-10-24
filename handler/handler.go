package handler

import (
	"fmt"
	"time"
	"bufio"
	"errors"
	"strings"
	"context"
	"encoding/json"
	"github.com/op/go-logging"
	"github.com/dgraph-io/badger"
	"docker.io/go-docker"
	"docker.io/go-docker/api/types"
	"github.com/docker/go-connections/nat"
	"docker.io/go-docker/api/types/network"
	"docker.io/go-docker/api/types/strslice"
	"github.com/openbaton/go-openbaton/catalogue"
	"docker.io/go-docker/api/types/container"
	"docker.io/go-docker/api/types/swarm"
	"math/rand"
	"runtime/debug"
	"docker.io/go-docker/api/types/mount"
	"math"
)

type NetConf struct {
	IpV4Address string
}

type VnfrConfig struct {
	VnfrID       string
	ContainerIDs map[string][]string
	Name         string
	DNSs         []string
	ImageName    string
	BaseHostname string
	Cmd          strslice.StrSlice
	PubPort      []string
	ExpPort      []string
	Mnts         []string
	Own          map[string]string
	NetworkCfg   map[string]NetConf
	Foreign      map[string][]map[string]string
	VimInstance  map[string]*catalogue.VIMInstance
	VduService   map[string]swarm.Service
}

var (
	opt = badger.DefaultOptions
	kv  *badger.KV
	ctx = context.Background()
)

type HandlerVnfmImpl struct {
	Logger     *logging.Logger
	Tsl        bool
	CertFolder string
}

func (h *HandlerVnfmImpl) ActionForResume(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) catalogue.Action {
	return catalogue.NoActionSpecified
}

func (h *HandlerVnfmImpl) CheckInstantiationFeasibility() error {

	return nil
}

func (h *HandlerVnfmImpl) Configure(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmImpl) HandleError(vnfr *catalogue.VirtualNetworkFunctionRecord) error {
	h.Logger.Errorf("Recevied Error for vnfr: %v", vnfr.Name)
	return nil
}

func (h *HandlerVnfmImpl) Heal(vnfr *catalogue.VirtualNetworkFunctionRecord, component *catalogue.VNFCInstance, cause string) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmImpl) Instantiate(vnfr *catalogue.VirtualNetworkFunctionRecord, scripts interface{}, vimInstances map[string][]*catalogue.VIMInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	if vnfr.VDUs == nil {
		return nil, errors.New("no VDU provided")
	}
	config := VnfrConfig{
		VnfrID:       vnfr.ID,
		DNSs:         make([]string, 0),
		PubPort:      make([]string, 0),
		VimInstance:  make(map[string]*catalogue.VIMInstance),
		ContainerIDs: make(map[string][]string),
	}
	ownConfig := make(map[string]string)

	for _, cp := range vnfr.Configurations.ConfigurationParameters {
		kLower := strings.ToLower(cp.ConfKey)
		if strings.Contains(kLower, "cmd") || strings.Contains(kLower, "command") {
			config.Cmd = strings.Split(cp.Value, " ")
		} else if strings.Contains(kLower, "publish") {
			config.PubPort = append(config.PubPort, cp.Value)
		} else if kLower == "volumes" {
			if strings.Contains(cp.Value, ";") {
				config.Mnts = strings.Split(cp.Value, ";")
			} else {
				config.Mnts = []string{cp.Value}
			}

		} else if strings.Contains(kLower, "expose") {
			config.ExpPort = append(config.PubPort, cp.Value)
		} else if strings.Contains(kLower, "dns") {
			config.DNSs = append(config.DNSs, cp.Value)
		} else if strings.Contains(kLower, "hostname") {
			config.BaseHostname = cp.Value
		} else {
			ownConfig[cp.ConfKey] = cp.Value
		}
	}
	config.Own = ownConfig

	config.NetworkCfg = make(map[string]NetConf)
	netNames := make([]string, 0)
	pubPorts := make([]string, 0)
	for _, ps := range config.PubPort {
		split := strings.Split(ps, ":")
		h.Logger.Debugf("Ports: %d --> %d", split[0], split[1])
		pubPorts = append(pubPorts, split[0], split[1])
	}

	for _, vdu := range vnfr.VDUs {
		vdu.VNFCInstances = make([]*catalogue.VNFCInstance, 0)
		vimInstanceChosen := vimInstances[vdu.ParentVDU][rand.Intn(len(vimInstances[vdu.ParentVDU]))]
		config.VimInstance[vdu.ID] = vimInstanceChosen

		cps := make([]*catalogue.VNFDConnectionPoint, 0)

		h.Logger.Debugf("%v VNF has %v VNFC(s)", vnfr.Name, len(vdu.VNFCs))
		ips := make([]*catalogue.IP, 0)
		for _, cp := range vdu.VNFCs[0].ConnectionPoints {
			h.Logger.Debugf("%s: Fixed Ip is: %v", vnfr.Name, cp.FixedIp)
			config.NetworkCfg[cp.VirtualLinkReference] = NetConf{
				IpV4Address: cp.FixedIp,
			}
			newCp := &catalogue.VNFDConnectionPoint{
				VirtualLinkReference: cp.VirtualLinkReference,
				FloatingIP:           "random",
				Type:                 "docker",
				InterfaceID:          0,
				FixedIp:              cp.FixedIp,
				ChosenPool:           cp.ChosenPool,
			}
			h.Logger.Debugf("Adding New Connection Point: %v", newCp)
			cps = append(cps, newCp)
			netNames = append(netNames, cp.VirtualLinkReference)
			ips = append(ips, &catalogue.IP{
				NetName: cp.VirtualLinkReference,
				IP:      cp.FixedIp,
			})
			config.Own[strings.ToUpper(cp.VirtualLinkReference)] = cp.FixedIp
		}
		imageChosen, err := chooseImage(vdu, vimInstanceChosen)
		hostname := fmt.Sprintf("%s", vnfr.Name)
		if err != nil {
			debug.PrintStack()
			return nil, err
		}
		config.ImageName = imageChosen

		for _, vnfc := range vdu.VNFCs {
			vdu.VNFCInstances = append(vdu.VNFCInstances, &catalogue.VNFCInstance{
				VIMID:            vimInstanceChosen.ID,
				Hostname:         hostname,
				State:            "ACTIVE",
				VCID:             vnfc.ID,
				ConnectionPoints: cps,
				VNFComponent:     vnfc,
				//FloatingIPs:      fips,
				IPs: ips,
			})
		}

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

func (h *HandlerVnfmImpl) Modify(vnfr *catalogue.VirtualNetworkFunctionRecord, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
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

func (h *HandlerVnfmImpl) Query() error {
	return nil
}

func (h *HandlerVnfmImpl) Resume(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmImpl) Scale(scaleInOrOut catalogue.Action, vnfr *catalogue.VirtualNetworkFunctionRecord, component catalogue.Component, scripts interface{}, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmImpl) Start(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	cfg := VnfrConfig{}
	err := getConfig(vnfr.ID, &cfg, h.Logger)
	if err != nil {
		h.Logger.Errorf("Error while getting config: %v", err)
		return nil, err
	}
	for _, vdu := range vnfr.VDUs {
		resp, err := h.dockerStartContainer(cfg, vdu)
		if err != nil {
			return nil, err
		}
		cfg.ContainerIDs[vdu.ID] = append(cfg.ContainerIDs[vdu.ID], resp.ID)
	}
	SaveConfig(vnfr.ID, cfg, h.Logger)
	return vnfr, nil
}

func (h *HandlerVnfmImpl) dockerStartContainer(cfg VnfrConfig, vdu *catalogue.VirtualDeploymentUnit) (*container.ContainerCreateCreatedBody, error) {

	cl, err := getClient(cfg.VimInstance[vdu.ID], h.CertFolder, h.Tsl)
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
	if err != nil {
		h.Logger.Errorf("Error while getting client: %v", err)
		return nil, err
	}
	endCfg := make(map[string]*network.EndpointSettings)
	for netName, values := range cfg.NetworkCfg {
		hostnames := []string{fmt.Sprintf("%s.%s", cfg.Name, netName)}
		h.Logger.Debugf("%v: Network %v --> %v, %v", cfg.Name, netName, values.IpV4Address, hostnames)
		netIds, _ := getNetworkIdsFromNames(cl, []string{netName})
		endCfg[netName] = &network.EndpointSettings{
			IPAddress: values.IpV4Address,
			Aliases:   hostnames,
			IPAMConfig: &network.EndpointIPAMConfig{
				IPv4Address: values.IpV4Address,
			},
			NetworkID: netIds[0],
		}
	}
	firstCfg := make(map[string]*network.EndpointSettings)
	x := math.MaxInt64
	var firstNetName string
	for _, vnfc := range vdu.VNFCs {
		for _, cp := range vnfc.ConnectionPoints {
			if cp.InterfaceID < x {
				firstNetName = cp.VirtualLinkReference
				x = cp.InterfaceID
			}
		}
	}
	h.Logger.Debugf("%s: First network is: %s", cfg.Name, firstNetName)
	firstCfg[firstNetName] = endCfg[firstNetName]

	delete(endCfg, firstNetName)
	networkingConfig := network.NetworkingConfig{
		EndpointsConfig: firstCfg,
	}
	pBinds := make(nat.PortSet)
	for _, v := range cfg.PubPort {
		p, _ := nat.NewPort("tcp", v)
		pBinds[p] = struct{}{}
	}
	hostCfg := container.HostConfig{
		DNS:    cfg.DNSs,
		CapAdd: []string{"NET_ADMIN", "SYS_ADMIN"},
		Mounts: mounts,
	}
	envList := GetEnv(h.Logger, cfg)

	//h.Logger.Noticef("%s: EnvVar: %v", cfg.Name, envList)
	h.Logger.Noticef("%s: Image: %v", cfg.Name, cfg.ImageName)

	config := &container.Config{
		Image: cfg.ImageName,
		Env:   envList,
		//ExposedPorts: pBinds,
		Hostname: cfg.Name,
		Cmd:      cfg.Cmd,
	}

	resp, err := cl.ContainerCreate(ctx, config, &hostCfg, &networkingConfig, cfg.Name)
	if err != nil {
		return nil, err
	}

	options := types.ContainerStartOptions{
	}
	if err := cl.ContainerStart(ctx, resp.ID, options); err != nil {
		return nil, err
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
	return &resp, nil
}

func (h *HandlerVnfmImpl) readLogsFromContainer(cl *docker.Client, contID string, cfg VnfrConfig) {
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

func (h *HandlerVnfmImpl) StartVNFCInstance(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmImpl) Stop(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmImpl) StopVNFCInstance(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmImpl) Terminate(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
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
		var timeout time.Duration = 10 * time.Second
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

func (h *HandlerVnfmImpl) UpdateSoftware(script *catalogue.Script, vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmImpl) UpgradeSoftware() error {
	return nil
}

func (h *HandlerVnfmImpl) UserData() string {
	return ""
}
