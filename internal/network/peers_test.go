package network

import (
	"encoding/json"
	"testing"

	"tailscale.com/ipn/ipnstate"
)

func TestPeersKeepBoxesOnlineFirst(t *testing.T) {
	var st ipnstate.Status
	err := json.Unmarshal([]byte(`{"BackendState":"Running","Peer":{
		"nodekey:1111111111111111111111111111111111111111111111111111111111111111":{"HostName":"phone","DNSName":"phone.example.ts.net.","OS":"iOS","Online":true,"TailscaleIPs":["100.64.0.9"]},
		"nodekey:2222222222222222222222222222222222222222222222222222222222222222":{"HostName":"devbox","DNSName":"dev-alex.example.ts.net.","OS":"linux","Online":false,"TailscaleIPs":["fd7a::1","100.64.0.2"]},
		"nodekey:3333333333333333333333333333333333333333333333333333333333333333":{"HostName":"build","DNSName":"build.example.ts.net.","OS":"linux","Online":true,"TailscaleIPs":["100.64.0.3"]}
	}}`), &st)
	if err != nil {
		t.Fatal(err)
	}
	got := peersFrom(&st)
	if len(got) != 2 || got[0].Name != "build" || got[1].Name != "dev-alex" {
		t.Fatalf("peers = %+v; want build (online) then dev-alex, and no phone", got)
	}
	if got[1].IP != "100.64.0.2" || got[1].DNSName != "dev-alex.example.ts.net" {
		t.Fatalf("dev-alex = %+v; want its IPv4 and DNS name without the dot", got[1])
	}
}
