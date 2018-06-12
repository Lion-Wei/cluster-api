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

package huawei

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"reflect"
	"strings"

	"github.com/golang/glog"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/cluster-api/cloud/huawei/clients"
	huaweiconfigv1 "sigs.k8s.io/cluster-api/cloud/huawei/huaweiproviderconfig/v1alpha1"
	"sigs.k8s.io/cluster-api/cloud/huawei/machinesetup"
	apierrors "sigs.k8s.io/cluster-api/errors"
	"sigs.k8s.io/cluster-api/kubeadm"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
	client "sigs.k8s.io/cluster-api/pkg/client/clientset_generated/clientset/typed/cluster/v1alpha1"
	"sigs.k8s.io/cluster-api/util"
)

const (
	MachineSetupConfigPath   = "/etc/machinesetup/machine_setup_configs.yaml"
	StartupScriptPath        = "/home/machine_startup.sh"
	SshPrivateKeyPath        = "/etc/sshkeys/private"
	SshPublicKeyPath         = "/etc/sshkeys/public"
	SshKeyUserPath           = "/etc/sshkeys/user"
	CloudConfigPath          = "/etc/cloud/cloud_config.yaml"
	setupLogPath             = "/var/log/machineSetup.log"
	OpenstackIPAnnotationKey = "openstack-ip-address"
	OpenstackIdAnnotationKey = "openstack-resourceId"
)

type SshCreds struct {
	user           string
	privateKeyPath string
	publicKey      string
}

type HuaweiClient struct {
	scheme              *runtime.Scheme
	kubeadm             *kubeadm.Kubeadm
	machineClient       client.MachineInterface
	machineSetupWatcher *machinesetup.ConfigWatch
	codecFactory        *serializer.CodecFactory
	machineService      *clients.InstanceService
	sshCred             *SshCreds
	*DeploymentClient
}

// readCloudConfigFromFile read cloud config from file
// which should include username/password/region/tenentID...
func readCloudConfigFromFile(path string) *clients.CloudConfig {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return nil
	}
	cloudConfig := &clients.CloudConfig{}
	if yaml.Unmarshal(bytes, cloudConfig) != nil {
		return nil
	}
	return cloudConfig
}

func NewMachineActuator(machineClient client.MachineInterface) (*HuaweiClient, error) {
	// TODO: Need use env or flag to pass cloud config file path
	cloudConfig := readCloudConfigFromFile(CloudConfigPath)
	if cloudConfig == nil {
		return nil, fmt.Errorf("Get cloud config from file %q err", CloudConfigPath)
	}
	machineService, err := clients.NewInstanceService(&clients.CloudConfig{})
	if err != nil {
		return nil, err
	}

	var sshCred SshCreds
	if _, err := os.Stat(SshPrivateKeyPath); err != nil {
		return nil, fmt.Errorf("ssh key pair need to be specified")
	}
	sshCred.privateKeyPath = SshPrivateKeyPath

	b, err := ioutil.ReadFile(SshKeyUserPath)
	if err != nil {
		return nil, err
	}
	sshCred.user = string(b)

	b, err = ioutil.ReadFile(SshPublicKeyPath)
	if err != nil {
		return nil, err
	}
	sshCred.publicKey = string(b)

	if machineService.CreateKeyPair(sshCred.user, sshCred.publicKey) != nil {
		return nil, fmt.Errorf("create ssh key pair err: %v", err)
	}

	scheme, codecFactory, err := huaweiconfigv1.NewSchemeAndCodecs()
	if err != nil {
		return nil, err
	}

	setupConfigWatcher, err := machinesetup.NewConfigWatch(MachineSetupConfigPath)
	if err != nil {
		return nil, fmt.Errorf("error creating machine setup config watcher: %v", err)
	}

	return &HuaweiClient{
		machineClient:       machineClient,
		machineService:      machineService,
		codecFactory:        codecFactory,
		machineSetupWatcher: setupConfigWatcher,
		kubeadm:             kubeadm.New(),
		scheme:              scheme,
		sshCred:             &sshCred,
		DeploymentClient:    NewDeploymentClient(),
	}, nil
}

