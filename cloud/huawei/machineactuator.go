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
	"os/exec"
	"reflect"
	"strings"

	"github.com/golang/glog"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/cluster-api/cloud/huawei/clients"
	huaweiconfigv1 "sigs.k8s.io/cluster-api/cloud/huawei/huaweiproviderconfig/v1alpha1"
	"sigs.k8s.io/cluster-api/cloud/huawei/machinesetup"
	apierrors "sigs.k8s.io/cluster-api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
	client "sigs.k8s.io/cluster-api/pkg/client/clientset_generated/clientset/typed/cluster/v1alpha1"
	"sigs.k8s.io/cluster-api/util"
)

const (
	MachineFileName               = "machines.yaml"
	setupScriptPath               = "/home/setup.sh"
	setupLogPath                  = "/var/log/machineSetup.log"
	InstanceIdAnnotationKey       = "hw-resourceId"
	cloudConfigPath               = "/home/cloudconfig.yaml"
	MachineControllerSshKeySecret = "machine-controller-sshkeys"
	defaultKeyPairName            = "openstack"
	tempPrivateKeyPath            = "/tmp/ssh/private"
)

type SshCreds struct {
	user           string
	privateKeyPath string
}

type HuaweiClient struct {
	scheme              *runtime.Scheme
	kubeadmToken        string
	machineClient       client.MachineInterface
	machineSetupWatcher *machinesetup.ConfigWatch
	codecFactory        *serializer.CodecFactory
	machineService      *clients.InstanceService
	sshCred             *SshCreds
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

func NewMachineActuator(kubeadmToken string, machineClient client.MachineInterface, machineSetupConfigPath string) (*HuaweiClient, error) {
	// TODO: Need use env or flag to pass cloud config file path
	cloudConfig := readCloudConfigFromFile(cloudConfigPath)
	if cloudConfig == nil {
		return nil, fmt.Errorf("Get cloud config from file %q err", cloudConfigPath)
	}
	machineService, err := clients.NewInstanceService(&clients.CloudConfig{})
	if err != nil {
		return nil, err
	}

	var sshCred SshCreds
	if _, err := os.Stat("/etc/sshkeys/private"); err == nil {
		// get keypair from file if run inside machine controller pod
		sshCred.privateKeyPath = "/etc/sshkeys/private"

		b, err := ioutil.ReadFile("/etc/sshkeys/user")
		if err == nil {
			sshCred.user = string(b)
		} else {
			return nil, err
		}
	} else {
		// create new ssh keypair if bootstrap a new cluster
		keyPairName := fmt.Sprintf("%s-%s", defaultKeyPairName, util.RandomString(4))
		keyPair, err := machineService.CreateKeyPair(keyPairName)
		if err != nil {
			return nil, fmt.Errorf("Create sshkeypair err: %v", err)
		}
		ioutil.WriteFile(tempPrivateKeyPath, []byte(keyPair.PrivateKey), 0400)
		sshCred = SshCreds{
			user:           keyPair.Name,
			privateKeyPath: tempPrivateKeyPath,
		}
	}

	scheme, codecFactory, err := huaweiconfigv1.NewSchemeAndCodecs()
	if err != nil {
		return nil, err
	}

	setupConfigWatcher, err := machinesetup.NewConfigWatch(machineSetupConfigPath)
	if err != nil {
		return nil, fmt.Errorf("error creating machine setup config watcher: %v", err)
	}

	return &HuaweiClient{
		machineClient:       machineClient,
		machineService:      machineService,
		codecFactory:        codecFactory,
		machineSetupWatcher: setupConfigWatcher,
		kubeadmToken:        kubeadmToken,
		scheme:              scheme,
		sshCred:             &sshCred,
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
		Path:     setupScriptPath,
		Contents: []byte(setupScript),
	})

	cmd := fmt.Sprintf(machinesetup.StartCmd, setupScriptPath, setupScriptPath, setupLogPath)

	instance, err = hc.machineService.InstanceCreate(providerConfig, personality, cmd, hc.sshCred.user)
	if err != nil {
		return hc.handleMachineError(machine, apierrors.CreateMachine(
			"error creating Huawei instance: %v", err))
	}

	return hc.updateAnnotation(machine, instance.ID)
}

func (hc *HuaweiClient) Delete(machine *clusterv1.Machine) error {
	instance, err := hc.instanceExists(machine)
	if err != nil {
		return err
	}

	if instance == nil {
		glog.Infof("Skipped deleting a VM that is already deleted.\n")
		return nil
	}

	id := machine.ObjectMeta.Annotations[InstanceIdAnnotationKey]
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
		err = hc.Delete(currentMachine)
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

func (hc *HuaweiClient) Exists(machine *clusterv1.Machine) (bool, error) {
	instance, err := hc.instanceExists(machine)
	if err != nil {
		return false, err
	}
	return instance != nil, err
}

func (hc *HuaweiClient) GetIP(machine *clusterv1.Machine) (string, error) {
	instance, err := hc.instanceExists(machine)
	if err != nil {
		return "", err
	}
	if instance == nil {
		return "", fmt.Errorf("Machine instance doesn't not exist")
	}

	// extract ip from instance detail(only support ipv4 type ip for now)
	if instance.AccessIPv4 != "" && net.ParseIP(instance.AccessIPv4) != nil {
		return instance.AccessIPv4, nil
	}
	return getIPFromInstanceAddress(instance)
}

func getIPFromInstanceAddress(instance *clients.Instance) (string, error) {
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

func (hc *HuaweiClient) GetKubeConfig(master *clusterv1.Machine) (string, error) {
	if hc.sshCred == nil {
		return "", fmt.Errorf("Get kubeConfig failed, don't have ssh keypair information")
	}
	ip, err := hc.GetIP(master)
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

func (hc *HuaweiClient) CreateMachineController(cluster *clusterv1.Cluster, initialMachines []*clusterv1.Machine, clientSet kubernetes.Clientset) error {
	// create rolebinding
	err := run("kubectl", "create", "rolebinding",
		"-n", "kube-system", "machine-controller", "--role=extension-apiserver-authentication-reader",
		"--serviceaccount=default:default")
	if err != nil {
		return err
	}

	// create secret for ssh private key
	err = run("kubectl", "create", "secret", "generic", MachineControllerSshKeySecret,
		"--from-file=private="+hc.sshCred.privateKeyPath, "--from-literal=user="+hc.sshCred.user)
	if err != nil {
		return fmt.Errorf("couldn't create service account key as credential: %v", err)
	}

	// create configmap for machine setup config
	machineSetupConfig, err := hc.machineSetupWatcher.GetMachineSetupConfig()
	if err != nil {
		return err
	}
	setupConfigYaml, err := machineSetupConfig.GetYaml()
	if err != nil {
		return err
	}
	machineConfigMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "machine-setup"},
		Data: map[string]string{
			MachineFileName: setupConfigYaml,
		},
	}
	if _, err := clientSet.CoreV1().ConfigMaps(corev1.NamespaceDefault).Create(&machineConfigMap); err != nil {
		return err
	}

	if err := CreateApiServerAndController(hc.kubeadmToken); err != nil {
		return err
	}
	return nil
}

func (hc *HuaweiClient) PostDelete(cluster *clusterv1.Cluster, machines []*clusterv1.Machine) error {
	return nil
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
	machine.ObjectMeta.Annotations[InstanceIdAnnotationKey] = id
	_, err := hc.machineClient.Update(machine)
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
	id, find := machine.Annotations[InstanceIdAnnotationKey]
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

func run(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("error: %v, output: %s", err, string(out))
	}
	return nil
}
