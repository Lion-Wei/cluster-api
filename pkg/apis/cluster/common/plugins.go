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

package common

import (
	"fmt"
	"strings"
	"sync"

	"github.com/golang/glog"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
	"sigs.k8s.io/cluster-api/pkg/util"
)

var (
	providersMutex sync.Mutex
	providers      = make(map[string]interface{})
)

// RegisterClusterProvisioner registers a ClusterProvisioner by name.  This
// is expected to happen during app startup.
func RegisterClusterProvisioner(name string, provisioner interface{}) {
	providersMutex.Lock()
	defer providersMutex.Unlock()
	if _, found := providers[name]; found {
		glog.Fatalf("Cluster provisioner %q was registered twice", name)
	}
	glog.V(1).Infof("Registered cluster provisioner %q", name)
	providers[name] = provisioner
}

func ClusterProvisioner(name string) (interface{}, error) {
	providersMutex.Lock()
	defer providersMutex.Unlock()
	provisioner, found := providers[name]
	if !found {
		return nil, fmt.Errorf("unable to find provisioner for %s", name)
	}
	return provisioner, nil
}

const OpenstackIPAnnotationKey = "openstack-ip-address"

func init() {
	RegisterClusterProvisioner("openstack", &DeploymentClient{})
}

// TODO: This file should be moved to vendor after #416 finished.
type DeploymentClient struct{}

func (*DeploymentClient) GetIP(cluster *clusterv1.Cluster, machine *clusterv1.Machine) (string, error) {
	if machine.ObjectMeta.Annotations != nil {
		if ip, ok := machine.ObjectMeta.Annotations[OpenstackIPAnnotationKey]; ok {
			glog.Infof("Returning IP from machine annotation %s", ip)
			return ip, nil
		}
	}

	return "", fmt.Errorf("could not get IP")
}

func (d *DeploymentClient) GetKubeConfig(cluster *clusterv1.Cluster, master *clusterv1.Machine) (string, error) {
	ip, err := d.GetIP(cluster, master)
	if err != nil {
		return "", err
	}

	result := strings.TrimSpace(util.ExecCommand(
		"ssh", "-i", "/root/.ssh/openstack_tmp",
		"-o", "StrictHostKeyChecking no",
		"-o", "UserKnownHostsFile /dev/null",
		fmt.Sprintf("cc@%s", ip),
		"echo STARTFILE; sudo cat /etc/kubernetes/admin.conf"))
	parts := strings.Split(result, "STARTFILE")
	if len(parts) != 2 {
		return "", nil
	}
	return strings.TrimSpace(parts[1]), nil
}
