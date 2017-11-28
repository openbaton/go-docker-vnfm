package handler

import (
	"fmt"
	"time"
	"strconv"
	"strings"
	"net/http"
	"crypto/tls"
	"path/filepath"
	"docker.io/go-docker"
	"github.com/pkg/errors"
	"docker.io/go-docker/api"
	"golang.org/x/net/context"
	"github.com/op/go-logging"
	"docker.io/go-docker/api/types"
	"docker.io/go-docker/api/types/swarm"
	"docker.io/go-docker/api/types/mount"
	"github.com/docker/go-connections/tlsconfig"
	"github.com/openbaton/go-openbaton/catalogue"
	"runtime/debug"
)

func getClient(instance *catalogue.DockerVimInstance, certDirectory string, tsl bool) (*docker.Client, error) {
	var cli *docker.Client
	var err error
	if strings.HasPrefix(instance.AuthURL, "unix:") {
		cli, err = docker.NewClient(instance.AuthURL, api.DefaultVersion, nil, nil)
	} else {
		var tlsc *tls.Config
		if tsl {
			options := tlsconfig.Options{
				CAFile:             filepath.Join(certDirectory, "ca.pem"),
				CertFile:           filepath.Join(certDirectory, "cert.pem"),
				KeyFile:            filepath.Join(certDirectory, "key.pem"),
				InsecureSkipVerify: false,
			}
			tlsc, err = tlsconfig.Client(options)
			if err != nil {
				return nil, err
			}
		}
		http_client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: tlsc,
			},
			CheckRedirect: docker.CheckRedirect,
		}
		cli, err = docker.NewClient(instance.AuthURL, api.DefaultVersion, http_client, nil)
	}
	return cli, err
}

func createService(l *logging.Logger, client *docker.Client, ctx context.Context, replicas uint64, image, baseHostname string, cmd, networkIds, pubPorts, constraints []string, aliases map[string][]string) (*swarm.Service, error) {
	networks := make([]swarm.NetworkAttachmentConfig, 0)
	for _, netId := range networkIds {
		netName, err := getNetNameFromId(client, netId)
		if err != nil {
			l.Debugf("Error: %v", err)
			return nil, err
		}
		var als []string
		if val, ok := aliases[netName]; ok {
			als = val
		}
		//} else {
		//	als = []string{fmt.Sprintf("%s.%s", baseHostname, netName), baseHostname}
		//}
		l.Debugf("Adding aliases %v --> %v", baseHostname, als)
		networks = append(networks, swarm.NetworkAttachmentConfig{
			Target:  netId,
			Aliases: als,
		})
	}

	var ports []swarm.PortConfig
	if len(pubPorts) > 1 {
		trg, err := strconv.ParseUint(pubPorts[1], 10, 32)
		if err != nil {
			return nil, err
		}
		pub, err := strconv.ParseUint(pubPorts[0], 10, 32)
		if err != nil {
			return nil, err
		}
		ports = []swarm.PortConfig{{
			TargetPort:    uint32(trg),
			PublishedPort: uint32(pub),
		}}
	}
	serviceSpec := swarm.ServiceSpec{

		Mode: swarm.ServiceMode{
			Replicated: &swarm.ReplicatedService{
				Replicas: &replicas,
			},
		},
		EndpointSpec: &swarm.EndpointSpec{
			Ports: ports,
		},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{
				Command:  cmd,
				Image:    image,
				Hostname: baseHostname,
				//Privileges: &swarm.Privileges{
				//	SELinuxContext: &swarm.SELinuxContext{
				//		Disable: true,
				//	},
				//},
			},
			Networks: networks,
			Placement: &swarm.Placement{
				Constraints: constraints,
			},
		},
		Annotations: swarm.Annotations{
			Name: baseHostname,
		},
	}

	serviceCreateOptions := types.ServiceCreateOptions{

	}
	resp, err := client.ServiceCreate(ctx, serviceSpec, serviceCreateOptions)
	if err != nil {
		debug.PrintStack()
		return nil, err
	}
	srv, err := waitUntilIp(client, ctx, resp.ID)
	if err != nil {
		debug.PrintStack()
		return nil, err
	}
	return srv, nil
}
func waitUntilIp(client *docker.Client, ctx context.Context, id string) (*swarm.Service, error) {
	timeout := 0
	for {
		srv, _, err := client.ServiceInspectWithRaw(ctx, id, types.ServiceInspectOptions{})
		if hasIp(srv) {
			return &srv, err
		}
		if timeout > 10000 {
			return nil, errors.New("Timeout waiting for ip")
		}
		timeout++
		time.Sleep(5 * time.Millisecond)
	}
}
func hasIp(service swarm.Service) bool {
	for _, virtualIP := range service.Endpoint.VirtualIPs {
		ownIp := strings.Split(virtualIP.Addr, "/")[0]
		if ownIp != "" {
			return true
		}
	}
	return false
}

