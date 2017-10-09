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
)

const certDirectory string = "/Users/lto/.docker/machine/machines/myvm1/"

func getClient(instance *catalogue.VIMInstance, certDirectory string, tsl bool) (*docker.Client, error) {
	var cli *docker.Client
	var err error
	if strings.HasPrefix(instance.AuthURL, "unix:") {
		cli, err = docker.NewClient(instance.AuthURL, instance.Tenant, nil, nil)
	} else {
		var tlsc *tls.Config
		if tsl{
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

func createService(client *docker.Client, ctx context.Context, replicas uint64, image, hostname string, networkIds, pubPorts []string) (*swarm.Service, error) {
	networks := make([]swarm.NetworkAttachmentConfig, 0)
	for _, netName := range networkIds {
		networks = append(networks, swarm.NetworkAttachmentConfig{
			Target:  netName,
			Aliases: []string{hostname},
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
				Hostname: hostname,
			},
			Networks: networks,
		},
		Annotations: swarm.Annotations{
			Name: hostname,
		},
	}
	serviceCreateOptions := types.ServiceCreateOptions{

	}
	resp, err := client.ServiceCreate(ctx, serviceSpec, serviceCreateOptions)
	if err != nil {
		return nil, err
	}
	srv, _, err := client.ServiceInspectWithRaw(ctx, resp.ID, types.ServiceInspectOptions{})
	return &srv, nil
}

func updateService(client *docker.Client, ctx context.Context, service *swarm.Service, replica uint64, env []string) (error) {
	serviceSpec := swarm.ServiceSpec{
		Mode: swarm.ServiceMode{
			Replicated: &swarm.ReplicatedService{
				Replicas: &replica,
			},
		},
		TaskTemplate: swarm.TaskSpec{
			ContainerSpec: &swarm.ContainerSpec{
				Image:    service.Spec.TaskTemplate.ContainerSpec.Image,
				Hostname: service.Spec.TaskTemplate.ContainerSpec.Hostname,
				Command:  service.Spec.TaskTemplate.ContainerSpec.Command,
				Env:      env,
			},
			Networks: service.Spec.TaskTemplate.Networks,
		},

		EndpointSpec: service.Spec.EndpointSpec,
		Annotations:  service.Spec.Annotations,
	}
	serviceCreateOptions := types.ServiceUpdateOptions{

	}

	_, err := client.ServiceUpdate(ctx, service.ID, service.Version, serviceSpec, serviceCreateOptions)
	if err != nil {
		return err
	}
	*service, _, err = client.ServiceInspectWithRaw(ctx, service.ID, types.ServiceInspectOptions{})
	return err
}
