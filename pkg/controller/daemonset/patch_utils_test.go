package daemonset

import (
	"testing"

	appsv1beta1 "github.com/openkruise/kruise/apis/apps/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestApplyPatchesToPodTemplate(t *testing.T) {
	baseTemplate := &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"app": "test"},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "test-image:latest",
					Env: []corev1.EnvVar{
						{Name: "DEFAULT", Value: "value"},
					},
				},
			},
		},
	}

	patchData := runtime.RawExtension{
		Raw: []byte(`
			{
				"spec": {
					"containers": [
						{
							"name": "test-container",
							"env": [
								{"name": "PATCHED", "value": "patched-value"}
							]
						}
					]
				}
			}
		`),
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"node-type": "special"},
		},
	}

	patches := []appsv1beta1.DaemonSetPatch{
		{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"node-type": "special"},
			},
			Priority: 100,
			Patch:    patchData,
		},
	}

	// Test patch application
	patchedTemplate, err := applyPatchesToPodTemplate(
		&appsv1beta1.DaemonSet{
			Spec: appsv1beta1.DaemonSetSpec{
				Patches: patches,
			},
		},
		node,
		baseTemplate,
	)
	if err != nil {
		t.Fatalf("Failed to apply patches: %v", err)
	}

	// Verify patch was applied
	if len(patchedTemplate.Spec.Containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(patchedTemplate.Spec.Containers))
	}

	container := patchedTemplate.Spec.Containers[0]
	if len(container.Env) != 2 {
		t.Fatalf("Expected 2 env vars, got %d", len(container.Env))
	}

	foundPatched := false
	for _, env := range container.Env {
		if env.Name == "PATCHED" && env.Value == "patched-value" {
			foundPatched = true
			break
		}
	}

	if !foundPatched {
		t.Error("Patched environment variable not found")
	}
}

func TestApplyMultiplePatches(t *testing.T) {
	baseTemplate := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "base-image",
				},
			},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"type": "special",
				"env":  "prod",
			},
		},
	}

	patches := []appsv1beta1.DaemonSetPatch{
		{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"type": "special"},
			},
			Priority: 50,
			Patch: runtime.RawExtension{
				Raw: []byte(`{"spec":{"containers":[{"name":"test-container","image":"special-image"}]}}`),
			},
		},
		{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			Priority: 100,
			Patch: runtime.RawExtension{
				Raw: []byte(`{"spec":{"containers":[{"name":"test-container","env":[{"name":"ENV","value":"production"}]}]}}`),
			},
		},
	}

	// Test patch application
	patchedTemplate, err := applyPatchesToPodTemplate(
		&appsv1beta1.DaemonSet{
			Spec: appsv1beta1.DaemonSetSpec{
				Patches: patches,
			},
		},
		node,
		baseTemplate,
	)
	if err != nil {
		t.Fatalf("Failed to apply multiple patches: %v", err)
	}

	// Verify patches were applied in correct order (prod patch last)
	container := patchedTemplate.Spec.Containers[0]
	if container.Image != "special-image" {
		t.Errorf("Expected image 'special-image', got '%s'", container.Image)
	}

	if len(container.Env) != 1 || container.Env[0].Name != "ENV" || container.Env[0].Value != "production" {
		t.Errorf("Expected prod env var, got %v", container.Env)
	}
}

func TestNoMatchingPatches(t *testing.T) {
	baseTemplate := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "base-image",
				},
			},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"unrelated": "label"},
		},
	}

	patches := []appsv1beta1.DaemonSetPatch{
		{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"nonexistent": "label"},
			},
			Patch: runtime.RawExtension{
				Raw: []byte(`{"spec":{"containers":[{"name":"test-container","image":"wrong-image"}]}}`),
			},
		},
	}

	// Test patch application
	patchedTemplate, err := applyPatchesToPodTemplate(
		&appsv1beta1.DaemonSet{
			Spec: appsv1beta1.DaemonSetSpec{
				Patches: patches,
			},
		},
		node,
		baseTemplate,
	)
	if err != nil {
		t.Fatalf("Failed to apply patches: %v", err)
	}
	// Verify no patches were applied
	container := patchedTemplate.Spec.Containers[0]
	if container.Image != "base-image" {
		t.Errorf("Expected no changes, but image changed to '%s'", container.Image)
	}
}

func TestEmptyPatches(t *testing.T) {
	baseTemplate := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "base-image",
				},
			},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"type": "special"},
		},
	}

	// Test patch application
	patchedTemplate, err := applyPatchesToPodTemplate(
		&appsv1beta1.DaemonSet{
			Spec: appsv1beta1.DaemonSetSpec{
				Patches: []appsv1beta1.DaemonSetPatch{},
			},
		},
		node,
		baseTemplate,
	)
	if err != nil {
		t.Fatalf("Failed with empty patches: %v", err)
	}

	if len(patchedTemplate.Spec.Containers) != 1 || patchedTemplate.Spec.Containers[0].Image != "base-image" {
		t.Error("Empty patches should not modify the base template")
	}
}

func TestPrioritySorting(t *testing.T) {
	baseTemplate := &corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "base-image",
				},
			},
		},
	}

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{"type": "special"},
		},
	}

	patches := []appsv1beta1.DaemonSetPatch{
		{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"type": "special"},
			},
			Priority: 10,
			Patch: runtime.RawExtension{
				Raw: []byte(`{"spec":{"containers":[{"name":"test-container","image":"low-priority"}]}}`),
			},
		},
		{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"type": "special"},
			},
			Priority: 100,
			Patch: runtime.RawExtension{
				Raw: []byte(`{"spec":{"containers":[{"name":"test-container","image":"high-priority"}]}}`),
			},
		},
	}

	// Test patch application
	patchedTemplate, err := applyPatchesToPodTemplate(
		&appsv1beta1.DaemonSet{
			Spec: appsv1beta1.DaemonSetSpec{
				Patches: patches,
			},
		},
		node,
		baseTemplate,
	)
	if err != nil {
		t.Fatalf("Failed to apply patches with priority: %v", err)
	}

	container := patchedTemplate.Spec.Containers[0]
	if container.Image != "high-priority" {
		t.Errorf("Expected high-priority patch to override, got '%s'", container.Image)
	}
}
