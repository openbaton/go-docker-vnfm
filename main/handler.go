package main

import (
	"fmt"
	"time"
	"bufio"
	"bytes"
	"errors"
	"strings"
	"context"
	"net/http"
	"io/ioutil"
	"encoding/gob"
	"encoding/json"
	"github.com/op/go-logging"
	"github.com/dgraph-io/badger"
	"github.com/docker/docker/client"
	"github.com/docker/docker/api/types"
	"github.com/docker/go-connections/nat"
	"github.com/openbaton/go-openbaton/sdk"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/openbaton/go-openbaton/catalogue"
	"github.com/docker/docker/api/types/container"
)

type NetConf struct {
	IpV4Address string
}

type VnfrConfig struct {
	VnfrID      string
	ContainerID string
	Name        string
	DNSs        []string
	ImageName   string
	Cmd         strslice.StrSlice
	ExpPort     []string
	Own         map[string]string
	NetworkCfg  map[string]NetConf
	Foreign     map[string][]map[string]string
	VimInstance catalogue.VIMInstance
}

var (
	opt = badger.DefaultOptions
	kv  *badger.KV
	ctx = context.Background()
)

func getClient(instance catalogue.VIMInstance) (*client.Client, error) {
	var cli *client.Client
	var err error
	if strings.HasPrefix(instance.AuthURL, "unix:") {
		cli, err = client.NewClient(instance.AuthURL, instance.Tenant, nil, nil)
	} else {
		http_client := &http.Client{
			Transport: &http.Transport{
				//TLSClientConfig: tlsc,
			},
			CheckRedirect: client.CheckRedirect,
		}
		cli, err = client.NewClient(instance.AuthURL, instance.Tenant, http_client, nil)
	}
	return cli, err
}

func InitDB(persist bool, dir_path string) {
	var dir string
	if !persist {
		dir, _ = ioutil.TempDir(dir_path, "badger")
	} else {
		dir = dir_path
	}
	opt.Dir = dir
	opt.ValueDir = dir
	var err error
	kv, err = badger.NewKV(&opt)
	if err != nil {
		fmt.Errorf("Error while creating database: %v", err)
	}
}

type HandlerVnfmImpl struct {
	logger *logging.Logger
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
	h.logger.Errorf("Recevied Error for vnfr: %v", vnfr.Name)
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
		Cmd:     strslice.StrSlice{},
		DNSs:    make([]string, 3),
		ExpPort: make([]string, 3),
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
	vnfr.VDUs[0].VNFCInstances = make([]*catalogue.VNFCInstance, 1)
	config.VimInstance = *vimInstances[vnfr.VDUs[0].ParentVDU][0]
	lenCps := len(vnfr.VDUs[0].VNFCs[0].ConnectionPoints)
	cps := make([]*catalogue.VNFDConnectionPoint, lenCps)
	config.NetworkCfg = make(map[string]NetConf)
	for _, cp := range vnfr.VDUs[0].VNFCs[0].ConnectionPoints {
		config.NetworkCfg[cp.VirtualLinkReference] = NetConf{
			IpV4Address: cp.FixedIp,
		}
		cps = append(cps, &catalogue.VNFDConnectionPoint{
			VirtualLinkReference: cp.VirtualLinkReference,
			FloatingIP:           "random",
			Type:                 "docker",
			InterfaceID:          0,
			FixedIp:              cp.FixedIp,
			ChosenPool:           cp.ChosenPool,
		})
	}

	hostname := fmt.Sprintf("%s-%s", vnfr.Name, sdk.RandomString(4))
	vnfr.VDUs[0].VNFCInstances[0] = &catalogue.VNFCInstance{
		VIMID:            config.VimInstance.ID,
		Hostname:         hostname,
		State:            "ACTIVE",
		VCID:             vnfr.VDUs[0].VNFCs[0].ID,
		ConnectionPoints: cps,
		VNFComponent:     vnfr.VDUs[0].VNFCs[0],
	}
	config.ImageName = vnfr.VDUs[0].VMImages[0]
	config.Name = hostname
	err := saveConfig(vnfr.ID, config)
	return vnfr, err
}

