package clients

import (
	"fmt"
	"sigs.k8s.io/cluster-api/util"
	"testing"
)

func TestInstanceList(t *testing.T) {
	cfg := &CloudConfig{
		Username:   "Lion-Wei",
		Password:   "Liang-1992",
		DomainName: "Lion-Wei",
		TenantID:   "edadc15415ee444ab2b489030f3db401",
		Region:     "cn-north-1",
	}
	is, err := NewInstanceService(cfg)
	if err != nil {
		t.Fatalf("Create new instance service err: %v", err)
	}

	// test update token
	oldToken := is.provider.Token()
	is.provider.SetToken("fake-token")
	err = is.UpdateToken()
	if err != nil {
		t.Fatalf("Test update token err: %v", err)
	}
	if oldToken == is.provider.Token() {
		t.Fatalf("Test failed, expect new token be different with new token")
	}
	if is.provider.Token() == "fake-token" {
		t.Fatalf("Test failed, token didn't update")
	}

	// test instance list
	list, err := is.getInstanceList(nil)
	if err != nil || len(*list) == 0 {
		t.Fatalf("Get instance list failed.")
	}

	for _, instance := range *list {
		detail, err := is.GetInstance(instance.ID)
		if err != nil {
			t.Fatalf("Get instance detail failed: %v", err)
		}
		fmt.Printf("instance detail is: %v", detail)
	}

	// test create keyPair
	keyPair, err := is.CreateKeyPair("root")
	if err != nil {
		t.Fatalf("Create keyPair Faied: %v", err)
	}

	// test use ssh key run command in instance
	key, err := GetPurePrivateKey(keyPair.PrivateKey)
	res := util.ExecCommand(
		"ssh", fmt.Sprintf("-%s", key),
		"-o", "StrictHostKeyChecking no",
		"-o", "UserKnownHostsFile /dev/null",
		fmt.Sprintf("%s@%s", keyPair.Name, (*list)[0].AccessIPv4),
		"echo STARTFILE; sudo cat /root/lw")
	if res != "Hello, word!" {
		t.Fatalf("exec ssh command err, res is: %s", res)
	}
}
