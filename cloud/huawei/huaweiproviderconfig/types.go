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

package huaweiproviderconfig

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TODO: finish provider config
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type HuaweiProviderConfig struct {
	metav1.TypeMeta `json:",inline"`
	// The name of your server instance.
	Name string `json:"name"`
	// The flavor reference, as a UUID or full URL, for the flavor for your server instance.
	FlavorRef string `json:"flavorRef"`
	// The UUID of the image to use for your server instance.
	ImageRef string `json:"imageRef"`
	// A networks object. Required parameter when there are multiple networks defined for the tenant.
	// When you do not specify the networks parameter, the server attaches to the only network created for the current tenant.
	Networks []NetworkParam `json:"networks,omitempty"`

	// The availability zone from which to launch the server.
	AvailabilityZone string `json:"availability_zone,omitempty"`

	VpcID      string     `json:"vpcid"`
	RootVolume RootVolume `json:"root_volume"`
}

type NetworkParam struct {
	// The UUID of the network. Required if you omit the port attribute.
	UUID string `json:"uuid,omitempty"`
	// A fixed IPv4 address for the NIC.
	FixedIp string `json:"fixed_ip,omitempty"`
}

type RootVolume struct {
	VolumeType  string                 `json:"volumeType"`
	Size        int                    `json:"diskSize,omitempty"`
	ExtendParam map[string]interface{} `json:"extendParam,omitempty"`
}
