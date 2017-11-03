package handler

import (
	"fmt"
	"errors"
	"strings"
	"github.com/op/go-logging"
	"docker.io/go-docker/api/types/swarm"
	"docker.io/go-docker/api/types/strslice"
	"github.com/openbaton/go-openbaton/catalogue"
)

type Aliases map[string][]string

type NetConf struct {
	IpV4Address string
}

type VnfrConfig struct {
	VnfrID        string
	ContainerIDs  map[string][]string
	Name          string
	DNSs          []string
	ImageName     string
	BaseHostname  string
	RestartPolicy string
	Cmd           strslice.StrSlice
	PubPort       []string
	ExpPort       []string
	Constraints   []string
	Mnts          []string
	Own           map[string]string
	NetworkCfg    map[string]NetConf
	Foreign       map[string][]map[string]string
	VimInstance   map[string]*catalogue.VIMInstance
	VduService    map[string]swarm.Service
}

func NewVnfrConfig(vnfr *catalogue.VirtualNetworkFunctionRecord) VnfrConfig {
	return VnfrConfig{
		VnfrID:       vnfr.ID,
		DNSs:         make([]string, 0),
		PubPort:      make([]string, 0),
		ExpPort:      make([]string, 0),
		Constraints:  make([]string, 0),
		VimInstance:  make(map[string]*catalogue.VIMInstance),
		ContainerIDs: make(map[string][]string),
		Own:          make(map[string]string),
		NetworkCfg:   make(map[string]NetConf),
		VduService:   make(map[string]swarm.Service),
	}
}

func FillConfig(vnfr *catalogue.VirtualNetworkFunctionRecord, config *VnfrConfig) Aliases {
	aliases := make(map[string][]string)
	for _, cp := range vnfr.Configurations.ConfigurationParameters {
		kLower := strings.ToLower(cp.ConfKey)
		if strings.Contains(kLower, "cmd") || strings.Contains(kLower, "command") {
			config.Cmd = strings.Split(cp.Value, " ")
		} else if strings.Contains(kLower, "publish") {
			config.PubPort = append(config.PubPort, cp.Value)
		} else if kLower == "aliases" { // aliases looks like mgmt:name1,name2;net_d:name3,name4
			if strings.Contains(cp.Value, ";") {
				for _, val := range strings.Split(cp.Value, ";") {
					netName, als := ExtractAliases(val)
					aliases[netName] = als
				}
			} else {
				netName, als := ExtractAliases(cp.Value)
				aliases[netName] = als
			}
		} else if strings.Contains(kLower, "expose") {
			config.ExpPort = append(config.ExpPort, cp.Value)
		} else if strings.Contains(kLower, "restart_policy_condition") {
			config.RestartPolicy = cp.Value
		} else if strings.Contains(kLower, "constraints") {
			if strings.Contains(cp.Value, ";") {
				config.Constraints = strings.Split(cp.Value, ";")
			} else {
				config.Constraints = []string{cp.Value}
			}
		} else if kLower == "volumes" {
			if strings.Contains(cp.Value, ";") {
				config.Mnts = strings.Split(cp.Value, ";")
			} else {
				config.Mnts = []string{cp.Value}
			}
		} else if strings.Contains(kLower, "dns") {
			config.DNSs = append(config.DNSs, cp.Value)
		} else if strings.Contains(kLower, "hostname") {
			config.BaseHostname = cp.Value
		} else {
			config.Own[cp.ConfKey] = cp.Value
		}
	}
	return aliases
}

func ExtractAliases(val string) (string, []string) {
	alias := strings.Split(val, ":")
	var alPerNet []string
	if strings.Contains(alias[1], ",") {
		alPerNet = strings.Split(alias[1], ",")
	} else {
		alPerNet = []string{alias[1]}
	}
	return alias[0], alPerNet
}

func chooseImage(vdu *catalogue.VirtualDeploymentUnit, vimInstance *catalogue.VIMInstance) (string, error) {
	for _, img := range vimInstance.Images {
		for _, imgName := range vdu.VMImages {
			if img.Name == imgName || img.ID == imgName {
				return imgName, nil
			}
		}
	}
	return "", errors.New(fmt.Sprintf("Image with name or id %v not found", vdu.VMImages))
}

func GetCPsAndIpsFromFixedIps(vdu *catalogue.VirtualDeploymentUnit, l *logging.Logger, vnfr *catalogue.VirtualNetworkFunctionRecord, config VnfrConfig) ([]*catalogue.IP, []*catalogue.VNFDConnectionPoint, []string) {
	netNames := make([]string, 0)
	cps := make([]*catalogue.VNFDConnectionPoint, 0)
	ips := make([]*catalogue.IP, 0)
	for _, cp := range vdu.VNFCs[0].ConnectionPoints {
		l.Debugf("%s: Fixed Ip is: %v", vnfr.Name, cp.FixedIp)
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
		l.Debugf("Adding New Connection Point: %v", newCp)
		cps = append(cps, newCp)
		netNames = append(netNames, cp.VirtualLinkReference)
		ips = append(ips, &catalogue.IP{
			NetName: cp.VirtualLinkReference,
			IP:      cp.FixedIp,
		})
		config.Own[strings.ToUpper(cp.VirtualLinkReference)] = cp.FixedIp
	}
	return ips, cps, netNames
}

func SetupVNFCInstance(vdu *catalogue.VirtualDeploymentUnit, vimInstanceChosen *catalogue.VIMInstance, hostname string, cps []*catalogue.VNFDConnectionPoint, fips []*catalogue.IP, ips []*catalogue.IP) {
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
}