func (hc *HuaweiClient) Create(cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	if hc.machineSetupWatcher == nil {
		return errors.New("a valid machine setup config watcher is required!")
	}

	providerConfig, err := hc.providerconfig(machine.Spec.ProviderConfig)
	if err != nil {
		return hc.handleMachineError(machine, apierrors.InvalidMachineConfiguration(
			"Cannot unmarshal providerConfig field: %v", err))
	}

	if verr := hc.validateMachine(machine, providerConfig); verr != nil {
		return hc.handleMachineError(machine, verr)
	}

	instance, err := hc.instanceExists(machine)
	if err != nil {
		return err
	}
	if instance != nil {
		glog.Infof("Skipped creating a VM that already exists.\n")
		return nil
	}

	machineSetupConfig, err := hc.machineSetupWatcher.GetMachineSetupConfig()
	if err != nil {
		return err
	}
	configParams := &machinesetup.ConfigParams{
		Roles:    machine.Spec.Roles,
		Versions: machine.Spec.Versions,
	}
	setupScript, err := machineSetupConfig.GetSetupScript(configParams)
	if err != nil {
		return err
	}
	personality, err := machineSetupConfig.GetPersonality(configParams)
	if err != nil {
		return err
	}
	personality = append(personality, machinesetup.Personality{
		Path:     StartupScriptPath,
		Contents: []byte(setupScript),
	})

	cmd := fmt.Sprintf(machinesetup.StartCmd, StartupScriptPath, StartupScriptPath, setupLogPath)

	instance, err = hc.machineService.InstanceCreate(providerConfig, personality, cmd, hc.sshCred.user)
	if err != nil {
		return hc.handleMachineError(machine, apierrors.CreateMachine(
			"error creating Huawei instance: %v", err))
	}

	return hc.updateAnnotation(machine, instance.ID)
}

func (hc *HuaweiClient) Delete(cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	instance, err := hc.instanceExists(machine)
	if err != nil {
		return err
	}

	if instance == nil {
		glog.Infof("Skipped deleting a VM that is already deleted.\n")
		return nil
	}

	id := machine.ObjectMeta.Annotations[OpenstackIdAnnotationKey]
	err = hc.machineService.InstanceDelete(id)
	if err != nil {
		return hc.handleMachineError(machine, apierrors.DeleteMachine(
			"error deleting Huawei instance: %v", err))
	}

	return nil
}

func (hc *HuaweiClient) Update(cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	status, err := hc.instanceStatus(machine)
	if err != nil {
		return err
	}

	currentMachine := (*clusterv1.Machine)(status)
	if currentMachine == nil {
		instance, err := hc.instanceExists(machine)
		if err != nil {
			return err
		}
		if instance == nil {
			return fmt.Errorf("Cannot retrieve current state to update machine %v", machine.ObjectMeta.Name)
		}
	}

	if !hc.requiresUpdate(currentMachine, machine) {
		return nil
	}

	if util.IsMaster(currentMachine) {
		// TODO: add master inplace
		glog.Errorf("master inplace update failed: %v", err)
	} else {
		glog.Infof("re-creating machine %s for update.", currentMachine.ObjectMeta.Name)
		err = hc.Delete(cluster, currentMachine)
		if err != nil {
			glog.Errorf("delete machine %s for update failed: %v", currentMachine.ObjectMeta.Name, err)
		} else {
			err = hc.Create(cluster, machine)
			if err != nil {
				glog.Errorf("create machine %s for update failed: %v", machine.ObjectMeta.Name, err)
			}
		}
	}

	return nil
}

func (hc *HuaweiClient) Exists(cluster *clusterv1.Cluster, machine *clusterv1.Machine) (bool, error) {
	instance, err := hc.instanceExists(machine)
	if err != nil {
		return false, err
	}
	return instance != nil, err
}

func getIPFromInstance(instance *clients.Instance) (string, error) {
	if instance.AccessIPv4 != "" && net.ParseIP(instance.AccessIPv4) != nil {
		return instance.AccessIPv4, nil
	}
	type huaweiNetwork struct {
		Addr    string `json:"addr"`
		Version string `json:"version"`
		Type    string `json:"OS-EXT-IPS:type"`
	}

	var networkList []huaweiNetwork
	for _, b := range instance.Addresses {
		list, err := json.Marshal(b)
		if err != nil {
			return "", fmt.Errorf("extract IP from instance err: %v", err)
		}
		json.Unmarshal(list, networkList)
		fmt.Printf("\nlist is: %q\nUnmarsheled to: %+v\n", list, networkList)
		for _, network := range networkList {
			if network.Type == "floating" && network.Version == "4" {
				return network.Addr, nil
			}
		}
	}
	return "", fmt.Errorf("extract IP from instance err")
}

