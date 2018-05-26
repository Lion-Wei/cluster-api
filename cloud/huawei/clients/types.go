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
	"fmt"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"net/http"
)

type CloudConfig struct {
	AuthUrl   string `json:"auth_url"`
	TenantId  string `json:"tenant_id"`
	ApiGWAddr string `json:"ApiGWAddr"`
}

type InstanceCreateOptions struct {
	Name             string
	FlavorRef        string
	ImageRef         string
	Nics             []Nic
	VpcId            string
	RootVolume       *RootVolume
	AvailabilityZone string
	UserData         string
	Personality      servers.Personality
}

type RootVolume struct {
	VolumeType  string
	Size        int
	ExtendParam map[string]interface{}
}

type Nic struct {
	SubnetId  string
	IpAddress string
}

func (opts InstanceCreateOptions) ToInstanceCreateMap() (map[string]interface{}, error) {
	if opts.Name == "" {
		return nil, fmt.Errorf("Missing field required for cloudserver creation: name")
	}
	if opts.ImageRef == "" {
		return nil, fmt.Errorf("Missing field required for cloudserver creation: imageRef")
	}
	if opts.FlavorRef == "" {
		return nil, fmt.Errorf("Missing field required for cloudserver creation: flavorRef")
	}
	if opts.VpcId == "" {
		return nil, fmt.Errorf("Missing field required for cloudserver creation: vpcid")
	}
	if len(opts.Nics) == 0 {
		return nil, fmt.Errorf("Missing field required for cloudserver creation: nics")
	}
	if opts.RootVolume == nil {
		return nil, fmt.Errorf("Missing field required for cloudserver creation: root_volume")
	}
	if opts.AvailabilityZone == "" {
		return nil, fmt.Errorf("Missing field required for cloudserver creation: availability_zone")
	}

	server := make(map[string]interface{})
	server["name"] = opts.Name
	server["imageRef"] = opts.ImageRef
	server["flavorRef"] = opts.FlavorRef
	server["vpcid"] = opts.VpcId

	var nics []interface{}
	for _, nic := range opts.Nics {
		nics = append(nics, map[string]interface{}{
			"subnet_id":  nic.SubnetId,
			"ip_address": nic.IpAddress,
		})
	}
	server["nics"] = nics

	rootVolume := make(map[string]interface{})
	rootVolume["volumetype"] = opts.RootVolume.VolumeType
	rootVolume["size"] = opts.RootVolume.Size
	rootVolume["extendparam"] = opts.RootVolume.ExtendParam
	server["root_volume"] = rootVolume

	server["availability_zone"] = opts.AvailabilityZone

	if len(opts.Personality) != 0 {
		server["personality"] = opts.Personality
	}

	serverMap := make(map[string]interface{})
	serverMap["server"] = server
	return serverMap, nil
}

type InstanceDeleteOpts struct {
	Servers        []map[string]string
	DeletePublicIp *bool
	DeleteVolume   *bool
}

func (opts InstanceDeleteOpts) ToServerDeleteMap() (map[string]interface{}, error) {
	if len(opts.Servers) == 0 {
		return nil, fmt.Errorf("Missing field required for cloudserver deletion: servers")
	}
	serverMap := make(map[string]interface{})
	serverMap["servers"] = opts.Servers
	if opts.DeletePublicIp != nil {
		serverMap["delete_publicip"] = *opts.DeletePublicIp
	}

	if opts.DeleteVolume != nil {
		serverMap["delete_volume"] = *opts.DeleteVolume
	}
	return serverMap, nil
}

type Result struct {
	gophercloud.Result
	Response *http.Response
}

type JobStatus string

type SubJob struct {
	BeginTime  string            `json:"begin_time"`
	EndTime    string            `json:"end_time"`
	ErrorCode  string            `json:"error_code"`
	FailReason string            `json:"fail_reason"`
	JobID      string            `json:"job_id"`
	JobType    string            `json:"job_type"`
	Status     JobStatus         `json:"status"`
	Entities   map[string]string `json:"entities"`
}

