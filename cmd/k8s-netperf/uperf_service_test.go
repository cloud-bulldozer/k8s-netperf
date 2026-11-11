package main

import (
	"testing"

	"github.com/cloud-bulldozer/k8s-netperf/pkg/config"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestUperfService(t *testing.T) {
	svc := func(name string) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name}}
	}
	s := config.PerfScenarios{
		UperfService:      svc("uperf-service"),
		UperfLatService:   svc("uperf-histogram-service"),
		UperfVmService:    svc("uperf-vm-service"),
		UperfLatVmService: svc("uperf-vm-histogram-service"),
	}
	testCases := []struct {
		profile string
		virt    bool
		want    string
	}{
		{profile: "TCP_STREAM", want: "uperf-service"},
		{profile: "TCP_RR", want: "uperf-service"},
		{profile: "TCP_STREAM_LAT", want: "uperf-histogram-service"},
		{profile: "TCP_STREAM", virt: true, want: "uperf-vm-service"},
		{profile: "TCP_STREAM_LAT", virt: true, want: "uperf-vm-service"},
	}
	for _, tc := range testCases {
		if got := uperfService(s, tc.profile, tc.virt).Name; got != tc.want {
			t.Errorf("uperfService(%s, virt=%t) = %s, want %s", tc.profile, tc.virt, got, tc.want)
		}
	}
}
