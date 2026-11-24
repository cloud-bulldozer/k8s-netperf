package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testRunUUID = "5d1c7e1a-8a52-4f0e-9a43-run-uuid-test"

var namespacesGVR = corev1.SchemeGroupVersion.WithResource("namespaces")

func existingNamespace(annotations map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, Annotations: annotations}}
}

func namespaceActions(c *fake.Clientset, verbs ...string) []k8stesting.Action {
	var out []k8stesting.Action
	for _, a := range c.Actions() {
		for _, verb := range verbs {
			if a.GetResource().Resource == "namespaces" && a.GetVerb() == verb {
				out = append(out, a)
			}
		}
	}
	return out
}

// storedNamespace reads the tracker directly, past any reactor a test installed.
func storedNamespace(t *testing.T, c *fake.Clientset) *corev1.Namespace {
	t.Helper()
	obj, err := c.Tracker().Get(namespacesGVR, "", namespace)
	if err != nil {
		t.Fatalf("namespace %s not stored: %v", namespace, err)
	}
	return obj.(*corev1.Namespace)
}

func forbid(resource string) k8stesting.ReactionFunc {
	return func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(corev1.Resource(resource), "", nil)
	}
}

func TestBuildInfraAnnotatesCreatedNamespace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		udn   bool
		label string
	}{
		{"default", false, namespace},
		{"udn", true, "k8s.ovn.org/primary-user-defined-network"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientset()
			if err := BuildInfra(c, tc.udn, testRunUUID); err != nil {
				t.Fatal(err)
			}
			creates := namespaceActions(c, "create")
			if len(creates) != 1 {
				t.Fatalf("got %d namespace creates, want 1", len(creates))
			}
			sent := creates[0].(k8stesting.CreateAction).GetObject().(*corev1.Namespace)
			if got := sent.Annotations[RunUUIDAnnotation]; got != testRunUUID {
				t.Errorf("Create request annotation = %q, want %q", got, testRunUUID)
			}
			if _, ok := sent.Labels[tc.label]; !ok || len(sent.Labels) != 1 {
				t.Errorf("Create request labels = %v, want only %q", sent.Labels, tc.label)
			}
			if got := storedNamespace(t, c).Annotations[RunUUIDAnnotation]; got != testRunUUID {
				t.Errorf("stored annotation = %q, want %q", got, testRunUUID)
			}
		})
	}
}

func TestBuildInfraReusesExistingNamespaceUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name        string
		annotations map[string]string
	}{
		{"unmarked", nil},
		{"another run", map[string]string{RunUUIDAnnotation: "another-run"}},
		{"same UUID", map[string]string{RunUUIDAnnotation: testRunUUID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientset(existingNamespace(tc.annotations))
			if err := BuildInfra(c, false, testRunUUID); err != nil {
				t.Fatal(err)
			}
			if writes := namespaceActions(c, "create", "update", "patch"); len(writes) != 0 {
				t.Fatalf("existing namespace was written: %v", writes)
			}
			got, ok := storedNamespace(t, c).Annotations[RunUUIDAnnotation]
			want, wantOK := tc.annotations[RunUUIDAnnotation]
			if got != want || ok != wantOK {
				t.Errorf("annotation = %q (present %v), want %q (present %v)", got, ok, want, wantOK)
			}
		})
	}
}

func TestBuildInfraCreateRaceLeavesOtherNamespace(t *testing.T) {
	c := fake.NewClientset(existingNamespace(map[string]string{RunUUIDAnnotation: "another-run"}))
	c.PrependReactor("get", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(corev1.Resource("namespaces"), namespace)
	})
	if err := BuildInfra(c, false, testRunUUID); err == nil {
		t.Fatal("BuildInfra succeeded on a namespace created by someone else")
	}
	if writes := namespaceActions(c, "update", "patch"); len(writes) != 0 {
		t.Fatalf("other namespace was written: %v", writes)
	}
	if got := storedNamespace(t, c).Annotations[RunUUIDAnnotation]; got != "another-run" {
		t.Errorf("annotation = %q, want another-run", got)
	}
}

// A namespace left behind by a failure after its Create must already carry the run UUID.
func TestBuildInfraFailureLeavesMarkedNamespace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inject func(c *fake.Clientset)
	}{
		{"create response lost", func(c *fake.Clientset) {
			c.PrependReactor("create", "namespaces", func(a k8stesting.Action) (bool, runtime.Object, error) {
				if err := c.Tracker().Add(a.(k8stesting.CreateAction).GetObject()); err != nil {
					return true, nil, err
				}
				return true, nil, apierrors.NewTimeoutError("response lost", 1)
			})
		}},
		{"service account refused", func(c *fake.Clientset) {
			c.PrependReactor("create", "serviceaccounts", forbid("serviceaccounts"))
		}},
		{"role binding refused", func(c *fake.Clientset) {
			c.PrependReactor("create", "rolebindings", forbid("rolebindings"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fake.NewClientset()
			tc.inject(c)
			if err := BuildInfra(c, false, testRunUUID); err == nil {
				t.Fatal("BuildInfra succeeded despite the injected failure")
			}
			if got := storedNamespace(t, c).Annotations[RunUUIDAnnotation]; got != testRunUUID {
				t.Errorf("annotation = %q, want %q", got, testRunUUID)
			}
		})
	}
}
