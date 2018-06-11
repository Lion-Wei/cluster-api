package clients

import (
	"fmt"
	"io/ioutil"
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
	privateKey := `-----BEGIN RSA PRIVATE KEY-----
MIICWwIBAAKBgQDqmRwwqRXPFsEE2ipDlt03ePyLjtEP/cS4dKgqWQAnEPUdYr6e
OELQO55J/vY3Od6gR/IsJqtePbtEb1nXKDAXR0WAt2oMrK721zc4B54+dYBL1YQV
v7qcKMNgw7/m4npUjjIzSlx3TfRiJNamTpfl7MNzqpaubY0EioIsIiO1jQIBIwKB
gQCaKhnW1YNcM4YnwpDNVIK+Da1FxEA9tWQEL2cxxXUg/IPRe2dSB7bgnDTRikoH
CMV/RTi97YaOYxSpUHzd2JSgUyWY+2gbaoEp7xSDSEqt/J3LhQR53pip9fU6ZRK5
/JDe2kIVpUA1YH8Uqj5U9Cjt0t8tCjSWcOjMJtlUTY2TSwJBAPYMFDbCbQUG2Sh3
wlbSWm+tExgXayVWg6ON+ebPT7GejCLJvfBYR8XbmQx86KerShiUyJAjC+S7kOSx
SQCAClECQQD0FniSxmrN7rFZMPCyF3cWGsBA62uVkgrBAwLe9Xwq9DKDczMFgRX1
QSQVSM0R/D0NualY1bJ4015fkUyVXOx9AkA/RO9BR/Al3TCGv7WhTAigX7Rz6MPH
xcoUHTGhwEexVKexLI/tWId8BUScz6mKM1wyNOMdv958pUKD8xLFnUR7AkBaqUK6
LHDQJXUSf+RfZ80ld6Z+g1PYeBKfdiWjRTy/f0X2T1wX/L8DUrWhgXC9iZMFGRMD
vRZnZHOClQ3RE+LPAkEAn3QxNC9r3/U7ZHFIhQV5jc4l7W26ucmqJc0Z6WxU6vkq
D2Cw4GnZoXjN9OX/cHVuGDPygJQpAA7xITRzB4Rz6g==
-----END RSA PRIVATE KEY-----`
	ioutil.WriteFile("/root/private.key", []byte(privateKey), 0400)
	res := util.ExecCommand(
		"ssh", fmt.Sprintf("-i %s", "/root/private.key"),
		"-o", "StrictHostKeyChecking no", "-o", "UserKnownHostsFile /dev/null",
		fmt.Sprintf("%s@%s", keyPair.Name, (*list)[0].AccessIPv4),
		"echo STARTFILE; sudo cat /root/lw")
	if res != "Hello, word!" {
		t.Fatalf("exec ssh command err, res is: %s", res)
	}
}
