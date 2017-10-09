package handler

import (
	"github.com/op/go-logging"
	"bufio"
	"strings"
	"fmt"
	"errors"
	"encoding/json"
	"github.com/openbaton/go-openbaton/sdk"
	"github.com/openbaton/go-openbaton/catalogue"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker"
	"math/rand"
	"docker.io/go-docker/api/types/swarm"
	"runtime/debug"
)

type HandlerVnfmSwarm struct {
	Logger *logging.Logger
	Tsl bool
}

func (h *HandlerVnfmSwarm) ActionForResume(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) catalogue.Action {
	return catalogue.NoActionSpecified
}

func (h *HandlerVnfmSwarm) CheckInstantiationFeasibility() error {

	return nil
}

func (h *HandlerVnfmSwarm) Configure(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) HandleError(vnfr *catalogue.VirtualNetworkFunctionRecord) error {
	h.Logger.Errorf("Recevied Error for vnfr: %v", vnfr.Name)
	return nil
}

func (h *HandlerVnfmSwarm) Heal(vnfr *catalogue.VirtualNetworkFunctionRecord, component *catalogue.VNFCInstance, cause string) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) Instantiate(vnfr *catalogue.VirtualNetworkFunctionRecord, scripts interface{}, vimInstances map[string][]*catalogue.VIMInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	if vnfr.VDUs == nil {
		return nil, errors.New("no VDU provided")
	}
	config := VnfrConfig{
		VnfrID:       vnfr.ID,
		DNSs:         make([]string, 0),
		ExpPort:      make([]string, 0),
		VimInstance:  make(map[string]*catalogue.VIMInstance),
		VduService:   make(map[string]swarm.Service),
		ContainerIDs: make(map[string][]string),
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
		vdu.VNFCInstances = make([]*catalogue.VNFCInstance, 0)
		vimInstanceChosen := vimInstances[vdu.ParentVDU][rand.Intn(len(vimInstances[vdu.ParentVDU]))]
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
		imageChosen, err := chooseImage(vdu, vimInstanceChosen)
		if err != nil {
			debug.PrintStack()
			return nil, err
		}
		config.ImageName = imageChosen
		// Starting service
		hostname := fmt.Sprintf("%s-%s", vnfr.Name, sdk.RandomString(4))
		cli, err := getClient(vimInstanceChosen, certDirectory, h.Tsl)
		if err != nil {
			h.Logger.Errorf("Error: %v", err)
			return nil, err
		}
		netIds, err := getNetworkIdsFromNames(cli, netNames)
		if err != nil {
			h.Logger.Errorf("Error: %v", err)
			return nil, err
		}
		srv, err := createService(cli, ctx, 0, config.ImageName, hostname, netIds, pubPorts)
		if err != nil {
			h.Logger.Errorf("Error: %v", err)
			return nil, err
		}

		//TODO fix for more than one CP
		fips := make([]*catalogue.IP, 0)
		ips := make([]*catalogue.IP, 0)
		for _, net := range srv.Endpoint.VirtualIPs {
			h.Logger.Debugf("%v, FIP: %v", vnfr.Name, net)
			nameFromId, err := getNetNameFromId(cli, net.NetworkID)
			if err != nil {
				return nil, err
			}
			ips = append(fips, &catalogue.IP{
				IP:      strings.Split(net.Addr, "/")[0],
				NetName: nameFromId,
			})
			fips = append(fips, &catalogue.IP{
				NetName: nameFromId,
				IP:      strings.Split(strings.Split(vimInstanceChosen.AuthURL, "//")[1], ":")[0],
			})
		}

		for _, vnfc := range vdu.VNFCs {
			vdu.VNFCInstances = append(vdu.VNFCInstances, &catalogue.VNFCInstance{
				VIMID:            vimInstanceChosen.ID,
				Hostname:         hostname,
				State:            "ACTIVE",
				VCID:             vnfc.ID,
				ConnectionPoints: cps,
				VNFComponent:     vnfc,
				FloatingIPs:      fips,
				IPs:              ips,
			})
		}

		config.Name = vnfr.Name

		config.VduService[vdu.ID] = *srv
	}

	err := SaveConfig(vnfr.ID, config)
	if err != nil {
		h.Logger.Errorf("Error: %v", err)
		return nil, err
	}
	return vnfr, err
}
func chooseImage(vdu *catalogue.VirtualDeploymentUnit, vimInstance *catalogue.VIMInstance) (string, error) {
	for _, img := range vimInstance.Images{
		for _, imgName := range vdu.VMImages{
			if img.Name == imgName || img.ID == imgName{
				return imgName, nil
			}
		}
	}
	return "", errors.New(fmt.Sprintf("Image with name or id %v not found", vdu.VMImages))
}
func getNetNameFromId(cl *docker.Client, netId string) (string, error) {
	nets, _ := cl.NetworkList(ctx, types.NetworkListOptions{})
	for _, net := range nets {
		if net.ID == netId {
			return net.Name, nil
		}
	}
	return "", errors.New(fmt.Sprintf("No network with id %v", netId))
}

