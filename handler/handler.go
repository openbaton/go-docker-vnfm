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
	"github.com/openbaton/go-openbaton/sdk"
	"docker.io/go-docker/api/types/network"
	"docker.io/go-docker/api/types/strslice"
	"github.com/openbaton/go-openbaton/catalogue"
	"docker.io/go-docker/api/types/container"
	"docker.io/go-docker/api/types/swarm"
	"math/rand"
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
	Cmd          strslice.StrSlice
	ExpPort      []string
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
		VnfrID:  vnfr.ID,
		DNSs:    make([]string, 0),
		ExpPort: make([]string, 0),
	}
	ownConfig := make(map[string]string)

	for _, cp := range vnfr.Configurations.ConfigurationParameters {
		kLower := strings.ToLower(cp.ConfKey)
		if strings.Contains(kLower, "cmd") || strings.Contains(kLower, "command") {
			config.Cmd = strings.Split(cp.Value, " ")
		} else if strings.Contains(kLower, "expose") {
			config.ExpPort = append(config.ExpPort, cp.Value)
		} else if strings.Contains(kLower, "dns") {
			config.DNSs = append(config.DNSs, cp.Value)
		} else {
			ownConfig[cp.ConfKey] = cp.Value
		}
	}
	config.Own = ownConfig

	config.NetworkCfg = make(map[string]NetConf)
	netNames := make([]string, 0)
	pubPorts := make([]string, 0)
	for _, ps := range config.ExpPort {
		split := strings.Split(ps, ":")
		pubPorts = append(pubPorts, split[0], split[1])
	}

	for _, vdu := range vnfr.VDUs {
		vnfr.VDUs[0].VNFCInstances = make([]*catalogue.VNFCInstance, 1)
		vimInstanceChosen := vimInstances[vdu.ParentVDU][rand.Intn(len(vimInstances[vdu.ParentVDU]))-1]
		config.VimInstance[vdu.ID] = vimInstanceChosen

		cps := make([]*catalogue.VNFDConnectionPoint, 0)

		h.Logger.Debugf("%v VNF has %v VNFC(s)", vnfr.Name, len(vdu.VNFCs))
		for _, cp := range vdu.VNFCs[0].ConnectionPoints {
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
		}
		config.ImageName = vdu.VMImages[0]
		hostname := fmt.Sprintf("%s-%s", vnfr.Name, sdk.RandomString(4))

		for _, vnfc := range vdu.VNFCs {
			vdu.VNFCInstances = append(vdu.VNFCInstances, &catalogue.VNFCInstance{
				VIMID:            vimInstanceChosen.ID,
				Hostname:         hostname,
				State:            "ACTIVE",
				VCID:             vnfc.ID,
				ConnectionPoints: cps,
				VNFComponent:     vnfc,
				//FloatingIPs:      fips,
				//IPs:              ips,
			})
		}

		config.Name = vnfr.Name
	}

	err := SaveConfig(vnfr.ID, config)
	if err != nil {
		h.Logger.Errorf("Error: %v", err)
		return nil, err
	}
	return vnfr, err
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
	h.Logger.Noticef("DepencencyRecord is: %s", string(js))
	config := VnfrConfig{}
	err := getConfig(vnfr.ID, &config)
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
		h.Logger.Debugf("TempMap is %v", tmpMap)
		config.Foreign[foreignName] = append(config.Foreign[foreignName], tmpMap)
	}
	h.Logger.Noticef("%s: Foreign Config is: %v", config.Name, config.Foreign)
	SaveConfig(vnfr.ID, config)
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
	err := getConfig(vnfr.ID, &cfg)
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
	SaveConfig(vnfr.ID, cfg)
	return vnfr, nil
}

func (h *HandlerVnfmImpl) dockerStartContainer(cfg VnfrConfig, vdu *catalogue.VirtualDeploymentUnit) (*container.ContainerCreateCreatedBody, error) {

	cl, err := getClient(cfg.VimInstance[vdu.ID], h.CertFolder, h.Tsl)
	if err != nil {
		h.Logger.Errorf("Error while getting client: %v", err)
		return nil, err
	}
	endCfg := make(map[string]*network.EndpointSettings)
	for netName, values := range cfg.NetworkCfg {
		endCfg[netName] = &network.EndpointSettings{
			IPAddress: values.IpV4Address,
			Aliases:   []string{cfg.Name},
		}
	}

	networkingConfig := network.NetworkingConfig{
		EndpointsConfig: endCfg,
	}
	pBinds := make(nat.PortSet)
	for _, v := range cfg.ExpPort {
		p, _ := nat.NewPort("tcp", v)
		pBinds[p] = struct{}{}
	}
	hostCfg := container.HostConfig{
		DNS: cfg.DNSs,
	}
	envList := make([]string, len(cfg.Own))

	x := 0
	for k, v := range cfg.Own {
		envList[x] = fmt.Sprintf("%s=%s", k, v)
		x++
	}
	x = 0
	for k, dp := range cfg.Foreign {
		for _, kv := range dp {
			for key, val := range kv {
				envList = append(envList, fmt.Sprintf("%s_%s=%s", strings.ToUpper(k), strings.ToUpper(key), val))
				x++
			}
		}
	}

	h.Logger.Noticef("%s: EnvVar: %v", cfg.Name, envList)
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
	h.readLogsFromContainer(cl, resp.ID, cfg)
	return &resp, nil
}

func (h *HandlerVnfmImpl) readLogsFromContainer(cl *docker.Client, contID string, cfg VnfrConfig) {
	logs, _ := cl.ContainerLogs(ctx, contID, types.ContainerLogsOptions{
		Details:    false,
		Follow:     false,
		Timestamps: true,
	})
	if logs != nil {
		for {
			rd := bufio.NewReader(logs)
			line, _, err := rd.ReadLine()
			h.Logger.Infof("%s: Logs: %v", cfg.Name, string(line))
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
	err := getConfig(vnfr.ID, cfg)
	if err != nil {
		h.Logger.Errorf("Error while getting config: %v", err)
		return nil, err
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
				cl.ContainerRemove(ctx, id, types.ContainerRemoveOptions{
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
