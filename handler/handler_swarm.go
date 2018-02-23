package handler

import (
	"fmt"
	"bufio"
	"errors"
	"math/rand"
	"runtime/debug"
	"encoding/json"
	"docker.io/go-docker"
	"github.com/op/go-logging"
	"docker.io/go-docker/api/types"
	"github.com/openbaton/go-openbaton/catalogue"
)

type VnfmSwarmHandler struct {
	Logger     *logging.Logger
	Tsl        bool
	CertFolder string
}

func (h *VnfmSwarmHandler) ActionForResume(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) catalogue.Action {
	return catalogue.NoActionSpecified
}

func (h *VnfmSwarmHandler) CheckInstantiationFeasibility() error {

	return nil
}

func (h *VnfmSwarmHandler) Configure(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmSwarmHandler) HandleError(vnfr *catalogue.VirtualNetworkFunctionRecord) error {
	h.Logger.Errorf("Recevied Error for vnfr: %v", vnfr.Name)
	return nil
}

func (h *VnfmSwarmHandler) Heal(vnfr *catalogue.VirtualNetworkFunctionRecord, component *catalogue.VNFCInstance, cause string) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmSwarmHandler) Instantiate(vnfr *catalogue.VirtualNetworkFunctionRecord, scripts interface{}, vimInstances map[string][]interface{}) (*catalogue.VirtualNetworkFunctionRecord, error) {
	if vnfr.VDUs == nil {
		return nil, errors.New("no VDU provided")
	}
	config := NewVnfrConfig(vnfr)
	aliases := FillConfig(vnfr, &config, h.Logger)

	config.NetworkCfg = make(map[string]NetConf)

	pubPorts := make([]string, 0)

	for _, ps := range config.PubPort {
		pubPorts = append(pubPorts, ps[0], ps[1])
	}

	for _, vdu := range vnfr.VDUs {
		vdu.VNFCInstances = make([]*catalogue.VNFCInstance, 0)
		vimInstanceChosen := vimInstances[vdu.ParentVDU][rand.Intn(len(vimInstances[vdu.ParentVDU]))]
		dockerVimInstance := vimInstanceChosen.(*catalogue.DockerVimInstance)
		config.VimInstance[vdu.ID] = dockerVimInstance

		h.Logger.Debugf("%v VNF has %v VNFC(s)", vnfr.Name, len(vdu.VNFCs))
		cli, err := getClient(dockerVimInstance, h.CertFolder, h.Tsl)
		if err != nil {
			h.Logger.Errorf("Error: %v", err)
			return nil, err
		}
		_, cps, netNames, err := GetCPsAndIpsFromFixedIps(cli, vdu, h.Logger, vnfr, config)
		if err != nil {
			h.Logger.Errorf("Error: %v", err)
			return nil, err
		}
		imageChosen, err := chooseImage(vdu, dockerVimInstance)
		if err != nil {
			debug.PrintStack()
			return nil, err
		}
		config.ImageName = imageChosen
		// Starting service
		if config.BaseHostname == "" {
			config.BaseHostname = fmt.Sprintf("%s", vnfr.Name)
		}

		netIds, err := getNetworkIdsFromNames(cli, netNames)
		if err != nil {
			h.Logger.Errorf("Error: %v", err)
			return nil, err
		}
		srv, err := createService(h.Logger, cli, ctx, 0, config.ImageName, config.BaseHostname, config.Cmd, netIds, pubPorts, config.Constraints, aliases)
		if err != nil {
			debug.PrintStack()
			h.Logger.Errorf("Error: %v", err)
			return nil, err
		}

		ips, fips, err := GetIpsFromService(cli, h.Logger, &config, vnfr, srv)

		SetupVNFCInstance(vdu, dockerVimInstance, config.BaseHostname, cps, fips, ips)

		config.Name = vnfr.Name

		config.VduService[vdu.ID] = *srv
	}

	err := SaveConfig(vnfr.ID, config, h.Logger)
	if err != nil {
		h.Logger.Errorf("Error: %v", err)
		return nil, err
	}
	return vnfr, err
}

func (h *VnfmSwarmHandler) Modify(vnfr *catalogue.VirtualNetworkFunctionRecord, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
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
	h.Logger.Debugf("%s: Foreign Config is: %v", config.Name, config.Foreign)
	SaveConfig(vnfr.ID, config, h.Logger)
	return vnfr, nil
}

func (h *VnfmSwarmHandler) Query() error {
	return nil
}

func (h *VnfmSwarmHandler) Resume(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmSwarmHandler) Scale(scaleInOrOut catalogue.Action, vnfr *catalogue.VirtualNetworkFunctionRecord, component catalogue.Component, scripts interface{}, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmSwarmHandler) Start(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	cfg := VnfrConfig{}
	err := getConfig(vnfr.ID, &cfg, h.Logger)
	if err != nil {
		h.Logger.Errorf("Error while getting config: %v", err)
		return nil, err
	}
	//resp, err := h.dockerStartContainer(cfg)
	for _, vdu := range vnfr.VDUs {
		cli, err := getClient(cfg.VimInstance[vdu.ID], h.CertFolder, h.Tsl)
		if err != nil {
			h.Logger.Errorf("Error while getting Client: %v", err)
			return nil, err
		}
		var vnfcCount uint64
		for _, vdu := range vnfr.VDUs {
			vnfcCount += uint64(len(vdu.VNFCs))
		}
		service := cfg.VduService[vdu.ID]
		err = updateService(h.Logger, cli, ctx, &service, vnfcCount, GetEnv(h.Logger, cfg), cfg.Mnts, cfg.Constraints, cfg.RestartPolicy)
		if err != nil {
			h.Logger.Errorf("Unable to update: %v", err)
			//return nil, err
		}
	}
	SaveConfig(vnfr.ID, cfg, h.Logger)
	return vnfr, nil
}

func (h *VnfmSwarmHandler) readLogsFromContainer(cl *docker.Client, contID string, cfg VnfrConfig) {
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

func (h *VnfmSwarmHandler) StartVNFCInstance(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmSwarmHandler) Stop(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmSwarmHandler) StopVNFCInstance(vnfr *catalogue.VirtualNetworkFunctionRecord, vnfcInstance *catalogue.VNFCInstance) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmSwarmHandler) Terminate(vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
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
		cl.ServiceRemove(ctx, cfg.VduService[vdu.ID].ID)
	}
	deleteConfig(vnfr.ID)

	return vnfr, nil
}

func (h *VnfmSwarmHandler) UpdateSoftware(script *catalogue.Script, vnfr *catalogue.VirtualNetworkFunctionRecord) (*catalogue.VirtualNetworkFunctionRecord, error) {
	return vnfr, nil
}

func (h *VnfmSwarmHandler) UpgradeSoftware() error {
	return nil
}

func (h *VnfmSwarmHandler) UserData() string {
	return ""
}
