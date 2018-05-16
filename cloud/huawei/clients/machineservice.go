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
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
)

const (
	HttpsHead = "https://"
)

// TODO: finish machine service
type MachineService struct {
	cloudserver *gophercloud.ServiceClient
	region      string
	version     string
	apiUrl      string
}

// url to create client
// url to create cloudserver
func NewMachineService(url, projectid string) (*MachineService, error) {
	client, err := openstack.NewClient(url)
	if err != nil {
		return nil, err
	}

	v1EvsUrl := gophercloud.NormalizeURL(HttpsHead + url + "v1/" + projectid)
	cloudServer := &gophercloud.ServiceClient{ProviderClient: client, Endpoint: v1EvsUrl}
	return &MachineService{
		cloudserver: cloudServer,
		region:      "region",
		version:     "version",
		apiUrl:      "url",
	}, nil
}

// CreateMachine creates a machine instance and returns the machineId of the instance.
func (ms *MachineService) CreateMachine(options MachineCreateOptions) (result MachineCreateResult, err error) {
	return result, nil
}

func (ms *MachineService) DeleteMachine(keyInfo ResourceKeyInfo) (result MachineDeleteResult, err error) {
	return result, nil
}
func (ms *MachineService) GetMachine(keyInfo ResourceKeyInfo) (result MachineGetResult, err error) {
	return result, nil
}

type MachineCreateOptions struct {
	AvailabilityZone string
	Metadata         map[string]string
	Image            string
}

type MachineCreateResult struct {
}

type ResourceKeyInfo struct {
	Ident string
}

type MachineDeleteResult struct {
}

type MachineGetResult struct {
	NetworkInterfaces []*NetworkInterface `json:"networkInterfaces,omitempty"`
}

type NetworkInterface struct {
	Name string
	IP   string
}
