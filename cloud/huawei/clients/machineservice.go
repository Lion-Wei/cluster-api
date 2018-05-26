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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
	"golang.org/x/net/html/atom"
	"k8s.io/violin/api/v1"
	"net/http"
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
	cloudserver *gophercloud.ServiceClient
}

type InstanceList struct {
	Name string
	Id   string
}

func NewInstanceService(cfg *CloudConfig) (*InstanceService, error) {
	client, err := openstack.NewClient(cfg.AuthUrl)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
		},
	}
	client.HTTPClient.Transport = transport
	client.HTTPClient.Timeout = defaultTimeout

	evsUrl := gophercloud.NormalizeURL(cfg.ApiGWAddr + "v1/" + cfg.TenantId)
	cloudServer := &gophercloud.ServiceClient{ProviderClient: client, Endpoint: evsUrl}
	return &InstanceService{
		cloudserver: cloudServer,
	}, nil
}

func (is *InstanceService) InstanceCreate(opts InstanceCreateOptions) (instanceId string, err error) {
	var res Result
	reqBody, err := opts.ToInstanceCreateMap()
	if err != nil {
		return "", err
	}
	client := is.cloudserver
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

	client := is.cloudserver
	res.Response, res.Err = client.Post(client.ServiceURL("cloudservers", "delete"), reqBody, &res.Body, &gophercloud.RequestOpts{
		OkCodes: []int{200, 201, 202, 203, 204},
	})

	_, err = is.WaitJobFinish(res)
	return err
}

func (is *InstanceService) getInstanceList(opts *InstanceListOpts) (instanceList []*InstanceList, err error) {
	var res Result
	var listRes []InstanceListResult
	client := is.cloudserver
	url := client.ServiceURL("servers")

	if opts != nil {
		query, err := opts.ToServerListQuery()
		if err != nil {
			return nil, fmt.Errorf("Failed to get listQuery: %v", err)
		}
		url += query
	}
	res.Response, res.Err = client.Get(url, &res.Body, nil)
	if res.Err != nil {
		return nil, fmt.Errorf("Failed to get instance list: %v", res.Err)
	}
	body, _ := json.Marshal(res.Body)
	json.Unmarshal(body, &listRes)
	if len(listRes) == 0 {
		return instanceList, nil
	}
	for _, instance := range listRes {
		instanceList = append(instanceList, &InstanceList{
			Name: instance.Name,
			Id:   instance.Id,
		})
	}
	return instanceList, nil
}

func (is *InstanceService) GetInstance(resourceId string) (instance *InstanceDetail, err error) {
	var res Result
	client := is.cloudserver
	res.Response, res.Err = client.Get(client.ServiceURL("servers", resourceId), &res.Body, &gophercloud.RequestOpts{
		OkCodes: []int{200, 203},
	})
	if res.Err != nil {
		return nil, res.Err
	}
	body, _ := json.Marshal(res.Body)
	err = json.Unmarshal(body, instance)
	if err != nil {
		return nil, err
	}
	return instance, nil
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
	client, res, jobRes := is.cloudserver, Result{}, JobDetail{}

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
	client := is.cloudserver
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
