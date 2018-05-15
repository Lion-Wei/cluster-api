package clients

import (
	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack"
)

const (
	HttpsHead = "https://"
)

// TODO: finish machine service
type MachineService struct {
	cloudserver *gophercloud.ServiceClient
	region      string
	version     string
	apiUrl      string
}

// url to create client
// url to create cloudserver
func NewMachineService(url, projectid string) (*MachineService, error) {
	client, err := openstack.NewClient(url)
	if err != nil {
		return nil, err
	}

	v1EvsUrl := gophercloud.NormalizeURL(HttpsHead + url + "v1/" + projectid)
	cloudServer := &gophercloud.ServiceClient{ProviderClient: client, Endpoint: v1EvsUrl}
	return &MachineService{
		cloudserver: cloudServer,
		region:      "region",
		version:     "version",
		apiUrl:      "url",
	}, nil
}

// CreateMachine creates a machine instance and returns the machineId of the instance.
func (ms *MachineService) CreateMachine(options MachineCreateOptions) (result MachineCreateResult, err error) {
	return result, nil
}

func (ms *MachineService) DeleteMachine(keyInfo ResourceKeyInfo) (result MachineDeleteResult, err error) {
	return result, nil
}
func (ms *MachineService) GetMachine(keyInfo ResourceKeyInfo) (result MachineGetResult, err error) {
	return result, nil
}

type MachineCreateOptions struct {
	AvailabilityZone string
	Metadate         map[string]string
	Image            string
}

type MachineCreateResult struct {
}

type ResourceKeyInfo struct {
	Ident string
}

type MachineDeleteResult struct {
}

type MachineGetResult struct {
	NetworkInterfaces []*NetworkInterface `json:"networkInterfaces,omitempty"`
}

type NetworkInterface struct {
	Name string
	IP   string
}
