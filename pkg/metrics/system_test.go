package metrics

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func newVersionConfigMap(data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      microShiftVersionConfigMap,
			Namespace: microShiftVersionNamespace,
		},
		Data: data,
	}
}

func TestMicroShiftVersionFromAllKeys(t *testing.T) {
	client := fake.NewSimpleClientset(newVersionConfigMap(map[string]string{
		"version": "4.22.0~rc.2",
		"major":   "4",
		"minor":   "22",
	}))
	info, err := MicroShiftVersion(client)
	if err != nil {
		t.Fatalf("MicroShiftVersion returned unexpected error: %v", err)
	}
	if info.Version != "4.22.0~rc.2" {
		t.Errorf("Version = %q, want %q", info.Version, "4.22.0~rc.2")
	}
	if info.MajorMinor != "4.22" {
		t.Errorf("MajorMinor = %q, want %q", info.MajorMinor, "4.22")
	}
}

func TestMicroShiftVersionDerivesMajorMinorFromVersion(t *testing.T) {
	client := fake.NewSimpleClientset(newVersionConfigMap(map[string]string{
		"version": "4.22.0~rc.2",
	}))
	info, err := MicroShiftVersion(client)
	if err != nil {
		t.Fatalf("MicroShiftVersion returned unexpected error: %v", err)
	}
	if info.Version != "4.22.0~rc.2" {
		t.Errorf("Version = %q, want %q", info.Version, "4.22.0~rc.2")
	}
	if info.MajorMinor != "4.22" {
		t.Errorf("MajorMinor = %q, want %q (regex-derived)", info.MajorMinor, "4.22")
	}
}

func TestMicroShiftVersionMissingConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()
	if _, err := MicroShiftVersion(client); err == nil {
		t.Fatal("MicroShiftVersion succeeded, expected error for missing ConfigMap")
	}
}

func TestMicroShiftVersionMissingVersionKey(t *testing.T) {
	cases := []struct {
		name string
		data map[string]string
	}{
		{name: "no version key", data: map[string]string{"major": "4", "minor": "22"}},
		{name: "empty version", data: map[string]string{"version": ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(newVersionConfigMap(tc.data))
			if _, err := MicroShiftVersion(client); err == nil {
				t.Fatal("MicroShiftVersion succeeded, expected error")
			}
		})
	}
}
