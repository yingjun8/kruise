# KEP-20260228: Advanced DaemonSet Node Label Patches

## Summary

This proposal introduces a new feature for OpenKruise Advanced DaemonSet that enables users to apply different pod template patches based on node labels. This addresses the common requirement of managing heterogeneous node clusters with a single DaemonSet while maintaining different configurations for different node types.

## Motivation

### Problem Statement

Currently, Advanced DaemonSet uses a uniform pod template for all nodes, which presents challenges in heterogeneous clusters where different node types require different configurations. Common scenarios include:

- **Storage-heavy nodes** vs **standard nodes** requiring different cache configurations
- **GPU nodes** vs **CPU nodes** requiring different resource requests
- **Edge nodes** vs **cloud nodes** requiring different environment variables
- **High-memory nodes** vs **standard nodes** requiring different JVM heap sizes

### Real-world Use Cases

1. **Nydus Image Acceleration**: Large disk nodes (2TB+) need different cache configurations than standard nodes (500GiB)
2. **Monitoring Agents**: Different sampling rates based on node capacity
3. **CNI Plugins**: Edge nodes require lightweight configurations
4. **Storage Daemons**: Automatic configuration adjustment based on local disk capacity

### Current Workarounds and Limitations

| Approach | Issues |
|----------|--------|
| Multiple DaemonSets | Configuration duplication, complex upgrades, maintenance overhead |
| Mutating Webhook | Additional service deployment, certificate management, performance overhead |
| Node-level ConfigMaps | Requires pod restart for configuration changes, limited flexibility |

## Proposal

### Goals

- Enable single Advanced DaemonSet to manage pods with different configurations across heterogeneous nodes
- Maintain backward compatibility with existing DaemonSet configurations
- Provide intuitive and Kubernetes-native configuration experience
- Support both simple and complex patch scenarios

### Non-Goals

- Support for non-label-based node selection (e.g., based on node annotations or taints)
- Dynamic configuration updates without pod recreation
- Cross-namespace configuration sharing

### User Stories

#### Story 1: Nydus Cache Configuration
As a cluster administrator, I want to deploy Nydus daemon on all nodes with automatic cache size configuration based on node storage capacity.

```yaml
apiVersion: apps.kruise.io/v1beta1
kind: DaemonSet
metadata:
  name: nydus-daemonset
spec:
  selector:
    matchLabels:
      app: nydus
  template:
    metadata:
      labels:
        app: nydus
    spec:
      containers:
      - name: nydusd
        image: nydus:latest
        env:
        - name: LOCAL_CACHE_SIZE
          value: "500Gi"  # Default for standard nodes
  patches:
  - selector:
      matchLabels:
        node-role/nydus-storage: large
    patch:
      spec:
        containers:
        - name: nydusd
          env:
          - name: LOCAL_CACHE_SIZE
            value: "2Ti"
          - name: FEATURE_GATE_LARGE_DISK
            value: "true"
```

#### Story 2: GPU Node Specialization
As an ML platform engineer, I need to deploy monitoring agents with different configurations for GPU and CPU nodes.

```yaml
apiVersion: apps.kruise.io/v1beta1
kind: DaemonSet
metadata:
  name: gpu-monitor
spec:
  patches:
  - selector:
      matchLabels:
        accelerator: nvidia-tesla-v100
    patch:
      spec:
        containers:
        - name: monitor
          resources:
            requests:
              nvidia.com/gpu: 1
              memory: 2Gi
          env:
          - name: GPU_MONITORING
            value: "enabled"
```

## Design Details

### API Specification

#### New API Types

```go
// DaemonSetSpec extension
type DaemonSetSpec struct {
    // ... existing fields ...
    
    // Patches defines a list of patches to apply to the pod template
    // based on node label matching
    // +optional
    Patches []DaemonSetPatch `json:"patches,omitempty"`
}

// DaemonSetPatch defines a patch to apply when node labels match the selector
type DaemonSetPatch struct {
    // Selector is a label query over nodes that should match this patch
    Selector *metav1.LabelSelector `json:"selector"`
    
    // Patch contains the patch to apply to the pod template
    // The patch follows Kubernetes strategic merge patch format
    Patch runtime.RawExtension `json:"patch"`
    
    // Priority defines the order of patch application when multiple patches match
    // Higher values have higher priority
    // +optional
    Priority int32 `json:"priority,omitempty"`
}
```

#### Validation Rules

1. **Patch Size Limit**: Each patch must not exceed 1KB in size
2. **Total Patches Limit**: Maximum 10 patches per DaemonSet
3. **Selector Validation**: Must be valid label selector format
4. **Patch Validation**: Must be valid strategic merge patch for PodTemplateSpec
5. **Conflict Resolution**: Later patches in the list override earlier ones when selectors overlap

