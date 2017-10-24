package handler

import (
	"strings"
	"docker.io/go-docker"
	"github.com/openbaton/go-openbaton/catalogue"
	"net/http"
	"golang.org/x/net/context"
	"docker.io/go-docker/api/types/swarm"
	"docker.io/go-docker/api/types"
	"github.com/docker/go-connections/tlsconfig"
	"docker.io/go-docker/api"
	"path/filepath"
	"strconv"
	"crypto/tls"
	"docker.io/go-docker/api/types/mount"
	"fmt"
	"github.com/op/go-logging"
	"github.com/pkg/errors"
	"time"
)

func getClient(instance *catalogue.VIMInstance, certDirectory string, tsl bool) (*docker.Client, error) {
	var cli *docker.Client
	var err error
	if strings.HasPrefix(instance.AuthURL, "unix:") {
		cli, err = docker.NewClient(instance.AuthURL, instance.Tenant, nil, nil)
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

func createService(l *logging.Logger, client *docker.Client, ctx context.Context, replicas uint64, image, baseHostname string, networkIds, pubPorts []string) (*swarm.Service, error) {
	networks := make([]swarm.NetworkAttachmentConfig, 0)
	for _, netId := range networkIds {
		netName, err := getNetNameFromId(client, netId)
		if err != nil {
			l.Debugf("Error: %v", err)
			return nil, err
		}
		aliases := []string{fmt.Sprintf("%s.%s", baseHostname, netName), baseHostname}
		l.Debugf("Adding aliases %v --> %v", baseHostname, aliases)
		networks = append(networks, swarm.NetworkAttachmentConfig{
			Target:  netId,
			Aliases: aliases,
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
				Image:    image,
				Hostname: baseHostname,
			},
			Networks: networks,
		},
		Annotations: swarm.Annotations{
			Name: baseHostname,
		},
	}

	serviceCreateOptions := types.ServiceCreateOptions{

	}
	resp, err := client.ServiceCreate(ctx, serviceSpec, serviceCreateOptions)
	if err != nil {
		return nil, err
	}
	waitUntilIp(client, ctx, resp.ID)
	srv, err := waitUntilIp(client, ctx, resp.ID)
	return srv, nil
}
func waitUntilIp(client *docker.Client, ctx context.Context, id string) (*swarm.Service, error) {
	timeout := 0
	for {
		srv, _, err := client.ServiceInspectWithRaw(ctx, id, types.ServiceInspectOptions{})
		if hasIp(srv) {
			return &srv, err
		}
		if timeout > 1000 {
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

func updateService(l *logging.Logger, client *docker.Client, ctx context.Context, service *swarm.Service, replica uint64, env []string, mnts []string) (error) {
	mounts := make([]mount.Mount, len(mnts))

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
			RestartPolicy: &swarm.RestartPolicy{
				MaxAttempts: &maxAttempt,
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
