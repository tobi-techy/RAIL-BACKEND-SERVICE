package airbills

import (
	"encoding/json"
	"testing"
)

func TestNetworkIDAcceptsSnakeCase(t *testing.T) {
	var resp NetworkCheckResponse
	raw := []byte(`{"status":"00","message":"Successful","data":{"network":"MTN","network_id":"01"}}`)
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.NetworkID() != "01" || resp.Data.Network != "MTN" {
		t.Fatalf("got id %q network %q", resp.NetworkID(), resp.Data.Network)
	}
}

func TestFlattenProductsLiveShapes(t *testing.T) {
	elect := []byte(`{"batch":"01","ELECTRIC_COMPANY":[{"electId":"01","electName":"Eko Electric","prodId":"01"}]}`)
	got := flattenProducts(elect, "")
	if len(got) != 1 || got[0].ElectID != "01" || got[0].Name != "Eko Electric" || got[0].ProdID != "01" {
		t.Fatalf("elect: %+v", got)
	}

	data := []byte(`{"batch":"01","dataPlan":{"MTN":[{"networkId":"01","prodId":"500","prodName":"500 MB","prodAmount":306}],"Glo":[{"networkId":"02","prodId":"9","prodName":"Glo 1GB","prodAmount":200}]}}`)
	got = flattenProducts(data, "01")
	if len(got) != 1 || got[0].NetworkID != "01" || got[0].Amount != 306 || got[0].Name != "500 MB" {
		t.Fatalf("data: %+v", got)
	}

	cable := []byte(`{"CableTv":{"showmax":[{"TvId":"showmax","prodId":"nova-weekly","prodName":"Nova","prodAmount":700}]}}`)
	got = flattenProducts(cable, "")
	if len(got) != 1 || got[0].ProdID != "nova-weekly" || got[0].Amount != 700 {
		t.Fatalf("cable: %+v", got)
	}
}