### Controller Implementation

#### Pod Creation Flow Enhancement

```go
func (dsc *ReconcileDaemonSet) createPodWithPatches(
    ctx context.Context,
    ds *appsv1beta1.DaemonSet,
    nodeName string,
    baseTemplate *corev1.PodTemplateSpec,
) (*corev1.Pod, error) {
    
    // Get node information
    node, err := dsc.nodeLister.Get(nodeName)
    if err != nil {
        return nil, fmt.Errorf("failed to get node %s: %v", nodeName, err)
    }
    
    // Start with base template
    patchedTemplate := baseTemplate.DeepCopy()
    
    // Apply matching patches in priority order
    patches := sortPatchesByPriority(ds.Spec.Patches)
    
    for _, patch := range patches {
        if matchesNodeSelector(node, patch.Selector) {
            patchedTemplate, err = applyStrategicMergePatch(
                patchedTemplate, 
                patch.Patch.Raw,
            )
            if err != nil {
                return nil, fmt.Errorf("failed to apply patch: %v", err)
            }
        }
    }
    
    return &corev1.Pod{
        ObjectMeta: patchedTemplate.ObjectMeta,
        Spec:       patchedTemplate.Spec,
    }, nil
}
```

#### Patch Application Logic

```go
func applyStrategicMergePatch(
    template *corev1.PodTemplateSpec,
    patchData []byte,
) (*corev1.PodTemplateSpec, error) {
    
    // Convert template to JSON
    originalJSON, err := json.Marshal(template)
    if err != nil {
        return nil, err
    }
    
    // Apply strategic merge patch
    patchedJSON, err := strategicpatch.StrategicMergePatch(
        originalJSON, 
        patchData, 
        &corev1.PodTemplateSpec{},
    )
    if err != nil {
        return nil, err
    }
    
    // Convert back to PodTemplateSpec
    var patchedTemplate corev1.PodTemplateSpec
    if err := json.Unmarshal(patchedJSON, &patchedTemplate); err != nil {
        return nil, err
    }
    
    return &patchedTemplate, nil
}
```

#### Node Label Matching

```go
func matchesNodeSelector(node *corev1.Node, selector *metav1.LabelSelector) bool {
    if selector == nil {
        return false
    }
    
    labelSelector, err := metav1.LabelSelectorAsSelector(selector)
    if err != nil {
        return false
    }
    
    return labelSelector.Matches(labels.Set(node.Labels))
}
```

### Revision Management

#### Hash Calculation Enhancement

To ensure correct revision management, the controller-revision-hash must include patch information:

```go
func computeHashWithPatches(
    ds *appsv1beta1.DaemonSet,
    nodeLabels map[string]string,
) string {
    
    // Include base template hash
    baseHash := kubecontroller.ComputeHash(&ds.Spec.Template, nil)
    
    // Include applicable patches
    var patchHashes []string
    patches := getApplicablePatches(ds.Spec.Patches, nodeLabels)
    
    for _, patch := range patches {
        patchHash := sha256.Sum256(patch.Patch.Raw)
        patchHashes = append(patchHashes, hex.EncodeToString(patchHash[:8]))
    }
    
    // Combine hashes
    combined := fmt.Sprintf("%s-%s", baseHash, strings.Join(patchHashes, ""))
    return kubecontroller.ComputeHash(&corev1.PodTemplateSpec{
        ObjectMeta: metav1.ObjectMeta{
            Annotations: map[string]string{
                "daemonset.kruise.io/patch-combined-hash": combined,
            },
        },
    }, nil)
}
```

### Rollout Strategy Integration

#### In-Place Updates with Patches

When patches are updated, the controller should:

1. **Identify affected pods**: Find pods whose node labels match changed patches
2. **Compute new hashes**: Generate new controller-revision-hash including patches
3. **Respect update strategy**: Honor partition, maxSurge, maxUnavailable settings
4. **Handle conflicts**: Ensure patch changes don't conflict with in-place updates

### Security Considerations

#### RBAC Requirements

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kruise-daemonset-patches-controller
rules:
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get", "list", "watch"]
- apiGroups: ["apps.kruise.io"]
  resources: ["daemonsets"]
  verbs: ["get", "list", "watch", "update", "patch"]
```

#### Admission Webhook Validation

```yaml
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingWebhookConfiguration
metadata:
  name: daemonset-patches-validator
