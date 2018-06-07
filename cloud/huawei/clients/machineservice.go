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
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"github.com/gophercloud/gophercloud/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/openstack/identity/v3/tokens"
	"time"
)

const (
	defaultTimeout                = time.Duration(30) * time.Second
	CreateServerJobType           = "createServer"
	JobInit             JobStatus = "INIT"
	JobSuccess          JobStatus = "SUCCESS"
	JobFailed           JobStatus = "FAILED"
	JobRunning          JobStatus = "RUNNING"

	hwTimeOut   = 10 * time.Minute
	hwWaitSleep = 10 * time.Second
)

type InstanceService struct {
	provider      *gophercloud.ProviderClient
	serviceClient *gophercloud.ServiceClient
	iamClient     *gophercloud.ServiceClient
	authOptions   *gophercloud.AuthOptions
}

type InstanceList struct {
	Name string
	Id   string
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

	serviceClient, err := openstack.NewComputeV2(provider, gophercloud.EndpointOpts{
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("Create serviceClient err: %v", err)
	}
	return &InstanceService{
		provider:      provider,
		iamClient:     iamClient,
		serviceClient: serviceClient,
		authOptions:   opts,
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

func (is *InstanceService) InstanceCreate(opts InstanceCreateOptions) (instanceId string, err error) {
	var res Result
	reqBody, err := opts.ToInstanceCreateMap()
	if err != nil {
		return "", err
	}
	client := is.serviceClient
	res.Response, res.Err = client.Post(client.ServiceURL("cloudservers"), reqBody, &res.Body, &gophercloud.RequestOpts{
		OkCodes: []int{200, 201, 202, 203, 204},
	})

	return is.WaitJobFinish(res)
}

func (is *InstanceService) InstanceDelete(id string) error {
	var res Result
	opts := InstanceDeleteOpts{
		Servers: []map[string]string{{
			"id": id,
		}},
	}
	reqBody, err := opts.ToServerDeleteMap()
	if err != nil {
		return err
	}

	client := is.serviceClient
	res.Response, res.Err = client.Post(client.ServiceURL("cloudservers", "delete"), reqBody, &res.Body, &gophercloud.RequestOpts{
		OkCodes: []int{200, 201, 202, 203, 204},
	})

	_, err = is.WaitJobFinish(res)
	return err
}

func (is *InstanceService) getInstanceList(opts *InstanceListOpts) (instanceList []*InstanceList, err error) {
	var listOpts servers.ListOpts
	if opts != nil {
		listOpts = servers.ListOpts{
			Name: opts.Name,
		}
	} else {
		listOpts = servers.ListOpts{}
	}

	allPages, err := servers.List(is.serviceClient, listOpts).AllPages()
	if err != nil {
		return nil, fmt.Errorf("Get service list err: %v", err)
	}
	servers, err := servers.ExtractServers(allPages)
	if err != nil {
		return nil, fmt.Errorf("Extract services list err: %v", err)
	}
	for _, server := range servers {
		instance := &InstanceList{
			Name: server.Name,
			Id:   server.ID,
		}
		instanceList = append(instanceList, instance)
	}
	return instanceList, nil
}

func (is *InstanceService) GetInstance(resourceId string) (instance *servers.Server, err error) {
	if resourceId == "" {
		return nil, fmt.Errorf("ResourceId should be specified to  get detail.")
	}
	server, err := servers.Get(is.serviceClient, resourceId).Extract()
	if err != nil {
		return nil, fmt.Errorf("Get server %q detail failed: %v", resourceId, err)
	}
	return server, err
}

func (is *InstanceService) WaitJobFinish(res Result) (resourceId string, err error) {
	body, _ := json.Marshal(res.Body)
	var serverResp struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &serverResp); err != nil {
		return "", fmt.Errorf("Failed to get jobId: %v", err)
	}

	start := time.Now()
	client, res, jobRes := is.serviceClient, Result{}, JobDetail{}

	for {
		res.Response, res.Err = client.Get(client.ServiceURL("jobs", serverResp.JobID), &res.Body, nil)
		if res.Err != nil {
			return "", res.Err
		}
		body, _ = json.Marshal(res.Body)
		json.Unmarshal(body, &jobRes)
		if jobRes.Status == JobSuccess {
			if jobRes.JobType != CreateServerJobType {
				return "", nil
			}
			subJobs := jobRes.Entities.SubJobs
			if len(subJobs) == 0 {
				return "", nil
			}
			return subJobs[0].Entities["server_id"], nil
		}
		select {
		case <-time.After(hwTimeOut):
			return "", fmt.Errorf("wait job %q timed out after %v", jobRes.JobType, time.Since(start))
		case <-time.After(hwWaitSleep):
		}
	}
}

func (is *InstanceService) CreateKeyPair(name string) (keyPair *SshKeyPair, err error) {
	opts := KeyPairCreateOpts{
		Name: name,
	}
	reqBody, err := opts.ToServerDeleteMap()
	if err != nil {
		return nil, err
	}

	var res Result
	client := is.serviceClient
	res.Response, res.Err = client.Post(client.ServiceURL("os-keypairs"), reqBody, &res.Body, &gophercloud.RequestOpts{
		OkCodes: []int{200},
	})
	if res.Err != nil {
		return nil, fmt.Errorf("Create keypair failed: %v", res.Err)
	}
	body, _ := json.Marshal(res.Body)

	var keyPairs []SshKeyPair
	if err := json.Unmarshal(body, &keyPairs); err != nil {
		return nil, fmt.Errorf("Create keypair failed: %v", err)
	}
	if len(keyPairs) == 0 {
		return nil, fmt.Errorf("Create keypair failed")
	}
	// in this case, should only have one keypair return
	return &keyPairs[0], nil
}