func saveConfig(vnfrId string, config VnfrConfig) error {
	//lock.Lock()
	//defer lock.Unlock()
	buf := new(bytes.Buffer)
	err := gob.NewEncoder(buf).Encode(config)
	if err != nil {
		return err
	}
	return kv.Set([]byte(vnfrId), buf.Bytes(), 0x00)
}

func (h *HandlerVnfmImpl) Modify(vnfr *catalogue.VirtualNetworkFunctionRecord, dependency *catalogue.VNFRecordDependency) (*catalogue.VirtualNetworkFunctionRecord, error) {
	js, _ := json.Marshal(dependency)
	h.logger.Noticef("DepencencyRecord is: %s", string(js))
	config := VnfrConfig{}
	err := getConfig(vnfr.ID, &config)
	if err != nil {
		h.logger.Errorf("Error while getting config: %v", err)
		return nil, err
	}

	for foreignName, vnfcDepParam := range dependency.VNFCParameters {
		if config.Foreign == nil {
			config.Foreign = make(map[string][]map[string]string)
		}
		config.Foreign[foreignName] = make([]map[string]string, len(vnfcDepParam.Parameters))
		x := 0
		for _, depParam := range vnfcDepParam.Parameters {
			h.logger.Debugf("Adding to config.foreign: %s", depParam.Parameters)
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
		h.logger.Debugf("TempMap is %v", tmpMap)
		config.Foreign[foreignName] = append(config.Foreign[foreignName], tmpMap)
	}
	h.logger.Noticef("%s: Foreign Config is: %v", config.Name, config.Foreign)
	saveConfig(vnfr.ID, config)
	return vnfr, nil
}

func getConfig(vnfrId string, config *VnfrConfig) error {
	kvItem := badger.KVItem{}
	kv.Get([]byte(vnfrId), &kvItem)
	return kvItem.Value(func(bs []byte) error {
		buf := bytes.NewBuffer(bs)
		err := gob.NewDecoder(buf).Decode(config)
		return err
	})
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
		h.logger.Errorf("Error while getting config: %v", err)
		return nil, err
	}
	resp, err := h.dockerStartContainer(cfg)
	if err != nil {
		return nil, err
	}
	cfg.ContainerID = resp.ID
	saveConfig(vnfr.ID, cfg)
	return vnfr, nil
}

func (h *HandlerVnfmImpl) dockerStartContainer(cfg VnfrConfig) (*container.ContainerCreateCreatedBody, error) {

	cl, err := getClient(cfg.VimInstance)
	if err != nil {
		h.logger.Errorf("Error while getting client: %v", err)
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

	h.logger.Noticef("%s: EnvVar: %v", cfg.Name, envList)
	h.logger.Noticef("%s: Image: %v", cfg.Name, cfg.ImageName)

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

func (h *HandlerVnfmImpl) readLogsFromContainer(cl *client.Client, contID string, cfg VnfrConfig) {
	logs, _ := cl.ContainerLogs(ctx, contID, types.ContainerLogsOptions{
		Details:    false,
		Follow:     false,
		Timestamps: true,
	})
	if logs != nil {
		for {
			rd := bufio.NewReader(logs)
			line, _, err := rd.ReadLine()
			h.logger.Infof("%s: Logs: %v", cfg.Name, string(line))
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
	h.logger.Noticef("Remove container for vnfr: %v", vnfr.Name)
	cfg := &VnfrConfig{}
	err := getConfig(vnfr.ID, cfg)
	if err != nil {
		h.logger.Errorf("Error while getting config: %v", err)
		return nil, err
	}
	cl, err := getClient(cfg.VimInstance)
	if err != nil {
		h.logger.Errorf("Error while getting client: %v", err)
		return nil, err
	}
	var timeout time.Duration = 10 * time.Second
	cl.ContainerStop(ctx, cfg.ContainerID, &timeout)
	cl.ContainerRemove(ctx, cfg.ContainerID, types.ContainerRemoveOptions{
		Force: true,
	})
	deleteConfig(vnfr.ID)

	return vnfr, nil
}

func deleteConfig(vnfrId string) error {
	return kv.Delete([]byte(vnfrId))
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