func (hc *HuaweiClient) GetKubeConfig(cluster *clusterv1.Cluster, master *clusterv1.Machine) (string, error) {
	if hc.sshCred == nil {
		return "", fmt.Errorf("Get kubeConfig failed, don't have ssh keypair information")
	}
	ip, err := hc.GetIP(cluster, master)
	if err != nil {
		return "", err
	}

	result := strings.TrimSpace(util.ExecCommand(
		"ssh", "-i", hc.sshCred.privateKeyPath,
		"-o", "StrictHostKeyChecking no",
		"-o", "UserKnownHostsFile /dev/null",
		fmt.Sprintf("%s@%s", "root", ip),
		"echo STARTFILE; sudo cat /etc/kubernetes/admin.conf"))
	parts := strings.Split(result, "STARTFILE")
	if len(parts) != 2 {
		return "", nil
	}
	return strings.TrimSpace(parts[1]), nil
}

// If the HuaweiClient has a client for updating Machine objects, this will set
// the appropriate reason/message on the Machine.Status. If not, such as during
// cluster installation, it will operate as a no-op. It also returns the
// original error for convenience, so callers can do "return handleMachineError(...)".
func (hc *HuaweiClient) handleMachineError(machine *clusterv1.Machine, err *apierrors.MachineError) error {
	if hc.machineClient != nil {
		reason := err.Reason
		message := err.Message
		machine.Status.ErrorReason = &reason
		machine.Status.ErrorMessage = &message
		hc.machineClient.UpdateStatus(machine)
	}

	glog.Errorf("Machine error: %v", err.Message)
	return err
}

func (hc *HuaweiClient) updateAnnotation(machine *clusterv1.Machine, id string) error {
	if machine.ObjectMeta.Annotations == nil {
		machine.ObjectMeta.Annotations = make(map[string]string)
	}
	machine.ObjectMeta.Annotations[OpenstackIdAnnotationKey] = id
	instance, _ := hc.instanceExists(machine)
	ip, err := getIPFromInstance(instance)
	if err != nil {
		return err
	}
	machine.ObjectMeta.Annotations[OpenstackIPAnnotationKey] = ip
	_, err = hc.machineClient.Update(machine)
	if err != nil {
		return err
	}
	return hc.updateInstanceStatus(machine)
}

func (hc *HuaweiClient) requiresUpdate(a *clusterv1.Machine, b *clusterv1.Machine) bool {
	// Do not want status changes. Do want changes that impact machine provisioning
	return !reflect.DeepEqual(a.Spec.ObjectMeta, b.Spec.ObjectMeta) ||
		!reflect.DeepEqual(a.Spec.ProviderConfig, b.Spec.ProviderConfig) ||
		!reflect.DeepEqual(a.Spec.Roles, b.Spec.Roles) ||
		!reflect.DeepEqual(a.Spec.Versions, b.Spec.Versions) ||
		a.ObjectMeta.Name != b.ObjectMeta.Name
}

func (hc *HuaweiClient) instanceExists(machine *clusterv1.Machine) (instance *clients.Instance, err error) {
	id, find := machine.Annotations[OpenstackIdAnnotationKey]
	if !find {
		return nil, nil
	}
	instance, err = hc.machineService.GetInstance(id)
	if err != nil {
		return nil, fmt.Errorf("Failed to get instance: %v", err)
	}
	return instance, err
}

// providerconfig get huawei provider config
func (hc *HuaweiClient) providerconfig(providerConfig clusterv1.ProviderConfig) (*huaweiconfigv1.HuaweiProviderConfig, error) {
	obj, gvk, err := hc.codecFactory.UniversalDecoder(huaweiconfigv1.SchemeGroupVersion).Decode(providerConfig.Value.Raw, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("decoding failure: %v", err)
	}
	config, ok := obj.(*huaweiconfigv1.HuaweiProviderConfig)
	if !ok {
		return nil, fmt.Errorf("failure to cast to huawei; type: %v", gvk)
	}

	return config, nil
}

func (hc *HuaweiClient) validateMachine(machine *clusterv1.Machine, config *huaweiconfigv1.HuaweiProviderConfig) *apierrors.MachineError {
	if machine.Spec.Versions.Kubelet == "" {
		return apierrors.InvalidMachineConfiguration("spec.versions.kubelet can't be empty")
	}
	// TODO: other validate of huaweiCloud
	return nil
}
