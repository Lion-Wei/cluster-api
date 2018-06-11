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
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"reflect"
	"strings"

	"encoding/json"
	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/kubernetes"
	"net"
	"sigs.k8s.io/cluster-api/cloud/huawei/clients"
	huaweiconfigv1 "sigs.k8s.io/cluster-api/cloud/huawei/huaweiproviderconfig/v1alpha1"
	"sigs.k8s.io/cluster-api/cloud/huawei/machinesetup"
	apierrors "sigs.k8s.io/cluster-api/errors"
	clusterv1 "sigs.k8s.io/cluster-api/pkg/apis/cluster/v1alpha1"
	client "sigs.k8s.io/cluster-api/pkg/client/clientset_generated/clientset/typed/cluster/v1alpha1"
	"sigs.k8s.io/cluster-api/util"
)

const (
	MachineFileName         = "machines.yaml"
	setupScriptPath         = "/home/setup.sh"
	setupLogPath            = "/var/log/machineSetup.log"
	InstanceIdAnnotationKey = "hw-resourceId"

	SshKeyPairName                = "clusterapi"
	PrivateKeyPrefix              = "-----BEGIN RSA PRIVATE KEY-----"
	PrivateKeySuffix              = "-----END RSA PRIVATE KEY-----"
	MachineControllerSshKeySecret = "machine-controller-sshkeys"
)

type HuaweiClient struct {
	scheme              *runtime.Scheme
	kubeadmToken        string
	machineClient       client.MachineInterface
	machineSetupWatcher *machinesetup.ConfigWatch
	codecFactory        *serializer.CodecFactory
	machineService      *clients.InstanceService
	sshKeyPair          *clients.SshKeyPair
}

func NewMachineActuator(kubeadmToken string, machineClient client.MachineInterface, machineSetupConfigPath string) (*HuaweiClient, error) {
	// TODO: Read cloud config(authurl, tenantId...) from file
	machineService, err := clients.NewInstanceService(&clients.CloudConfig{})
	if err != nil {
		return nil, err
	}
	// Only applicable if it's running inside machine controller pod.
	var keyPair *clients.SshKeyPair
	if _, err := os.Stat("/etc/sshkeys/private"); err == nil {
		b, err := ioutil.ReadFile("/etc/sshkeys/private")
		if err == nil {
			keyPair.PrivateKey = string(b)
		} else {
			return nil, err
		}

		b, err = ioutil.ReadFile("/etc/sshkeys/user")
		if err == nil {
			keyPair.Name = string(b)
		} else {
			return nil, err
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
		sshKeyPair:          keyPair,
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

	instance, err = hc.machineService.InstanceCreate(providerConfig, personality, cmd)
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
	return getIPFromInstance(instance)
}

func getIPFromInstance(instance *clients.Instance) (string, error) {
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
	if hc.sshKeyPair == nil {
		glog.Infof("Ssh key pair is empty, need create new keypair to get kubeConfig")
		keyPair, err := hc.machineService.CreateKeyPair(SshKeyPairName)
		if err != nil {
			return "", fmt.Errorf("Get kubeConfig failed, can't get ssh keypair: %v", err)
		}
		hc.sshKeyPair = keyPair
	}
	ip, err := hc.GetIP(master)
	if err != nil {
		return "", err
	}

	key, err := clients.GetPurePrivateKey(hc.sshKeyPair.PrivateKey)
	if err != nil {
		return "", err
	}

	result := strings.TrimSpace(util.ExecCommand(
		"ssh", fmt.Sprintf("-%s", key),
		"-o", "StrictHostKeyChecking no",
		"-o", "UserKnownHostsFile /dev/null",
		fmt.Sprintf("%s@%s", SshKeyPairName, ip),
		"echo STARTFILE; sudo cat /etc/kubernetes/admin.conf"))
	parts := strings.Split(result, "STARTFILE")
	if len(parts) != 2 {
		return "", nil
	}
	return strings.TrimSpace(parts[1]), nil
}

func (hc *HuaweiClient) CreateMachineController(cluster *clusterv1.Cluster, initialMachines []*clusterv1.Machine, clientSet kubernetes.Clientset) error {
	err := run("kubectl", "create", "rolebinding",
		"-n", "kube-system", "machine-controller", "--role=extension-apiserver-authentication-reader",
		"--serviceaccount=default:default")
	if err != nil {
		return err
	}

	// Create the named machines ConfigMap.
	// TODO: After pivot-based bootstrap is done, the named machine should be a ConfigMap and this logic will be removed.
	machineSetupConfig, err := hc.machineSetupWatcher.GetMachineSetupConfig()
	if err != nil {
		return err
	}
	err = run("kubectl", "create", "secret", "generic", MachineControllerSshKeySecret,
		fmt.Sprintf("--from-literal=private=\"%s\"", hc.sshKeyPair.PrivateKey),
		"--from-literal=user="+SshKeyPairName)
	if err != nil {
		return fmt.Errorf("couldn't create service account key as credential: %v", err)
	}

	yaml, err := machineSetupConfig.GetYaml()
	if err != nil {
		return err
	}
	machineConfigMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "machine-setup"},
		Data: map[string]string{
			MachineFileName: yaml,
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
	// TODO: need finish postDelete
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

func saveFile(contents, path string, perm os.FileMode) error {
	return ioutil.WriteFile(path, []byte(contents), perm)
}

func run(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("error: %v, output: %s", err, string(out))
	}
	return nil
}
