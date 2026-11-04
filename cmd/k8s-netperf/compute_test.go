package main

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestParseCompute(t *testing.T) {
	testCases := []struct {
		name, cpu, memory string
		wantCPU, wantMem  string
		wantErr           bool
	}{
		{name: "unset"},
		{name: "whole cpus", cpu: "2", memory: "2Gi", wantCPU: "2", wantMem: "2Gi"},
		{name: "millicpus", cpu: "500m", memory: "512Mi", wantCPU: "500m", wantMem: "512Mi"},
		{name: "decimal memory", cpu: "1", memory: "1G", wantCPU: "1", wantMem: "1G"},
		{name: "cpu without memory", cpu: "2", wantErr: true},
		{name: "memory without cpu", memory: "2Gi", wantErr: true},
		{name: "malformed cpu", cpu: "two", memory: "2Gi", wantErr: true},
		{name: "malformed memory", cpu: "2", memory: "2GB", wantErr: true},
		{name: "zero cpu", cpu: "0", memory: "2Gi", wantErr: true},
		{name: "negative memory", cpu: "2", memory: "-1Gi", wantErr: true},
		{name: "fractional memory", cpu: "2", memory: "1500m", wantErr: true},
		{name: "surrounding spaces", cpu: " 2", memory: "2Gi", wantErr: true},
		{name: "finer than a millicore", cpu: "0.0005", memory: "2Gi", wantErr: true},
		{name: "sub-millicore fraction", cpu: "1500500u", memory: "2Gi", wantErr: true},
		{name: "one millicore", cpu: "1m", memory: "2Gi", wantCPU: "1m", wantMem: "2Gi"},
		{name: "millicores as decimal", cpu: "0.25", memory: "2Gi", wantCPU: "250m", wantMem: "2Gi"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCompute(tc.cpu, tc.memory)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseCompute(%q, %q) = %v, want error", tc.cpu, tc.memory, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCompute(%q, %q) error = %v", tc.cpu, tc.memory, err)
			}
			if tc.wantCPU == "" {
				if len(got) != 0 {
					t.Fatalf("parseCompute(unset) = %v, want empty", got)
				}
				return
			}
			if len(got) != 2 {
				t.Fatalf("parseCompute() = %v, want cpu and memory only", got)
			}
			if q := got[corev1.ResourceCPU]; q.Cmp(resource.MustParse(tc.wantCPU)) != 0 {
				t.Errorf("cpu = %v, want %s", q.String(), tc.wantCPU)
			}
			if q := got[corev1.ResourceMemory]; q.Cmp(resource.MustParse(tc.wantMem)) != 0 {
				t.Errorf("memory = %v, want %s", q.String(), tc.wantMem)
			}
		})
	}
}

func TestIsWhole(t *testing.T) {
	for value, want := range map[string]bool{"2": true, "2000m": true, "500m": false, "1.5": false, "2Gi": true, "1500m": false} {
		if got := isWhole(resource.MustParse(value)); got != want {
			t.Errorf("isWhole(%s) = %t, want %t", value, got, want)
		}
	}
}
