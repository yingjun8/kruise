package validating

import (
	"testing"

	appsv1beta1 "github.com/openkruise/kruise/apis/apps/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestValidateDaemonSetPatches(t *testing.T) {
	patchData := runtime.RawExtension{
		Raw: []byte(`{"spec":{"containers":[{"name":"test","image":"test:latest"}]}}`),
	}

	tests := []struct {
		name    string
		patches []appsv1beta1.DaemonSetPatch
		wantErr bool
	}{
		{
			name: "valid patches",
			patches: []appsv1beta1.DaemonSetPatch{
				{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"key": "value"},
					},
					Patch: patchData,
				},
			},
			wantErr: false,
		},
		{
			name: "too many patches",
			patches: func() []appsv1beta1.DaemonSetPatch {
				patches := make([]appsv1beta1.DaemonSetPatch, 11)
				for i := range patches {
					patches[i] = appsv1beta1.DaemonSetPatch{
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"key": "value"},
						},
						Patch: patchData,
					}
				}
				return patches
			}(),
			wantErr: true,
		},
		{
			name: "invalid selector",
			patches: []appsv1beta1.DaemonSetPatch{
				{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"invalid-key-": "value"},
					},
					Patch: patchData,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid patch format",
			patches: []appsv1beta1.DaemonSetPatch{
				{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"key": "value"},
					},
					Patch: runtime.RawExtension{
						Raw: []byte(`invalid json`),
					},
				},
			},
			wantErr: true,
		},
		{
			name: "empty selector",
			patches: []appsv1beta1.DaemonSetPatch{
				{
					Selector: nil,
					Patch:    patchData,
				},
			},
			wantErr: true,
		},
		{
			name: "nil patch",
			patches: []appsv1beta1.DaemonSetPatch{
				{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"key": "value"},
					},
					Patch: runtime.RawExtension{},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := validateDaemonSetPatches(tt.patches, field.NewPath("spec", "patches"))
			if (len(errors) > 0) != tt.wantErr {
				t.Errorf("validateDaemonSetPatches() error = %v, wantErr %v", errors, tt.wantErr)
			}
		})
	}
}

func TestValidateDaemonSetPatchesPriority(t *testing.T) {
	patchData := runtime.RawExtension{
		Raw: []byte(`{"spec":{"containers":[{"name":"test","image":"test:latest"}]}}`),
	}

	patches := []appsv1beta1.DaemonSetPatch{
		{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"key": "value"},
			},
			Priority: 0,
			Patch:    patchData,
		},
		{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"key": "value"},
			},
			Priority: 1000,
			Patch:    patchData,
		},
	}

	errors := validateDaemonSetPatches(patches, field.NewPath("spec", "patches"))
	if len(errors) > 0 {
		t.Errorf("valid priority values should not cause errors: %v", errors)
	}
}

func TestValidateDaemonSetPatchesComplexSelector(t *testing.T) {
	patchData := runtime.RawExtension{
		Raw: []byte(`{"spec":{"containers":[{"name":"test","image":"test:latest"}]}}`),
	}

	patches := []appsv1beta1.DaemonSetPatch{
		{
			Selector: &metav1.LabelSelector{
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "disk-type",
						Operator: "In",
						Values:   []string{"ssd", "nvme"},
					},
				},
			},
			Patch: patchData,
		},
	}

	errors := validateDaemonSetPatches(patches, field.NewPath("spec", "patches"))
	if len(errors) > 0 {
		t.Errorf("valid complex selector should not cause errors: %v", errors)
	}
}
