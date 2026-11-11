package main

import (
	"testing"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/k8s"
)

func TestValidateImage(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testCases := []struct {
		image   string
		wantErr bool
	}{
		{image: k8s.DefaultImage},
		{image: "quay.io/cloud-bulldozer/k8s-netperf@" + digest},
		{image: "quay.io/cloud-bulldozer/k8s-netperf:v0.1.44@" + digest},
		{image: "registry.example.com:5000/bench/k8s-netperf:v1"},
		{image: "k8s-netperf"},
		{image: "", wantErr: true},
		{image: " quay.io/cloud-bulldozer/k8s-netperf", wantErr: true},
		{image: "quay.io/cloud-bulldozer/k8s-netperf ", wantErr: true},
		{image: "quay.io/Cloud-Bulldozer/k8s-netperf", wantErr: true},
		{image: "quay.io/cloud-bulldozer/k8s-netperf@sha256:0123", wantErr: true},
		{image: "quay.io/cloud-bulldozer/k8s-netperf@" + digest[:len(digest)-1] + "g", wantErr: true},
		{image: "quay.io/cloud-bulldozer/k8s-netperf@md5:0123456789abcdef0123456789abcdef", wantErr: true},
		{image: "quay.io/cloud-bulldozer/k8s-netperf:bad tag", wantErr: true},
	}
	for _, tc := range testCases {
		err := validateImage(tc.image)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateImage(%q) error = %v, wantErr %t", tc.image, err, tc.wantErr)
		}
	}
}
