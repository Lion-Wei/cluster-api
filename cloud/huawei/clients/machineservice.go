/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clients

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/extensions/keypairs"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
	huaweiconfigv1 "sigs.k8s.io/cluster-api/cloud/huawei/huaweiproviderconfig/v1alpha1"
	"sigs.k8s.io/cluster-api/cloud/huawei/machinesetup"
)

const (
	keyInsertPath = "/root/.ssh/authorized_keys"
)

type InstanceService struct {
	provider     *gophercloud.ProviderClient
	serverClient *gophercloud.ServiceClient
	iamClient    *gophercloud.ServiceClient
}

type CloudConfig struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DomainName string `json:"domain_name"`
	TenantID   string `json:"tenant_id"`
	Region     string `json:"region"`
}

type Instance struct {
	servers.Server
}

type SshKeyPair struct {
	Name string `json:"name"`

	// PublicKey is the public key from this pair, in OpenSSH format.
	// "ssh-rsa AAAAB3Nz..."
	PublicKey string `json:"public_key"`

	// PrivateKey is the private key from this pair, in PEM format.
	// "-----BEGIN RSA PRIVATE KEY-----\nMIICXA..."
	// It is only present if this KeyPair was just returned from a Create call.
	PrivateKey string `json:"private_key"`
}

type InstanceListOpts struct {
	// Name of the image in URL format.
	Image string `q:"image"`

	// Name of the flavor in URL format.
	Flavor string `q:"flavor"`

	// Name of the server as a string; can be queried with regular expressions.
	// Realize that ?name=bob returns both bob and bobb. If you need to match bob
	// only, you can use a regular expression matching the syntax of the
	// underlying database server implemented for Compute.
	Name string `q:"name"`
}

func NewInstanceService(cfg *CloudConfig) (*InstanceService, error) {
	authUrl := gophercloud.NormalizeURL("https://iam." + cfg.Region + ".myhuaweicloud.com/v3")
	opts := &gophercloud.AuthOptions{
		IdentityEndpoint: authUrl,
		Username:         cfg.Username,
		Password:         cfg.Password,
		DomainName:       cfg.DomainName,
		TenantID:         cfg.TenantID,
		AllowReauth:      true,
	}
	provider, err := openstack.AuthenticatedClient(*opts)
	if err != nil {
		return nil, fmt.Errorf("Create providerClient err: %v", err)
	}

	iamClient, err := openstack.NewIdentityV3(provider, gophercloud.EndpointOpts{
		Region: "",
	})
	if err != nil {
		return nil, fmt.Errorf("Create iamClient err: %v", err)
	}
	serverClient, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("Create serviceClient err: %v", err)
	}

	return &InstanceService{
		provider:     provider,
		iamClient:    iamClient,
		serverClient: serverClient,
	}, nil
}

// UpdateToken to update token if need.
func (is *InstanceService) UpdateToken() error {
	token := is.provider.Token()
	result, err := tokens.Validate(is.iamClient, token)
	if err != nil {
		return fmt.Errorf("Validate token err: %v", err)
	}
	if result {
		return nil
	}
	glog.V(2).Infof("Toen is out of date, need get new token.")
	reAuthFunction := is.provider.ReauthFunc
	if reAuthFunction() != nil {
		return fmt.Errorf("reAuth err: %v", err)
	}
	return nil
}

func (is *InstanceService) InstanceCreate(config *huaweiconfigv1.HuaweiProviderConfig, per []machinesetup.Personality, cmd string, keyName string) (instance *Instance, err error) {
	var createOpts servers.CreateOpts
	if config == nil {
		return nil, fmt.Errorf("create Options need be specified to create instace.")
	}
	userData := base64.StdEncoding.EncodeToString([]byte(cmd))
	var personality servers.Personality
	for _, file := range per {
		personality = append(personality, &servers.File{
			Path:     file.Path,
			Contents: file.Contents,
		})
	}
	createOpts = servers.CreateOpts{
		Name:             config.Name,
		ImageRef:         config.ImageRef,
		FlavorRef:        config.FlavorRef,
		AvailabilityZone: config.AvailabilityZone,
		Networks: []servers.Network{{
			UUID: config.Networks[0].UUID,
		}},
		UserData:    []byte(userData),
		Personality: personality,
	}
	server, err := servers.Create(is.serverClient, keypairs.CreateOptsExt{
		CreateOptsBuilder: createOpts,
		KeyName:           keyName,
	}).Extract()
	if err != nil {
		return nil, fmt.Errorf("Create new server err: %v", err)
	}
	return serverToInstance(server), nil
}

func (is *InstanceService) InstanceDelete(id string) error {
	return servers.Delete(is.serverClient, id).ExtractErr()
}

func (is *InstanceService) getInstanceList(opts *InstanceListOpts) (*[]Instance, error) {
	var listOpts servers.ListOpts
	if opts != nil {
		listOpts = servers.ListOpts{
			Name: opts.Name,
		}
	} else {
		listOpts = servers.ListOpts{}
	}

	allPages, err := servers.List(is.serverClient, listOpts).AllPages()
	if err != nil {
		return nil, fmt.Errorf("Get service list err: %v", err)
	}
	serverList, err := servers.ExtractServers(allPages)
	if err != nil {
		return nil, fmt.Errorf("Extract services list err: %v", err)
	}
	var instanceList []Instance
	for _, server := range serverList {
		instanceList = append(instanceList, Instance{server})
	}
	return &instanceList, nil
}

func (is *InstanceService) GetInstance(resourceId string) (instance *Instance, err error) {
	if resourceId == "" {
		return nil, fmt.Errorf("ResourceId should be specified to  get detail.")
	}
	server, err := servers.Get(is.serverClient, resourceId).Extract()
	if err != nil {
		return nil, fmt.Errorf("Get server %q detail failed: %v", resourceId, err)
	}
	return serverToInstance(server), err
}

func (is *InstanceService) CreateKeyPair(name string) (*SshKeyPair, error) {
	opts := keypairs.CreateOpts{
		Name: name,
	}
	keyPair, err := keypairs.Create(is.serverClient, opts).Extract()
	if err != nil {
		return nil, fmt.Errorf("Create keyPair failed: %v", err)
	}

	return &SshKeyPair{
		Name:       keyPair.Name,
		PrivateKey: keyPair.PrivateKey,
		PublicKey:  keyPair.PrivateKey,
	}, err
}

func serverToInstance(server *servers.Server) *Instance {
	return &Instance{*server}
}

func getIPFromInstance(instance Instance) (string, error) {
	type huaweiNetwork struct {
		Addr    string  `json:"addr"`
		Version float64 `json:"version"`
		Type    string  `json:"OS-EXT-IPS:type"`
	}

	for _, b := range instance.Addresses {
		list, err := json.Marshal(b)
		if err != nil {
			return "", fmt.Errorf("extract IP from instance err: %v", err)
		}
		var networkList []interface{}
		json.Unmarshal(list, &networkList)
		for _, network := range networkList {
			var hwNetwork huaweiNetwork
			b, _ := json.Marshal(network)
			json.Unmarshal(b, &hwNetwork)
			fmt.Printf("\nhwNetwork is: %+v\n", hwNetwork)
			if hwNetwork.Type == "floating" && hwNetwork.Version == 4.0 {
				return hwNetwork.Addr, nil
			}
		}
	}
	return "", fmt.Errorf("extract IP from instance err")
}
