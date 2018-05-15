/*
Copyright 2017 The Kubernetes Authors.

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
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"text/template"
	"time"

	"github.com/golang/glog"
	"sigs.k8s.io/cluster-api/cloud/huawei/config"
)

var apiServerImage = "gcr.io/k8s-cluster-api/cluster-apiserver:0.0.2"
var controllerManagerImage = "gcr.io/k8s-cluster-api/controller-manager:0.0.2"
var machineControllerImage = "gcr.io/karangoel-gke-1/huawei-machine-controller:0.0.1-dev"

func init() {
	if img, ok := os.LookupEnv("MACHINE_CONTROLLER_IMAGE"); ok {
		machineControllerImage = img
	}

	if img, ok := os.LookupEnv("CLUSTER_API_SERVER_IMAGE"); ok {
		apiServerImage = img
	}

	if img, ok := os.LookupEnv("CONTROLLER_MANAGER_IMAGE"); ok {
		controllerManagerImage = img
	}
}

type caCertParams struct {
	caBundle string
	tlsCrt   string
	tlsKey   string
}

func getApiServerCerts() (*caCertParams, error) {
	// TODO: get huawei cloud apiserver cert
	return nil, fmt.Errorf("huawei cloud getApiServerCerts haven't be realized")
}

func CreateApiServerAndController(token string) error {
	tmpl, err := template.New("config").Parse(config.ClusterAPIDeployConfigTemplate)
	if err != nil {
		return err
	}

	certParms, err := getApiServerCerts()
	if err != nil {
		glog.Errorf("Error: %v", err)
		return err
	}

	type params struct {
		Token                  string
		APIServerImage         string
		ControllerManagerImage string
		MachineControllerImage string
		CABundle               string
		TLSCrt                 string
		TLSKey                 string
	}

	var tmplBuf bytes.Buffer
	err = tmpl.Execute(&tmplBuf, params{
		Token:                  token,
		APIServerImage:         apiServerImage,
		ControllerManagerImage: controllerManagerImage,
		MachineControllerImage: machineControllerImage,
		CABundle:               certParms.caBundle,
		TLSCrt:                 certParms.tlsCrt,
		TLSKey:                 certParms.tlsKey,
	})
	if err != nil {
		return err
	}

	ioutil.WriteFile("/tmp/pods.yaml", tmplBuf.Bytes(), 0644)

	maxTries := 5
	for tries := 0; tries < maxTries; tries++ {
		err = deployConfig(tmplBuf.Bytes())
		if err == nil {
			return nil
		} else {
			if tries < maxTries-1 {
				glog.Info("Error scheduling machine controller. Will retry...\n")
				time.Sleep(3 * time.Second)
			}
		}
	}

	if err != nil {
		return fmt.Errorf("couldn't start machine controller: %v\n", err)
	} else {
		return nil
	}
}

func deployConfig(manifest []byte) error {
	cmd := exec.Command("kubectl", "create", "-f", "-")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	go func() {
		defer stdin.Close()
		stdin.Write(manifest)
	}()

	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	} else {
		return fmt.Errorf("couldn't create pod: %v, output: %s", err, string(out))
	}
}
