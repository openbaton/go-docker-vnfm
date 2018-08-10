  <img src="https://raw.githubusercontent.com/openbaton/openbaton.github.io/master/images/openBaton.png" width="250"/>

  Copyright © 2015-2016 [Open Baton](http://openbaton.org).
  Licensed under [Apache v2 License](http://www.apache.org/licenses/LICENSE-2.0).

# Docker VNFM for Open Baton
This VNF Manager, together with the [Docker Vim Driver](https://github.com/openbaton/go-docker-driver), allows Open Baton to deploy Container on top of a running [Docker](https://www.docker.com/) engine.

Both VNFM and VIM Driver are necessary in order to be able to deploy NS over Docker

# How to install the Docker VNFM

## Requirements

The go compiler has to be installed, please follow the go documentation on how to [download](https://golang.org/dl/) it

## Build the VNFM

Assuming that your `GOPATH` variable is set to $HOME/go (find out typing `go env`), run the following commands:

```bash
mkdir -p ~/go/src/github.com/openbaton
cd ~/go/src/github.com/openbaton
git clone git@github.com:openbaton/go-docker-vnfm.git
cd go-docker-vnfm
dep ensure
go build -o go-docker-vnfm
```

Afterwards check the usage by running:

```bash
./go-docker-vnfm --help
```

# How to start the Docker VNFM

If you don't need special configuration, start the go-docker-vnfm just by running:

```bash
./go-docker-vnfm
```

# How to use the Docker VNFM

The Docker VNFM works with the upstream Open Baton NFVO, so no changes are needed. Some fields of the VNFD could have a different meaning. An example of a MongoDB VNFPackage follows

### The VNFD

```json
{
    "name": "MongoDB",
    "vendor": "TUB",
    "version": "0.2",
    "lifecycle_event": [],
    "configurations": {
      "configurationParameters": [{
        "confKey":"KEY",
        "value":"Value"
      }],
      "name": "mongo-configuration"
    },
    "virtual_link": [{
      "name": "new-network"
    }],
    "vdu": [{
      "vm_image": [
      ],
      "scale_in_out": 2,
      "vnfc": [{
        "connection_point": [{
          "virtual_link_reference": "new-network"
        }]
      }]
    }],
    "deployment_flavour": [{
      "flavour_key": "m1.small"
    }],
    "type": "mongodb",
    "endpoint": "docker"
  }
```

* The _**Virtual Link**_ will be a new Docker Network created if not existing.
* The _**flavour_key**_ must be set to _m1.small_ (at the moment)  
* The _**vm_image**_ will be filled by the _metadata_ image name (see next section)  

### The Metadata.yaml

```yaml
name: MongoDB
description: MongoDB
provider: TUB
nfvo_version: 4.0.0
vim_types:
 - docker
image:
    upload: "check"
    names:
        - "mongo:latest"
    link: "mongo:latest"
image-config:
    name: "mongo:latest"
    diskFormat: QCOW2
    containerFormat: BARE
    minCPU: 0
    minDisk: 0
    minRam: 0
    isPublic: false
```

Here you can see some differences:
* **vim_types** must have docker (pointing to the Docker VIM Driver)
* **image upload** can be put to check in order to execute `docker pull` with the image link in case the image name is not available. _**NOTE: the image name must be the same as the link since in docker there is not distinction**_
* **image-config** the name must be the same as the link. the Disk Format and container format are ignored so you can use "QCOW2" and "BARE", as well as for the limits, everything can be 0,

## Build the VNFPackage

In order to build the VNF Package and to upload it please follow [our documentation](http://openbaton.github.io/documentation/vnf-package/)

# Issue tracker

Issues and bug reports should be posted to the GitHub Issue Tracker of this project

# What is Open Baton?

OpenBaton is an open source project providing a comprehensive implementation of the ETSI Management and Orchestration (MANO) specification.

Open Baton is a ETSI NFV MANO compliant framework. Open Baton was part of the OpenSDNCore (www.opensdncore.org) project started almost three years ago by Fraunhofer FOKUS with the objective of providing a compliant implementation of the ETSI NFV specification.

Open Baton is easily extensible. It integrates with OpenStack, and provides a plugin mechanism for supporting additional VIM types. It supports Network Service management either using a generic VNFM or interoperating with VNF-specific VNFM. It uses different mechanisms (REST or PUB/SUB) for interoperating with the VNFMs. It integrates with additional components for the runtime management of a Network Service. For instance, it provides autoscaling and fault management based on monitoring information coming from the the monitoring system available at the NFVI level.

# Source Code and documentation

The Source Code of the other Open Baton projects can be found [here][openbaton-github] and the documentation can be found [here][openbaton-doc] .

# News and Website

Check the [Open Baton Website][openbaton]
Follow us on Twitter @[openbaton][openbaton-twitter].

# Licensing and distribution
Copyright [2015-2016] Open Baton project

Licensed under the Apache License, Version 2.0 (the "License");

you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

# Support
The Open Baton project provides community support through the Open Baton Public Mailing List and through StackOverflow using the tags openbaton.

# Supported by
  <img src="https://raw.githubusercontent.com/openbaton/openbaton.github.io/master/images/fokus.png" width="250"/><img src="https://raw.githubusercontent.com/openbaton/openbaton.github.io/master/images/tu.png" width="150"/>

[fokus-logo]: https://raw.githubusercontent.com/openbaton/openbaton.github.io/master/images/fokus.png
[openbaton]: http://openbaton.org
[openbaton-doc]: http://openbaton.org/documentation
[openbaton-github]: http://github.org/openbaton
[openbaton-logo]: https://raw.githubusercontent.com/openbaton/openbaton.github.io/master/images/openBaton.png
[openbaton-mail]: mailto:users@openbaton.org
[openbaton-twitter]: https://twitter.com/openbaton
[tub-logo]: https://raw.githubusercontent.com/openbaton/openbaton.github.io/master/images/tu.png
[dummy-vnfm-amqp]: https://github.com/openbaton/dummy-vnfm-amqp
[get-openbaton-org]: http://get.openbaton.org/plugins/stable/