webhooks:
- name: daemonset-patches.kruise.io
  rules:
  - apiGroups: ["apps.kruise.io"]
    apiVersions: ["v1beta1"]
    operations: ["CREATE", "UPDATE"]
    resources: ["daemonsets"]
  clientConfig:
    service:
      name: kruise-webhook-service
      namespace: kruise-system
      path: "/validate-daemonset-patches"
```

### Test Plan

#### Unit Tests

1. **API Validation Tests**
   - Valid patch configurations
   - Invalid patch formats
   - Selector validation

2. **Controller Logic Tests**
   - Patch application correctness
   - Node label matching
   - Multiple patches with priorities
   - Revision hash calculation

3. **Edge Case Tests**
   - Empty patches list
   - Non-matching selectors
   - Invalid patch content
   - Large number of patches

#### Integration Tests

1. **End-to-End Scenarios**
   - Deploy DaemonSet with patches
   - Verify patch application on different nodes
   - Test rolling updates with patch changes
   - Test rollback functionality

2. **Performance Tests**
   - 1000+ node cluster with patches
   - Memory usage with large patches
   - CPU impact of patch matching

#### E2E Tests

```go
var _ = SIGDescribe("AdvancedDaemonSet Node Label Patches", func() {
    
    ginkgo.It("should apply patches based on node labels", func() {
        ds := &appsv1beta1.DaemonSet{
            Spec: appsv1beta1.DaemonSetSpec{
                Patches: []appsv1beta1.DaemonSetPatch{
                    {
                        Selector: &metav1.LabelSelector{
                            MatchLabels: map[string]string{"disk-type": "large"},
                        },
                        Patch: runtime.RawExtension{
                            Raw: []byte(`{"spec":{"containers":[{"name":"test","env":[{"name":"CACHE_SIZE","value":"2Ti"}]}]}}`),
                        },
                    },
                },
            },
        }
        
        // Create DaemonSet and verify patch application
        framework.ExpectNoError(createDaemonSetWithPatches(f, ds))
        framework.ExpectNoError(verifyPatchApplication(f, ds))
    })
})
```

### Graduation Criteria

#### Alpha (v1.7.0)

- [ ] Feature implemented behind feature gate `AdvancedDaemonSetNodeLabelPatches`
- [ ] Basic patch functionality working
- [ ] Unit tests with >80% coverage
- [ ] Documentation with examples

#### Beta (v1.8.0)

- [ ] Feature gate enabled by default
- [ ] E2E tests passing
- [ ] Performance benchmarks documented
- [ ] User feedback incorporated

#### GA (v1.9.0)

- [ ] Feature gate removed
- [ ] Production usage for 3+ months
- [ ] No critical bugs reported for 2+ months
- [ ] Complete documentation and examples

### Production Readiness Review

#### Feature Enablement and Rollback

- **Enablement**: Controlled by feature gate `AdvancedDaemonSetNodeLabelPatches`
- **Rollback**: Disable feature gate, existing pods remain unchanged
- **Upgrade**: Automatic migration, no user action required

#### Scalability

- **Maximum patches per DaemonSet**: 10
- **Maximum patch size**: 1KB per patch
- **Performance impact**: <1ms additional latency per pod creation

#### Monitoring

- **Metrics**:
  - `daemonset_patches_applied_total`: Counter for patch applications
  - `daemonset_patches_errors_total`: Counter for patch failures
  - `daemonset_patches_duration_seconds`: Histogram for patch application time

### Implementation History

| Date | PR | Description |
|------|----|-------------|
| 2026-02-28 | TBD | Initial KEP creation |
| TBD | TBD | Alpha implementation |
| TBD | TBD | Beta enhancements |
| TBD | TBD | GA graduation |

### Drawbacks

1. **Increased Complexity**: More complex API and controller logic
2. **Debugging Difficulty**: Harder to debug patch-related issues
3. **Resource Usage**: Slightly increased memory usage for patch storage

### Alternatives

#### Alternative 1: WorkloadSpread Integration
- **Pros**: Reuses existing infrastructure
- **Cons**: Additional CRD required, more complex user experience

#### Alternative 2: Mutating Webhook
- **Pros**: No API changes needed
- **Cons**: External dependency, performance overhead, operational complexity

### Infrastructure Needed

1. **CI/CD Changes**: Add new test suites for patches
2. **Documentation**: Comprehensive user guide and examples
3. **Monitoring**: New dashboards for patch metrics
4. **Support**: Updated troubleshooting guides

## References

- [OpenKruise Advanced DaemonSet Documentation](https://openkruise.io/docs/user-manuals/advanceddaemonset)
- [Kubernetes Strategic Merge Patch](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/update-api-object-kubectl-patch/)
- [WorkloadSpread Design](https://openkruise.io/docs/user-manuals/workloadspread)
