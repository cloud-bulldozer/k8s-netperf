package k8s

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func zoneNode(name, zone string) *corev1.Node {
	labels := map[string]string{}
	if zone != "" {
		labels["topology.kubernetes.io/zone"] = zone
	}
	return &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func podsOn(nodes ...string) corev1.PodList {
	var list corev1.PodList
	for _, node := range nodes {
		list.Items = append(list.Items, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-on-" + node}, Spec: corev1.PodSpec{NodeName: node}})
	}
	return list
}

func TestVerifyAcrossAZ(t *testing.T) {
	client := fake.NewClientset(zoneNode("a1", "zone-a"), zoneNode("a2", "zone-a"), zoneNode("b1", "zone-b"), zoneNode("unlabeled", ""))
	testCases := []struct {
		name    string
		clients []corev1.PodList
		servers []corev1.PodList
		wantErr string
	}{
		{name: "different zones", clients: []corev1.PodList{podsOn("a1"), podsOn("a2")}, servers: []corev1.PodList{podsOn("b1")}},
		{name: "same zone on labeled nodes", clients: []corev1.PodList{podsOn("a1")}, servers: []corev1.PodList{podsOn("a2")}, wantErr: "client node a1 and server node a2 are both in zone zone-a"},
		{name: "host network pair in one zone", clients: []corev1.PodList{podsOn("a1"), podsOn("b1")}, servers: []corev1.PodList{podsOn("b1"), {}}, wantErr: "both in zone zone-b"},
		{name: "local client on the server node", clients: []corev1.PodList{podsOn("b1")}, servers: []corev1.PodList{podsOn("b1")}, wantErr: "both in zone zone-b"},
		{name: "node without zone", clients: []corev1.PodList{podsOn("unlabeled")}, servers: []corev1.PodList{podsOn("b1")}, wantErr: "node unlabeled of pod pod-on-unlabeled has no topology.kubernetes.io/zone label"},
		{name: "VM launcher pods in one zone", clients: []corev1.PodList{{}, {}, {}, podsOn("a1")}, servers: []corev1.PodList{{}, {}, podsOn("a2")}, wantErr: "client node a1 and server node a2 are both in zone zone-a"},
		{name: "no pods", clients: []corev1.PodList{{}}, servers: []corev1.PodList{{}}},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyAcrossAZ(client, tc.clients, tc.servers)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("verifyAcrossAZ() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("verifyAcrossAZ() error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