type Entities struct {
	SubJobsTotal int      `json:"sub_jobs_total"`
	SubJobs      []SubJob `json:"sub_jobs"`
}

type JobDetail struct {
	Status     JobStatus `json:"status"`
	JobID      string    `json:"job_id"`
	JobType    string    `json:"job_type"`
	BeginTime  string    `json:"begin_time"`
	EndTime    string    `json:"end_time"`
	ErrorCode  string    `json:"error_code"`
	FailReason string    `json:"fail_reason"`
	Message    string    `json:"message"`
	Code       string    `json:"code"`
	Entities   Entities  `json:"entities"`
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

// ToServerListQuery formats a ListOpts into a query string.
func (opts InstanceListOpts) ToServerListQuery() (string, error) {
	q, err := gophercloud.BuildQueryString(opts)
	if err != nil {
		return "", err
	}
	return q.String(), nil
}

type KeyPairCreateOpts struct {
	Name string `json:"name"`
}

func (opts *KeyPairCreateOpts) ToServerDeleteMap() (map[string]interface{}, error) {
	server := make(map[string]interface{})
	if opts.Name == "" {
		return nil, fmt.Errorf("Missing field required for keypair creation: name")
	}
	server["name"] = opts.Name
	return server, nil
}

type SshKeyPair struct {
	Name       string `json:"name"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	UserId     string `json:"user_id"`
}

type InstanceListResult struct {
	Name  string         `json:"name"`
	Id    string         `json:"id"`
	Links []ResourceLink `json:"links"`
}

type InstanceDetail struct {
	// The UUID of the resource.
	ID string `json:"id"`
	// The name of the resource.
	Name string `json:"name"`
	// The addresses for the server.
	Addresses map[string]interface{} `json:"addresses"`
	// The ID and links for the flavor for your server instance.
	Flavor Flavor `json:"flavor"`
	// The name of associated key pair, if any.
	KeyName string `json:"key_name"`
	// The availability zone name.
	AvailabilityZone string `json:"OS-EXT-AZ:availability_zone"`
	// The server status.
	Status string `json:"status"`
	// The ID of the host.
	HostId string `json:"hostId"`
	// IPv4 address that should be used to access this server.
	AccessIPv4 string `json:"accessIPv4"`
	// IPv6 address that should be used to access this server.
	AccessIPv6 string `json:"accessIPv6"`
	// The instance name. The Compute API generates the instance name from the instance name template. Appears in the response for administrative users only.
	InstanceName string `json:"OS-EXT-SRV-ATTR:instance_name"`
	// The hostname set on the instance when it is booted.
	HostName string `json:"OS-EXT-SERV-ATTR:hostname"`
	// The date and time when the resource was created. The date and time stamp format is ISO 8601.
	Created string `json:"created"`
	// The date and time when the resource was updated. The date and time stamp format is ISO 8601.
	Updated string `json:"updated"`
	// The UUID of the tenant in a multi-tenancy cloud.
	TenantId string `json:"tenant_id"`
	// The user ID of the user who owns the server.
	UserId string `json:"user_id"`
	// TODO not covered all instance details, e.g. image
}

type Flavor struct {
	// The UUID of the flavor.
	Id string `json:"Id"`
	// The links of the flavor.
	Links []ResourceLink `json:"links"`
}

type ResourceLink struct {
	// Link includes HTTP references to the itself, useful for passing along to other APIs that might want a resource reference.
	Href string `json:"href"`
	// Types of link relations associated with resources, like "self","bookmark","alternate"
	// A self link contains a versioned link to the resource. Use these links when the link is followed immediately.
	// A bookmark link provides a permanent link to a resource that is appropriate for long term storage.
	// An alternate link can contain an alternate representation of the resource.
	Rel string `json:"rel"`
	// The type attribute provides a hint as to the type of representation to expect when following the link.
	Type string `json:"type,omitempty"`
}