func (h *HandlerVnfmSwarm) Modify(vnfr *catalogue.VirtualNetworkFunctionRecord, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
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
		config.Foreign[foreignName] = make([]map[string]string, 0)
		for _, depParam := range vnfcDepParam.Parameters {
			h.Logger.Debugf("Adding to config.foreign: %s", depParam.Parameters)
			config.Foreign[foreignName] = append(config.Foreign[foreignName], depParam.Parameters)
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

func (h *HandlerVnfmSwarm) Query() error {
	return nil
}

func (h *HandlerVnfmSwarm) Resume(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) Scale(scaleInOrOut catalogue.Action, vnfr *catalogue.VirtualNetworkFunctionRecord, component catalogue.Component, scripts interface{}, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) Start(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	cfg := VnfrConfig{}
	err := getConfig(vnfr.ID, &cfg)
	if err != nil {
		h.Logger.Errorf("Error while getting config: %v", err)
		return nil, err
	}
	//resp, err := h.dockerStartContainer(cfg)
	for _, vdu := range vnfr.VDUs {
		cli, err := getClient(cfg.VimInstance[vdu.ID], certDirectory, h.Tsl)
		if err != nil {
			h.Logger.Errorf("Error while getting Client: %v", err)
			return nil, err
		}
		var vnfcCount uint64
		for _, vdu := range vnfr.VDUs {
			vnfcCount += uint64(len(vdu.VNFCs))
		}
		service := cfg.VduService[vdu.ID]
		updateService(cli, ctx, &service, vnfcCount, h.getEnv(cfg))
		if err != nil {
			return nil, err
		}
	}
	SaveConfig(vnfr.ID, cfg)
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) getEnv(cfg VnfrConfig) []string {
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
	return envList
}

func (h *HandlerVnfmSwarm) readLogsFromContainer(cl *docker.Client, contID string, cfg VnfrConfig) {
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

func (h *HandlerVnfmSwarm) StartVNFCInstance(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) Stop(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) StopVNFCInstance(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) Terminate(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	h.Logger.Noticef("Remove container for vnfr: %v", vnfr.Name)
	cfg := &VnfrConfig{}
	err := getConfig(vnfr.ID, cfg)
	if err != nil {
		h.Logger.Errorf("Error while getting config: %v", err)
		h.Logger.Errorf("Probably not found")
		return vnfr, nil
	}
	for _, vdu := range vnfr.VDUs {
		cl, err := getClient(cfg.VimInstance[vdu.ID], certDirectory, h.Tsl)
		if err != nil {
			h.Logger.Errorf("Error while getting client: %v", err)
			return nil, err
		}
		cl.ServiceRemove(ctx, cfg.VduService[vdu.ID].ID)
	}
	deleteConfig(vnfr.ID)

	return vnfr, nil
}

func (h *HandlerVnfmSwarm) UpdateSoftware(script *catalogue.Script, vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *HandlerVnfmSwarm) UpgradeSoftware() error {
	return nil
}

func (h *HandlerVnfmSwarm) UserData() string {
	return ""
}
