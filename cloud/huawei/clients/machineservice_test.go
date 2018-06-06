package clients

import (
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
	if err != nil {
		t.Fatalf("Get instance list failed.")
	}
	t.Logf("instance List is:\n%v", list)
}