func updateService(l *logging.Logger, client *docker.Client, ctx context.Context, service *swarm.Service, replica uint64, env, mnts, constraints []string, restartPolicy string) (error) {
	mounts := make([]mount.Mount, len(mnts))
	var rp swarm.RestartPolicyCondition
	if restartPolicy == "on-failure" {
		rp = swarm.RestartPolicyConditionOnFailure
	} else if restartPolicy == "any" {
		rp = swarm.RestartPolicyConditionAny
	} else {
		rp = swarm.RestartPolicyConditionNone
	}
	for i, mnt := range mnts {
		split := strings.Split(mnt, ":")
		readOnly := false
		if len(split) > 2 {
			readOnly = split[2] == "ro"
		}
		l.Debugf("%s: Mount  %s --> %s", service.Spec.Name, split[0], split[1])
		mounts[i] = mount.Mount{
			Source:   split[0],
			Target:   split[1],
			ReadOnly: readOnly,
		}
	}
	var maxAttempt uint64 = 0
	serviceSpec := swarm.ServiceSpec{
		Mode: swarm.ServiceMode{
			Replicated: &swarm.ReplicatedService{
				Replicas: &replica,
			},
		},
		TaskTemplate: swarm.TaskSpec{
			Placement: &swarm.Placement{
				Constraints: constraints,
			},
			RestartPolicy: &swarm.RestartPolicy{
				MaxAttempts: &maxAttempt,
				Condition:   rp,
			},
			ContainerSpec: &swarm.ContainerSpec{
				Image:    service.Spec.TaskTemplate.ContainerSpec.Image,
				Hostname: service.Spec.TaskTemplate.ContainerSpec.Hostname,
				Command:  service.Spec.TaskTemplate.ContainerSpec.Command,
				Env:      env,
				Mounts:   mounts,
			},
			Networks: service.Spec.TaskTemplate.Networks,
		},
		EndpointSpec: service.Spec.EndpointSpec,
		Annotations:  service.Spec.Annotations,
	}
	serviceCreateOptions := types.ServiceUpdateOptions{

	}

	srv, _, _ := client.ServiceInspectWithRaw(ctx, service.ID, types.ServiceInspectOptions{})

	_, err := client.ServiceUpdate(ctx, service.ID, srv.Version, serviceSpec, serviceCreateOptions)
	if err != nil {

		return err
	}
	*service, _, err = client.ServiceInspectWithRaw(ctx, service.ID, types.ServiceInspectOptions{})
	return err
}

func GetEnv(l *logging.Logger, cfg VnfrConfig) []string {
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
	l.Noticef("%s: EnvVar: %v", cfg.Name, envList)
	return envList
}

func getNetNameFromId(cl *docker.Client, netId string) (string, error) {
	nets, _ := cl.NetworkList(ctx, types.NetworkListOptions{})
	for _, networkResource := range nets {
		if networkResource.ID == netId {
			return networkResource.Name, nil
		}
	}
	return "", errors.New(fmt.Sprintf("No network with id %v", netId))
}

func GetIpsFromService(cli *docker.Client, l *logging.Logger, config *VnfrConfig, vnfr *catalogue.VirtualNetworkFunctionRecord, srv *swarm.Service) (ips []*catalogue.IP, fips []*catalogue.IP, err error) {
	err = nil
	fips = make([]*catalogue.IP, 0)
	ips = make([]*catalogue.IP, 0)
	l.Debugf("%v", *srv)
	for _, virtualIP := range (*srv).Endpoint.VirtualIPs {
		l.Debugf("%v, IP: %v", vnfr.Name, virtualIP)
		nameFromId, err := getNetNameFromId(cli, virtualIP.NetworkID)
		if err != nil {
			return nil, nil, err
		}
		ownIp := strings.Split(virtualIP.Addr, "/")[0]
		config.Own[strings.ToUpper(nameFromId)] = ownIp
		ips = append(ips, &catalogue.IP{
			IP:      ownIp,
			NetName: nameFromId,
		})
		//fips = append(fips, &catalogue.IP{
		//	NetName: nameFromId,
		//	IP:      strings.Split(strings.Split(vimInstanceChosen.AuthURL, "//")[1], ":")[0],
		//})
	}
	return
}
