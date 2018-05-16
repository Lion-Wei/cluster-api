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
	"path"
	"reflect"

	"github.com/golang/glog"
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

type MachineService interface {
	// CreateMachine creates a machine instance and returns the machineId of the instance.
	CreateMachine(options clients.MachineCreateOptions) (clients.MachineCreateResult, error)
	// DeleteMachine deletes the machine with the specified machineId.
	DeleteMachine(keyInfo clients.ResourceKeyInfo) (clients.MachineDeleteResult, error)
	// GetMachine gets the machine with the specified machineId.
	GetMachine(keyInfo clients.ResourceKeyInfo) (clients.MachineGetResult, error)
}

const MachineFileName = "machines.yaml"

type HuaweiClient struct {
	scheme              *runtime.Scheme
	kubeadmToken        string
	machineClient       client.MachineInterface
	machineSetupWatcher *machinesetup.ConfigWatch
	codecFactory        *serializer.CodecFactory
	machineService      MachineService
}

func NewMachineActuator(kubeadmToken string, machineClient client.MachineInterface, machineSetupConfigPath string) (*HuaweiClient, error) {
	// initial machineService
	machineService, err := clients.NewMachineService("url", "projectID")
	if err != nil {
		return nil, err
	}
	// get scheme and codecFactory
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
	}, nil
}

func (hc *HuaweiClient) Create(cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	// validate machine config
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

	// get machine info
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
	metadate, err := machineSetupConfig.GetMetadata(configParams)
	if err != nil {
		return err
	}
	image, err := machineSetupConfig.GetImage(configParams)
	if err != nil {
		return err
	}
	az := providerConfig.AvailabilityZone

	// check instance exist or not
	instance, err := hc.instanceIfExists(machine)
	if err != nil {
		return err
	}

	if instance != nil {
		glog.Infof("Skipped creating a VM that already exists. \n")
		return nil
	}
	// TODO: more options need to be fill in
	opt := clients.MachineCreateOptions{
		AvailabilityZone: az,
		Metadate:         metadate,
		Image:            image,
	}
	// TODO: need handle response
	_, err = hc.machineService.CreateMachine(opt)
	if err != nil {
		return hc.handleMachineError(machine, apierrors.CreateMachine(
			"error creating Huawei instance: %v", err))
	}

	if err = saveFile(setupScript, path.Join("/tmp", "machine-setup.sh"), 0644); err != nil {
		return hc.handleMachineError(machine, apierrors.CreateMachine(
			"error creating Huawei instance: %v", err))
	}
	// TODO: wait for instance ready, scp machine-setup.sh to instance
	return nil
}

func (hc *HuaweiClient) Delete(machine *clusterv1.Machine) error {
	instance, err := hc.instanceIfExists(machine)
	if err != nil {
		return err
	}

	if instance == nil {
		glog.Infof("Skipped deleting a VM that is already deleted.\n")
		return nil
	}

	config, err := hc.providerconfig(machine.Spec.ProviderConfig)
	if err != nil {
		return hc.handleMachineError(machine,
			apierrors.InvalidMachineConfiguration("Cannot unmarshal providerConfig field: %v", err))
	}

	if verr := hc.validateMachine(machine, config); verr != nil {
		return hc.handleMachineError(machine, verr)
	}

	_, err = hc.machineService.DeleteMachine(clients.ResourceKeyInfo{Ident: machine.Spec.Name})
	// TODO: handle delete result
	if err != nil {
		return hc.handleMachineError(machine, apierrors.DeleteMachine(
			"error deleting Huawei instance: %v", err))
	}

	return err
}

func (hc *HuaweiClient) Update(cluster *clusterv1.Cluster, machine *clusterv1.Machine) error {
	// Before updating, do some basic validation of the object first.
	config, err := hc.providerconfig(machine.Spec.ProviderConfig)
	if err != nil {
		return hc.handleMachineError(machine,
			apierrors.InvalidMachineConfiguration("Cannot unmarshal providerConfig field: %v", err))
	}
	if verr := hc.validateMachine(machine, config); verr != nil {
		return hc.handleMachineError(machine, verr)
	}

	status, err := hc.instanceStatus(machine)
	if err != nil {
		return err
	}

	currentMachine := (*clusterv1.Machine)(status)
	if currentMachine == nil {
		instance, err := hc.instanceIfExists(machine)
		if err != nil {
			return err
		}
		// TODO: Populating current state for boostrap machine
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
	if err != nil {
		return err
	}
	err = hc.updateInstanceStatus(machine)
	return err
}

func (hc *HuaweiClient) Exists(machine *clusterv1.Machine) (bool, error) {
	instance, err := hc.instanceIfExists(machine)
	if err != nil {
		return false, err
	}
	return instance != nil, err
}

func (hc *HuaweiClient) GetIP(machine *clusterv1.Machine) (string, error) {
	instance, err := hc.machineService.GetMachine(clients.ResourceKeyInfo{Ident: machine.Spec.Name})
	if err != nil {
		return "", err
	}

	var publicIP string

	for _, networkInterface := range instance.NetworkInterfaces {
		if networkInterface.Name == "nic0" {
			publicIP = networkInterface.IP
		}
	}
	return publicIP, nil
}

func (hc *HuaweiClient) GetKubeConfig(master *clusterv1.Machine) (string, error) {
	// TODO: ssh to get kubeconfig
	return "", fmt.Errorf("can't get Huawei cloud GetKubeConfig")
}

func (hc *HuaweiClient) CreateMachineController(cluster *clusterv1.Cluster, initialMachines []*clusterv1.Machine, clientSet kubernetes.Clientset) error {
	if err := CreateExtApiServerRoleBinding(); err != nil {
		return err
	}

	// Create the named machines ConfigMap.
	// After pivot-based bootstrap is done, the named machine should be a ConfigMap and this logic will be removed.
	machineSetupConfig, err := hc.machineSetupWatcher.GetMachineSetupConfig()
	if err != nil {
		return err
	}

	if err != nil {
		return err
	}
	yaml, err := machineSetupConfig.GetYaml()
	if err != nil {
		return err
	}
	nmConfigMap := corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "machines"},
		Data: map[string]string{
			MachineFileName: yaml,
		},
	}
	configMaps := clientSet.CoreV1().ConfigMaps(corev1.NamespaceDefault)
	if _, err := configMaps.Create(&nmConfigMap); err != nil {
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

func (hc *HuaweiClient) requiresUpdate(a *clusterv1.Machine, b *clusterv1.Machine) bool {
	// Do not want status changes. Do want changes that impact machine provisioning
	return !reflect.DeepEqual(a.Spec.ObjectMeta, b.Spec.ObjectMeta) ||
		!reflect.DeepEqual(a.Spec.ProviderConfig, b.Spec.ProviderConfig) ||
		!reflect.DeepEqual(a.Spec.Roles, b.Spec.Roles) ||
		!reflect.DeepEqual(a.Spec.Versions, b.Spec.Versions) ||
		a.ObjectMeta.Name != b.ObjectMeta.Name
}

func (hc *HuaweiClient) instanceIfExists(machine *clusterv1.Machine) (instanceStatus, error) {
	// TODO: consider
	return hc.instanceStatus(machine)
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

// TODO: We need to change this when we create dedicated service account for apiserver/controller
// pod.
func CreateExtApiServerRoleBinding() error {
	return run("kubectl", "create", "rolebinding",
		"-n", "kube-system", "machine-controller", "--role=extension-apiserver-authentication-reader",
		"--serviceaccount=default:default")
}

func run(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("error: %v, output: %s", err, string(out))
	}
	return nil
}
